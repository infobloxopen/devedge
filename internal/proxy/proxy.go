// Package proxy provides an in-process reverse proxy that replaces the
// external Traefik binary. It listens on the devedge EdgeIP (127.0.0.2)
// on ports 80 and 443, matches incoming requests by Host header against
// the route registry, and forwards them to the registered upstream.
// TLS certificates are generated on-the-fly per SNI hostname, signed by
// the mkcert CA when available.
package proxy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/infobloxopen/devedge/internal/certs"
	"github.com/infobloxopen/devedge/internal/registry"
	"github.com/infobloxopen/devedge/pkg/types"
)

// Proxy is a host-routing reverse proxy that listens on the EdgeIP.
type Proxy struct {
	reg    *registry.Registry
	logger *slog.Logger
	ca     *caState   // loaded CA for signing; nil means self-signed fallback
	cache  *certCache // shared cert cache, pre-warmed and used by GetCertificate

	caReason       string    // why the mkcert CA could not be loaded ("" when it was)
	selfSignedWarn sync.Once // one-shot warning when the first untrusted leaf is minted
}

// caState holds the parsed mkcert CA cert and key for on-the-fly signing.
type caState struct {
	cert *x509.Certificate
	key  any // crypto.Signer
}

// certCache caches generated TLS certificates by hostname.
type certCache struct {
	mu    sync.RWMutex
	certs map[string]*tls.Certificate
}

func newCertCache() *certCache {
	return &certCache{certs: make(map[string]*tls.Certificate)}
}

func (c *certCache) get(host string) (*tls.Certificate, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cert, ok := c.certs[host]
	return cert, ok
}

func (c *certCache) put(host string, cert *tls.Certificate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.certs[host] = cert
}

// New creates a Proxy backed by the given registry. If certPair is non-nil,
// the mkcert CA is loaded for dynamic cert generation; otherwise a self-signed
// CA is generated at startup.
func New(reg *registry.Registry, certPair *certs.CertPair, logger *slog.Logger) *Proxy {
	p := &Proxy{reg: reg, logger: logger, cache: newCertCache()}

	// Try to load the mkcert CA for signing.
	if ca, err := loadCA(); err == nil {
		p.ca = ca
		logger.Info("proxy using mkcert CA for dynamic TLS certs")
	} else {
		p.caReason = err.Error()
		logger.Warn("proxy using self-signed CA for TLS — browsers will reject every routed host",
			"reason", err,
			"hint", "run 'de install' to record the mkcert CAROOT for the daemon (or set DEVEDGE_CAROOT) and restart it; 'de doctor' reports this state")
	}

	return p
}

// UsingSelfSignedCA reports whether the proxy fell back to the untrusted
// self-signed CA because the mkcert CA could not be loaded.
func (p *Proxy) UsingSelfSignedCA() bool { return p.ca == nil }

// CAFallbackReason returns why the mkcert CA could not be loaded. Empty when
// the mkcert CA is in use.
func (p *Proxy) CAFallbackReason() string { return p.caReason }

// WarmCerts pre-generates TLS certificates for all currently registered
// route hostnames so the first request doesn't pay the generation cost.
func (p *Proxy) WarmCerts() {
	for _, route := range p.reg.List() {
		if _, ok := p.cache.get(route.Host); ok {
			continue
		}
		cert, err := p.generateCert(route.Host)
		if err != nil {
			p.logger.Warn("pre-generate cert failed", "host", route.Host, "err", err)
			continue
		}
		p.cache.put(route.Host, cert)
		p.logger.Info("pre-generated TLS cert", "host", route.Host)
	}
}

