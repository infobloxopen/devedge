// Package reconciler watches registry events and synchronizes the Traefik
// dynamic configuration directory and /etc/hosts to match the desired state.
package reconciler

import (
	"context"
	"log/slog"
	"time"

	"fmt"
	"os"

	"github.com/infobloxopen/devedge/internal/dns"
	"github.com/infobloxopen/devedge/internal/registry"
	"github.com/infobloxopen/devedge/internal/render"
)

// Reconciler synchronizes route state to Traefik config files and hosts entries.
type Reconciler struct {
	configDir string
	hostsPath string
	source    *registry.Registry
	logger    *slog.Logger
	sweepInt  time.Duration
}

// Option configures a Reconciler.
type Option func(*Reconciler)

// WithLogger sets the structured logger.
func WithLogger(l *slog.Logger) Option {
	return func(r *Reconciler) { r.logger = l }
}

// WithSweepInterval sets how often expired leases are garbage-collected.
func WithSweepInterval(d time.Duration) Option {
	return func(r *Reconciler) { r.sweepInt = d }
}

// WithHostsPath sets the hosts file path. Defaults to "" (disabled).
func WithHostsPath(p string) Option {
	return func(r *Reconciler) { r.hostsPath = p }
}

// New creates a Reconciler targeting the given config directory.
func New(configDir string, source *registry.Registry, opts ...Option) *Reconciler {
	rec := &Reconciler{
		configDir: configDir,
		source:    source,
		logger:    slog.Default(),
		sweepInt:  5 * time.Second,
	}
	for _, o := range opts {
		o(rec)
	}
	return rec
}

// Sync performs a single reconciliation pass: writes Traefik config files and
// updates /etc/hosts for all active routes, removing stale entries.
func (r *Reconciler) Sync() error {
	routes := r.source.List()
	r.logger.Info("reconciling", "routes", len(routes))

	if err := os.MkdirAll(r.configDir, 0755); err != nil {
		return fmt.Errorf("ensure config dir: %w", err)
	}

	if err := render.SyncAll(r.configDir, routes); err != nil {
		return err
	}

	if r.hostsPath != "" {
		hostnames := make([]string, len(routes))
		for i, route := range routes {
			hostnames[i] = route.Host
		}
		if err := dns.SyncHosts(r.hostsPath, hostnames); err != nil {
			r.logger.Error("hosts sync failed", "err", err)
			// Non-fatal: Traefik config is the primary concern.
		}
	}

	return nil
}

// OnEvent handles a registry event by triggering a sync.
func (r *Reconciler) OnEvent(e registry.Event) {
	r.logger.Info("registry event",
		"kind", e.Kind,
		"host", e.Route.Host,
	)
	if err := r.Sync(); err != nil {
		r.logger.Error("sync failed after event", "err", err)
	}
}

// Run starts the sweep loop. It blocks until the context is cancelled.
func (r *Reconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(r.sweepInt)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			swept := r.source.Sweep()
			if swept > 0 {
				r.logger.Info("swept expired leases", "count", swept)
				if err := r.Sync(); err != nil {
					r.logger.Error("sync failed after sweep", "err", err)
				}
			}
		}
	}
}
