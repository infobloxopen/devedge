package certs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// CARoot returns the mkcert CA root directory. It first tries `mkcert -CAROOT`,
// then falls back to well-known platform locations so the daemon can find the
// CA even when mkcert is not in PATH (e.g. running as a LaunchDaemon).
func CARoot() (string, error) {
	// Try mkcert binary first.
	if mkcert, err := findMkcert(); err == nil {
		out, err := exec.Command(mkcert, "-CAROOT").CombinedOutput()
		if err == nil {
			if root := strings.TrimSpace(string(out)); root != "" {
				return root, nil
			}
		}
	}

	// Fall back to well-known locations.
	candidates := mkcertCARootCandidates()
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "rootCA.pem")); err == nil {
			return dir, nil
		}
	}

	return "", fmt.Errorf("mkcert CA root not found; run 'mkcert -install' first")
}

// findMkcert locates the mkcert binary, checking PATH and common locations.
func findMkcert() (string, error) {
	if p, err := exec.LookPath("mkcert"); err == nil {
		return p, nil
	}
	for _, p := range []string{"/opt/homebrew/bin/mkcert", "/usr/local/bin/mkcert"} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("mkcert not found")
}

// mkcertCARootCandidates returns platform-specific well-known directories
// where mkcert stores its CA.
func mkcertCARootCandidates() []string {
	home, _ := os.UserHomeDir()
	if home == "" {
		// When running as root LaunchDaemon, UserHomeDir returns /var/root.
		// Check the owning user's home from DEVEDGE_HOME.
		if dh := os.Getenv("DEVEDGE_HOME"); dh != "" {
			home = filepath.Dir(dh) // e.g. /Users/dgarcia/.devedge -> /Users/dgarcia
		}
	}

	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		if home != "" {
			candidates = append(candidates, filepath.Join(home, "Library", "Application Support", "mkcert"))
		}
		// Also check the real user if running as root.
		if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
			candidates = append(candidates, filepath.Join("/Users", sudoUser, "Library", "Application Support", "mkcert"))
		}
	case "linux":
		if home != "" {
			candidates = append(candidates, filepath.Join(home, ".local", "share", "mkcert"))
		}
	}
	return candidates
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
