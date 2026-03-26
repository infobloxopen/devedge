// Package proxy provides an in-process reverse proxy that replaces the
// external Traefik binary. It listens on the devedge EdgeIP (127.0.0.2)
// on ports 80 and 443, matches incoming requests by Host header against
// the route registry, and forwards them to the registered upstream.
package proxy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/infobloxopen/devedge/internal/certs"
	"github.com/infobloxopen/devedge/internal/registry"
	"github.com/infobloxopen/devedge/pkg/types"
)

// Proxy is a host-routing reverse proxy that listens on the EdgeIP.
type Proxy struct {
	reg    *registry.Registry
	cert   *certs.CertPair
	logger *slog.Logger
}

// New creates a Proxy backed by the given registry.
func New(reg *registry.Registry, cert *certs.CertPair, logger *slog.Logger) *Proxy {
	return &Proxy{reg: reg, cert: cert, logger: logger}
}

// Run starts HTTP and HTTPS listeners on the EdgeIP. It blocks until
// the context is cancelled.
func (p *Proxy) Run(ctx context.Context) error {
	handler := p.handler()

	httpAddr := net.JoinHostPort(types.EdgeIP, "80")
	httpsAddr := net.JoinHostPort(types.EdgeIP, "443")

	httpSrv := &http.Server{
		Addr:    httpAddr,
		Handler: http.HandlerFunc(redirectHTTPS),
	}

	httpsSrv := &http.Server{
		Addr:    httpsAddr,
		Handler: handler,
	}

	tlsCert, err := p.loadOrGenerateCert()
	if err != nil {
		return fmt.Errorf("TLS setup: %w", err)
	}
	httpsSrv.TLSConfig = &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
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

// handler returns an http.Handler that routes by Host header.
func (p *Proxy) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		// Strip port if present.
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
				req.Host = host // preserve original Host header
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

// loadOrGenerateCert loads the mkcert keypair if available, otherwise
// generates an ephemeral self-signed certificate so TLS always works.
func (p *Proxy) loadOrGenerateCert() (tls.Certificate, error) {
	if p.cert != nil {
		return tls.LoadX509KeyPair(p.cert.CertFile, p.cert.KeyFile)
	}

	p.logger.Info("no mkcert CA available, generating self-signed certificate")
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "devedge self-signed"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"*.dev.test", "*.test"},
		IPAddresses:  []net.IP{net.ParseIP(types.EdgeIP)},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}, nil
}
