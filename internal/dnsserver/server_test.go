package dnsserver

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func runServer(t *testing.T, src SuffixSource, opts ...Option) (*Server, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	all := append([]Option{WithAddr("127.0.0.1:0")}, opts...)
	s := New(src, all...)
	runErr := make(chan error, 1)
	go func() { runErr <- s.Run(ctx) }()

	// Wait for the server to bind (or fail).
	deadline := time.After(3 * time.Second)
	for {
		if s.BoundAddr() != "" {
			break
		}
		select {
		case err := <-runErr:
			t.Fatalf("Run returned before binding: %v", err)
		case <-deadline:
			t.Fatal("server did not bind within 3s")
		case <-time.After(20 * time.Millisecond):
		}
	}

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-runErr:
			if err != nil {
				t.Logf("Run returned err: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not shut down within 5s")
		}
	})

	return s, cancel
}

func dnsQueryUDP(t *testing.T, addr, name string, qtype uint16) *dns.Msg {
	t.Helper()
	c := new(dns.Client)
	c.Net = "udp"
	c.Timeout = 2 * time.Second
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	resp, _, err := c.Exchange(m, addr)
	if err != nil {
		t.Fatalf("UDP exchange %s: %v", name, err)
	}
	return resp
}

func dnsQueryTCP(t *testing.T, addr, name string, qtype uint16) *dns.Msg {
	t.Helper()
	c := new(dns.Client)
	c.Net = "tcp"
	c.Timeout = 2 * time.Second
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	resp, _, err := c.Exchange(m, addr)
	if err != nil {
		t.Fatalf("TCP exchange %s: %v", name, err)
	}
	return resp
}

func TestServer_Run_BindsBothTransports(t *testing.T) {
	src := NewStaticSuffixSource("dev.test")
	s, _ := runServer(t, src)

	resp := dnsQueryUDP(t, s.BoundAddr(), "foo.dev.test", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
		t.Errorf("UDP A in-suffix: rcode=%d answers=%v", resp.Rcode, resp.Answer)
	}

	resp = dnsQueryTCP(t, s.BoundAddr(), "foo.dev.test", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
		t.Errorf("TCP A in-suffix: rcode=%d answers=%v", resp.Rcode, resp.Answer)
	}
}

func TestServer_Run_ShutsDownOnContextCancel(t *testing.T) {
	src := NewStaticSuffixSource("dev.test")
	ctx, cancel := context.WithCancel(context.Background())
	s := New(src, WithAddr("127.0.0.1:0"))
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	for s.BoundAddr() == "" {
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned err on cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return within 3s of ctx cancel")
	}
}

func TestServer_Run_RejectsNonLoopbackAddr(t *testing.T) {
	src := NewStaticSuffixSource("dev.test")
	s := New(src, WithAddr("0.0.0.0:0"))
	err := s.Run(context.Background())
	if err == nil {
		t.Fatal("Run accepted non-loopback addr")
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Errorf("error doesn't mention loopback: %v", err)
	}
}

func TestServer_Run_BindFailureReturnsError(t *testing.T) {
	// Grab a port, hold it, then ask the server to bind it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	// Also hold the UDP side at the same port so the server cannot
	// bind UDP either.
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatalf("setup udp: %v", err)
	}
	defer pc.Close()

	src := NewStaticSuffixSource("dev.test")
	s := New(src, WithAddr(addr))
	if err := s.Run(context.Background()); err == nil {
		t.Fatal("Run succeeded despite port collision")
	}
}

func TestPollLoop_AppliesDiffs(t *testing.T) {
	src := NewStaticSuffixSource("dev.test")
	s, _ := runServer(t, src,
		WithPollInterval(50*time.Millisecond),
		WithPollTimeout(500*time.Millisecond),
	)

	// Out-of-suffix should be REFUSED initially.
	resp := dnsQueryUDP(t, s.BoundAddr(), "foo.added.test", dns.TypeA)
	if resp.Rcode != dns.RcodeRefused {
		t.Fatalf("pre-update rcode = %d, want REFUSED", resp.Rcode)
	}

	src.Set([]string{"dev.test", "added.test"})

	// Wait up to 1s for the next poll to apply.
	deadline := time.Now().Add(1 * time.Second)
	for {
		resp = dnsQueryUDP(t, s.BoundAddr(), "foo.added.test", dns.TypeA)
		if resp.Rcode == dns.RcodeSuccess {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("poll did not apply added.test within 1s; last rcode=%d", resp.Rcode)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// erroringSource implements SuffixSource and returns an error from List
// after a configurable trigger. Initial List succeeds.
type erroringSource struct {
	mu       sync.Mutex
	suffixes []ConfiguredSuffix
	failNext bool
}

func (e *erroringSource) List(ctx context.Context) ([]ConfiguredSuffix, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failNext {
		return nil, errors.New("synthetic source error")
	}
	out := make([]ConfiguredSuffix, len(e.suffixes))
	copy(out, e.suffixes)
	return out, nil
}

func (e *erroringSource) Name() string { return "erroring" }

func (e *erroringSource) SetFail(v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failNext = v
}

func TestPollLoop_RetainsPriorSetOnSourceError(t *testing.T) {
	cs, _ := NewConfiguredSuffix("dev.test")
	src := &erroringSource{suffixes: []ConfiguredSuffix{cs}}

	s, _ := runServer(t, src,
		WithPollInterval(50*time.Millisecond),
		WithPollTimeout(500*time.Millisecond),
	)

	// Confirm initial set populated.
	if r := dnsQueryUDP(t, s.BoundAddr(), "x.dev.test", dns.TypeA); r.Rcode != dns.RcodeSuccess {
		t.Fatalf("initial dev.test: rcode=%d", r.Rcode)
	}

	src.SetFail(true)
	time.Sleep(300 * time.Millisecond)

	// After several failing polls the set should still answer for dev.test.
	if r := dnsQueryUDP(t, s.BoundAddr(), "x.dev.test", dns.TypeA); r.Rcode != dns.RcodeSuccess {
		t.Errorf("set was cleared on source error: rcode=%d", r.Rcode)
	}
}

func TestPollLoop_CancellationStopsLoop(t *testing.T) {
	src := NewStaticSuffixSource("dev.test")
	ctx, cancel := context.WithCancel(context.Background())
	s := New(src,
		WithAddr("127.0.0.1:0"),
		WithPollInterval(50*time.Millisecond),
		WithPollTimeout(500*time.Millisecond),
	)
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	for s.BoundAddr() == "" {
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel; poll loop may be leaked")
	}
}

func TestServer_ConcurrentUDPAndTCP(t *testing.T) {
	src := NewStaticSuffixSource("dev.test")
	s, _ := runServer(t, src)

	var wg sync.WaitGroup
	errs := make(chan error, 40)

	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			resp := dnsQueryUDP(t, s.BoundAddr(), "x.dev.test", dns.TypeA)
			if resp.Rcode != dns.RcodeSuccess {
				errs <- errors.New("udp rcode")
			}
		}()
		go func() {
			defer wg.Done()
			resp := dnsQueryTCP(t, s.BoundAddr(), "x.dev.test", dns.TypeA)
			if resp.Rcode != dns.RcodeSuccess {
				errs <- errors.New("tcp rcode")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent query: %v", err)
	}
}
