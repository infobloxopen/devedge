package platform

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/infobloxopen/devedge/internal/certs"
	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/internal/dns"
)

// CheckResult represents a single diagnostic check.
type CheckResult struct {
	Name    string
	Passed  bool
	Message string
}

// RunDoctor performs a series of health checks and returns the results.
func RunDoctor() []CheckResult {
	var results []CheckResult

	results = append(results, checkMkcert())
	results = append(results, checkMkcertCA())
	results = append(results, checkDaemonSocket())
	results = append(results, checkPort(80))
	results = append(results, checkPort(443))
	results = append(results, checkDNS())
	results = append(results, checkResolverConfig())

	return results
}

func checkMkcert() CheckResult {
	_, err := exec.LookPath("mkcert")
	if err != nil {
		return CheckResult{"mkcert installed", false, "mkcert not found in PATH"}
	}
	return CheckResult{"mkcert installed", true, "found in PATH"}
}

func checkMkcertCA() CheckResult {
	err := certs.EnsureCA()
	if err != nil {
		return CheckResult{"mkcert CA", false, err.Error()}
	}
	return CheckResult{"mkcert CA", true, "local CA installed"}
}

func checkDaemonSocket() CheckResult {
	path := daemon.DefaultSocketPath()
	if _, err := os.Stat(path); err != nil {
		return CheckResult{"daemon socket", false, fmt.Sprintf("not found at %s", path)}
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		return CheckResult{"daemon socket", false, fmt.Sprintf("exists but not connectable: %v", err)}
	}
	conn.Close()
	return CheckResult{"daemon socket", true, "connectable"}
}

func checkPort(port int) CheckResult {
	name := fmt.Sprintf("port %d", port)
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		if strings.Contains(err.Error(), "address already in use") {
			return CheckResult{name, true, "in use (expected if devedged is running)"}
		}
		return CheckResult{name, false, err.Error()}
	}
	ln.Close()
	return CheckResult{name, true, "available"}
}

func checkDNS() CheckResult {
	addrs, err := net.LookupHost("devedge-healthcheck.dev.test")
	if err != nil {
		return CheckResult{"DNS *.dev.test", false, "resolution failed (expected if hosts not configured yet)"}
	}
	for _, a := range addrs {
		if a == "127.0.0.1" || a == "127.0.0.2" || a == "::1" {
			return CheckResult{"DNS *.dev.test", true, fmt.Sprintf("resolves to %s", a)}
		}
	}
	return CheckResult{"DNS *.dev.test", false, fmt.Sprintf("resolves to %v, expected loopback", addrs)}
}

func checkResolverConfig() CheckResult {
	if dns.HasResolverConfig("dev.test") {
		return CheckResult{"macOS resolver", true, "/etc/resolver/dev.test exists"}
	}
	return CheckResult{"macOS resolver", false, "not configured (optional)"}
}
