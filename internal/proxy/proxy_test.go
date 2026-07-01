package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/registry"
	"github.com/infobloxopen/devedge/pkg/types"
)

// writeTestCA generates a signing CA in mkcert's on-disk layout
// (rootCA.pem + rootCA-key.pem) inside dir and returns the parsed CA cert.
func writeTestCA(t *testing.T, dir string) *x509.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "devedge test CA", Organization: []string{"mkcert development CA"}},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	certOut, err := os.Create(filepath.Join(dir, "rootCA.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	certOut.Close()

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyOut, err := os.Create(filepath.Join(dir, "rootCA-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	keyOut.Close()

	caCert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return caCert
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNew_UsesMkcertCAFromOverride(t *testing.T) {
	dir := t.TempDir()
	caCert := writeTestCA(t, dir)
	t.Setenv("DEVEDGE_CAROOT", dir)

	p := New(registry.New(), nil, testLogger())
	if p.UsingSelfSignedCA() {
		t.Fatalf("expected mkcert CA to load, fell back: %s", p.CAFallbackReason())
	}
	if p.CAFallbackReason() != "" {
		t.Errorf("CAFallbackReason = %q, want empty when the CA loaded", p.CAFallbackReason())
	}

	tlsCert, err := p.generateCert("foo.dev.test")
	if err != nil {
		t.Fatalf("generateCert: %v", err)
	}
	if len(tlsCert.Certificate) != 2 {
		t.Errorf("chain length = %d, want 2 (leaf + CA)", len(tlsCert.Certificate))
	}

	leaf, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	if leaf.Issuer.CommonName != caCert.Subject.CommonName {
		t.Errorf("leaf issuer = %q, want %q", leaf.Issuer.CommonName, caCert.Subject.CommonName)
	}

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:     pool,
		DNSName:   "foo.dev.test",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Errorf("leaf does not chain to the mkcert CA: %v", err)
	}
}

func TestNew_FallsBackToSelfSigned(t *testing.T) {
	// Explicit override pointing at a dir without a CA: loading must fail
	// and the proxy must report (not hide) the self-signed fallback.
	t.Setenv("DEVEDGE_CAROOT", t.TempDir())

	p := New(registry.New(), nil, testLogger())
	if !p.UsingSelfSignedCA() {
		t.Fatal("expected self-signed fallback when no CA is available")
	}
	if p.CAFallbackReason() == "" {
		t.Error("CAFallbackReason must explain why the CA could not be loaded")
	}

	tlsCert, err := p.generateCert("bar.dev.test")
	if err != nil {
		t.Fatalf("generateCert: %v", err)
	}
	if len(tlsCert.Certificate) != 1 {
		t.Errorf("chain length = %d, want 1 (self-signed leaf)", len(tlsCert.Certificate))
	}
	leaf, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	if leaf.Issuer.CommonName != leaf.Subject.CommonName {
		t.Errorf("leaf issuer = %q, subject = %q; fallback leaf should be self-signed",
			leaf.Issuer.CommonName, leaf.Subject.CommonName)
	}
}

// newProxyForTest builds a Proxy over a fresh registry with a self-signed CA
// (the CA path is irrelevant — these tests exercise handler() over plain HTTP
// via httptest, not TLS).
func newProxyForTest(t *testing.T) (*Proxy, *registry.Registry) {
	t.Helper()
	t.Setenv("DEVEDGE_CAROOT", t.TempDir()) // force deterministic self-signed CA
	reg := registry.New()
	return New(reg, nil, testLogger()), reg
}

// TestHandler_pathRouting registers two upstreams under ONE host — "/" → A and
// "/api" → B — and asserts requests reach the right backend by longest-prefix
// match. It also asserts StripPrefix trims the prefix ("/api/x" → backend sees
// "/x") while a non-stripping route preserves the full path.
func TestHandler_pathRouting(t *testing.T) {
	// Backend A: the catch-all (shell). Echoes the path it received.
	backendA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "A")
		w.Write([]byte(r.URL.Path))
	}))
	defer backendA.Close()

	// Backend B: the "/api" upstream with StripPrefix. Echoes the path it saw.
	backendB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "B")
		w.Write([]byte(r.URL.Path))
	}))
	defer backendB.Close()

	// Backend C: an "/assets" upstream WITHOUT StripPrefix — full path preserved.
	backendC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "C")
		w.Write([]byte(r.URL.Path))
	}))
	defer backendC.Close()

	p, reg := newProxyForTest(t)
	reg.Register(types.Route{Host: "app.dev.test", Path: "", Upstream: backendA.URL})
	reg.Register(types.Route{Host: "app.dev.test", Path: "/api", Upstream: backendB.URL, StripPrefix: true})
	reg.Register(types.Route{Host: "app.dev.test", Path: "/assets", Upstream: backendC.URL})

	handler := p.handler()

	cases := []struct {
		reqPath     string
		wantBackend string
		wantSeen    string // path the backend observed
	}{
		{"/", "A", "/"},
		{"/index.html", "A", "/index.html"},
		{"/api", "B", "/"},         // strip "/api" → "" → guarded to "/"
		{"/api/x", "B", "/x"},      // strip "/api" → "/x"
		{"/api/v1/y", "B", "/v1/y"},
		{"/assets/logo.png", "C", "/assets/logo.png"}, // non-strip preserves full path
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "http://app.dev.test"+c.reqPath, nil)
		req.Host = "app.dev.test"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200 (body: %s)", c.reqPath, w.Code, w.Body.String())
			continue
		}
		if got := w.Header().Get("X-Backend"); got != c.wantBackend {
			t.Errorf("%s: routed to backend %q, want %q", c.reqPath, got, c.wantBackend)
		}
		if got := w.Body.String(); got != c.wantSeen {
			t.Errorf("%s: backend saw path %q, want %q", c.reqPath, got, c.wantSeen)
		}
	}
}

// TestHandler_noRouteForHost returns 502 when the host has no route at all,
// preserving the original catch-all-miss behavior.
func TestHandler_noRouteForHost(t *testing.T) {
	p, _ := newProxyForTest(t)
	handler := p.handler()

	req := httptest.NewRequest("GET", "http://unknown.dev.test/", nil)
	req.Host = "unknown.dev.test"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 for unrouted host", w.Code)
	}
}

// TestHandler_backwardCompat confirms a single path-less route forwards the
// full path unchanged — today's exact behavior.
func TestHandler_backwardCompat(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.URL.Path))
	}))
	defer backend.Close()

	p, reg := newProxyForTest(t)
	reg.Register(types.Route{Host: "api.dev.test", Upstream: backend.URL})
	handler := p.handler()

	req := httptest.NewRequest("GET", "http://api.dev.test/v1/things?q=1", nil)
	req.Host = "api.dev.test"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Body.String(); got != "/v1/things" {
		t.Errorf("backend saw %q, want /v1/things (full path preserved)", got)
	}
}
