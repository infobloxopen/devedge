//go:build darwin

package dns

import (
	"fmt"
	"os"
	"path/filepath"
)

const resolverDir = "/etc/resolver"

// InstallResolverConfig creates a macOS /etc/resolver/ drop-in that tells the
// system resolver to send all queries for the given domain to loopback. This
// enables wildcard resolution without a custom DNS server — the system
// resolver handles the forwarding.
//
// This is supplementary to /etc/hosts management. The resolver drop-in
// provides wildcard support for hostnames not yet explicitly registered,
// while /etc/hosts provides immediate resolution without any listener.
func InstallResolverConfig(domain string) error {
	if err := os.MkdirAll(resolverDir, 0755); err != nil {
		return fmt.Errorf("create resolver dir: %w", err)
	}

	content := fmt.Sprintf("# Managed by devedge — do not edit\nnameserver 127.0.0.1\nport 15353\n")
	path := filepath.Join(resolverDir, domain)

	return os.WriteFile(path, []byte(content), 0644)
}

// RemoveResolverConfig removes the macOS resolver drop-in for the given domain.
func RemoveResolverConfig(domain string) error {
	path := filepath.Join(resolverDir, domain)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// HasResolverConfig checks if the macOS resolver drop-in exists.
func HasResolverConfig(domain string) bool {
	path := filepath.Join(resolverDir, domain)
	_, err := os.Stat(path)
	return err == nil
}
