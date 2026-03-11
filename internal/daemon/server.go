package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/infobloxopen/devedge/internal/certs"
	"github.com/infobloxopen/devedge/internal/reconciler"
	"github.com/infobloxopen/devedge/internal/registry"
	"github.com/infobloxopen/devedge/internal/render"
	traefikrt "github.com/infobloxopen/devedge/internal/traefik"
)

// DefaultSocketPath returns the default Unix socket path for the daemon.
func DefaultSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".devedge", "devedged.sock")
}

// DefaultConfigDir returns the default Traefik dynamic config directory.
func DefaultConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".devedge", "traefik", "dynamic")
}

// DefaultTraefikDir returns the base Traefik config directory.
func DefaultTraefikDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".devedge", "traefik")
}

// DefaultCertsDir returns the default certificate storage directory.
func DefaultCertsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".devedge", "certs")
}

// DefaultTCPAddr returns the default TCP address for the admin API.
func DefaultTCPAddr() string {
	return "127.0.0.1:15353"
}

// Server is the devedged control plane.
type Server struct {
	socketPath   string
	configDir    string
	traefikDir   string
	certsDir     string
	hostsPath    string
	tcpAddr      string
	manageTraefik bool
	reg          *registry.Registry
	rec          *reconciler.Reconciler
	api          *API
	logger       *slog.Logger
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
		socketPath:   DefaultSocketPath(),
		configDir:    DefaultConfigDir(),
		traefikDir:   DefaultTraefikDir(),
		certsDir:     DefaultCertsDir(),
		hostsPath:    filepath.Join(home, ".devedge", "hosts"),
		tcpAddr:      DefaultTCPAddr(),
		logger:       slog.Default(),
	}
	for _, o := range opts {
		o(s)
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
		// Generate an initial wildcard cert for dev.test.
		pair, err := certMgr.EnsureCert([]string{"*.dev.test", "dev.test"})
		if err != nil {
			s.logger.Warn("initial cert generation failed", "err", err)
		} else {
			certPair = pair
		}
	} else {
		s.logger.Warn("mkcert CA not available, skipping TLS setup", "err", err)
	}

	if err := render.WriteStaticConfig(s.traefikDir, s.configDir, certPair); err != nil {
		s.logger.Error("write static traefik config failed", "err", err)
	}

	// Start Traefik if managed.
	if s.manageTraefik {
		rt := traefikrt.NewRuntime(s.traefikDir, s.logger)
		staticPath := filepath.Join(s.traefikDir, "traefik.yaml")
		if err := rt.Start(ctx, staticPath); err != nil {
			s.logger.Error("traefik start failed", "err", err)
		} else {
			defer rt.Stop()
		}
	}

	// Remove stale socket.
	os.Remove(s.socketPath)

	// Listen on Unix socket.
	unixLn, err := net.Listen("unix", s.socketPath)
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
