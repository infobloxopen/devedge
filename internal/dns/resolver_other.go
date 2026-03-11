//go:build !darwin

package dns

// InstallResolverConfig is a no-op on non-macOS platforms.
// Linux and Windows rely solely on /etc/hosts management.
func InstallResolverConfig(domain string) error {
	return nil
}

// RemoveResolverConfig is a no-op on non-macOS platforms.
func RemoveResolverConfig(domain string) error {
	return nil
}

// HasResolverConfig always returns false on non-macOS platforms.
func HasResolverConfig(domain string) bool {
	return false
}
