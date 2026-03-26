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
	ca     *caState // loaded CA for signing; nil means self-signed fallback
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
	p := &Proxy{reg: reg, logger: logger}

	// Try to load the mkcert CA for signing.
	if ca, err := loadCA(); err == nil {
		p.ca = ca
		logger.Info("proxy using mkcert CA for dynamic TLS certs")
	} else {
		logger.Info("proxy using self-signed CA for TLS", "reason", err)
	}

	return p
}

// Run starts HTTP and HTTPS listeners on the EdgeIP. It blocks until
// the context is cancelled.
func (p *Proxy) Run(ctx context.Context) error {
	handler := p.handler()
	cache := newCertCache()

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
				return p.getCert(cache, hello.ServerName)
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
		p.logger.Info("proxy listening", "addr", httpsAddr, "proto", "https")
		ln, err := tls.Listen("tcp", httpsAddr, httpsSrv.TLSConfig)
		if err != nil {
			errCh <- fmt.Errorf("https listen: %w", err)
			return
		}
		if err := httpsSrv.Serve(ln); err != http.ErrServerClosed {
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

		route, ok := p.reg.Lookup(host)
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
