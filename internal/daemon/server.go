package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"

	"github.com/infobloxopen/devedge/internal/certs"
	"github.com/infobloxopen/devedge/internal/dnsserver"
	"github.com/infobloxopen/devedge/internal/proxy"
	"github.com/infobloxopen/devedge/internal/reconciler"
	"github.com/infobloxopen/devedge/internal/registry"
	"github.com/infobloxopen/devedge/internal/render"
)

// devedgeDir returns the base directory for all devedge state.
// Uses a fixed system path so it works the same whether the daemon
// runs as root (LaunchDaemon) or the current user.
func devedgeDir() string {
	// If DEVEDGE_HOME is set, use it (for testing).
	if dir := os.Getenv("DEVEDGE_HOME"); dir != "" {
		return dir
	}
	// Use the invoking user's home, not root's.
	// SUDO_USER is set when running via sudo.
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		return filepath.Join("/Users", sudoUser, ".devedge")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".devedge")
}

// DefaultSocketPath returns the default Unix socket path for the daemon.
func DefaultSocketPath() string {
	return filepath.Join(devedgeDir(), "devedged.sock")
}

// DefaultConfigDir returns the default Traefik dynamic config directory.
func DefaultConfigDir() string {
	return filepath.Join(devedgeDir(), "traefik", "dynamic")
}

// DefaultTraefikDir returns the base Traefik config directory.
func DefaultTraefikDir() string {
	return filepath.Join(devedgeDir(), "traefik")
}

// DefaultCertsDir returns the default certificate storage directory.
func DefaultCertsDir() string {
	return filepath.Join(devedgeDir(), "certs")
}

// DefaultTCPAddr returns the default TCP address for the admin API.
func DefaultTCPAddr() string {
	return "127.0.0.1:15353"
}

// DefaultDNSAddr returns the default DNS endpoint address.
// Matches the port written into /etc/resolver/<suffix> by
// internal/dns.InstallResolverConfig.
func DefaultDNSAddr() string {
	return dnsserver.DefaultAddr
}

// Server is the devedged control plane.
type Server struct {
	socketPath    string
	configDir     string
	traefikDir    string
	certsDir      string
	hostsPath     string
	tcpAddr       string
	dnsAddr       string
	dnsSource     dnsserver.SuffixSource
	manageTraefik bool
	reg           *registry.Registry
	rec           *reconciler.Reconciler
	api           *API
	logger        *slog.Logger
}

// ServerOption configures a Server.
type ServerOption func(*Server)

// WithSocketPath overrides the Unix socket path.
func WithSocketPath(p string) ServerOption {
	return func(s *Server) { s.socketPath = p }
}

// WithConfigDir overrides the Traefik dynamic config directory.
func WithConfigDir(d string) ServerOption {
	return func(s *Server) { s.configDir = d }
}

// WithHostsPath sets the hosts file path for DNS management.
func WithHostsPath(p string) ServerOption {
	return func(s *Server) { s.hostsPath = p }
}

// WithTCPAddr sets the TCP address for the admin API and dashboard.
func WithTCPAddr(addr string) ServerOption {
	return func(s *Server) { s.tcpAddr = addr }
}

// WithDNSAddr sets the loopback address the DNS endpoint binds.
// Defaults to dnsserver.DefaultAddr. Tests can pass "127.0.0.1:0" for
// an ephemeral port.
func WithDNSAddr(addr string) ServerOption {
	return func(s *Server) { s.dnsAddr = addr }
}

// WithDNSSuffixSource overrides the SuffixSource used by the DNS
// server. Defaults to dnsserver.NewPlatformSuffixSource, which reads
// /etc/resolver/ on macOS and returns the empty set elsewhere.
func WithDNSSuffixSource(src dnsserver.SuffixSource) ServerOption {
	return func(s *Server) { s.dnsSource = src }
}

// WithManageTraefik enables automatic Traefik subprocess management.
func WithManageTraefik(b bool) ServerOption {
	return func(s *Server) { s.manageTraefik = b }
}

// WithServerLogger sets the logger.
func WithServerLogger(l *slog.Logger) ServerOption {
	return func(s *Server) { s.logger = l }
}

// NewServer creates a Server with the given options.
func NewServer(opts ...ServerOption) *Server {
	home, _ := os.UserHomeDir()
	s := &Server{
		socketPath: DefaultSocketPath(),
		configDir:  DefaultConfigDir(),
		traefikDir: DefaultTraefikDir(),
		certsDir:   DefaultCertsDir(),
		hostsPath:  filepath.Join(home, ".devedge", "hosts"),
		tcpAddr:    DefaultTCPAddr(),
		dnsAddr:    DefaultDNSAddr(),
		logger:     slog.Default(),
	}
	for _, o := range opts {
		o(s)
	}
	if s.dnsSource == nil {
		s.dnsSource = dnsserver.NewPlatformSuffixSource(s.logger)
	}

	// Build reconciler options.
	recOpts := []reconciler.Option{
		reconciler.WithLogger(s.logger),
	}
	if s.hostsPath != "" {
		recOpts = append(recOpts, reconciler.WithHostsPath(s.hostsPath))
	}

	// Create registry, reconciler, and wire them.
	s.reg = registry.New()
	s.rec = reconciler.New(s.configDir, s.reg, recOpts...)
	s.reg.SetOnChange(s.rec.OnEvent)

	s.api = NewAPI(s.reg, s.logger)
	return s
}

