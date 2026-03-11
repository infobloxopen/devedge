// Package certs manages locally-trusted TLS certificates for devedge
// hostnames using mkcert as the underlying tool.
package certs

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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

// EnsureCA checks that the mkcert local CA is installed. Returns an error
// if mkcert is not available or the CA is not installed.
func EnsureCA() error {
	if _, err := exec.LookPath("mkcert"); err != nil {
		return fmt.Errorf("mkcert not found in PATH: %w", err)
	}
	// Check CAROOT is set up.
	out, err := exec.Command("mkcert", "-CAROOT").CombinedOutput()
	if err != nil {
		return fmt.Errorf("mkcert -CAROOT failed: %w", err)
	}
	caRoot := strings.TrimSpace(string(out))
	if caRoot == "" {
		return fmt.Errorf("mkcert CA root is empty; run 'mkcert -install' first")
	}
	certPath := filepath.Join(caRoot, "rootCA.pem")
	if _, err := os.Stat(certPath); err != nil {
		return fmt.Errorf("mkcert CA not found at %s; run 'mkcert -install' first", certPath)
	}
	return nil
}

// InstallCA runs 'mkcert -install' to install the local CA into system
// and browser trust stores.
func InstallCA() error {
	cmd := exec.Command("mkcert", "-install")
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
// does not already exist or if the hostname set has changed. Returns the
// paths to the cert and key files.
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

	args := make([]string, 0, len(sorted)+2)
	args = append(args, "-cert-file", certFile, "-key-file", keyFile)
	args = append(args, sorted...)

	cmd := exec.Command("mkcert", args...)
	cmd.Dir = m.certsDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("mkcert failed: %w\noutput: %s", err, out)
	}

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
