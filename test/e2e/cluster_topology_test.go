package e2e

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/cluster"
	"github.com/infobloxopen/devedge/internal/depruntime"
	"github.com/infobloxopen/devedge/internal/dsn"
)

// topoTestCluster is a sacrificial, uniquely named cluster used by the topology
// e2e. It is deliberately NOT the real shared "devedge" cluster: these tests run
// on developer machines, so they must never create/reuse/delete the developer's
// actual shared dev cluster. The shared-name *mapping* (dev → "devedge") is
// unit-tested in internal/cluster; here we prove the ensure/reuse *lifecycle*
// against real k3d.
const topoTestCluster = "devedge-e2e-topo"

// TestClusterTopology_ensureReuse_e2e (CT-4 / C1, C4, C5): on a clean machine
// EnsureCluster creates + bootstraps the cluster exactly once, a second ensure
// reuses it (no duplicate, fast path), re-ensure is idempotent, and the user's
// current kube context is never mutated.
func TestClusterTopology_ensureReuse_e2e(t *testing.T) {
	requireE2E(t)

	provider := &cluster.K3dProvider{}
	_ = exec.Command("k3d", "cluster", "delete", topoTestCluster).Run() // clean any leftover
	t.Cleanup(func() { _ = exec.Command("k3d", "cluster", "delete", topoTestCluster).Run() })

	// C5: capture the current kube context up front; ensure must leave it unchanged.
	before := currentKubeContext()

	e := cluster.NewEnsurer(provider)
	e.LockDir = t.TempDir()
	ctx := context.Background()

	// C1: clean machine → ensure creates the cluster exactly once.
	if err := e.EnsureCluster(ctx, topoTestCluster); err != nil {
		t.Fatalf("first EnsureCluster: %v", err)
	}
	if n := clusterCount(t, topoTestCluster); n != 1 {
		t.Fatalf("after first ensure, cluster count = %d, want 1", n)
	}

	// C1 reuse + C4 idempotent: a second ensure reuses (a re-create would fail with
	// "cluster already exists"); no duplicate; reuse takes the fast path.
	start := time.Now()
	if err := e.EnsureCluster(ctx, topoTestCluster); err != nil {
		t.Fatalf("second EnsureCluster (reuse): %v", err)
	}
	if n := clusterCount(t, topoTestCluster); n != 1 {
		t.Fatalf("after reuse, cluster count = %d, want 1 (no duplicate)", n)
	}
	if elapsed := time.Since(start); elapsed > 60*time.Second {
		t.Errorf("reuse took %s — expected the fast path (no create/bootstrap)", elapsed)
	}

	// C5: the user's current kube context is unchanged (FR-013/D8).
	if after := currentKubeContext(); after != before {
		t.Errorf("ensure mutated the current kube context: %q -> %q", before, after)
	}

	// The cluster is bootstrapped and reachable on its own context — cert-manager
	// (a bootstrap prerequisite) and the devedge ClusterIssuer are present.
	kubeCtx := provider.KubeContext(topoTestCluster)
	if out, err := exec.Command("kubectl", "--context", kubeCtx, "get", "ns", "cert-manager", "-o", "name").CombinedOutput(); err != nil {
		t.Errorf("bootstrapped cluster unreachable / cert-manager missing: %v\n%s", err, out)
	}
	if out, err := exec.Command("kubectl", "--context", kubeCtx, "get", "clusterissuer", "devedge-local", "-o", "name").CombinedOutput(); err != nil {
		t.Errorf("devedge ClusterIssuer not installed by bootstrap: %v\n%s", err, out)
	}
}

