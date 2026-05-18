package platform

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/infobloxopen/devedge/internal/certs"
	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/internal/dns"
	"github.com/infobloxopen/devedge/internal/dnsserver"
	"github.com/infobloxopen/devedge/pkg/types"
	mdns "github.com/miekg/dns"
)

// CheckResult represents a single diagnostic check.
type CheckResult struct {
	Name    string
	Passed  bool
	Message string
}

// doctorDNSAddr is the DNS endpoint the doctor probes. Overridable for
// tests; defaults to the configured daemon DNS address.
var doctorDNSAddr = daemon.DefaultDNSAddr()

// doctorResolverFactory builds the *net.Resolver used by the
// system-resolver probe. Overridable for tests.
var doctorResolverFactory = func() *net.Resolver { return net.DefaultResolver }

// RunDoctor performs a series of health checks and returns the results.
func RunDoctor() []CheckResult {
	var results []CheckResult

	results = append(results, checkMkcert())
	results = append(results, checkMkcertCA())
	results = append(results, checkDaemonSocket())
	results = append(results, checkPort(80))
	results = append(results, checkPort(443))
	results = append(results, checkDNSEndpoint())
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

// checkDNSEndpoint sends a synthetic A query directly to the DNS
// endpoint over UDP and expects a response within 250 ms. This isolates
// "DNS endpoint not responding" from "/etc/resolver/ misconfigured" so
// the user-facing message points at the actual fault. (Spec FR-006/FR-007.)
func checkDNSEndpoint() CheckResult {
	addr := doctorDNSAddr
	c := new(mdns.Client)
	c.Net = "udp"
	c.Timeout = 250 * time.Millisecond
	m := new(mdns.Msg)
	m.SetQuestion(mdns.Fqdn("devedge-healthcheck.dev.test"), mdns.TypeA)
	_, _, err := c.Exchange(m, addr)
	if err != nil {
		return CheckResult{
			"DNS endpoint",
			false,
			fmt.Sprintf("not responding on %s/udp (devedged not running, port in use, or DNS server not started): %v", addr, err),
		}
	}
	return CheckResult{
		"DNS endpoint",
		true,
		fmt.Sprintf("UDP responsive on %s", addr),
	}
}

// checkDNS performs an end-to-end resolution probe via the system
// resolver. The probe round-trips through the OS resolver framework,
// which on macOS reads /etc/resolver/<suffix> and forwards to the
// devedge DNS endpoint. It validates the full path (FR-006).
//
// On failure the message distinguishes "resolver returned no answer"
// from "resolver returned a non-loopback address" so the operator can
// see whether the system resolver is reaching us at all.
func checkDNS() CheckResult {
	suffixes := suffixesForProbe()
	if len(suffixes) == 0 {
		return CheckResult{
			"DNS *.dev.test",
			false,
			"no DNS suffix configured (run `de install` first)",
		}
	}
	resolver := doctorResolverFactory()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	suffix := suffixes[0]
	target := "devedge-healthcheck." + suffix
	name := "DNS *." + suffix

	addrs, err := resolver.LookupHost(ctx, target)
	if err != nil {
		return CheckResult{name, false, fmt.Sprintf("system resolver failed: %v", err)}
	}
	if len(addrs) == 0 {
		return CheckResult{name, false, "system resolver returned no addresses"}
	}
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil {
			continue
		}
		if ip.IsLoopback() {
			return CheckResult{name, true, fmt.Sprintf("resolves to %s via system resolver", a)}
		}
	}
	return CheckResult{name, false, fmt.Sprintf("resolves to %v, expected loopback (EdgeIP=%s)", addrs, types.EdgeIP)}
}

// suffixesForProbe returns the suffixes the doctor probe should test.
// Falls back to "dev.test" when the platform source is empty (e.g.
// /etc/resolver/ has not been written yet) so the message still names
// a concrete suffix instead of a generic "DNS" line.
func suffixesForProbe() []string {
	src := dnsserver.NewPlatformSuffixSource(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	list, err := src.List(ctx)
	if err != nil || len(list) == 0 {
		return []string{"dev.test"}
	}
	out := make([]string, 0, len(list))
	for _, cs := range list {
		out = append(out, cs.Name)
	}
	return out
}

func checkResolverConfig() CheckResult {
	if dns.HasResolverConfig("dev.test") {
		return CheckResult{"macOS resolver", true, "/etc/resolver/dev.test exists"}
	}
	return CheckResult{"macOS resolver", false, "not configured (optional)"}
}
