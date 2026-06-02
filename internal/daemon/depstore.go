package daemon

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/infobloxopen/devedge/internal/depruntime"
	"github.com/infobloxopen/devedge/internal/helm"
)

// Target is the resolved cluster a service's dependencies are provisioned against
// (004). An empty Target.KubeContext preserves the pre-topology behavior:
// provisioning lands on the daemon's current kube context.
type Target struct {
	KubeContext string
	Namespace   string // defaults to helm.DefaultNamespace when empty
}

func (t Target) namespace() string {
	if t.Namespace == "" {
		return helm.DefaultNamespace
	}
	return t.Namespace
}

func (t Target) key() string {
	return t.KubeContext + "\x00" + t.namespace()
}

// ProvisionerFactory builds a Provisioner for a resolved (kubeContext, namespace).
// The daemon caches one per target so concurrent projects on different clusters
// each get their own port-forward set (a single provisioner cannot serve two
// clusters at once).
type ProvisionerFactory func(kubeContext, namespace string) depruntime.Provisioner

// DepManager is the daemon's dependency-runtime control point. It mirrors the
// route registry's role for dependencies: it holds each service's desired
// dependency set, the target it was provisioned against, and the latest observed
// reconcile results, and drives a per-target depruntime reconciler. Reconcile runs
// synchronously on Apply because the CLI (`de project up`) waits for readiness
// (FR-004); dependencies have no lease to sweep, so no background loop is needed.
type DepManager struct {
	mu      sync.Mutex
	factory ProvisionerFactory
	baseDir string
	timeout time.Duration
	recs    map[string]*depruntime.Reconciler  // per-target reconciler (cached)
	provs   map[string]depruntime.Provisioner  // per-target provisioner (for Close)
	records map[string]depRecord
	logger  *slog.Logger
}

type depRecord struct {
	deps    []depruntime.Dep
	target  Target
	results []depruntime.Result
}

// NewDepManager builds a DepManager. The factory lazily produces a Provisioner per
// resolved target; baseDir is the DSN-file root; a zero timeout uses the
// reconciler default.
func NewDepManager(factory ProvisionerFactory, baseDir string, timeout time.Duration, logger *slog.Logger) *DepManager {
	return &DepManager{
		factory: factory,
		baseDir: baseDir,
		timeout: timeout,
		recs:    map[string]*depruntime.Reconciler{},
		provs:   map[string]depruntime.Provisioner{},
		records: map[string]depRecord{},
		logger:  logger,
	}
}

// reconcilerFor returns the cached reconciler for a target, building (and caching)
// its provisioner on first use. Caller need not hold m.mu.
func (m *DepManager) reconcilerFor(t Target) *depruntime.Reconciler {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := t.key()
	if rec, ok := m.recs[key]; ok {
		return rec
	}
	prov := m.factory(t.KubeContext, t.namespace())
	rec := depruntime.NewReconciler(prov, m.baseDir, m.timeout)
	m.recs[key] = rec
	m.provs[key] = prov
	return rec
}

// Apply reconciles a service's declared dependencies to their desired state on the
// resolved target and stores the results. Idempotent (FR-008). Emits structured
// logs per dependency (Principle V observability).
func (m *DepManager) Apply(ctx context.Context, service string, target Target, deps []depruntime.Dep) []depruntime.Result {
	// Toggle migration (FR-015): if this service was last provisioned on a
	// different target (e.g. it just opted into a dedicated cluster), release its
	// footprint on the prior cluster first so it is never running in two places,
	// then provision on the newly resolved target.
	m.mu.Lock()
	prior, existed := m.records[service]
	m.mu.Unlock()
	if existed && prior.target != target && len(prior.deps) > 0 {
		if err := m.reconcilerFor(prior.target).Release(ctx, service, prior.deps, false); err != nil {
			m.logger.Warn("migration: release prior footprint", "service", service,
				"from", prior.target.KubeContext, "to", target.KubeContext, "error", err)
		} else {
			m.logger.Info("migrated service to a new cluster", "service", service,
				"from", prior.target.KubeContext, "to", target.KubeContext)
		}
	}

	rec := m.reconcilerFor(target)
	results := rec.Reconcile(ctx, service, deps)

	m.mu.Lock()
	m.records[service] = depRecord{deps: deps, target: target, results: results}
	m.mu.Unlock()

	for _, r := range results {
		if r.Ready() {
			m.logger.Info("dependency ready", "service", service, "dependency", r.Name, "engine", r.Engine, "context", target.KubeContext)
		} else {
			m.logger.Warn("dependency not ready", "service", service, "dependency", r.Name, "state", r.State, "error", r.Err, "context", target.KubeContext)
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

// Release tears down a service's dependencies on the target they were applied
// against (the DELETE request carries no target, so it is read from the stored
// record). clean drops the isolation slices. Forgets the service afterwards.
func (m *DepManager) Release(ctx context.Context, service string, clean bool) error {
	m.mu.Lock()
	rec := m.records[service]
	delete(m.records, service)
	m.mu.Unlock()

	m.logger.Info("dependencies released", "service", service, "clean", clean, "count", len(rec.deps), "context", rec.target.KubeContext)
	return m.reconcilerFor(rec.target).Release(ctx, service, rec.deps, clean)
}

// Close stops every cached provisioner that holds resources (e.g. supervised
// port-forwards). Called on daemon shutdown.
func (m *DepManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.provs {
		if c, ok := p.(interface{ Close() }); ok {
			c.Close()
		}
	}
}
