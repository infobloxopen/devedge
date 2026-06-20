package integration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/internal/dnsserver"
	"github.com/infobloxopen/devedge/pkg/types"
	"github.com/miekg/dns"
)

// pickEphemeralDNSAddr reserves a free loopback port by opening and
// closing both a TCP listener and a UDP socket. There is an unavoidable
// race between releasing the port and the daemon rebinding it, but it
// is small enough for tests on a developer or CI machine.
func pickEphemeralDNSAddr(t *testing.T) string {
	t.Helper()
	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve tcp: %v", err)
	}
	addr := tcpLn.Addr().String()
	tcpLn.Close()

	// Confirm UDP for the same port is also free.
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatalf("reserve udp: %v", err)
	}
	pc.Close()
	return addr
}

func startDNSDaemon(t *testing.T, suffixes ...string) (string, *dnsServerHandle) {
	t.Helper()

	tmpDir := t.TempDir()
	t.Setenv("DEVEDGE_HOME", tmpDir)

	socketPath := shortSocketPath(t)
	configDir := filepath.Join(tmpDir, "dynamic")
	hostsFile := filepath.Join(tmpDir, "hosts")
	_ = os.WriteFile(hostsFile, []byte("127.0.0.1\tlocalhost\n"), 0644)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	src := dnsserver.NewStaticSuffixSource(suffixes...)
	dnsAddr := pickEphemeralDNSAddr(t)

	srv := daemon.NewServer(
		daemon.WithSocketPath(socketPath),
		daemon.WithConfigDir(configDir),
		daemon.WithServerLogger(logger),
		daemon.WithHostsPath(hostsFile),
		daemon.WithTCPAddr("127.0.0.1:0"),
		daemon.WithDNSAddr(dnsAddr),
		daemon.WithDNSSuffixSource(src),
	)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	// Wait for the unix socket to indicate readiness.
	for i := 0; i < 200; i++ {
		if conn, err := net.Dial("unix", socketPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Wait until the DNS endpoint actually answers (the daemon binds
	// DNS in a goroutine after the unix socket is up).
	waitForDNS(t, dnsAddr, suffixes)

	h := &dnsServerHandle{
		cancel: cancel,
		done:   errCh,
	}
	t.Cleanup(h.shutdown)
	return dnsAddr, h
}

type dnsServerHandle struct {
	cancel context.CancelFunc
	done   chan error
}

func (h *dnsServerHandle) shutdown() {
	h.cancel()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
	}
}

func waitForDNS(t *testing.T, addr string, suffixes []string) {
	t.Helper()
	if len(suffixes) == 0 {
		// No suffix configured; just check the port is bound by
		// querying anything and expecting REFUSED.
		probeUntil(t, addr, "probe.test", dns.RcodeRefused, 3*time.Second)
		return
	}
	probeName := fmt.Sprintf("probe.%s", suffixes[0])
	probeUntil(t, addr, probeName, dns.RcodeSuccess, 3*time.Second)
}

func probeUntil(t *testing.T, addr, name string, wantRcode int, timeout time.Duration) {
	t.Helper()
	c := new(dns.Client)
	c.Net = "udp"
	c.Timeout = 250 * time.Millisecond
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), dns.TypeA)
	deadline := time.Now().Add(timeout)
	for {
		resp, _, err := c.Exchange(m, addr)
		if err == nil && resp.Rcode == wantRcode {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("DNS endpoint %s not ready: last err=%v rcode=%v", addr, err, rcodeOf(resp))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func rcodeOf(m *dns.Msg) int {
	if m == nil {
		return -1
	}
	return m.Rcode
}

func TestDNSServer_UDP_RoundTrip(t *testing.T) {
	addr, _ := startDNSDaemon(t, "dev.test")
	resp := exchange(t, addr, "udp", "service.dev.test", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode = %d, want NOERROR", resp.Rcode)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("answers = %d, want 1", len(resp.Answer))
	}
	a, ok := resp.Answer[0].(*dns.A)
	if !ok || !a.A.Equal(net.ParseIP(types.EdgeIP)) {
		t.Errorf("answer = %v, want %s", resp.Answer[0], types.EdgeIP)
	}
}

func TestDNSServer_TCP_RoundTrip(t *testing.T) {
	addr, _ := startDNSDaemon(t, "dev.test")
	resp := exchange(t, addr, "tcp", "service.dev.test", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode = %d, want NOERROR", resp.Rcode)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("answers = %d, want 1", len(resp.Answer))
	}
	a, ok := resp.Answer[0].(*dns.A)
	if !ok || !a.A.Equal(net.ParseIP(types.EdgeIP)) {
		t.Errorf("answer = %v, want %s", resp.Answer[0], types.EdgeIP)
	}
}

func TestDNSServer_UDP_TCP_Concurrent(t *testing.T) {
	addr, _ := startDNSDaemon(t, "dev.test")

	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			resp := exchange(t, addr, "udp", fmt.Sprintf("u%d.dev.test", i), dns.TypeA)
			if resp.Rcode != dns.RcodeSuccess {
				errs <- errors.New("udp non-success")
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			resp := exchange(t, addr, "tcp", fmt.Sprintf("t%d.dev.test", i), dns.TypeA)
			if resp.Rcode != dns.RcodeSuccess {
				errs <- errors.New("tcp non-success")
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent: %v", err)
	}
}

// US3: wildcard semantics through the full daemon path. A name that
// was never registered as a route still resolves to EdgeIP because the
// DNS handler is wildcard-by-design — it consults the suffix set, not
// the route registry.
func TestDNSServer_WildcardForUnregisteredName(t *testing.T) {
	addr, _ := startDNSDaemon(t, "dev.test")

	resp := exchange(t, addr, "udp", "never-registered-anywhere.dev.test", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("wildcard query rcode = %d, want NOERROR", resp.Rcode)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("answers = %d, want 1", len(resp.Answer))
	}
	a := resp.Answer[0].(*dns.A)
	if !a.A.Equal(net.ParseIP(types.EdgeIP)) {
		t.Errorf("wildcard answer = %s, want %s", a.A, types.EdgeIP)
	}
}

func TestSuffixSet_AddRemovePropagatesWithoutRestart(t *testing.T) {
	src := dnsserver.NewStaticSuffixSource("dev.test")

	tmpDir := t.TempDir()
	t.Setenv("DEVEDGE_HOME", tmpDir)
	socketPath := shortSocketPath(t)
	hostsFile := filepath.Join(tmpDir, "hosts")
	_ = os.WriteFile(hostsFile, []byte("127.0.0.1\tlocalhost\n"), 0644)
	dnsAddr := pickEphemeralDNSAddr(t)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	srv := daemon.NewServer(
		daemon.WithSocketPath(socketPath),
		daemon.WithConfigDir(filepath.Join(tmpDir, "dynamic")),
		daemon.WithServerLogger(logger),
		daemon.WithHostsPath(hostsFile),
		daemon.WithTCPAddr("127.0.0.1:0"),
		daemon.WithDNSAddr(dnsAddr),
		daemon.WithDNSSuffixSource(src),
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	for i := 0; i < 200; i++ {
		if conn, err := net.Dial("unix", socketPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitForDNS(t, dnsAddr, []string{"dev.test"})

	// Pre-update: new.test is REFUSED.
	resp := exchange(t, dnsAddr, "udp", "x.new.test", dns.TypeA)
	if resp.Rcode != dns.RcodeRefused {
		t.Fatalf("pre-update x.new.test rcode = %d, want REFUSED", resp.Rcode)
	}

	src.Set([]string{"dev.test", "new.test"})

	// The default poll interval is 5s; allow up to 8s.
	deadline := time.Now().Add(8 * time.Second)
	for {
		resp = exchange(t, dnsAddr, "udp", "x.new.test", dns.TypeA)
		if resp.Rcode == dns.RcodeSuccess {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("new.test never became authoritative after Set(); last rcode=%d", resp.Rcode)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Remove dev.test; it should stop answering.
	src.Set([]string{"new.test"})
	deadline = time.Now().Add(8 * time.Second)
	for {
		resp = exchange(t, dnsAddr, "udp", "x.dev.test", dns.TypeA)
		if resp.Rcode == dns.RcodeRefused {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dev.test still authoritative after removal; last rcode=%d", resp.Rcode)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func exchange(t *testing.T, addr, transport, name string, qtype uint16) *dns.Msg {
	t.Helper()
	c := new(dns.Client)
	c.Net = transport
	c.Timeout = 2 * time.Second
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	resp, _, err := c.Exchange(m, addr)
	if err != nil {
		t.Fatalf("%s exchange %s: %v", transport, name, err)
	}
	return resp
}
