package daemon

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/infobloxopen/devedge/internal/depruntime"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// CT-3: ApplyDependencies with an empty kube context provisions against the
// default (current-context) provisioner exactly as today — empty Target.KubeContext
// preserves the pre-topology behavior (back-compat).
func TestDepManager_emptyContextBackCompat(t *testing.T) {
	var builtWith []string
	factory := func(kubeContext, namespace string) depruntime.Provisioner {
		builtWith = append(builtWith, kubeContext)
		return apiFakeProv{}
	}
	mgr := NewDepManager(factory, t.TempDir(), 0, discardLogger())

	results := mgr.Apply(context.Background(), "svc", Target{},
		[]depruntime.Dep{{Name: "db", Engine: depruntime.EnginePostgres, Port: 5432}})

	if len(results) != 1 || !results[0].Ready() {
		t.Fatalf("want 1 ready result, got %+v", results)
	}
	if len(builtWith) != 1 || builtWith[0] != "" {
		t.Errorf("empty Target should build the default (empty-context) provisioner, built with %v", builtWith)
	}
}

// T008: the DepManager lazily builds and caches one provisioner per (context,
// namespace); the same context is reused, a new context gets a new provisioner.
func TestDepManager_perContextProvisioner(t *testing.T) {
	built := map[string]int{}
	factory := func(kubeContext, namespace string) depruntime.Provisioner {
		built[kubeContext]++
		return apiFakeProv{}
	}
	mgr := NewDepManager(factory, t.TempDir(), 0, discardLogger())
	deps := []depruntime.Dep{{Name: "db", Engine: depruntime.EnginePostgres, Port: 5432}}

	mgr.Apply(context.Background(), "a", Target{KubeContext: "k3d-devedge"}, deps)
	mgr.Apply(context.Background(), "b", Target{KubeContext: "k3d-devedge"}, deps) // reuse
	mgr.Apply(context.Background(), "c", Target{KubeContext: "k3d-other"}, deps)   // new

	if built["k3d-devedge"] != 1 {
		t.Errorf("shared context built %d times, want 1 (cached)", built["k3d-devedge"])
	}
	if built["k3d-other"] != 1 {
		t.Errorf("second context built %d times, want 1", built["k3d-other"])
	}
}

// T008: Release routes to the provisioner the service was applied against — the
// DELETE endpoint carries no target, so the manager must remember it.
func TestDepManager_releaseUsesAppliedTarget(t *testing.T) {
	dropped := map[string]bool{}
	factory := func(kubeContext, namespace string) depruntime.Provisioner {
		return &recordingProv{ctx: kubeContext, dropped: dropped}
	}
	mgr := NewDepManager(factory, t.TempDir(), 0, discardLogger())
	deps := []depruntime.Dep{{Name: "db", Engine: depruntime.EnginePostgres, Port: 5432}}

	mgr.Apply(context.Background(), "svc", Target{KubeContext: "k3d-devedge"}, deps)
	if err := mgr.Release(context.Background(), "svc", true); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !dropped["k3d-devedge"] {
		t.Errorf("clean release did not drop on the applied context, dropped=%v", dropped)
	}
}

// T031/FR-015: when a service's resolved target changes (e.g. it opts into a
// dedicated cluster), the next Apply releases the prior footprint and the service
// is thereafter associated only with the new cluster — never running in two
// places. Proven here: after a shared→dedicated toggle, a clean down targets the
// dedicated cluster and never the shared one.
func TestDepManager_toggleMigration(t *testing.T) {
	dropped := map[string]bool{}
	factory := func(kubeContext, namespace string) depruntime.Provisioner {
		return &recordingProv{ctx: kubeContext, dropped: dropped}
	}
	mgr := NewDepManager(factory, t.TempDir(), 0, discardLogger())
	deps := []depruntime.Dep{{Name: "db", Engine: depruntime.EnginePostgres, Port: 5432}}

	mgr.Apply(context.Background(), "svc", Target{KubeContext: "k3d-devedge"}, deps)          // shared
	mgr.Apply(context.Background(), "svc", Target{KubeContext: "k3d-devedge-proj-svc"}, deps) // toggled dedicated

	if err := mgr.Release(context.Background(), "svc", true); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !dropped["k3d-devedge-proj-svc"] {
		t.Errorf("after migration, a clean down must target the dedicated cluster; dropped=%v", dropped)
	}
	if dropped["k3d-devedge"] {
		t.Errorf("a clean down must NOT touch the shared cluster after migration; dropped=%v", dropped)
	}
}

// recordingProv is a ready-for-everything provisioner that records the context it
// dropped on, to prove Release routes to the right per-context provisioner.
type recordingProv struct {
	ctx     string
	dropped map[string]bool
}

func (p *recordingProv) EnsureInstance(_ context.Context, ref depruntime.InstanceRef) (depruntime.Instance, error) {
	return depruntime.Instance{Engine: ref.Engine, Host: string(ref.Engine) + ".dev.test", Port: 5432}, nil
}
func (p *recordingProv) Ready(context.Context, depruntime.InstanceRef) error       { return nil }
func (p *recordingProv) EnsureDatabase(context.Context, depruntime.Binding) error { return nil }
func (p *recordingProv) DropDatabase(context.Context, depruntime.Binding) error {
	p.dropped[p.ctx] = true
	return nil
}
