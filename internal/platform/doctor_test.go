package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/pkg/types"
	mdns "github.com/miekg/dns"
)

// withDNSAddr sets the doctor's DNS endpoint addr for the duration of t.
func withDNSAddr(t *testing.T, addr string) {
	t.Helper()
	prev := doctorDNSAddr
	doctorDNSAddr = addr
	t.Cleanup(func() { doctorDNSAddr = prev })
}

// startEphemeralDNS spins up a miekg/dns UDP server that answers A
// queries with EdgeIP and returns its addr. Used to back the endpoint
// probe with a real listener.
func startEphemeralDNS(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	srv := &mdns.Server{
		PacketConn: pc,
		Handler: mdns.HandlerFunc(func(w mdns.ResponseWriter, req *mdns.Msg) {
			resp := new(mdns.Msg)
			resp.SetReply(req)
			if len(req.Question) == 1 && req.Question[0].Qtype == mdns.TypeA {
				resp.Answer = []mdns.RR{&mdns.A{
					Hdr: mdns.RR_Header{
						Name:   req.Question[0].Name,
						Rrtype: mdns.TypeA,
						Class:  mdns.ClassINET,
						Ttl:    60,
					},
					A: net.ParseIP(types.EdgeIP),
				}}
			}
			_ = w.WriteMsg(resp)
		}),
	}
	done := make(chan struct{})
	go func() {
		_ = srv.ActivateAndServe()
		close(done)
	}()
	t.Cleanup(func() {
		_ = srv.Shutdown()
		<-done
	})
	return pc.LocalAddr().String()
}

func TestCheckDNSEndpoint_HealthyListener_ReportsSuccess(t *testing.T) {
	addr := startEphemeralDNS(t)
	withDNSAddr(t, addr)

	r := checkDNSEndpoint()
	if !r.Passed {
		t.Errorf("checkDNSEndpoint failed: %s", r.Message)
	}
	if !strings.Contains(r.Message, addr) {
		t.Errorf("message %q does not include the addr %q", r.Message, addr)
	}
}

func TestCheckDNSEndpoint_NoListener_ReportsFailureWithAddr(t *testing.T) {
	// Reserve a port, then close, so nothing answers there.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	withDNSAddr(t, addr)

	r := checkDNSEndpoint()
	if r.Passed {
		t.Fatalf("checkDNSEndpoint unexpectedly passed: %s", r.Message)
	}
	if !strings.Contains(r.Message, addr) {
		t.Errorf("failure message %q must mention addr %q", r.Message, addr)
	}
	if !strings.Contains(r.Message, "/udp") {
		t.Errorf("failure message %q must mention transport (/udp)", r.Message)
	}
}

// stubResolver returns a *net.Resolver whose Dial routes through the
// in-process miekg/dns server bound at addr.
func stubResolver(addr string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", addr)
		},
	}
}

func withResolverFactory(t *testing.T, f func() *net.Resolver) {
	t.Helper()
	prev := doctorResolverFactory
	doctorResolverFactory = f
	t.Cleanup(func() { doctorResolverFactory = prev })
}

func TestCheckDNSSystemResolver_ResolvesToEdgeIP_Passes(t *testing.T) {
	addr := startEphemeralDNS(t)
	withResolverFactory(t, func() *net.Resolver { return stubResolver(addr) })

	r := checkDNS()
	if !r.Passed {
		t.Fatalf("checkDNS failed: %s", r.Message)
	}
	if !strings.Contains(r.Message, types.EdgeIP) {
		t.Errorf("success message %q must include %s", r.Message, types.EdgeIP)
	}
}

func TestCheckDNSSystemResolver_NoAddrs_Fails(t *testing.T) {
	withResolverFactory(t, func() *net.Resolver {
		return &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return nil, errors.New("synthetic dial failure")
			},
		}
	})

	r := checkDNS()
	if r.Passed {
		t.Fatalf("checkDNS unexpectedly passed: %s", r.Message)
	}
	if !strings.Contains(r.Message, "system resolver failed") {
		t.Errorf("failure message %q must identify system resolver as failing", r.Message)
	}
}

