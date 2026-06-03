package migrate

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	migratelib "github.com/golang-migrate/migrate/v4"
)

// defaultMigrateTimeout bounds a single Migrate call when the caller's context carries
// no deadline (R10 / analyze-gate G2: bounded timeout + clear error on exceed). The
// reconcile path (T009) passes its own deadline; this is the backstop for direct callers.
const defaultMigrateTimeout = 5 * time.Minute

// ForkApplier is the production Applier. It wraps the Infoblox golang-migrate fork
// (imported as github.com/golang-migrate/migrate/v4 via the go.mod replace) and enables
// the fork's persisted down-migration store + dirty-state recovery (WithDirtyStateConfig),
// so a rollback survives the running image's source tree changing (FR-012) and a failed
// migration auto-recovers on a corrected re-run without manual cleanup (FR-007). It is the
// single "apply migrations" implementation shared by local-run (daemon reconcile) and the
// deploy-mode image `migrate` subcommand (FR-006). The drivers it relies on (pgx/v5 database,
// file/iofs source) are registered by this package's blank imports (drivers.go).
type ForkApplier struct{}

// NewForkApplier returns a production Applier backed by the migrate fork.
func NewForkApplier() *ForkApplier { return &ForkApplier{} }

// compile-time check: the production applier satisfies the portable seam.
var _ Applier = (*ForkApplier)(nil)

// Migrate brings the database at dsn to the version declared by src — going UP or DOWN
// as needed (the relative version decides the direction). The target is the highest
// migration version in src; deploying an older source (lower target) therefore rolls the
// schema back. It is idempotent (a no-op run reports AlreadyCurrent) and recovers a dirty
// database left by a prior failed run (FR-007, common case). Implements Applier.
//
// The persisted store (store.Dir) is used as the migration SOURCE: src's files are copied
// into it additively each run, so the store holds the union of every applied version plus
// the current set. A down step therefore stays available even when the current source (an
// older image or branch) no longer ships it (FR-012, persisted down-migrations).
func (a *ForkApplier) Migrate(ctx context.Context, dsn string, src Source, store DownStore) (Result, error) {
	if store.Dir == "" {
		return Result{}, errors.New("migrate: a persisted down-store directory is required")
	}
	dbURL, err := toPgxURL(dsn)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(store.Dir, 0o755); err != nil {
		return Result{}, fmt.Errorf("prepare down-store %s: %w", store.Dir, err)
	}
	if err := copyMigrationFiles(src.Path, store.Dir); err != nil {
		return Result{}, err
	}
	// The version this source targets; the engine migrates up or down to reach it.
	target, err := maxVersion(src.Path)
	if err != nil {
		return Result{}, err
	}

	m, err := migratelib.New("file://"+filepath.ToSlash(store.Dir), dbURL)
	if err != nil {
		return Result{}, fmt.Errorf("open migrator: %w", err)
	}
	defer m.Close()
	// Enable dirty-state recovery against the store (R2/FR-007); the store is both the
	// recovery source and the persisted down-migration store.
	if err := m.WithDirtyStateConfig(store.Dir, store.Dir, true); err != nil {
		return Result{}, fmt.Errorf("enable dirty-state recovery: %w", err)
	}

	from, dirty := versionAndDirty(m)
	if from == target && !dirty {
		// Already at the declared version and clean — idempotent no-op (SC-002).
		return Result{FromVersion: from, ToVersion: from, AlreadyCurrent: true}, nil
	}

	if err := runBounded(ctx, m, func() error { return m.Migrate(target) }); err != nil {
		if errors.Is(err, migratelib.ErrNoChange) {
			return Result{FromVersion: from, ToVersion: from, AlreadyCurrent: true}, nil
		}
		// A failed apply leaves the DB dirty; the next run recovers it (FR-007, common case).
		return Result{}, fmt.Errorf("migrate to v%d: %w", target, err)
	}

	to, _ := versionAndDirty(m)
	applied := 0
	if to > from { // forward migrations applied (a down reports 0 applied, with ToVersion < FromVersion)
		if applied, err = countApplied(store.Dir, from, to); err != nil {
			return Result{}, err
		}
	}
	return Result{FromVersion: from, ToVersion: to, Applied: applied}, nil
}

