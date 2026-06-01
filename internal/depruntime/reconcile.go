package depruntime

import (
	"context"
	"fmt"
	"time"

	"github.com/infobloxopen/devedge/internal/dsn"
)

// State is a binding's reconcile state (data-model.md).
type State string

const (
	StatePending       State = "Pending"
	StateInstanceReady State = "InstanceReady"
	StateProvisioned   State = "Provisioned"
	StateReady         State = "Ready"
	StateFailed        State = "Failed"
)

// Result is the observed outcome for one dependency after a reconcile pass. It
// never carries the real DSN or credentials (those live only in the DSN file).
type Result struct {
	Name        string
	Engine      Engine
	State       State
	EnvVarName  string
	EnvVarValue string // indirect fsnotify DSN; "" until Ready
	DSNFilePath string
	Err         string // actionable, per-dependency; "" on success
}

// Ready reports whether the dependency reached the Ready state.
func (r Result) Ready() bool { return r.State == StateReady }

// Reconciler converges a service's declared dependencies to running, reachable,
// isolated backing stores via a Provisioner.
type Reconciler struct {
	prov             Provisioner
	baseDir          string        // DSN-file base (e.g. ~/.devedge)
	readinessTimeout time.Duration // bounded wait per dependency (FR-004)
}

// NewReconciler builds a Reconciler. A zero timeout defaults to 60s.
func NewReconciler(prov Provisioner, baseDir string, readinessTimeout time.Duration) *Reconciler {
	if readinessTimeout <= 0 {
		readinessTimeout = 60 * time.Second
	}
	return &Reconciler{prov: prov, baseDir: baseDir, readinessTimeout: readinessTimeout}
}

// Reconcile drives each declared dependency to Ready, idempotently. Failures are
// per-dependency and leave no half-provisioned residue that blocks a retry
// (FR-008/FR-009). It returns one Result per dependency, in input order.
func (r *Reconciler) Reconcile(ctx context.Context, service string, deps []Dep) []Result {
	engineCount := map[Engine]int{}
	for _, d := range deps {
		engineCount[d.Engine]++
	}

	results := make([]Result, 0, len(deps))
	for _, d := range deps {
		ambiguous := engineCount[d.Engine] > 1
		res := Result{Name: d.Name, Engine: d.Engine, State: StatePending, EnvVarName: EnvVarName(d.Engine, d.Name, ambiguous)}
		results = append(results, r.reconcileOne(ctx, service, d, res))
	}
	return results
}

func (r *Reconciler) reconcileOne(ctx context.Context, service string, d Dep, res Result) Result {
	fail := func(err error) Result {
		res.State = StateFailed
		res.Err = err.Error()
		return res
	}

	if !Supported(d.Engine) {
		return fail(fmt.Errorf("dependency %q: engine %q has no runtime support (supported: %v)", d.Name, d.Engine, SupportedEngines))
	}

	inst, err := r.prov.EnsureInstance(ctx, d.Engine, d.Version)
	if err != nil {
		return fail(fmt.Errorf("dependency %q: ensure %s instance: %w", d.Name, d.Engine, err))
	}
	res.State = StateInstanceReady

	if err := r.waitReady(ctx, d.Engine); err != nil {
		return fail(fmt.Errorf("dependency %q: %s not ready within %s: %w", d.Name, d.Engine, r.readinessTimeout, err))
	}

	binding, err := NewBinding(service, d)
	if err != nil {
		return fail(fmt.Errorf("dependency %q: %w", d.Name, err))
	}
	if err := r.prov.EnsureDatabase(ctx, binding); err != nil {
		return fail(fmt.Errorf("dependency %q: provision isolation: %w", d.Name, err))
	}
	res.State = StateProvisioned

	port := inst.Port
	if port == 0 {
		port = d.Port
	}
	realDSN, err := dsn.RealDSN(dsn.Conn{
		Engine: string(d.Engine), Host: inst.Host, Port: port,
		Database: binding.Database, User: binding.User, Password: binding.Password,
	})
	if err != nil {
		return fail(fmt.Errorf("dependency %q: %w", d.Name, err))
	}
	path := dsn.FilePath(r.baseDir, service, d.Name)
	if err := dsn.WriteDSNFile(path, realDSN); err != nil {
		return fail(fmt.Errorf("dependency %q: write DSN file: %w", d.Name, err))
	}

	res.DSNFilePath = path
	res.EnvVarValue = dsn.IndirectEnv(string(d.Engine), path)
	res.State = StateReady
	return res
}

// waitReady polls the provisioner's readiness probe with backoff, bounded by the
// reconciler's timeout (FR-004). Honors context cancellation.
func (r *Reconciler) waitReady(ctx context.Context, engine Engine) error {
	ctx, cancel := context.WithTimeout(ctx, r.readinessTimeout)
	defer cancel()

	backoff := 200 * time.Millisecond
	for {
		err := r.prov.Ready(ctx, engine)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			// err is the last (non-nil) readiness failure; surface it over the
			// generic context error so the message is actionable.
			return err
		case <-time.After(backoff):
		}
		if backoff < 2*time.Second {
			backoff *= 2
		}
	}
}