func TestCheckDNSSystemResolver_NonLoopbackAddr_Fails(t *testing.T) {
	// Spin up a server that returns a non-loopback A record.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	srv := &mdns.Server{
		PacketConn: pc,
		Handler: mdns.HandlerFunc(func(w mdns.ResponseWriter, req *mdns.Msg) {
			resp := new(mdns.Msg)
			resp.SetReply(req)
			resp.Answer = []mdns.RR{&mdns.A{
				Hdr: mdns.RR_Header{
					Name:   req.Question[0].Name,
					Rrtype: mdns.TypeA,
					Class:  mdns.ClassINET,
					Ttl:    60,
				},
				A: net.ParseIP("203.0.113.5"),
			}}
			_ = w.WriteMsg(resp)
		}),
	}
	done := make(chan struct{})
	go func() {
		_ = srv.ActivateAndServe()
		close(done)
	}()
	t.Cleanup(func() {
		_ = srv.Shutdown()
		<-done
	})

	addr := pc.LocalAddr().String()
	withResolverFactory(t, func() *net.Resolver { return stubResolver(addr) })

	r := checkDNS()
	if r.Passed {
		t.Fatalf("checkDNS unexpectedly passed with non-loopback answer: %s", r.Message)
	}
	if !strings.Contains(r.Message, "expected loopback") {
		t.Errorf("failure message %q must explain the loopback expectation", r.Message)
	}
}

func TestCheckDNSEndpoint_Timeout(t *testing.T) {
	// Sanity check: the probe respects its own timeout.
	start := time.Now()
	withDNSAddr(t, "127.0.0.1:1") // unlikely-to-respond port
	_ = checkDNSEndpoint()
	if elapsed := time.Since(start); elapsed > 750*time.Millisecond {
		t.Errorf("probe took %v, expected under 750ms", elapsed)
	}
}

// withStatusURL overrides the daemon status base URL for a test.
func withStatusURL(t *testing.T, url string) {
	t.Helper()
	prev := doctorStatusBaseURL
	doctorStatusBaseURL = url
	t.Cleanup(func() { doctorStatusBaseURL = prev })
}

// serveStatus spins up an httptest server answering GET /v1/status with body.
func serveStatus(t *testing.T, body any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(body) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckProxyTLS_Mkcert_Passes(t *testing.T) {
	srv := serveStatus(t, map[string]any{
		"status": "running",
		"routes": 3,
		"tls":    daemon.TLSStatus{Mode: "mkcert", CARoot: "/Users/u/Library/Application Support/mkcert"},
	})
	withStatusURL(t, srv.URL)

	r := checkProxyTLS("")
	if !r.Passed {
		t.Fatalf("checkProxyTLS failed: %s", r.Message)
	}
	if !strings.Contains(r.Message, "mkcert") {
		t.Errorf("message %q should name the mkcert CA", r.Message)
	}
}

func TestCheckProxyTLS_SelfSigned_Fails(t *testing.T) {
	reason := "CA cert not found at /var/root/Library/Application Support/mkcert/rootCA.pem"
	srv := serveStatus(t, map[string]any{
		"status": "running",
		"routes": 3,
		"tls":    daemon.TLSStatus{Mode: "self-signed", Reason: reason},
	})
	withStatusURL(t, srv.URL)

	r := checkProxyTLS("")
	if r.Passed {
		t.Fatalf("checkProxyTLS unexpectedly passed: %s", r.Message)
	}
	if !strings.Contains(r.Message, "SELF-SIGNED") {
		t.Errorf("failure message %q must call out the self-signed CA", r.Message)
	}
	if !strings.Contains(r.Message, reason) {
		t.Errorf("failure message %q must include the fallback reason", r.Message)
	}
	if !strings.Contains(r.Message, "de install") {
		t.Errorf("failure message %q must point at the fix ('de install')", r.Message)
	}
}

func TestCheckProxyTLS_DaemonOffline_Skips(t *testing.T) {
	// Reserve a port, then close, so nothing answers there.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	url := "http://" + ln.Addr().String()
	ln.Close()
	withStatusURL(t, url)

	r := checkProxyTLS("")
	// Offline is not a FAIL — checkDaemonSocket already covers that case.
	if !r.Passed {
		t.Errorf("offline daemon should report Passed=true (skipped), got: %s", r.Message)
	}
	if !strings.Contains(r.Message, "skipped") {
		t.Errorf("message %q should mention 'skipped'", r.Message)
	}
}

func TestCheckProxyTLS_OldDaemonWithoutTLSField_Skips(t *testing.T) {
	srv := serveStatus(t, map[string]any{"status": "running", "routes": 2})
	withStatusURL(t, srv.URL)

	r := checkProxyTLS("")
	if !r.Passed {
		t.Errorf("daemon without tls report should be skipped, got FAIL: %s", r.Message)
	}
	if !strings.Contains(r.Message, "skipped") {
		t.Errorf("message %q should mention 'skipped'", r.Message)
	}
}

// withToolchainURL overrides the toolchain endpoint base URL for a test.
func withToolchainURL(t *testing.T, url string) {
	t.Helper()
	prev := doctorToolchainBaseURL
	doctorToolchainBaseURL = url
	t.Cleanup(func() { doctorToolchainBaseURL = prev })
}

// serveToolchain spins up an httptest server answering GET /v1/doctor/toolchain.
func serveToolchain(t *testing.T, resp daemon.ToolchainResponse) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/doctor/toolchain" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckDaemonToolchain_ToolsFound(t *testing.T) {
	srv := serveToolchain(t, daemon.ToolchainResponse{
		Tools: []daemon.ToolInfo{
			{Name: "helm", Found: true, Path: "/opt/homebrew/bin/helm"},
			{Name: "kubectl", Found: true, Path: "/Users/u/.rd/bin/kubectl"},
			{Name: "docker", Found: true, Path: "/usr/local/bin/docker"},
		},
		PathSearched: "/Users/u/.rd/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin",
	})
	withToolchainURL(t, srv.URL)

	results := checkDaemonToolchain("")
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d: %v", len(results), results)
	}
	for _, r := range results {
		if !r.Passed {
			t.Errorf("result %q unexpectedly failed: %s", r.Name, r.Message)
		}
		if r.Message == "" {
			t.Errorf("result %q should report the resolved path", r.Name)
		}
	}
}