// Run starts HTTP and HTTPS listeners on the EdgeIP. It blocks until
// the context is cancelled.
func (p *Proxy) Run(ctx context.Context) error {
	handler := p.handler()

	// Pre-generate certs for any routes already registered.
	p.WarmCerts()

	// Periodically warm certs for newly registered routes.
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.WarmCerts()
			}
		}
	}()

	httpAddr := net.JoinHostPort(types.EdgeIP, "80")
	httpsAddr := net.JoinHostPort(types.EdgeIP, "443")

	httpSrv := &http.Server{
		Addr:    httpAddr,
		Handler: http.HandlerFunc(redirectHTTPS),
	}

	httpsSrv := &http.Server{
		Addr:    httpsAddr,
		Handler: handler,
		TLSConfig: &tls.Config{
			GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				return p.getCert(p.cache, hello.ServerName)
			},
		},
	}

	errCh := make(chan error, 2)

	go func() {
		p.logger.Info("proxy listening", "addr", httpAddr, "proto", "http")
		if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
			errCh <- fmt.Errorf("http: %w", err)
		}
	}()

	go func() {
		p.logger.Info("proxy listening", "addr", httpsAddr, "proto", "https+tcp")
		rawLn, err := net.Listen("tcp", httpsAddr)
		if err != nil {
			errCh <- fmt.Errorf("https listen: %w", err)
			return
		}
		// SNI router: TCP routes get piped directly, HTTP routes go to httpsSrv.
		httpLn := p.sniListener(rawLn, httpsSrv.TLSConfig)
		if err := httpsSrv.Serve(httpLn); err != http.ErrServerClosed {
			errCh <- fmt.Errorf("https: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		httpSrv.Shutdown(shutCtx)
		httpsSrv.Shutdown(shutCtx)
		return nil
	case err := <-errCh:
		return err
	}
}

// getCert returns a cached or freshly generated TLS certificate for the
// given hostname, signed by the mkcert CA (or self-signed as fallback).
func (p *Proxy) getCert(cache *certCache, serverName string) (*tls.Certificate, error) {
	if serverName == "" {
		serverName = "localhost"
	}

	if cert, ok := cache.get(serverName); ok {
		return cert, nil
	}

	cert, err := p.generateCert(serverName)
	if err != nil {
		return nil, err
	}
	cache.put(serverName, cert)
	p.logger.Info("generated TLS cert", "host", serverName)
	return cert, nil
}

// generateCert creates a TLS certificate for the given hostname.
func (p *Proxy) generateCert(hostname string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: hostname},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(825 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{hostname},
		IPAddresses:  []net.IP{net.ParseIP(types.EdgeIP)},
	}

	// Sign with mkcert CA if available, otherwise self-sign.
	issuer := tmpl
	signerKey := any(key)
	if p.ca != nil {
		issuer = p.ca.cert
		signerKey = p.ca.key
	} else {
		// Loud, once: every cert minted from here on is untrusted, and the
		// only runtime symptom is TLS handshake-error spam from clients.
		p.selfSignedWarn.Do(func() {
			p.logger.Warn("serving self-signed TLS certificates — clients will reject them",
				"first_host", hostname,
				"reason", p.caReason,
				"hint", "run 'de install' to record the mkcert CAROOT and restart the daemon")
		})
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, issuer, &key.PublicKey, signerKey)
	if err != nil {
		return nil, err
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}

	// Include the CA cert in the chain so clients can verify.
	if p.ca != nil {
		tlsCert.Certificate = append(tlsCert.Certificate, p.ca.cert.Raw)
	}

	return tlsCert, nil
}

// handler returns an http.Handler that routes by Host header.
func (p *Proxy) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}

		route, ok := p.reg.Lookup(host, r.URL.Path)
		if !ok {
			http.Error(w, "no route for host: "+host, http.StatusBadGateway)
			return
		}

		upstream, err := url.Parse(route.Upstream)
		if err != nil {
			http.Error(w, "bad upstream: "+err.Error(), http.StatusBadGateway)
			return
		}

		proxy := &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				req.URL.Scheme = upstream.Scheme
				req.URL.Host = upstream.Host
				req.Host = host
				// When the matched route strips its prefix, trim Path from the
				// request path before forwarding (e.g. "/api/x" → "/x" behind an
				// "/api" gateway). Guard against an empty result → "/".
				if route.StripPrefix && route.Path != "" {
					trimmed := strings.TrimPrefix(req.URL.Path, route.Path)
					if trimmed == "" || trimmed[0] != '/' {
						trimmed = "/" + trimmed
					}
					req.URL.Path = trimmed
				}
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				p.logger.Error("proxy error", "host", host, "upstream", upstream, "err", err)
				http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
			},
		}
		proxy.ServeHTTP(w, r)
	})
}

// redirectHTTPS sends an HTTP→HTTPS redirect.
func redirectHTTPS(w http.ResponseWriter, r *http.Request) {
	target := "https://" + r.Host + r.URL.RequestURI()
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

// loadCA reads the mkcert CA certificate and key from well-known locations.
func loadCA() (*caState, error) {
	certPEM, keyPEM, err := certs.ReadCAFiles()
	if err != nil {
		return nil, err
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("no PEM block in CA cert")
	}
	caCert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA cert: %w", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("no PEM block in CA key")
	}
	caKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		caKey, err = x509.ParseECPrivateKey(keyBlock.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse CA key: %w", err)
		}
	}

	return &caState{cert: caCert, key: caKey}, nil
}
