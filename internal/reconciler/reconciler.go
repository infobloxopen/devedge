// Package reconciler watches registry events and synchronizes the Traefik
// dynamic configuration directory, /etc/hosts, and TLS certificates to match
// the desired state.
package reconciler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/infobloxopen/devedge/internal/certs"
	"github.com/infobloxopen/devedge/internal/dns"
	"github.com/infobloxopen/devedge/internal/registry"
	"github.com/infobloxopen/devedge/internal/render"
)

// Reconciler synchronizes route state to Traefik config files, hosts entries,
// and TLS certificates.
type Reconciler struct {
	configDir string
	hostsPath string
	certMgr   *certs.Manager
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

// WithCertManager enables certificate management during reconciliation.
func WithCertManager(m *certs.Manager) Option {
	return func(r *Reconciler) { r.certMgr = m }
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

// Sync performs a single reconciliation pass: writes Traefik config files,
// updates /etc/hosts, and ensures TLS certificates cover all active hostnames.
func (r *Reconciler) Sync() error {
	routes := r.source.List()
	r.logger.Info("reconciling", "routes", len(routes))

	if err := os.MkdirAll(r.configDir, 0755); err != nil {
		return fmt.Errorf("ensure config dir: %w", err)
	}

	if err := render.SyncAll(r.configDir, routes); err != nil {
		return err
	}

	hostnames := make([]string, len(routes))
	for i, route := range routes {
		hostnames[i] = route.Host
	}

	if r.hostsPath != "" {
		if err := dns.SyncHosts(r.hostsPath, hostnames); err != nil {
			r.logger.Error("hosts sync failed", "err", err)
		}
	}

	if r.certMgr != nil && len(hostnames) > 0 {
		if _, err := r.certMgr.EnsureCert(hostnames); err != nil {
			r.logger.Error("cert sync failed", "err", err)
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
