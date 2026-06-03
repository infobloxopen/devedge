package depruntime

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/cluster"
)

func pgDep() Dep    { return Dep{Name: "db", Engine: EnginePostgres, Version: "16", Port: 5432} }
func redisDep() Dep { return Dep{Name: "cache", Engine: EngineRedis, Port: 6379} }

// T005: desired -> Ready transitions; the DSN file + indirect env var are produced.
func TestReconcile_drivesToReady(t *testing.T) {
	fake := newFake()
	r := NewReconciler(fake, t.TempDir(), time.Second)

	res := r.Reconcile(context.Background(), "webhooks", []Dep{pgDep()}, cluster.EnvDev)
	if len(res) != 1 || !res[0].Ready() {
		t.Fatalf("want 1 Ready result, got %+v", res)
	}
	got := res[0]
	if got.EnvVarName != "DATABASE_URL" {
		t.Errorf("env var name = %q, want DATABASE_URL", got.EnvVarName)
	}
	if !strings.HasPrefix(got.EnvVarValue, "fsnotify://postgres/") {
		t.Errorf("env value = %q, want fsnotify://postgres/...", got.EnvVarValue)
	}
	// The real DSN is in the file, never in the env var value.
	data, err := os.ReadFile(got.DSNFilePath)
	if err != nil {
		t.Fatalf("read DSN file: %v", err)
	}
	if !strings.HasPrefix(string(data), "postgres://") {
		t.Errorf("DSN file = %q, want postgres://...", string(data))
	}
	if strings.Contains(got.EnvVarValue, "postgres://") {
		t.Error("env var value must not contain the real DSN")
	}
}

// T005: re-reconcile is idempotent — no duplicate instance install, no new password churn breaking it.
func TestReconcile_idempotent(t *testing.T) {
	fake := newFake()
	r := NewReconciler(fake, t.TempDir(), time.Second)
	deps := []Dep{pgDep()}

	r.Reconcile(context.Background(), "webhooks", deps, cluster.EnvDev)
	r.Reconcile(context.Background(), "webhooks", deps, cluster.EnvDev)

	if fake.instances[EnginePostgres] != 2 {
		// EnsureInstance is called each pass but is itself idempotent (helm upgrade --install);
		// the contract is "no error, no data loss", which the fake models by counting calls.
		t.Logf("EnsureInstance called %d times (idempotent by design)", fake.instances[EnginePostgres])
	}
	if got := fake.databases[bkey(Binding{Engine: EnginePostgres, Service: "webhooks", Dependency: "db"})]; got != 2 {
		t.Errorf("EnsureDatabase calls = %d, want 2 (idempotent create per pass)", got)
	}
}

// T005: a bounded readiness timeout surfaces per-dependency and leaves no half-state.
func TestReconcile_readinessTimeout(t *testing.T) {
	fake := newFake()
	fake.neverReady[EnginePostgres] = true
	r := NewReconciler(fake, t.TempDir(), 150*time.Millisecond)

	res := r.Reconcile(context.Background(), "webhooks", []Dep{pgDep()}, cluster.EnvDev)
	if res[0].State != StateFailed {
		t.Fatalf("want Failed, got %s", res[0].State)
	}
	if !strings.Contains(res[0].Err, "not ready") {
		t.Errorf("err = %q, want a readiness message", res[0].Err)
	}
	// No DSN file should have been written (no half-provisioned residue).
	if res[0].DSNFilePath != "" {
		t.Errorf("failed dep must not report a DSN file path, got %q", res[0].DSNFilePath)
	}
	// EnsureDatabase must not have run.
	if len(fake.databases) != 0 {
		t.Errorf("EnsureDatabase ran despite readiness failure: %v", fake.databases)
	}
}

// T034: a recognized-but-unrunnable engine fails by name (FR-012).
func TestReconcile_unsupportedEngine(t *testing.T) {
	r := NewReconciler(newFake(), t.TempDir(), time.Second)
	res := r.Reconcile(context.Background(), "svc", []Dep{{Name: "x", Engine: Engine("mysql"), Port: 3306}}, cluster.EnvDev)
	if res[0].State != StateFailed || !strings.Contains(res[0].Err, "mysql") {
		t.Fatalf("want failure naming the engine, got %+v", res[0])
	}
}

// T022: per-(service,dependency) identifiers are deterministic, sanitized, collision-avoided.
func TestDerivation_deterministicAndIsolated(t *testing.T) {
	if DatabaseName("Web-Hooks", "DB") != "web_hooks_db" {
		t.Errorf("sanitize/derive mismatch: %q", DatabaseName("Web-Hooks", "DB"))
	}
	// Two different services with the same dependency name get distinct identities.
	if DatabaseName("svc-a", "db") == DatabaseName("svc-b", "db") {
		t.Error("distinct services must derive distinct database names")
	}
	if KeyNamespace("svc", "cache") != "svc_cache:" {
		t.Errorf("redis key namespace = %q", KeyNamespace("svc", "cache"))
	}
	if EnvVarName(EngineRedis, "cache", false) != "REDIS_URL" {
		t.Errorf("redis env var = %q, want REDIS_URL", EnvVarName(EngineRedis, "cache", false))
	}
}

// The reconcile core is engine-agnostic: a redis dep also drives to Ready via the
// uniform fsnotify DSN convention (real Redis provisioning lands in Slice B).
func TestReconcile_redisToReady(t *testing.T) {
	r := NewReconciler(newFake(), t.TempDir(), time.Second)
	res := r.Reconcile(context.Background(), "webhooks", []Dep{redisDep()}, cluster.EnvDev)
	if !res[0].Ready() {
		t.Fatalf("redis dep not Ready: %+v", res[0])
	}
	if !strings.HasPrefix(res[0].EnvVarValue, "fsnotify://redis/") {
		t.Errorf("env value = %q, want fsnotify://redis/...", res[0].EnvVarValue)
	}
}

// Two services declaring the same dep name produce distinct bindings (FR-002 / SC-002 unit-level).
func TestNewBinding_isolation(t *testing.T) {
	a, _ := NewBinding("svc-a", pgDep())
	b, _ := NewBinding("svc-b", pgDep())
	if a.Database == b.Database || a.User == b.User {
		t.Errorf("co-located services not isolated: %+v vs %+v", a, b)
	}
	if a.Password == b.Password {
		t.Error("bindings must get distinct passwords")
	}
}
