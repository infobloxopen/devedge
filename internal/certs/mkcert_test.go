package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFakeCert creates a self-signed cert with the given DNS SANs for testing.
func writeFakeCert(t *testing.T, path string, dnsNames []string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     dnsNames,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
}

func TestCertCovers(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "test.pem")

	writeFakeCert(t, certFile, []string{"a.dev.test", "b.dev.test"})

	tests := []struct {
		name      string
		hostnames []string
		want      bool
	}{
		{"subset covered", []string{"a.dev.test"}, true},
		{"all covered", []string{"a.dev.test", "b.dev.test"}, true},
		{"missing host", []string{"a.dev.test", "c.dev.test"}, false},
		{"empty", []string{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := certCovers(certFile, tt.hostnames)
			if err != nil {
				t.Fatalf("certCovers: %v", err)
			}
			if got != tt.want {
				t.Errorf("certCovers = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCertCovers_missingFile(t *testing.T) {
	_, err := certCovers("/nonexistent", []string{"a.dev.test"})
	if err == nil {
		t.Error("expected error for missing file")
	}
}
