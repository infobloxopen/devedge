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

// withToolchainURL overrides the toolchain endpoint base URL for a test.
func withToolchainURL(t *testing.T, url string) {
	t.Helper()
	prev := doctorToolchainBaseURL
	doctorToolchainBaseURL = url
	t.Cleanup(func() { doctorToolchainBaseURL = prev })
}

func TestCheckDaemonToolchain_ToolsFound(t *testing.T) {
	resp := daemon.ToolchainResponse{
		Tools: []daemon.ToolInfo{
			{Name: "helm", Found: true, Path: "/opt/homebrew/bin/helm"},
			{Name: "kubectl", Found: true, Path: "/Users/u/.rd/bin/kubectl"},
			{Name: "k3d", Found: true, Path: "/usr/local/bin/k3d"},
		},
		PathSearched: "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/doctor/toolchain" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	withToolchainURL(t, srv.URL)

	results := checkDaemonToolchain("")
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d: %v", len(results), results)
	}
	for _, r := range results {
		if !r.Passed {
			t.Errorf("result %q unexpectedly failed: %s", r.Name, r.Message)
		}
	}
}

func TestCheckDaemonToolchain_ToolMissing(t *testing.T) {
	resp := daemon.ToolchainResponse{
		Tools: []daemon.ToolInfo{
			{Name: "helm", Found: true, Path: "/opt/homebrew/bin/helm"},
			{Name: "kubectl", Found: false, Path: ""},
		},
		PathSearched: "/usr/bin:/bin",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	withToolchainURL(t, srv.URL)

	results := checkDaemonToolchain("")
	passed := 0
	failed := 0
	for _, r := range results {
		if r.Passed {
			passed++
		} else {
			failed++
			if !strings.Contains(r.Message, "PATH=") {
				t.Errorf("fail message %q must include PATH= to show searched path", r.Message)
			}
		}
	}
	if passed != 1 || failed != 1 {
		t.Errorf("want 1 pass + 1 fail, got %d pass + %d fail", passed, failed)
	}
}

func TestCheckDaemonToolchain_DaemonOffline(t *testing.T) {
	// Point at a URL that refuses connections (no server running).
	withToolchainURL(t, "http://127.0.0.1:1")

	results := checkDaemonToolchain("")
	if len(results) != 1 {
		t.Fatalf("want 1 skipped result, got %d", len(results))
	}
	if results[0].Passed {
		t.Error("offline daemon should not report Passed")
	}
	if !strings.Contains(results[0].Message, "skipped") {
		t.Errorf("message %q should mention 'skipped'", results[0].Message)
	}
}
