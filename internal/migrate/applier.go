package migrate

import "context"

// Applier brings a target database to the declared schema and optionally seeds it.
// The real implementation wraps the infobloxopen/migrate fork (separate task); a fake
// backs unit tests without a DB.
type Applier interface {
	// Migrate applies all pending migrations from src to the database at dsn, persisting
	// applied down steps to the configured store. Idempotent; recovers a dirty DB.
	Migrate(ctx context.Context, dsn string, src Source, store DownStore) (Result, error)

	// Seed applies seed once per fresh database, keyed by a marker; no-op if already applied.
	// Skipped entirely by the caller in CI/ephemeral mode (FR-013).
	Seed(ctx context.Context, dsn string, seed Source) (seeded bool, err error)
}

// Source is a filesystem location of SQL: a migrations directory, or a seed file/dir.
type Source struct{ Path string }

// DownStore is the directory where applied up/down migration files are persisted so a
// rollback survives the running image's source tree changing (R2/FR-012). Empty Dir disables it.
type DownStore struct{ Dir string }

// Result reports the outcome of a Migrate call.
type Result struct {
	FromVersion    uint
	ToVersion      uint
	Applied        int
	AlreadyCurrent bool
}