// TestClusterTopology_coexistence_e2e (CT-5 / SC-002): two projects declaring an
// identically named dependency ("db") on the *same* (shared) cluster get
// per-service isolated stores — neither reads the other — and dropping one leaves
// the other healthy. Co-existence rides on 003's per-(service,dependency)
// isolation; this proves it holds when both land on one resolved cluster.
func TestClusterTopology_coexistence_e2e(t *testing.T) {
	requireE2E(t)
	kubeCtx := ephemeralCluster(t) // one cluster stands in for the shared "devedge"

	prov := depruntime.NewHelmProvisioner(kubeCtx)
	t.Cleanup(prov.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	// Shared per-engine instance on the cluster.
	inst, err := prov.EnsureInstance(ctx, depruntime.InstanceRef{Engine: depruntime.EnginePostgres, Version: "16"})
	if err != nil {
		t.Fatalf("EnsureInstance: %v", err)
	}
	waitPostgresReady(t, ctx, prov)

	// Two projects, each with a dependency named "db" — the collision case.
	dep := depruntime.Dep{Name: "db", Engine: depruntime.EnginePostgres, Port: 5432}
	ba, _ := depruntime.NewBinding("svc-a", dep)
	bb, _ := depruntime.NewBinding("svc-b", dep)
	if ba.Database == bb.Database || ba.User == bb.User {
		t.Fatalf("co-located services must derive distinct db/user: a=%+v b=%+v", ba, bb)
	}
	for _, b := range []depruntime.Binding{ba, bb} {
		if err := prov.EnsureDatabase(ctx, b); err != nil {
			t.Fatalf("EnsureDatabase %s: %v", b.Service, err)
		}
	}

	connFor := func(b depruntime.Binding) string {
		d, _ := dsn.RealDSN(dsn.Conn{
			Engine: "postgres", Host: inst.Host, Port: inst.Port,
			Database: b.Database, User: b.User, Password: b.Password,
		})
		return d + "?sslmode=disable"
	}

	// Each service writes a distinct marker into its own database.
	psqlExec(t, ctx, connFor(ba), "CREATE TABLE t(v text); INSERT INTO t VALUES ('a-only');")
	psqlExec(t, ctx, connFor(bb), "CREATE TABLE t(v text); INSERT INTO t VALUES ('b-only');")

	// Isolation: each reads back ONLY its own marker (separate databases — neither
	// can see the other's table).
	if got := psqlQuery(t, ctx, connFor(ba), "SELECT v FROM t"); got != "a-only" {
		t.Errorf("svc-a reads %q, want a-only", got)
	}
	if got := psqlQuery(t, ctx, connFor(bb), "SELECT v FROM t"); got != "b-only" {
		t.Errorf("svc-b reads %q, want b-only", got)
	}

	// `down --clean` one project drops only its slice; the other stays healthy.
	if err := prov.DropDatabase(ctx, ba); err != nil {
		t.Fatalf("DropDatabase svc-a: %v", err)
	}
	if got := psqlQuery(t, ctx, connFor(bb), "SELECT v FROM t"); got != "b-only" {
		t.Errorf("after dropping svc-a, svc-b reads %q, want b-only (collateral damage)", got)
	}
}

// TestDedicatedInstance_e2e (T030 / FR-016): a dependency marked dedicated gets its
// own Helm release + statefulset, isolated from the shared per-engine instance,
// while the shared instance still serves co-located default services.
func TestDedicatedInstance_e2e(t *testing.T) {
	requireE2E(t)
	kubeCtx := ephemeralCluster(t)

	prov := depruntime.NewHelmProvisioner(kubeCtx)
	t.Cleanup(prov.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	shared, err := prov.EnsureInstance(ctx, depruntime.InstanceRef{Engine: depruntime.EnginePostgres, Version: "16"})
	if err != nil {
		t.Fatalf("shared EnsureInstance: %v", err)
	}
	ded, err := prov.EnsureInstance(ctx, depruntime.InstanceRef{
		Engine: depruntime.EnginePostgres, Version: "16", Dedicated: true, Service: "ded-svc",
	})
	if err != nil {
		t.Fatalf("dedicated EnsureInstance: %v", err)
	}

	// Distinct forwards ⇒ distinct pods/instances.
	if shared.Port == ded.Port {
		t.Errorf("shared and dedicated must forward to distinct instances (both :%d)", shared.Port)
	}
	// Both releases exist as distinct statefulsets in the deps namespace.
	for _, sts := range []string{"devedge-postgres", "devedge-postgres-ded-svc"} {
		if out, err := exec.Command("kubectl", "--context", kubeCtx, "-n", "devedge-deps",
			"get", "statefulset", sts, "-o", "name").CombinedOutput(); err != nil {
			t.Errorf("expected statefulset %q (isolated dedicated release): %v\n%s", sts, err, out)
		}
	}
}

// TestDedicatedCluster_e2e (T021 / FR-010): a cluster.dedicated project resolves to
// its OWN cluster (distinct from the shared "devedge"), is ensured independently,
// and a destructive down removes only that dedicated cluster. Uses a sacrificial
// dedicated name so the developer's real shared cluster is never touched.
func TestDedicatedCluster_e2e(t *testing.T) {
	requireE2E(t)
	provider := &cluster.K3dProvider{}

	// Resolution: dedicated → devedge-proj-<slug>; default → shared "devedge".
	ded := cluster.Resolve(provider, cluster.EnvDev, "e2e-ded", true)
	def := cluster.Resolve(provider, cluster.EnvDev, "e2e-def", false)
	if def.Name != "devedge" || !ded.Dedicated || ded.Name == def.Name {
		t.Fatalf("resolution wrong: dedicated=%+v default=%+v", ded, def)
	}

	name := ded.Name // devedge-proj-e2e-ded — sacrificial, never the real shared cluster
	_ = exec.Command("k3d", "cluster", "delete", name).Run()
	t.Cleanup(func() { _ = exec.Command("k3d", "cluster", "delete", name).Run() })

	e := cluster.NewEnsurer(provider)
	e.LockDir = t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := e.EnsureCluster(ctx, name); err != nil {
		t.Fatalf("EnsureCluster dedicated: %v", err)
	}
	if clusterCount(t, name) != 1 {
		t.Fatalf("dedicated cluster %q not created", name)
	}

	// Destructive down removes the dedicated cluster only.
	if err := e.Teardown(name); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if clusterCount(t, name) != 0 {
		t.Errorf("dedicated cluster %q not removed by destructive down", name)
	}
}

// waitPostgresReady polls the provisioner's readiness probe within the context.
func waitPostgresReady(t *testing.T, ctx context.Context, prov *depruntime.HelmProvisioner) {
	t.Helper()
	for i := 0; i < 30; i++ {
		if err := prov.Ready(ctx, depruntime.InstanceRef{Engine: depruntime.EnginePostgres}); err == nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("postgres did not become ready")
}

func psqlExec(t *testing.T, ctx context.Context, connStr, sql string) {
	t.Helper()
	if out, err := exec.CommandContext(ctx, "psql", connStr, "-v", "ON_ERROR_STOP=1", "-tAc", sql).CombinedOutput(); err != nil {
		t.Fatalf("psql exec failed: %v\nsql=%s\nout=%s", err, sql, out)
	}
}

func psqlQuery(t *testing.T, ctx context.Context, connStr, sql string) string {
	t.Helper()
	out, err := exec.CommandContext(ctx, "psql", connStr, "-tAc", sql).CombinedOutput()
	if err != nil {
		t.Fatalf("psql query failed: %v\nsql=%s\nout=%s", err, sql, out)
	}
	return strings.TrimSpace(string(out))
}

// currentKubeContext returns the active kube context, or "" if none/unavailable.
func currentKubeContext() string {
	out, err := exec.Command("kubectl", "config", "current-context").CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// clusterCount reports how many k3d clusters carry the given name (0 or 1).
func clusterCount(t *testing.T, name string) int {
	t.Helper()
	out, err := exec.Command("k3d", "cluster", "list", "-o", "json").CombinedOutput()
	if err != nil {
		t.Fatalf("k3d cluster list: %v\n%s", err, out)
	}
	var clusters []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &clusters); err != nil {
		t.Fatalf("parse k3d list: %v\n%s", err, out)
	}
	n := 0
	for _, c := range clusters {
		if c.Name == name {
			n++
		}
	}
	return n
}
