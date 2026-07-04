package migrate

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	migratelib "github.com/golang-migrate/migrate/v4"

	dsnutil "github.com/infobloxopen/devedge/internal/dsn"
)

// StatusResult reports where a database sits relative to the migrations on disk.
// It is read-only — computing it never applies or rolls back a migration.
type StatusResult struct {
	// CurrentVersion is the highest migration version recorded as applied in the
	// database (0 when none has been applied).
	CurrentVersion uint
	// Dirty is true when the last migration failed part-way and the schema is in an
	// indeterminate state; the next Migrate recovers it (FR-007).
	Dirty bool
	// TargetVersion is the highest migration version declared on disk (maxVersion).
	TargetVersion uint
}

// UpToDate reports whether the database is at the on-disk target and not dirty.
func (s StatusResult) UpToDate() bool {
	return !s.Dirty && s.CurrentVersion == s.TargetVersion
}

// Status reports the database's current schema version + dirty flag against the
// target declared by src, without mutating the database. It backs `de migrate
// status` (FR-010) and the post-apply gate `de migrate verify`.
func (a *ForkApplier) Status(_ context.Context, dsn string, src Source) (StatusResult, error) {
	dbURL, err := toPgxURL(dsn)
	if err != nil {
		return StatusResult{}, err
	}
	target, err := maxVersion(src.Path)
	if err != nil {
		return StatusResult{}, err
	}
	// Read directly from the source dir — Status never writes, so it needs no
	// persisted down-store copy.
	m, err := migratelib.New("file://"+filepath.ToSlash(src.Path), dbURL)
	if err != nil {
		return StatusResult{}, fmt.Errorf("open migrator: %w", err)
	}
	defer m.Close()
	cur, dirty := versionAndDirty(m)
	return StatusResult{CurrentVersion: cur, Dirty: dirty, TargetVersion: target}, nil
}

// WithConnTimeouts returns dsn with lock_timeout and statement_timeout applied on
// the migration CONNECTION via the libpq/pgx `options` startup parameter, in
// milliseconds, preserving any options/params already present. A non-positive
// duration omits that timeout.
//
// Timeouts belong on the connection, never in a migration file: a per-file `SET`
// line would be a second statement and break CREATE INDEX CONCURRENTLY (which must
// run alone, outside a transaction). This is the near-zero-downtime seatbelt — a
// contended migration then fails fast (and the deploy Job retries in a quiet
// window) instead of queueing behind live queries and stalling the database.
func WithConnTimeouts(dsn string, lock, statement time.Duration) (string, error) {
	if lock <= 0 && statement <= 0 {
		return dsn, nil
	}
	u, err := url.Parse(dsn)
	if err != nil {
		// SEC-005: never wrap the raw dsn (or the *url.Error verbatim, whose
		// Error() text embeds it) into the returned error.
		return "", fmt.Errorf("parse database DSN %s: %w", dsnutil.Redact(dsn), unwrapURLErr(err))
	}
	q := u.Query()
	var opts []string
	if existing := strings.TrimSpace(q.Get("options")); existing != "" {
		opts = append(opts, existing)
	}
	if lock > 0 {
		opts = append(opts, fmt.Sprintf("-c lock_timeout=%d", lock.Milliseconds()))
	}
	if statement > 0 {
		opts = append(opts, fmt.Sprintf("-c statement_timeout=%d", statement.Milliseconds()))
	}
	q.Set("options", strings.Join(opts, " "))
	u.RawQuery = q.Encode()
	return u.String(), nil
}
