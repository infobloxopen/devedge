package daemon

import (
	"context"
	"log/slog"
	"sync"

	"github.com/infobloxopen/devedge/internal/depruntime"
)

// DepManager is the daemon's dependency-runtime control point. It mirrors the
// route registry's role for dependencies: it holds each service's desired
// dependency set and the latest observed reconcile results, and drives the
// depruntime reconciler. Reconcile runs synchronously on Apply because the CLI
// (`de project up`) waits for readiness (FR-004); dependencies have no lease to
// sweep, so no background loop is needed.
type DepManager struct {
	mu      sync.Mutex
	rec     *depruntime.Reconciler
	records map[string]depRecord
	logger  *slog.Logger
}

type depRecord struct {
	deps    []depruntime.Dep
	results []depruntime.Result
}

// NewDepManager builds a DepManager over a depruntime reconciler.
func NewDepManager(rec *depruntime.Reconciler, logger *slog.Logger) *DepManager {
	return &DepManager{rec: rec, records: map[string]depRecord{}, logger: logger}
}

// Apply reconciles a service's declared dependencies to their desired state and
// stores the results. Idempotent (FR-008). Emits structured logs per dependency
// (Principle V observability).
func (m *DepManager) Apply(ctx context.Context, service string, deps []depruntime.Dep) []depruntime.Result {
	results := m.rec.Reconcile(ctx, service, deps)

	m.mu.Lock()
	m.records[service] = depRecord{deps: deps, results: results}
	m.mu.Unlock()

	for _, r := range results {
		if r.Ready() {
			m.logger.Info("dependency ready", "service", service, "dependency", r.Name, "engine", r.Engine)
		} else {
			m.logger.Warn("dependency not ready", "service", service, "dependency", r.Name, "state", r.State, "error", r.Err)
		}
	}
	return results
}

// Get returns the latest observed results for a service (never raw credentials).
func (m *DepManager) Get(service string) []depruntime.Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.records[service].results
}

// Release tears down a service's dependencies (clean drops the isolation slices).
// Forgets the service afterwards.
func (m *DepManager) Release(ctx context.Context, service string, clean bool) error {
	m.mu.Lock()
	rec := m.records[service]
	delete(m.records, service)
	m.mu.Unlock()

	m.logger.Info("dependencies released", "service", service, "clean", clean, "count", len(rec.deps))
	return m.rec.Release(ctx, service, rec.deps, clean)
}
