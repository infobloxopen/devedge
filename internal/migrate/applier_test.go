package migrate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeSQL creates a named SQL file in dir with the given content.
func writeSQL(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writeSQL %s: %v", name, err)
	}
}

// TestMigrate_Idempotency verifies SC-002: a second Migrate with no new files returns
// AlreadyCurrent=true and Applied=0.
func TestMigrate_Idempotency(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	writeSQL(t, src, "001_init.up.sql", "CREATE TABLE a (id INT);")
	writeSQL(t, src, "001_init.down.sql", "DROP TABLE a;")

	fa := NewFakeApplier()
	dsn := "postgres://localhost/testdb"

	r1, err := fa.Migrate(ctx, dsn, Source{Path: src}, DownStore{})
	if err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if r1.Applied != 1 {
		t.Errorf("first Migrate: want Applied=1, got %d", r1.Applied)
	}
	if r1.AlreadyCurrent {
		t.Errorf("first Migrate: want AlreadyCurrent=false")
	}

	r2, err := fa.Migrate(ctx, dsn, Source{Path: src}, DownStore{})
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if r2.Applied != 0 {
		t.Errorf("second Migrate: want Applied=0, got %d", r2.Applied)
	}
	if !r2.AlreadyCurrent {
		t.Errorf("second Migrate: want AlreadyCurrent=true")
	}
	if r2.FromVersion != r2.ToVersion {
		t.Errorf("second Migrate: FromVersion=%d ToVersion=%d, should be equal", r2.FromVersion, r2.ToVersion)
	}
}

// TestMigrate_FailedThenCorrectedRecovery verifies FR-007/SC-004: a failed migration leaves
// the DB dirty; after the failure condition is cleared the next Migrate recovers and succeeds
// without any manual reset.
func TestMigrate_FailedThenCorrectedRecovery(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	writeSQL(t, src, "001_init.up.sql", "CREATE TABLE a (id INT);")
	writeSQL(t, src, "001_init.down.sql", "DROP TABLE a;")
	writeSQL(t, src, "002_add_col.up.sql", "-- FAIL\nALTER TABLE a ADD COLUMN v TEXT;")
	writeSQL(t, src, "002_add_col.down.sql", "ALTER TABLE a DROP COLUMN v;")

	fa := NewFakeApplier()
	dsn := "postgres://localhost/testdb2"

	// First Migrate applies 001, then fails on 002.
	_, err := fa.Migrate(ctx, dsn, Source{Path: src}, DownStore{})
	if err == nil {
		t.Fatal("expected error from bad migration, got nil")
	}

	// Correct the bad migration file.
	writeSQL(t, src, "002_add_col.up.sql", "ALTER TABLE a ADD COLUMN v TEXT;")

	// Second Migrate must recover and succeed — no manual reset needed.
	r2, err := fa.Migrate(ctx, dsn, Source{Path: src}, DownStore{})
	if err != nil {
		t.Fatalf("recovery Migrate: %v", err)
	}
	if r2.Applied < 1 {
		t.Errorf("recovery Migrate: want at least 1 Applied, got %d", r2.Applied)
	}
	if r2.ToVersion != 2 {
		t.Errorf("recovery Migrate: want ToVersion=2, got %d", r2.ToVersion)
	}
}

// TestMigrate_PersistedDownSurvivesSourceRemoval verifies FR-012/SC-007: down steps
// persisted by an earlier apply remain usable after the source files are removed from src.Path.
func TestMigrate_PersistedDownSurvivesSourceRemoval(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	store := t.TempDir()

	writeSQL(t, src, "001_init.up.sql", "CREATE TABLE a (id INT);")
	writeSQL(t, src, "001_init.down.sql", "DROP TABLE a;")
	writeSQL(t, src, "002_add_col.up.sql", "ALTER TABLE a ADD COLUMN v TEXT;")
	writeSQL(t, src, "002_add_col.down.sql", "ALTER TABLE a DROP COLUMN v;")

	fa := NewFakeApplier()
	dsn := "postgres://localhost/testdb3"

	r, err := fa.Migrate(ctx, dsn, Source{Path: src}, DownStore{Dir: store})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if r.Applied != 2 {
		t.Fatalf("Migrate: want Applied=2, got %d", r.Applied)
	}

	// Remove source files to simulate image change / directory gone.
	if err := os.RemoveAll(src); err != nil {
		t.Fatalf("RemoveAll src: %v", err)
	}

	// Rolling back to version 0 must succeed using only the persisted store.
	if err := fa.Down(ctx, dsn, DownStore{Dir: store}, 0); err != nil {
		t.Fatalf("Down after source removal: %v", err)
	}

	// DB should now be at version 0.
	state := fa.dbState(dsn)
	if state.version != 0 {
		t.Errorf("after Down: want version=0, got %d", state.version)
	}
}

// TestSeed_OnceAndReset verifies SC-005: Seed applies once; a second call is a no-op
// (seeded=false); after a Reset (simulating --clean DropDatabase) Seed re-applies.
func TestSeed_OnceAndReset(t *testing.T) {
	ctx := context.Background()
	seedDir := t.TempDir()
	writeSQL(t, seedDir, "seed.sql", "INSERT INTO a VALUES (1);")

	fa := NewFakeApplier()
	dsn := "postgres://localhost/testdb4"

	seeded1, err := fa.Seed(ctx, dsn, Source{Path: seedDir})
	if err != nil {
		t.Fatalf("first Seed: %v", err)
	}
	if !seeded1 {
		t.Errorf("first Seed: want seeded=true")
	}

	seeded2, err := fa.Seed(ctx, dsn, Source{Path: seedDir})
	if err != nil {
		t.Fatalf("second Seed: %v", err)
	}
	if seeded2 {
		t.Errorf("second Seed: want seeded=false (already applied)")
	}

	// Simulate --clean DropDatabase.
	fa.Reset(dsn)

	seeded3, err := fa.Seed(ctx, dsn, Source{Path: seedDir})
	if err != nil {
		t.Fatalf("Seed after Reset: %v", err)
	}
	if !seeded3 {
		t.Errorf("Seed after Reset: want seeded=true")
	}
}
