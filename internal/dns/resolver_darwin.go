//go:build darwin

package dns

import (
	"fmt"
	"os"
	"path/filepath"
)

const resolverDir = "/etc/resolver"

// InstallResolverConfig creates a macOS /etc/resolver/ drop-in that tells the
// system resolver to send all queries for the given domain to the
// devedge in-process DNS endpoint on 127.0.0.1:15354. mDNSResponder
// reads this file and routes queries for the domain to the configured
// nameserver+port, enabling wildcard resolution for any hostname in
// the suffix.
//
// The HTTP admin API on 127.0.0.1:15353 is unrelated and continues to
// serve only HTTP. The DNS endpoint is bound on a dedicated port so the
// two never collide.
func InstallResolverConfig(domain string) error {
	if err := os.MkdirAll(resolverDir, 0755); err != nil {
		return fmt.Errorf("create resolver dir: %w", err)
	}

	content := "# Managed by devedge — do not edit\nnameserver 127.0.0.1\nport 15354\n"
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
