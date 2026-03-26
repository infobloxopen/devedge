// Package certs manages locally-trusted TLS certificates for devedge
// hostnames. When possible it uses the mkcert CA to sign certificates
// using Go's crypto/x509 directly — no mkcert binary needed at runtime.
package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Manager handles certificate issuance and lifecycle for registered hostnames.
type Manager struct {
	mu       sync.Mutex
	certsDir string
	logger   *slog.Logger
}

// NewManager creates a certificate manager that stores certs in the given
// directory.
func NewManager(certsDir string, logger *slog.Logger) *Manager {
	return &Manager{
		certsDir: certsDir,
		logger:   logger,
	}
}

// EnsureCA checks that the mkcert local CA is available. Uses CARoot()
// which checks both PATH and well-known locations.
func EnsureCA() error {
	root, err := CARoot()
	if err != nil {
		return err
	}
	certPath := filepath.Join(root, "rootCA.pem")
	if _, err := os.Stat(certPath); err != nil {
		return fmt.Errorf("mkcert CA not found at %s; run 'mkcert -install' first", certPath)
	}
	return nil
}

// InstallCA runs 'mkcert -install' to install the local CA into system
// and browser trust stores.
func InstallCA() error {
	mkcert, err := findMkcert()
	if err != nil {
		return err
	}
	cmd := exec.Command(mkcert, "-install")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// CertPair holds paths to a certificate and its private key.
type CertPair struct {
	CertFile string
	KeyFile  string
}

// EnsureCert generates a certificate covering the given hostnames if one
// does not already exist or if the hostname set has changed. Signs using
// the mkcert CA directly via Go crypto — no mkcert binary needed.
func (m *Manager) EnsureCert(hostnames []string) (*CertPair, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.MkdirAll(m.certsDir, 0755); err != nil {
		return nil, fmt.Errorf("create certs dir: %w", err)
	}

	sorted := make([]string, len(hostnames))
	copy(sorted, hostnames)
	sort.Strings(sorted)

	certFile := filepath.Join(m.certsDir, "devedge.pem")
	keyFile := filepath.Join(m.certsDir, "devedge-key.pem")

	// Check if existing cert already covers these hostnames.
	if covers, _ := certCovers(certFile, sorted); covers {
		m.logger.Info("existing cert covers all hostnames", "count", len(sorted))
		return &CertPair{CertFile: certFile, KeyFile: keyFile}, nil
	}

	m.logger.Info("generating certificate", "hostnames", len(sorted))

	// Read the mkcert CA.
	caCertPEM, caKeyPEM, err := ReadCAFiles()
	if err != nil {
		return nil, fmt.Errorf("read CA: %w", err)
	}

	caBlock, _ := pem.Decode(caCertPEM)
	if caBlock == nil {
		return nil, fmt.Errorf("failed to decode CA cert PEM")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA cert: %w", err)
	}

	caKeyBlock, _ := pem.Decode(caKeyPEM)
	if caKeyBlock == nil {
		return nil, fmt.Errorf("failed to decode CA key PEM")
	}
	caKey, err := x509.ParsePKCS8PrivateKey(caKeyBlock.Bytes)
	if err != nil {
		// Try EC private key format.
		caKey, err = x509.ParseECPrivateKey(caKeyBlock.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse CA key: %w", err)
		}
	}

	// Generate leaf key and certificate.
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))

	var dnsNames []string
	var ips []net.IP
	for _, h := range sorted {
		if ip := net.ParseIP(h); ip != nil {
			ips = append(ips, ip)
		} else {
			dnsNames = append(dnsNames, h)
		}
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: sorted[0]},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(825 * 24 * time.Hour), // ~2 years, within CA limits
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
		IPAddresses:  ips,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("sign certificate: %w", err)
	}

	// Write cert PEM (leaf + CA chain).
	certOut, err := os.Create(certFile)
	if err != nil {
		return nil, err
	}
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	pem.Encode(certOut, caBlock) // include CA for chain
	certOut.Close()

	// Write key PEM.
	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return nil, err
	}
	keyOut, err := os.Create(keyFile)
	if err != nil {
		return nil, err
	}
	pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	keyOut.Close()

	return &CertPair{CertFile: certFile, KeyFile: keyFile}, nil
}

// CertDir returns the directory where certificates are stored.
func (m *Manager) CertDir() string {
	return m.certsDir
}

// certCovers checks if the certificate at path covers all the given hostnames.
func certCovers(certFile string, hostnames []string) (bool, error) {
	data, err := os.ReadFile(certFile)
	if err != nil {
		return false, err
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return false, fmt.Errorf("no PEM block found in %s", certFile)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, err
	}

	// Build set of SANs from the certificate.
	sans := make(map[string]bool, len(cert.DNSNames))
	for _, name := range cert.DNSNames {
		sans[name] = true
	}

	for _, h := range hostnames {
		if !sans[h] {
			return false, nil
		}
	}
	return true, nil
}
