// Deploy-mode schema-migration e2e (US2, feature 006). Gated behind DEVEDGE_E2E=1.
// It deploys a service whose image exposes a `migrate` subcommand (the testdata fixture):
// the Helm pre-install/pre-upgrade hook Job applies the schema before the Deployment rolls
// (FR-003/FR-006-deploy/SC-003), and a redeploy of an image that drops a migration rolls the
// schema back using the down step persisted in the side-provisioned PVC — even though that
// image no longer ships the down file (FR-012/SC-007).
package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/cluster"
	"github.com/infobloxopen/devedge/internal/deploy"
	"github.com/infobloxopen/devedge/internal/depruntime"
	"github.com/infobloxopen/devedge/internal/dsn"
	"github.com/infobloxopen/devedge/internal/helm"
)

const (
	mV1Up   = "000001_widgets.up.sql"
	mV1Down = "000001_widgets.down.sql"
	mV2Up   = "000002_note.up.sql"
	mV2Down = "000002_note.down.sql"
)

var migV1 = map[string]string{
	mV1Up:   "CREATE TABLE widgets (id serial PRIMARY KEY);",
	mV1Down: "DROP TABLE widgets;",
}

// migV2 adds a column on top of v1.
var migV2 = map[string]string{
	mV1Up:   "CREATE TABLE widgets (id serial PRIMARY KEY);",
	mV1Down: "DROP TABLE widgets;",
	mV2Up:   "ALTER TABLE widgets ADD COLUMN note text;",
	mV2Down: "ALTER TABLE widgets DROP COLUMN note;",
}