// Run starts the daemon. It blocks until the context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	// Ensure directories exist.
	for _, dir := range []string{
		filepath.Dir(s.socketPath),
		s.configDir,
		s.traefikDir,
		s.certsDir,
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}

	// Write static Traefik config.
	certMgr := certs.NewManager(s.certsDir, s.logger)
	var certPair *certs.CertPair
	if err := certs.EnsureCA(); err == nil {
		// Generate a wildcard cert covering all .test subdomains.
		pair, err := certMgr.EnsureCert([]string{"*.test", "*.dev.test", "*.dk-local.test", "dev.test"})
		if err != nil {
			s.logger.Warn("initial cert generation failed", "err", err)
		} else {
			certPair = pair
		}
	} else {
		s.logger.Warn("mkcert CA not available, skipping TLS setup",
			"err", err,
			"hint", "run 'de install' to record the mkcert CAROOT for the daemon (or set DEVEDGE_CAROOT) and restart it")
	}

	if err := render.WriteStaticConfig(s.traefikDir, s.configDir, certPair); err != nil {
		s.logger.Error("write static traefik config failed", "err", err)
	}

	// Start embedded reverse proxy on EdgeIP (127.0.0.2:80/443).
	if s.manageTraefik {
		p := proxy.New(s.reg, certPair, s.logger)
		s.api.SetTLSStatus(proxyTLSStatus(p))
		go func() {
			if err := p.Run(ctx); err != nil {
				s.logger.Error("proxy failed", "err", err)
			}
		}()
	}

	// Start the authoritative DNS endpoint. Fail-open: bind/serve
	// errors are logged but do not abort the daemon, so the HTTP
	// admin API and proxy continue running even when DNS is unhealthy.
	// de doctor's DNS endpoint probe is the user-visible signal for
	// a DNS-layer fault.
	dnsServer := dnsserver.New(s.dnsSource,
		dnsserver.WithAddr(s.dnsAddr),
		dnsserver.WithLogger(s.logger),
	)
	go func() {
		if err := dnsServer.Run(ctx); err != nil {
			s.logger.Error("dnsserver failed", "addr", s.dnsAddr, "err", err)
		}
	}()

	// Remove stale socket.
	os.Remove(s.socketPath)

	// Listen on Unix socket with world-writable permissions so non-root
	// users can connect to the root-owned daemon.
	oldUmask := syscall.Umask(0111) // creates socket as 0666
	unixLn, err := net.Listen("unix", s.socketPath)
	syscall.Umask(oldUmask)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.socketPath, err)
	}
	defer unixLn.Close()
	defer os.Remove(s.socketPath)

	// Listen on TCP for browser-accessible dashboard and API.
	tcpLn, err := net.Listen("tcp", s.tcpAddr)
	if err != nil {
		s.logger.Warn("TCP listener failed, dashboard won't be browser-accessible",
			"addr", s.tcpAddr, "err", err)
		tcpLn = nil
	}

	unixSrv := &http.Server{Handler: s.api.Handler()}
	var tcpSrv *http.Server

	if tcpLn != nil {
		tcpSrv = &http.Server{Handler: s.api.Handler()}
		go func() {
			s.logger.Info("dashboard available", "url", "http://"+s.tcpAddr+"/ui")
			if err := tcpSrv.Serve(tcpLn); err != http.ErrServerClosed {
				s.logger.Error("TCP server error", "err", err)
			}
		}()
	}

	// Start reconciler sweep loop.
	go s.rec.Run(ctx)

	// Initial sync.
	if err := s.rec.Sync(); err != nil {
		s.logger.Error("initial sync failed", "err", err)
	}

	// Shutdown on context cancel.
	go func() {
		<-ctx.Done()
		unixSrv.Shutdown(context.Background())
		if tcpSrv != nil {
			tcpSrv.Shutdown(context.Background())
		}
	}()

	s.logger.Info("devedged listening",
		"socket", s.socketPath,
		"tcp", s.tcpAddr,
	)
	if err := unixSrv.Serve(unixLn); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// proxyTLSStatus derives the API-visible TLS status from the proxy's CA
// state, so `de status` / `de doctor` can flag the self-signed fallback
// (issue #8) instead of reporting healthy while browsers reject every host.
func proxyTLSStatus(p *proxy.Proxy) TLSStatus {
	if p.UsingSelfSignedCA() {
		return TLSStatus{Mode: "self-signed", Reason: p.CAFallbackReason()}
	}
	st := TLSStatus{Mode: "mkcert"}
	if root, err := certs.CARoot(); err == nil {
		st.CARoot = root
	}
	return st
}
