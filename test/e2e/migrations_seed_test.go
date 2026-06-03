// Dev-seed e2e (US3, feature 006). Gated behind DEVEDGE_E2E=1. It declares migrations + a
// seed, and asserts (against the per-service isolated DB over the supervised port-forward):
// the seed is applied once after migration, a re-up does not duplicate it, `down --clean`
// then up re-seeds, and an ephemeral/CI environment applies the schema but skips the seed
// (FR-013).
package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/cluster"
	"github.com/infobloxopen/devedge/internal/depruntime"
)

func TestMigrationsSeed_e2e(t *testing.T) {
	requireE2E(t)
	kubeCtx := ephemeralCluster(t)
	prov := depruntime.NewHelmProvisioner(kubeCtx)
	t.Cleanup(prov.Close)
	base := t.TempDir()
	rec := depruntime.NewReconciler(prov, base, 3*time.Minute)

	migDir := filepath.Join(t.TempDir(), "m")
	if err := os.MkdirAll(migDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMigrationFile(t, migDir, "000001_notes.up.sql", "CREATE TABLE notes (id serial PRIMARY KEY, body text);")
	writeMigrationFile(t, migDir, "000001_notes.down.sql", "DROP TABLE notes;")
	seedFile := filepath.Join(t.TempDir(), "dev.sql")
	if err := os.WriteFile(seedFile, []byte("INSERT INTO notes(body) VALUES ('hello');"), 0o644); err != nil {
		t.Fatal(err)
	}

	dep := depruntime.Dep{Name: "db", Engine: depruntime.EnginePostgres, Port: 5432, Migrations: migDir, Seed: seedFile}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	reconcile := func(env cluster.Environment) depruntime.Result {
		return rec.Reconcile(ctx, "seedsvc", []depruntime.Dep{dep}, env)[0]
	}
	release := func(clean bool) {
		if err := rec.Release(ctx, "seedsvc", []depruntime.Dep{dep}, clean); err != nil {
			t.Fatalf("release(clean=%v): %v", clean, err)
		}
	}

	// 1) up (dev): the seed is applied once, after the migration.
	r := reconcile(cluster.EnvDev)
	if !r.Ready() || r.Seed == nil || !r.Seed.Applied {
		t.Fatalf("expected seed applied, got ready=%v seed=%+v err=%s", r.Ready(), r.Seed, r.Err)
	}
	conn := connStrFor(t, r)
	if got, _ := psqlScalar(t, ctx, conn, "SELECT count(*) FROM notes WHERE body='hello'"); got != "1" {
		t.Fatalf("seed row should be present after up: count=%q", got)
	}

	// 2) re-up: apply-once — no duplicate, reported as already-seeded (SC-005 seed part).
	r = reconcile(cluster.EnvDev)
	if !r.Ready() || r.Seed == nil || r.Seed.Applied {
		t.Fatalf("re-up should report already-seeded, got seed=%+v", r.Seed)
	}
	if got, _ := psqlScalar(t, ctx, conn, "SELECT count(*) FROM notes"); got != "1" {
		t.Fatalf("seed must apply once (no duplicate on re-up): count=%q", got)
	}

	// 3) down --clean then up: the DB drop removes the marker, so up re-seeds.
	release(true)
	r = reconcile(cluster.EnvDev)
	if !r.Ready() || r.Seed == nil || !r.Seed.Applied {
		t.Fatalf("--clean then up should re-seed, got seed=%+v err=%s", r.Seed, r.Err)
	}
	conn = connStrFor(t, r)
	if got, _ := psqlScalar(t, ctx, conn, "SELECT count(*) FROM notes WHERE body='hello'"); got != "1" {
		t.Fatalf("re-seed row should be present after --clean+up: count=%q", got)
	}

	// 4) ephemeral/CI: the schema is applied but the seed is skipped entirely (FR-013).
	release(true)
	r = reconcile(cluster.EnvEphemeral)
	if !r.Ready() || r.Seed == nil || !r.Seed.SkippedCI {
		t.Fatalf("CI up should skip the seed, got ready=%v seed=%+v err=%s", r.Ready(), r.Seed, r.Err)
	}
	conn = connStrFor(t, r)
	if got, err := psqlScalar(t, ctx, conn, "SELECT to_regclass('public.notes') IS NOT NULL"); err != nil || got != "t" {
		t.Fatalf("schema should be applied even in CI: got=%q err=%v", got, err)
	}
	if got, _ := psqlScalar(t, ctx, conn, "SELECT count(*) FROM notes"); got != "0" {
		t.Fatalf("seed must be skipped in CI (no rows): count=%q", got)
	}
}
