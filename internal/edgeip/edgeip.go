// Package edgeip ensures the devedge EdgeIP is routable on the loopback
// interface so the in-process edge proxy can bind it.
//
// The proxy listens on a dedicated loopback address (pkg/types.EdgeIP,
// 127.0.0.2) for ports 80/443. On macOS a secondary loopback address is NOT
// routable until it is added as an alias on lo0 ("ifconfig lo0 alias
// 127.0.0.2"), and that alias does not survive a reboot — so the daemon, which
// runs as root under launchd, re-ensures it on every startup. On Linux the
// whole 127.0.0.0/8 block is already loopback, so ensuring is a no-op.
package edgeip

import "net"

// Routable reports whether ip can be bound on this host right now — i.e. the
// loopback alias is present. It binds an ephemeral port (not 80/443), so it
// needs no privilege and does not conflict with a running edge proxy. Used by
// diagnostics to tell "alias missing" apart from "edge process down".
func Routable(ip string) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort(ip, "0"))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
