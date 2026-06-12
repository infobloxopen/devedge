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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/registry"
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
