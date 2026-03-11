package certs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CARoot returns the mkcert CA root directory.
func CARoot() (string, error) {
	out, err := exec.Command("mkcert", "-CAROOT").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("mkcert -CAROOT: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// CAFiles returns the paths to the mkcert root CA cert and key.
func CAFiles() (certPath, keyPath string, err error) {
	root, err := CARoot()
	if err != nil {
		return "", "", err
	}
	certPath = filepath.Join(root, "rootCA.pem")
	keyPath = filepath.Join(root, "rootCA-key.pem")

	if _, err := os.Stat(certPath); err != nil {
		return "", "", fmt.Errorf("CA cert not found at %s: %w", certPath, err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		return "", "", fmt.Errorf("CA key not found at %s: %w", keyPath, err)
	}
	return certPath, keyPath, nil
}

// ReadCAFiles reads the mkcert CA certificate and key bytes.
func ReadCAFiles() (cert, key []byte, err error) {
	certPath, keyPath, err := CAFiles()
	if err != nil {
		return nil, nil, err
	}
	cert, err = os.ReadFile(certPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read CA cert: %w", err)
	}
	key, err = os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read CA key: %w", err)
	}
	return cert, key, nil
}
