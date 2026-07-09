//go:build darwin

package edgeip

import (
	"fmt"
	"os/exec"
	"strings"
)

// EnsureAlias makes ip routable on lo0, returning whether it had to add the
// alias (false when it was already present). It is idempotent and requires
// root — the daemon runs as root under launchd, which is where this is meant
// to be called.
func EnsureAlias(ip string) (added bool, err error) {
	if Routable(ip) {
		return false, nil
	}
	out, err := exec.Command("ifconfig", "lo0", "alias", ip).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("ifconfig lo0 alias %s (needs root): %w: %s",
			ip, err, strings.TrimSpace(string(out)))
	}
	if !Routable(ip) {
		return false, fmt.Errorf("added lo0 alias %s but it is still not routable", ip)
	}
	return true, nil
}
