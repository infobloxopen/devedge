package dnsserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/infobloxopen/devedge/pkg/types"
	"github.com/miekg/dns"
)

// DefaultAddr is the loopback address+port the DNS endpoint binds when
// the daemon is not configured otherwise. The matching /etc/resolver/
// drop-in (see internal/dns/resolver_darwin.go) writes the same port.
const DefaultAddr = "127.0.0.1:15354"

// shutdownBudget bounds how long Run waits for in-flight queries during
// graceful shutdown.
const shutdownBudget = 2 * time.Second

// Server runs an in-process authoritative DNS endpoint over UDP and TCP
// for the configured suffixes.
type Server struct {
	addr   string
	source SuffixSource
	edgeIP net.IP
	set    *AuthoritativeSet
	logger *slog.Logger

	// pollInterval and pollTimeout are exposed only for tests.
	pollInterval time.Duration
	pollTimeout  time.Duration

	// boundAddr is populated after Run binds. Tests inspect it when
	// the constructor was called with a ":0" address.
	mu        sync.Mutex
	boundAddr string
}

// Option configures a Server.
type Option func(*Server)

// WithAddr overrides the bind address. Must be a loopback address.
func WithAddr(addr string) Option { return func(s *Server) { s.addr = addr } }

// WithLogger sets the structured logger.
func WithLogger(l *slog.Logger) Option { return func(s *Server) { s.logger = l } }

// WithEdgeIP overrides the edge IP returned in synthetic A records.
func WithEdgeIP(ip net.IP) Option { return func(s *Server) { s.edgeIP = ip } }

// WithPollInterval overrides the SuffixSource poll period. For tests.
func WithPollInterval(d time.Duration) Option { return func(s *Server) { s.pollInterval = d } }

// WithPollTimeout overrides the per-call SuffixSource timeout. For tests.
func WithPollTimeout(d time.Duration) Option { return func(s *Server) { s.pollTimeout = d } }

