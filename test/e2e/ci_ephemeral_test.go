package e2e

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/cluster"
	"github.com/infobloxopen/devedge/internal/depruntime"
	"github.com/infobloxopen/devedge/internal/dsn"
)

// TestCIEphemeral_createTeardown_e2e (CT-6 / FR-007/008): EnsureEphemeral creates a
// per-run-unique cluster (without touching the user's current context), and
// Teardown removes it within a bounded time leaving no leftover. The `de ci run`
// wrapper's "tear down on success/failure/signal" orchestration is unit-tested
// (cmd/de/ci_test.go); here we prove the real ensure/teardown boundary.
func TestCIEphemeral_createTeardown_e2e(t *testing.T) {
	requireE2E(t)

	// Deterministic run id → predictable, cleanable cluster name.
	t.Setenv("GITHUB_RUN_ID", "")
	t.Setenv("DEVEDGE_RUN_ID", "e2eephem")
	const expected = "devedge-ci-e2eephem"
	_ = exec.Command("k3d", "cluster", "delete", expected).Run()
	t.Cleanup(func() { _ = exec.Command("k3d", "cluster", "delete", expected).Run() })

	e := cluster.NewEnsurer(&cluster.K3dProvider{})
	e.LockDir = t.TempDir()

	before := currentKubeContext()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	target, err := e.EnsureEphemeral(ctx)
	if err != nil {
		t.Fatalf("EnsureEphemeral: %v", err)
	}
	if target.Name != expected || !target.Ephemeral {
		t.Fatalf("target = %+v, want name=%s ephemeral=true", target, expected)
	}
	if clusterCount(t, expected) != 1 {
		t.Fatalf("ephemeral cluster %q not created", expected)
	}
	// FR-013: the user's current kube context is not mutated.
	if after := currentKubeContext(); after != before {
		t.Errorf("ephemeral create mutated current context: %q -> %q", before, after)
	}

	// Teardown removes the cluster with no leftover (FR-007), within a bounded time.
	start := time.Now()
	if err := e.Teardown(target.Name); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if clusterCount(t, expected) != 0 {
		t.Errorf("ephemeral cluster left behind after teardown")
	}
	if d := time.Since(start); d > 90*time.Second {
		t.Errorf("teardown took %s, expected it to be bounded", d)
	}
}

// TestCIEphemeral_dependencyParity_e2e (CT-7 / FR-014/SC-004): the same dependency
// provisioning + DSN roundtrip the 003 suite exercises also passes on a
// devedge-ensured *dedicated ephemeral* (CI) cluster — same code path, no test
// changes. The shared-cluster side of the parity is covered by the existing
// dependency_postgres/redis e2e (bare k3d == the shared topology) and the
// coexistence test.
func TestCIEphemeral_dependencyParity_e2e(t *testing.T) {
	requireE2E(t)

	t.Setenv("GITHUB_RUN_ID", "")
	t.Setenv("DEVEDGE_RUN_ID", "e2eparity")
	const name = "devedge-ci-e2eparity"
	_ = exec.Command("k3d", "cluster", "delete", name).Run()
	t.Cleanup(func() { _ = exec.Command("k3d", "cluster", "delete", name).Run() })

	e := cluster.NewEnsurer(&cluster.K3dProvider{})
	e.LockDir = t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	target, err := e.EnsureEphemeral(ctx)
	if err != nil {
		t.Fatalf("EnsureEphemeral: %v", err)
	}
	t.Cleanup(func() { _ = e.Teardown(target.Name) })

	prov := depruntime.NewHelmProvisioner(target.KubeContext)
	t.Cleanup(prov.Close)

	inst, err := prov.EnsureInstance(ctx, depruntime.InstanceRef{Engine: depruntime.EnginePostgres, Version: "16"})
	if err != nil {
		t.Fatalf("EnsureInstance on CI cluster: %v", err)
	}
	waitPostgresReady(t, ctx, prov)

	b, _ := depruntime.NewBinding("ci-svc", depruntime.Dep{Name: "db", Engine: depruntime.EnginePostgres, Port: 5432})
	if err := prov.EnsureDatabase(ctx, b); err != nil {
		t.Fatalf("EnsureDatabase on CI cluster: %v", err)
	}
	conn, _ := dsn.RealDSN(dsn.Conn{
		Engine: "postgres", Host: inst.Host, Port: inst.Port,
		Database: b.Database, User: b.User, Password: b.Password,
	})
	conn += "?sslmode=disable"
	psqlExec(t, ctx, conn, "CREATE TABLE t(v int); INSERT INTO t VALUES (7);")
	if got := psqlQuery(t, ctx, conn, "SELECT v FROM t"); got != "7" {
		t.Errorf("dependency roundtrip on CI cluster read %q, want 7", got)
	}
}