// buildMigrateImage cross-compiles the testdata migrate fixture for the cluster's arch,
// bakes the given migrations into /migrations, builds the image and imports it into k3d.
func buildMigrateImage(t *testing.T, ctx context.Context, clusterName, tag string, migrations map[string]string) string {
	t.Helper()
	bctx := t.TempDir()
	migDir := filepath.Join(bctx, "migrations")
	if err := os.MkdirAll(migDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range migrations {
		if err := os.WriteFile(filepath.Join(migDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Build the fixture by import path (works from this package dir; testdata is built
	// when named explicitly), statically for the cluster's linux arch.
	bin := filepath.Join(bctx, "migratesvc")
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, "github.com/infobloxopen/devedge/test/e2e/testdata/migratesvc")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build fixture: %v", err)
	}
	dockerfile := "FROM alpine:3.20\nCOPY migratesvc /migratesvc\nCOPY migrations /migrations\nENTRYPOINT [\"/migratesvc\"]\n"
	if err := os.WriteFile(filepath.Join(bctx, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	img, err := deploy.DockerK3dBuilder{}.EnsureImage(ctx, deploy.ImageSource{Build: &deploy.BuildSource{Context: bctx, Tag: tag}}, clusterName)
	if err != nil {
		t.Fatalf("build/import image %s: %v", tag, err)
	}
	return img
}

func TestMigrationsDeploy_e2e(t *testing.T) {
	requireE2E(t)
	kubeCtx := ephemeralCluster(t)
	clusterName := strings.TrimPrefix(kubeCtx, "k3d-")
	const svc = "deploysvc"
	ns := helm.DefaultNamespace

	prov := depruntime.NewHelmProvisioner(kubeCtx)
	t.Cleanup(prov.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// Provision the shared Postgres + this service's isolated DB, its in-cluster DSN
	// Secret, and the persisted down-store PVC (what the daemon reconcile does for a
	// migrations-declaring dependency) — but do NOT apply the schema here: the deploy hook
	// must be the one to apply it (proving schema-before-serve).
	inst, err := prov.EnsureInstance(ctx, depruntime.InstanceRef{Engine: depruntime.EnginePostgres, Version: "16"})
	if err != nil {
		t.Fatalf("EnsureInstance: %v", err)
	}
	ready := false
	for i := 0; i < 30; i++ {
		if prov.Ready(ctx, depruntime.InstanceRef{Engine: depruntime.EnginePostgres}) == nil {
			ready = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !ready {
		t.Fatal("postgres did not become ready")
	}
	b, err := depruntime.NewBinding(svc, depruntime.Dep{Name: "db", Engine: depruntime.EnginePostgres, Port: 5432})
	if err != nil {
		t.Fatal(err)
	}
	if err := prov.EnsureDatabase(ctx, b); err != nil {
		t.Fatalf("EnsureDatabase: %v", err)
	}
	if err := prov.EnsureConnSecret(ctx, b); err != nil {
		t.Fatalf("EnsureConnSecret: %v", err)
	}
	if err := prov.EnsureMigrationStore(ctx, b); err != nil {
		t.Fatalf("EnsureMigrationStore: %v", err)
	}

	// A host-side DSN (over the supervised port-forward) to verify the in-cluster schema.
	realDSN, err := dsn.RealDSN(dsn.Conn{
		Engine: "postgres", Host: inst.Host, Port: inst.Port,
		Database: b.Database, User: b.User, Password: b.Password,
	})
	if err != nil {
		t.Fatal(err)
	}
	connStr := realDSN + "?sslmode=disable"

	// The migrations wiring devedge's deploy path computes (cmd/de/deploy.go).
	mig := &deploy.MigrationDeploy{
		SecretName:    cluster.ProjectSlug(svc) + "-db-dsn",
		DownStorePVC:  depruntime.DownStorePVCName(svc, "db"),
		DownStorePath: "/var/lib/devedge/downstore",
	}
	wl := deploy.Workload{
		Service: svc, Port: 8080, Replicas: 1, Hostname: svc + ".dev.test",
		Deps:       []deploy.DepEnv{{Name: "db", Engine: "postgres", EnvVar: "DATABASE_URL"}},
		Migrations: mig,
	}
	deployer := deploy.NewDeployer(kubeCtx, ns, clusterName)

	// --- T012: deploy an image bundling v1+v2. The pre-install hook applies the schema
	//     before the Deployment rolls; helm --wait succeeding proves schema-before-serve. ---
	imgV2 := buildMigrateImage(t, ctx, clusterName, "migratesvc:v2", migV2)
	if _, err := deployer.Deploy(ctx, wl, deploy.ImageSource{Image: imgV2}); err != nil {
		t.Fatalf("deploy (v2) failed — hook should apply schema before the workload rolls: %v", err)
	}
	// The hook applied v1+v2 in-cluster: verify via the host port-forward.
	if got, err := psqlScalar(t, ctx, connStr, "SELECT to_regclass('public.widgets') IS NOT NULL"); err != nil || got != "t" {
		t.Fatalf("hook should have created the widgets table: got=%q err=%v", got, err)
	}
	if got, _ := psqlScalar(t, ctx, connStr, "SELECT count(*) FROM information_schema.columns WHERE table_name='widgets' AND column_name='note'"); got != "1" {
		t.Fatalf("hook should have applied v2 (note column): got=%q", got)
	}
	// SC-003 — a dependent query against the migrated schema succeeds after deploy.
	if got, err := psqlScalar(t, ctx, connStr, "SELECT count(*) FROM widgets"); err != nil || got != "0" {
		t.Fatalf("dependent query should succeed against the migrated schema: got=%q err=%v", got, err)
	}

	// --- T013: redeploy an image bundling ONLY v1 (it does not ship v2's down file). The
	//     hook targets v1, so the schema rolls back v2->v1 using the v2 down step persisted
	//     in the PVC from the first deploy (FR-012/SC-007). ---
	imgV1 := buildMigrateImage(t, ctx, clusterName, "migratesvc:v1", migV1)
	if _, err := deployer.Deploy(ctx, wl, deploy.ImageSource{Image: imgV1}); err != nil {
		t.Fatalf("redeploy (v1) failed — rollback via persisted down-store: %v", err)
	}
	if got, _ := psqlScalar(t, ctx, connStr, "SELECT count(*) FROM information_schema.columns WHERE table_name='widgets' AND column_name='note'"); got != "0" {
		t.Fatalf("redeploy to v1 should have rolled back v2 (note column gone) via the persisted store: got=%q", got)
	}
	if got, err := psqlScalar(t, ctx, connStr, "SELECT to_regclass('public.widgets') IS NOT NULL"); err != nil || got != "t" {
		t.Fatalf("v1 schema (widgets table) should remain after rollback to v1: got=%q err=%v", got, err)
	}
}