func TestCheckDaemonToolchain_ToolMissing(t *testing.T) {
	srv := serveToolchain(t, daemon.ToolchainResponse{
		Tools: []daemon.ToolInfo{
			{Name: "helm", Found: false},
			{Name: "kubectl", Found: true, Path: "/usr/bin/kubectl"},
		},
		PathSearched: "/usr/bin:/bin:/usr/sbin:/sbin",
	})
	withToolchainURL(t, srv.URL)

	results := checkDaemonToolchain("")
	var passed, failed int
	for _, r := range results {
		if r.Passed {
			passed++
			continue
		}
		failed++
		if !strings.Contains(r.Message, "PATH=/usr/bin:/bin:/usr/sbin:/sbin") {
			t.Errorf("failure message %q must show the daemon's searched PATH", r.Message)
		}
		if !strings.Contains(r.Message, "de install") {
			t.Errorf("failure message %q must point at the fix ('de install')", r.Message)
		}
	}
	if passed != 1 || failed != 1 {
		t.Errorf("want 1 pass + 1 fail, got %d pass + %d fail", passed, failed)
	}
}

func TestCheckDaemonToolchain_DaemonOffline_Skips(t *testing.T) {
	// Reserve a port, then close, so nothing answers there.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	url := "http://" + ln.Addr().String()
	ln.Close()
	withToolchainURL(t, url)

	results := checkDaemonToolchain("")
	if len(results) != 1 {
		t.Fatalf("want 1 skipped result, got %d", len(results))
	}
	// Offline is not a FAIL — checkDaemonSocket already covers that case.
	if !results[0].Passed {
		t.Errorf("offline daemon should report Passed=true (skipped), got: %s", results[0].Message)
	}
	if !strings.Contains(results[0].Message, "skipped") {
		t.Errorf("message %q should mention 'skipped'", results[0].Message)
	}
}

func TestCheckDaemonToolchain_OldDaemonWithoutEndpoint_Skips(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // daemon predates GET /v1/doctor/toolchain
	}))
	t.Cleanup(srv.Close)
	withToolchainURL(t, srv.URL)

	results := checkDaemonToolchain("")
	if len(results) != 1 {
		t.Fatalf("want 1 skipped result, got %d", len(results))
	}
	if !results[0].Passed || !strings.Contains(results[0].Message, "skipped") {
		t.Errorf("old daemon should be skipped, got Passed=%v %q", results[0].Passed, results[0].Message)
	}
}
