// Package traefik manages the Traefik process lifecycle as a subprocess
// of devedged.
package traefik

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Runtime manages the Traefik subprocess.
type Runtime struct {
	mu        sync.Mutex
	configDir string
	logger    *slog.Logger
	cmd       *exec.Cmd
	cancel    context.CancelFunc
}

// NewRuntime creates a Traefik runtime manager.
func NewRuntime(configDir string, logger *slog.Logger) *Runtime {
	return &Runtime{
		configDir: configDir,
		logger:    logger,
	}
}

// FindBinary locates the Traefik binary on the system.
func FindBinary() (string, error) {
	if p, err := exec.LookPath("traefik"); err == nil {
		return p, nil
	}
	// Check common Homebrew locations.
	candidates := []string{
		"/usr/local/bin/traefik",
		"/opt/homebrew/bin/traefik",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("traefik binary not found; install it or add to PATH")
}

// Start launches Traefik as a subprocess with the given static config file.
// It automatically restarts Traefik if it exits unexpectedly.
func (r *Runtime) Start(ctx context.Context, staticConfigPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cmd != nil {
		return fmt.Errorf("traefik already running")
	}

	bin, err := FindBinary()
	if err != nil {
		return err
	}

	childCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel

	go r.supervise(childCtx, bin, staticConfigPath)
	return nil
}

// Stop terminates the Traefik subprocess.
func (r *Runtime) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}

	if r.cmd != nil && r.cmd.Process != nil {
		r.logger.Info("stopping traefik")
		r.cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() {
			r.cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			r.cmd.Process.Kill()
		}
		r.cmd = nil
	}
	return nil
}

// IsRunning reports whether Traefik is currently running.
func (r *Runtime) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cmd != nil && r.cmd.Process != nil && r.cmd.ProcessState == nil
}

// supervise runs Traefik and restarts it on unexpected exits.
func (r *Runtime) supervise(ctx context.Context, bin, configPath string) {
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		r.mu.Lock()
		cmd := exec.CommandContext(ctx, bin,
			"--configFile="+configPath,
		)
		cmd.Stdout = os.Stderr // Traefik logs to stderr of daemon
		cmd.Stderr = os.Stderr
		r.cmd = cmd
		r.mu.Unlock()

		r.logger.Info("starting traefik", "bin", bin, "config", configPath)
		err := cmd.Run()

		r.mu.Lock()
		r.cmd = nil
		r.mu.Unlock()

		if ctx.Err() != nil {
			return // Context cancelled, clean shutdown.
		}

		if err != nil {
			r.logger.Error("traefik exited", "err", err, "restart_in", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			// Exponential backoff capped at 30s.
			backoff = min(backoff*2, 30*time.Second)
		} else {
			backoff = time.Second
		}
	}
}
