package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
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

// doctorStatusBaseURL is the base URL used by checkProxyTLS. Empty string
// means "use the Unix domain socket at the default daemon socket path".
// Overridden in tests to point at an httptest.Server.
var doctorStatusBaseURL = ""

// doctorToolchainBaseURL is the base URL used by checkDaemonToolchain. Empty
// string means "use the Unix domain socket at the default daemon socket
// path". Overridden in tests to point at an httptest.Server.
var doctorToolchainBaseURL = ""

// RunDoctor performs a series of health checks and returns the results.
func RunDoctor() []CheckResult {
	var results []CheckResult

	results = append(results, checkMkcert())
	results = append(results, checkMkcertCA())
	results = append(results, checkDaemonSocket())
	results = append(results, checkProxyTLS(daemon.DefaultSocketPath()))
	results = append(results, checkEdgeIP())
	results = append(results, checkEdgeListening())
	results = append(results, checkDNSEndpoint())
	results = append(results, checkDNS())
	results = append(results, checkResolverConfig())
	results = append(results, checkDaemonToolchain(daemon.DefaultSocketPath())...)

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

// checkEdgeIP verifies the EdgeIP (127.0.0.2) is routable — i.e. the loopback
// alias exists. It binds an ephemeral port on the EdgeIP (needs no privilege,
// does not conflict with the running edge proxy on :443). If the alias is
// missing, net.Listen fails with "can't assign requested address" and the edge
// proxy cannot bind, so nothing serves. This replaces the old checkPort(80/443)
// which reported a FREE port as "available" — i.e. passed precisely when the
// edge was down.
func checkEdgeIP() CheckResult { return checkEdgeIPFor(types.EdgeIP) }

func checkEdgeIPFor(ip string) CheckResult {
	name := "edge IP " + ip
	ln, err := net.Listen("tcp", net.JoinHostPort(ip, "0"))
	if err != nil {
		if strings.Contains(err.Error(), "assign requested address") {
			return CheckResult{name, false, fmt.Sprintf(
				"%s not routable — loopback alias missing; run 'sudo de install' then 'de start' (the daemon adds it as root), or 'sudo ifconfig lo0 alias %s'",
				ip, ip)}
		}
		// Some other error (e.g. transient) — can't conclude the alias is missing.
		return CheckResult{name, true, fmt.Sprintf("assumed routable (%v)", err)}
	}
	ln.Close()
	return CheckResult{name, true, fmt.Sprintf("%s routable (loopback alias present)", ip)}
}

// checkEdgeListening verifies something is actually listening on the EdgeIP's
// :443 — i.e. the edge proxy is up and serving. A daemon can report "running"
// while its proxy failed to bind (the error is logged, not fatal), so this is
// the check that catches a dead edge behind a live daemon.
func checkEdgeListening() CheckResult {
	return checkEdgeListeningFor(net.JoinHostPort(types.EdgeIP, "443"))
}

func checkEdgeListeningFor(addr string) CheckResult {
	const name = "edge proxy :443"
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		if strings.Contains(err.Error(), "assign requested address") {
			return CheckResult{name, false, fmt.Sprintf(
				"%s not routable (loopback alias missing) — see the edge IP check", addr)}
		}
		return CheckResult{name, false, fmt.Sprintf(
			"nothing listening on %s — daemon stopped or the edge proxy failed to bind (check devedged logs): %v", addr, err)}
	}
	conn.Close()
	return CheckResult{name, true, fmt.Sprintf("listening on %s", addr)}
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

// checkProxyTLS asks the running daemon which CA the proxy signs TLS
// certificates with (GET /v1/status, "tls" block). The self-signed fallback
// means every browser rejects every routed host, so it is reported as a
// failure, not a warning (issue #8). A daemon that is offline or predates
// the TLS status report is skipped, not failed — checkDaemonSocket already
// covers the offline case.
func checkProxyTLS(socketPath string) CheckResult {
	const name = "proxy TLS CA"
	const timeout = 2 * time.Second

	// Build an HTTP client: a Unix-socket client in production, a TCP client
	// when doctorStatusBaseURL is overridden by tests.
	baseURL := doctorStatusBaseURL
	var httpClient *http.Client
	if baseURL == "" {
		baseURL = "http://devedge"
		httpClient = &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.DialTimeout("unix", socketPath, timeout)
				},
			},
		}
	} else {
		httpClient = &http.Client{Timeout: timeout}
	}

	req, err := http.NewRequestWithContext(context.Background(), "GET", baseURL+"/v1/status", nil)
	if err != nil {
		return CheckResult{name, false, fmt.Sprintf("build request: %v", err)}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return CheckResult{name, true, "skipped (daemon offline)"}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return CheckResult{name, false, fmt.Sprintf("unexpected status %d from daemon", resp.StatusCode)}
	}

	var st struct {
		TLS *daemon.TLSStatus `json:"tls"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return CheckResult{name, false, fmt.Sprintf("decode response: %v", err)}
	}
	if st.TLS == nil {
		return CheckResult{name, true, "skipped (daemon does not report TLS state — restart it after upgrading)"}
	}

	switch st.TLS.Mode {
	case "mkcert":
		msg := "mkcert CA"
		if st.TLS.CARoot != "" {
			msg = fmt.Sprintf("mkcert CA (%s)", st.TLS.CARoot)
		}
		return CheckResult{name, true, msg}
	case "self-signed":
		msg := "proxy is serving a SELF-SIGNED CA — browsers reject every routed host"
		if st.TLS.Reason != "" {
			msg += ": " + st.TLS.Reason
		}
		msg += "; run 'de install' to record the mkcert CAROOT (or set DEVEDGE_CAROOT) and restart the daemon"
		return CheckResult{name, false, msg}
	default:
		return CheckResult{name, false, fmt.Sprintf("unknown TLS mode %q reported by daemon", st.TLS.Mode)}
	}
}

// checkDaemonToolchain queries the daemon's GET /v1/doctor/toolchain endpoint
// so the doctor validates tool resolution from the daemon's vantage: launchd
// starts the daemon with the minimal system PATH, so the user's shell
// resolving helm/kubectl/docker says nothing about whether the daemon can
// exec them (issue #9). socketPath is the daemon's Unix domain socket; it is
// ignored when doctorToolchainBaseURL is overridden for tests. A daemon that
// is offline or predates the endpoint is skipped, not failed —
// checkDaemonSocket already covers the offline case.
func checkDaemonToolchain(socketPath string) []CheckResult {
	const name = "daemon toolchain"
	const timeout = 2 * time.Second

	baseURL := doctorToolchainBaseURL
	var httpClient *http.Client
	if baseURL == "" {
		baseURL = "http://devedge"
		httpClient = &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.DialTimeout("unix", socketPath, timeout)
				},
			},
		}
	} else {
		httpClient = &http.Client{Timeout: timeout}
	}

	req, err := http.NewRequestWithContext(context.Background(), "GET", baseURL+"/v1/doctor/toolchain", nil)
	if err != nil {
		return []CheckResult{{name, false, fmt.Sprintf("build request: %v", err)}}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return []CheckResult{{name, true, "skipped (daemon offline)"}}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return []CheckResult{{name, true, "skipped (daemon predates the toolchain check — restart it after upgrading)"}}
	}
	if resp.StatusCode != http.StatusOK {
		return []CheckResult{{name, false, fmt.Sprintf("unexpected status %d from daemon", resp.StatusCode)}}
	}

	var tc daemon.ToolchainResponse
	if err := json.NewDecoder(resp.Body).Decode(&tc); err != nil {
		return []CheckResult{{name, false, fmt.Sprintf("decode response: %v", err)}}
	}

	results := make([]CheckResult, 0, len(tc.Tools))
	for _, tool := range tc.Tools {
		toolName := "daemon tool: " + tool.Name
		if tool.Found {
			results = append(results, CheckResult{toolName, true, tool.Path})
		} else {
			results = append(results, CheckResult{toolName, false,
				fmt.Sprintf("not found in daemon PATH=%s — re-run 'de install' and reload the daemon (launchctl bootout/bootstrap; kickstart does not re-read plist env)", tc.PathSearched)})
		}
	}
	return results
}