// New constructs a Server. The SuffixSource is mandatory.
func New(source SuffixSource, opts ...Option) *Server {
	s := &Server{
		addr:         DefaultAddr,
		source:       source,
		edgeIP:       net.ParseIP(types.EdgeIP),
		set:          NewAuthoritativeSet(),
		logger:       slog.Default(),
		pollInterval: pollInterval,
		pollTimeout:  pollTimeout,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// BoundAddr returns the address the server actually bound to. Useful
// when the configured addr used port 0 for an ephemeral port. Returns
// the empty string before Run has bound the listeners.
func (s *Server) BoundAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.boundAddr
}

// Run starts the DNS server. It blocks until ctx is cancelled or both
// servers exit. It returns nil on clean shutdown and a non-nil error if
// either listener failed to bind or returned an unrecoverable error.
//
// Failure of the initial SuffixSource.List is not fatal: the server
// starts with an empty AuthoritativeSet (REFUSED to all) and the next
// successful poll populates it.
func (s *Server) Run(ctx context.Context) error {
	if s.source == nil {
		return errors.New("dnsserver: nil SuffixSource")
	}
	if err := validateLoopback(s.addr); err != nil {
		return err
	}

	// Initial synchronous List so the first incoming query sees a
	// populated set when possible.
	initCtx, cancel := context.WithTimeout(ctx, s.pollTimeout)
	if list, err := s.source.List(initCtx); err == nil {
		added, _ := s.set.Replace(list)
		s.logger.Info("dnsserver.suffixes_initial",
			"source", s.source.Name(),
			"now", suffixNamesStr(s.set.Snapshot()),
			"added", suffixNamesStr(added),
		)
	} else {
		s.logger.Warn("dnsserver.suffix_poll_failed",
			"source", s.source.Name(),
			"phase", "initial",
			"err", err,
		)
	}
	cancel()

	h := NewHandler(s.set, s.edgeIP, s.logger)

	// Bind UDP first to claim the port.
	udpPC, err := net.ListenPacket("udp", s.addr)
	if err != nil {
		s.logger.Error("dnsserver.bind_failed", "addr", s.addr, "transport", "udp", "err", err)
		return fmt.Errorf("dnsserver: bind udp %s: %w", s.addr, err)
	}

	// For ":0" the chosen port lives on the UDP socket; reuse it for TCP.
	tcpAddr := udpPC.LocalAddr().String()
	tcpLn, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		_ = udpPC.Close()
		s.logger.Error("dnsserver.bind_failed", "addr", tcpAddr, "transport", "tcp", "err", err)
		return fmt.Errorf("dnsserver: bind tcp %s: %w", tcpAddr, err)
	}

	s.mu.Lock()
	s.boundAddr = tcpAddr
	s.mu.Unlock()

	udpSrv := &dns.Server{PacketConn: udpPC, Handler: h}
	tcpSrv := &dns.Server{Listener: tcpLn, Handler: h}

	s.logger.Info("dnsserver.started",
		"addr", tcpAddr,
		"suffixes", suffixNamesStr(s.set.Snapshot()),
	)

	errCh := make(chan error, 2)
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := udpSrv.ActivateAndServe(); err != nil {
			errCh <- fmt.Errorf("udp: %w", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := tcpSrv.ActivateAndServe(); err != nil {
			errCh <- fmt.Errorf("tcp: %w", err)
		}
	}()

	// Polling loop.
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		s.runPollLoop(ctx)
	}()

	// Block until ctx is done or one of the servers exits unexpectedly.
	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-errCh:
		serveErr = err
	}

	// Graceful shutdown of both servers with a bounded budget.
	sdCtx, sdCancel := context.WithTimeout(context.Background(), shutdownBudget)
	defer sdCancel()
	if err := udpSrv.ShutdownContext(sdCtx); err != nil {
		s.logger.Info("dnsserver.shutdown_udp_err", "err", err)
	}
	if err := tcpSrv.ShutdownContext(sdCtx); err != nil {
		s.logger.Info("dnsserver.shutdown_tcp_err", "err", err)
	}

	wg.Wait()
	<-pollDone

	// Drain errCh; if Ctx was cancelled, a "server closed" error is expected.
	if serveErr != nil && !isClosedErr(serveErr) {
		return serveErr
	}
	return nil
}

func (s *Server) runPollLoop(ctx context.Context) {
	t := time.NewTicker(s.pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.pollOnce(ctx)
		}
	}
}

func (s *Server) pollOnce(ctx context.Context) {
	callCtx, cancel := context.WithTimeout(ctx, s.pollTimeout)
	defer cancel()
	list, err := s.source.List(callCtx)
	if err != nil {
		s.logger.Warn("dnsserver.suffix_poll_failed",
			"source", s.source.Name(),
			"err", err,
		)
		return
	}
	added, removed := s.set.Replace(list)
	if len(added) == 0 && len(removed) == 0 {
		return
	}
	s.logger.Info("dnsserver.suffixes_changed",
		"source", s.source.Name(),
		"added", suffixNamesStr(added),
		"removed", suffixNamesStr(removed),
		"now", suffixNamesStr(s.set.Snapshot()),
	)
}

func validateLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("dnsserver: invalid addr %q: %w", addr, err)
	}
	if host == "" {
		return fmt.Errorf("dnsserver: addr %q missing host (loopback required)", addr)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Treat as hostname; accept "localhost" as a convenience.
		if host == "localhost" {
			return nil
		}
		return fmt.Errorf("dnsserver: addr %q host is not an IP", addr)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("dnsserver: addr %q is not a loopback address", addr)
	}
	return nil
}

func suffixNamesStr(in []ConfiguredSuffix) []string {
	out := make([]string, len(in))
	for i, cs := range in {
		out[i] = cs.Name
	}
	return out
}

// isClosedErr reports whether err is a benign "use of closed network
// connection" or "server closed" returned from a server that was
// shut down via Shutdown.
func isClosedErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "server closed")
}
