package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/infobloxopen/devedge/internal/reconciler"
	"github.com/infobloxopen/devedge/internal/registry"
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

// Server is the devedged control plane.
type Server struct {
	socketPath string
	configDir  string
	hostsPath  string
	reg        *registry.Registry
	rec        *reconciler.Reconciler
	api        *API
	logger     *slog.Logger
}

// ServerOption configures a Server.
type ServerOption func(*Server)

// WithSocketPath overrides the Unix socket path.
func WithSocketPath(p string) ServerOption {
	return func(s *Server) { s.socketPath = p }
}

// WithConfigDir overrides the Traefik config directory.
func WithConfigDir(d string) ServerOption {
	return func(s *Server) { s.configDir = d }
}

// WithHostsPath sets the hosts file path for DNS management.
func WithHostsPath(p string) ServerOption {
	return func(s *Server) { s.hostsPath = p }
}

// WithServerLogger sets the logger.
func WithServerLogger(l *slog.Logger) ServerOption {
	return func(s *Server) { s.logger = l }
}

// NewServer creates a Server with the given options.
func NewServer(opts ...ServerOption) *Server {
	s := &Server{
		socketPath: DefaultSocketPath(),
		configDir:  DefaultConfigDir(),
		logger:     slog.Default(),
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

	// Create registry first, then reconciler, then wire them together.
	// The registry onChange callback delegates through a pointer so that
	// the reconciler can be created after the registry.
	s.reg = registry.New()
	s.rec = reconciler.New(s.configDir, s.reg, recOpts...)
	s.reg.SetOnChange(s.rec.OnEvent)

	s.api = NewAPI(s.reg, s.logger)
	return s
}

// Run starts the daemon. It blocks until the context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	// Ensure directories exist.
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0755); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}
	if err := os.MkdirAll(s.configDir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// Remove stale socket.
	os.Remove(s.socketPath)

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.socketPath, err)
	}
	defer ln.Close()
	defer os.Remove(s.socketPath)

	srv := &http.Server{Handler: s.api.Handler()}

	// Start reconciler sweep loop.
	go s.rec.Run(ctx)

	// Initial sync.
	if err := s.rec.Sync(); err != nil {
		s.logger.Error("initial sync failed", "err", err)
	}

	// Shutdown on context cancel.
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()

	s.logger.Info("devedged listening", "socket", s.socketPath)
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}
