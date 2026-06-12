package certs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// caRootRecordName is the file under the devedge home dir where `de install`
// records the absolute mkcert CAROOT resolved in the installing user's context.
const caRootRecordName = "caroot"

// CARoot returns the mkcert CA root directory. Resolution order:
//
//  1. DEVEDGE_CAROOT / CAROOT environment overrides. An explicit override
//     that does not contain rootCA.pem is an error, never a silent fallback.
//  2. The CAROOT recorded by `de install` under the devedge home dir. The
//     daemon runs as root under launchd ($HOME=/var/root), so deriving the
//     CAROOT from the runtime $HOME finds nothing there — the install-time
//     record points it at the installing user's CA (issue #8).
//  3. `mkcert -CAROOT`, only when the reported directory actually holds
//     rootCA.pem (under sudo, mkcert reports root's empty CAROOT).
//  4. Well-known platform locations.
func CARoot() (string, error) {
	return resolveCARoot(false)
}

// resolveCARoot implements CARoot. When ignoreRecord is true the install-time
// record is skipped, so callers that are about to (re)write the record resolve
// the live CA location instead of echoing a possibly stale record back.
func resolveCARoot(ignoreRecord bool) (string, error) {
	// Explicit overrides win and must be valid.
	for _, env := range []string{"DEVEDGE_CAROOT", "CAROOT"} {
		if dir := os.Getenv(env); dir != "" {
			if !hasRootCA(dir) {
				return "", fmt.Errorf("%s=%s is set but rootCA.pem was not found there; run 'mkcert -install' or fix the override", env, dir)
			}
			return dir, nil
		}
	}

	// CAROOT recorded by `de install` in the installing user's context.
	// A record whose CA has since disappeared falls through to live discovery.
	if !ignoreRecord {
		if dir := recordedCARoot(); dir != "" && hasRootCA(dir) {
			return dir, nil
		}
	}

	// Ask the mkcert binary. Verify the answer: when invoked as root (sudo,
	// launchd) mkcert derives its CAROOT from root's $HOME, where no CA lives.
	if mkcert, err := findMkcert(); err == nil {
		out, err := exec.Command(mkcert, "-CAROOT").CombinedOutput()
		if err == nil {
			if root := strings.TrimSpace(string(out)); root != "" && hasRootCA(root) {
				return root, nil
			}
		}
	}

	// Fall back to well-known locations.
	candidates := mkcertCARootCandidates()
	for _, dir := range candidates {
		if hasRootCA(dir) {
			return dir, nil
		}
	}

	return "", fmt.Errorf("mkcert CA root not found; run 'mkcert -install' first")
}

// hasRootCA reports whether dir contains a rootCA.pem.
func hasRootCA(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "rootCA.pem"))
	return err == nil
}

// CARootRecordPath returns the path of the install-time CAROOT record under
// the devedge home dir. The home-dir derivation mirrors the daemon's
// devedgeDir() (this package cannot import internal/daemon: daemon imports
// certs).
func CARootRecordPath() string {
	return filepath.Join(devedgeHome(), caRootRecordName)
}

// devedgeHome returns the base directory for devedge state: $DEVEDGE_HOME
// when set (the LaunchDaemon plist sets it), the invoking user's home under
// sudo, the current user's home otherwise.
func devedgeHome() string {
	if dir := os.Getenv("DEVEDGE_HOME"); dir != "" {
		return dir
	}
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		return filepath.Join("/Users", sudoUser, ".devedge")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".devedge")
}

// recordedCARoot returns the CAROOT recorded by `de install`, or "" when no
// record exists.
func recordedCARoot() string {
	data, err := os.ReadFile(CARootRecordPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// PersistCARoot resolves the mkcert CAROOT in the calling context (the
// installing user's) and records it under the devedge home dir, so the
// daemon — which runs as root with a different $HOME — keeps resolving the
// same CA across restarts. Returns the recorded directory.
func PersistCARoot() (string, error) {
	root, err := resolveCARoot(true)
	if err != nil {
		return "", err
	}
	path := CARootRecordPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("create devedge home: %w", err)
	}
	if err := os.WriteFile(path, []byte(root+"\n"), 0644); err != nil {
		return "", fmt.Errorf("record CAROOT: %w", err)
	}
	return root, nil
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
