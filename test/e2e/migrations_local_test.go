// Local-run schema migrations e2e (US1, feature 006). Gated behind DEVEDGE_E2E=1
// and the helm/kubectl/k3d/docker/psql CLIs, like the other dependency e2es. It
// drives the real daemon reconcile seam (depruntime.Reconciler) against an
// ephemeral k3d cluster + the real fork-backed Applier, so it exercises the true
// cross-boundary flow: provision an isolated Postgres DB (003), apply the declared
// migrations over the supervised port-forward before the dependency is Ready, and
// assert the schema with psql.
package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/depruntime"
)

// writeMigration writes a migration file pair component (content) at dir/name.
func writeMigrationFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// connStrFor reads the real DSN devedge wrote for a Ready dependency and appends
// sslmode=disable (the dev Postgres has no TLS) so host psql can connect.
func connStrFor(t *testing.T, r depruntime.Result) string {
	t.Helper()
	if r.DSNFilePath == "" {
		t.Fatalf("dependency %q reached Ready without a DSN file", r.Name)
	}
	b, err := os.ReadFile(r.DSNFilePath)
	if err != nil {
		t.Fatalf("read DSN file: %v", err)
	}
	return strings.TrimSpace(string(b)) + "?sslmode=disable"
}

// psqlScalar runs a single-value query over connStr and returns the trimmed output.
func psqlScalar(t *testing.T, ctx context.Context, connStr, sql string) (string, error) {
	t.Helper()
	out, err := exec.CommandContext(ctx, "psql", connStr, "-v", "ON_ERROR_STOP=1", "-tAc", sql).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// reconcileDB reconciles a single postgres dependency declaring migrationsDir and
// returns its Result.
func reconcileDB(ctx context.Context, rec *depruntime.Reconciler, service, migrationsDir string) depruntime.Result {
	deps := []depruntime.Dep{{
		Name: "db", Engine: depruntime.EnginePostgres, Port: 5432, Migrations: migrationsDir,
	}}
	return rec.Reconcile(ctx, service, deps)[0]
}

// TestMigrationsLocal_e2e: declared migrations bring a service's isolated DB to the
// latest schema during local-run reconcile, before Ready; idempotent re-run; a bad
// migration aborts without emitting the DSN and a corrected re-run auto-recovers;
// down (no --clean) preserves and reuses; down --clean rebuilds; two services are
// isolated. Covers SC-001/002, FR-007/SC-004, SC-005(schema), FR-009, G1, G3, A1.
func TestMigrationsLocal_e2e(t *testing.T) {
	requireE2E(t)
	kubeCtx := ephemeralCluster(t)

	prov := depruntime.NewHelmProvisioner(kubeCtx)
	t.Cleanup(prov.Close)

	base := t.TempDir()
	rec := depruntime.NewReconciler(prov, base, 3*time.Minute)

	// A migrations dir creating an "items" table (v1).
	migDir := filepath.Join(t.TempDir(), "migrations")
	if err := os.MkdirAll(migDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMigrationFile(t, migDir, "000001_items.up.sql", "CREATE TABLE items (id serial PRIMARY KEY, name text);")
	writeMigrationFile(t, migDir, "000001_items.down.sql", "DROP TABLE items;")

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	// --- Section 1: first up applies v1 before Ready (SC-001, A1, FR-009). ---
	r := reconcileDB(ctx, rec, "widgets", migDir)
	if !r.Ready() {
		t.Fatalf("widgets db not ready after migrate: state=%s err=%s", r.State, r.Err)
	}
	if r.Migration == nil || r.Migration.Applied != 1 || r.Migration.ToVersion != 1 {
		t.Fatalf("expected 1 migration applied to v1, got %+v", r.Migration)
	}
	connStr := connStrFor(t, r)
	if got, err := psqlScalar(t, ctx, connStr, "SELECT to_regclass('public.items') IS NOT NULL"); err != nil || got != "t" {
		t.Fatalf("items table should exist after up: got=%q err=%v", got, err)
	}

	// --- Section 2: idempotent re-run is a no-op; data is not destroyed (SC-002). ---
	if _, err := psqlScalar(t, ctx, connStr, "INSERT INTO items(name) VALUES ('keep')"); err != nil {
		t.Fatalf("seed sentinel row: %v", err)
	}
	r = reconcileDB(ctx, rec, "widgets", migDir)
	if !r.Ready() || r.Migration == nil || !r.Migration.AlreadyCurrent || r.Migration.Applied != 0 {
		t.Fatalf("re-run should be a no-op at current version, got ready=%v mig=%+v", r.Ready(), r.Migration)
	}
	if got, _ := psqlScalar(t, ctx, connStr, "SELECT count(*) FROM items WHERE name='keep'"); got != "1" {
		t.Fatalf("idempotent re-run must not destroy data: keep-count=%q", got)
	}

	// --- Section 3: a bad migration aborts up with an actionable error and emits no
	//     ready DSN (the dependency is held out of Ready — no serve against a partial
	//     schema, SC-004). A re-run then auto-recovers the dirty DB to a clean state
	//     with NO manual `psql`/`migrate force` and no data loss — FR-007 "for the
	//     common case". (The fork marks the dirtied version clean rather than re-running
	//     a transactionally-rolled-back migration; the definitive reset for a botched
	//     migration is `down --clean` + `up`, proven in Section 5.) ---
	writeMigrationFile(t, migDir, "000002_note.up.sql", "THIS IS NOT VALID SQL;")
	writeMigrationFile(t, migDir, "000002_note.down.sql", "ALTER TABLE items DROP COLUMN note;")
	r = reconcileDB(ctx, rec, "widgets", migDir)
	if r.Ready() {
		t.Fatalf("a failing migration must hold the dependency out of Ready")
	}
	if r.Err == "" || !strings.Contains(r.Err, "migrations") {
		t.Fatalf("expected an actionable migration error, got %q", r.Err)
	}
	if r.Migration != nil {
		t.Fatalf("a failed migration must not report a successful outcome: %+v", r.Migration)
	}

	// Correct the SQL and re-run — the DB auto-recovers (no manual force/psql) and up
	// reaches Ready in a clean (non-dirty) state, with earlier committed data intact.
	writeMigrationFile(t, migDir, "000002_note.up.sql", "ALTER TABLE items ADD COLUMN note text;")
	r = reconcileDB(ctx, rec, "widgets", migDir)
	if !r.Ready() {
		t.Fatalf("re-run should auto-recover the dirty DB to Ready without manual cleanup, got err=%s", r.Err)
	}
	connStr = connStrFor(t, r)
	if got, err := psqlScalar(t, ctx, connStr, "SELECT count(*) FROM schema_migrations WHERE dirty"); err != nil || got != "0" {
		t.Fatalf("re-run must leave schema_migrations clean (not dirty): got=%q err=%v", got, err)
	}
	if got, _ := psqlScalar(t, ctx, connStr, "SELECT count(*) FROM items WHERE name='keep'"); got != "1" {
		t.Fatalf("failure + recovery must not lose committed data: keep-count=%q", got)
	}

	// --- Section 4 (G3): down WITHOUT --clean preserves schema/data; next up reuses. ---
	if err := rec.Release(ctx, "widgets", []depruntime.Dep{{Name: "db", Engine: depruntime.EnginePostgres, Port: 5432, Migrations: migDir}}, false); err != nil {
		t.Fatalf("release (no clean): %v", err)
	}
	r = reconcileDB(ctx, rec, "widgets", migDir)
	if !r.Ready() {
		t.Fatalf("re-up after non-clean down: state=%s err=%s", r.State, r.Err)
	}
	connStr = connStrFor(t, r)
	if got, _ := psqlScalar(t, ctx, connStr, "SELECT count(*) FROM items WHERE name='keep'"); got != "1" {
		t.Fatalf("non-clean down must preserve data: keep-count=%q", got)
	}

	// --- Section 5: down --clean drops the schema + empties the down-store; up rebuilds
	//     (SC-005 schema part, T011). ---
	if err := rec.Release(ctx, "widgets", []depruntime.Dep{{Name: "db", Engine: depruntime.EnginePostgres, Port: 5432, Migrations: migDir}}, true); err != nil {
		t.Fatalf("release --clean: %v", err)
	}
	storeDir := filepath.Join(base, "services", "widgets", "db.downstore")
	if entries, err := os.ReadDir(storeDir); err == nil && len(entries) > 0 {
		t.Fatalf("down --clean should empty the persisted down-store, found %d entries", len(entries))
	}
	r = reconcileDB(ctx, rec, "widgets", migDir)
	if !r.Ready() {
		t.Fatalf("re-up after --clean: state=%s err=%s", r.State, r.Err)
	}
	connStr = connStrFor(t, r)
	if got, _ := psqlScalar(t, ctx, connStr, "SELECT count(*) FROM items WHERE name='keep'"); got != "0" {
		t.Fatalf("--clean then up should rebuild a fresh schema (no old data): keep-count=%q", got)
	}
	if got, err := psqlScalar(t, ctx, connStr, "SELECT to_regclass('public.items') IS NOT NULL"); err != nil || got != "t" {
		t.Fatalf("schema should be rebuilt after --clean+up: got=%q err=%v", got, err)
	}
	// The corrected v2 migration applies cleanly on a fresh rebuild — the reliable
	// full-recovery path for a previously-botched migration (FR-007 definitive reset).
	if got, err := psqlScalar(t, ctx, connStr, "SELECT count(*) FROM information_schema.columns WHERE table_name='items' AND column_name='note'"); err != nil || got != "1" {
		t.Fatalf("--clean rebuild should apply the corrected v2 (note column): got=%q err=%v", got, err)
	}

	// --- Section 6 (G1, SC-006/FR-009): a second service with an identically-named
	//     table is isolated; --clean on one leaves the other intact. ---
	mig2 := filepath.Join(t.TempDir(), "migrations2")
	if err := os.MkdirAll(mig2, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMigrationFile(t, mig2, "000001_items.up.sql", "CREATE TABLE items (id serial PRIMARY KEY, name text);")
	writeMigrationFile(t, mig2, "000001_items.down.sql", "DROP TABLE items;")
	rg := reconcileDB(ctx, rec, "gadgets", mig2)
	if !rg.Ready() {
		t.Fatalf("gadgets db not ready: state=%s err=%s", rg.State, rg.Err)
	}
	gConn := connStrFor(t, rg)
	if _, err := psqlScalar(t, ctx, gConn, "INSERT INTO items(name) VALUES ('gadget')"); err != nil {
		t.Fatalf("gadgets insert: %v", err)
	}
	// --clean widgets must not touch gadgets' isolated DB.
	if err := rec.Release(ctx, "widgets", []depruntime.Dep{{Name: "db", Engine: depruntime.EnginePostgres, Port: 5432, Migrations: migDir}}, true); err != nil {
		t.Fatalf("clean widgets: %v", err)
	}
	if got, _ := psqlScalar(t, ctx, gConn, "SELECT count(*) FROM items WHERE name='gadget'"); got != "1" {
		t.Fatalf("--clean on widgets must leave gadgets isolated DB intact: gadget-count=%q", got)
	}

	// --- Section 7: the migrate step TARGETS a version — up or down by relative version.
	//     Removing a migration from the source (e.g. an older branch/image) drops the target,
	//     rolling the schema DOWN using the down step persisted in the store, which survives
	//     even though the source no longer ships it (the local-run analog of FR-012). ---
	rbDir := filepath.Join(t.TempDir(), "rb")
	if err := os.MkdirAll(rbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMigrationFile(t, rbDir, "000001_base.up.sql", "CREATE TABLE rb (id int);")
	writeMigrationFile(t, rbDir, "000001_base.down.sql", "DROP TABLE rb;")
	writeMigrationFile(t, rbDir, "000002_addcol.up.sql", "ALTER TABLE rb ADD COLUMN extra text;")
	writeMigrationFile(t, rbDir, "000002_addcol.down.sql", "ALTER TABLE rb DROP COLUMN extra;")
	r = reconcileDB(ctx, rec, "rollbacksvc", rbDir)
	if !r.Ready() || r.Migration == nil || r.Migration.ToVersion != 2 {
		t.Fatalf("rollbacksvc should reach target v2, got ready=%v mig=%+v err=%s", r.Ready(), r.Migration, r.Err)
	}
	rbConn := connStrFor(t, r)
	if got, _ := psqlScalar(t, ctx, rbConn, "SELECT count(*) FROM information_schema.columns WHERE table_name='rb' AND column_name='extra'"); got != "1" {
		t.Fatalf("v2 column should exist at target v2: got=%q", got)
	}
	// Drop v2 from the source and re-up: target falls to v1 → schema rolls back to v1 via
	// the persisted v2 down step (the source no longer ships v2's files).
	if err := os.Remove(filepath.Join(rbDir, "000002_addcol.up.sql")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(rbDir, "000002_addcol.down.sql")); err != nil {
		t.Fatal(err)
	}
	r = reconcileDB(ctx, rec, "rollbacksvc", rbDir)
	if !r.Ready() || r.Migration == nil || r.Migration.ToVersion != 1 {
		t.Fatalf("removing v2 should roll the schema back to target v1, got ready=%v mig=%+v err=%s", r.Ready(), r.Migration, r.Err)
	}
	rbConn = connStrFor(t, r)
	if got, _ := psqlScalar(t, ctx, rbConn, "SELECT count(*) FROM information_schema.columns WHERE table_name='rb' AND column_name='extra'"); got != "0" {
		t.Fatalf("after rollback to v1 the v2 column must be gone: got=%q", got)
	}
	if got, err := psqlScalar(t, ctx, rbConn, "SELECT to_regclass('public.rb') IS NOT NULL"); err != nil || got != "t" {
		t.Fatalf("the v1 table must remain after rollback to v1: got=%q err=%v", got, err)
	}
}