// runBounded runs the migrate operation under a bounded deadline. If ctx has no deadline,
// defaultMigrateTimeout applies. On timeout it asks the engine to stop after the current
// migration (GracefulStop) and waits for it to unwind so the database lock is released,
// then returns a clear timeout error (G2).
func runBounded(ctx context.Context, m *migratelib.Migrate, run func() error) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultMigrateTimeout)
		defer cancel()
	}
	done := make(chan error, 1)
	go func() { done <- run() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// GracefulStop is buffered (cap 1); a non-blocking send avoids deadlock if the
		// engine is between migrations or already stopping.
		select {
		case m.GracefulStop <- true:
		default:
		}
		<-done // wait for the in-flight migration to finish and release the lock
		return fmt.Errorf("migration timed out: %w", ctx.Err())
	}
}

// versionAndDirty returns the database's current schema version and dirty flag, treating
// "no migration applied yet" (ErrNilVersion) and any read error as (0, false).
func versionAndDirty(m *migratelib.Migrate) (uint, bool) {
	v, dirty, err := m.Version()
	if err != nil {
		return 0, false
	}
	return v, dirty
}

// migrationFileRE matches a golang-migrate file name: <version>_<name>.(up|down).sql.
var migrationFileRE = regexp.MustCompile(`^[0-9]+_.+\.(up|down)\.sql$`)

// copyMigrationFiles copies migration SQL files from srcDir into storeDir, overwriting
// same-named files and leaving previously-stored versions in place. The store thus
// accumulates the union of every version ever applied plus the current source, so a down
// step stays available even when the current source no longer ships it (FR-012).
func copyMigrationFiles(srcDir, storeDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("read migrations %s: %w", srcDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !migrationFileRE.MatchString(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if err != nil {
			return fmt.Errorf("read %s: %w", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(storeDir, e.Name()), b, 0o644); err != nil {
			return fmt.Errorf("seed down-store %s: %w", e.Name(), err)
		}
	}
	return nil
}

// maxVersion returns the highest migration version declared in dir (0 if none).
func maxVersion(dir string) (uint, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		return 0, fmt.Errorf("scan migrations %s: %w", dir, err)
	}
	var max uint
	for _, f := range matches {
		v, err := parseVersion(strings.TrimSuffix(filepath.Base(f), ".up.sql"))
		if err != nil {
			continue
		}
		if v > max {
			max = v
		}
	}
	return max, nil
}

// countApplied reports how many migration files in dir have a version in (from, to] — the
// number applied by a Migrate call, for the FR-010 status report.
func countApplied(dir string, from, to uint) (int, error) {
	if to <= from {
		return 0, nil
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		return 0, fmt.Errorf("count applied migrations in %s: %w", dir, err)
	}
	n := 0
	for _, f := range matches {
		base := strings.TrimSuffix(filepath.Base(f), ".up.sql")
		v, err := parseVersion(base) // shared helper (fake.go); skips non-standard names
		if err != nil {
			continue
		}
		if v > from && v <= to {
			n++
		}
	}
	return n, nil
}

// toPgxURL normalizes a Postgres DSN to the scheme the fork's pgx/v5 driver registers under
// ("pgx5"), so callers may pass a standard postgres:// DSN (003's emitted form).
func toPgxURL(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse database DSN: %w", err)
	}
	switch u.Scheme {
	case "postgres", "postgresql", "pgx", "pgx5":
		u.Scheme = "pgx5"
	case "":
		return "", fmt.Errorf("database DSN %q has no scheme (expected postgres://...)", dsn)
	default:
		return "", fmt.Errorf("unsupported database DSN scheme %q (expected a postgres DSN)", u.Scheme)
	}
	// Dev/CI Postgres runs without TLS (reached over a port-forward locally, or
	// in-cluster Service DNS in deploy mode); default sslmode=disable when the DSN
	// doesn't specify one so the driver doesn't attempt a TLS negotiation.
	q := u.Query()
	if q.Get("sslmode") == "" {
		q.Set("sslmode", "disable")
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}
