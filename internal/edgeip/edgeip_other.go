//go:build !darwin

package edgeip

// EnsureAlias is a no-op off macOS. On Linux the entire 127.0.0.0/8 block is
// bound to loopback, so the EdgeIP is already routable without an alias.
func EnsureAlias(ip string) (added bool, err error) {
	return false, nil
}
