package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	dsnutil "github.com/infobloxopen/devedge/internal/dsn"
)

// seedMarkerTable is the devedge-owned bookkeeping table that records which seed has been
// applied to a database, keyed by a fingerprint of the seed source (R5/data-model §4). It
// lives with the data, so `down --clean` (DropDatabase) removes it and the next up re-seeds.
const seedMarkerTable = "devedge_seed"

// Seed applies the dev seed at seed.Path once per database. It is keyed by a content
// fingerprint recorded in the devedge_seed marker table: if a row for the fingerprint
// already exists it no-ops (returns false); otherwise it applies the seed SQL and records
// the marker in one transaction (returns true). The caller skips Seed entirely in CI/
// ephemeral environments (FR-013). Implements Applier.
func (a *ForkApplier) Seed(ctx context.Context, dsn string, seed Source) (bool, error) {
	sqlText, fingerprint, err := readSeed(seed.Path)
	if err != nil {
		return false, err
	}
	connStr, err := pqDSN(dsn)
	if err != nil {
		return false, err
	}
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return false, fmt.Errorf("seed: open database: %w", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (seed_fingerprint TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		seedMarkerTable)); err != nil {
		return false, fmt.Errorf("seed: ensure marker table: %w", err)
	}

	var exists bool
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s WHERE seed_fingerprint=$1)`, seedMarkerTable),
		fingerprint).Scan(&exists); err != nil {
		return false, fmt.Errorf("seed: check marker: %w", err)
	}
	if exists {
		slog.Info("seed: already applied", "fingerprint", fingerprint[:12])
		return false, nil // already seeded for this fingerprint
	}

	slog.Info("seed: applying", "source", seed.Path, "fingerprint", fingerprint[:12])
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("seed: begin: %w", err)
	}
	if _, err := tx.ExecContext(ctx, sqlText); err != nil {
		_ = tx.Rollback()
		return false, fmt.Errorf("seed: apply: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf(`INSERT INTO %s (seed_fingerprint) VALUES ($1)`, seedMarkerTable), fingerprint); err != nil {
		_ = tx.Rollback()
		return false, fmt.Errorf("seed: record marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("seed: commit: %w", err)
	}
	return true, nil
}

// readSeed reads the seed SQL from a file or a directory of *.sql files (sorted) and returns
// the combined SQL plus a sha256 fingerprint of its content (so editing the seed re-applies).
func readSeed(path string) (sqlText, fingerprint string, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", "", fmt.Errorf("seed: stat %s: %w", path, err)
	}
	var files []string
	if info.IsDir() {
		matches, err := filepath.Glob(filepath.Join(path, "*.sql"))
		if err != nil {
			return "", "", fmt.Errorf("seed: scan %s: %w", path, err)
		}
		sort.Strings(matches)
		files = matches
	} else {
		files = []string{path}
	}
	if len(files) == 0 {
		return "", "", fmt.Errorf("seed: no .sql files found at %s", path)
	}

	h := sha256.New()
	var b strings.Builder
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return "", "", fmt.Errorf("seed: read %s: %w", f, err)
		}
		h.Write(data)
		b.Write(data)
		b.WriteString("\n")
	}
	return b.String(), hex.EncodeToString(h.Sum(nil)), nil
}

// pqDSN normalizes a Postgres DSN for the lib/pq "postgres" driver, defaulting
// sslmode=disable (dev/CI Postgres has no TLS and lib/pq otherwise requires it).
func pqDSN(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		// SEC-005: never wrap the raw dsn (or the *url.Error verbatim, whose
		// Error() text embeds it) into the returned error.
		return "", fmt.Errorf("seed: parse dsn %s: %w", dsnutil.Redact(dsn), unwrapURLErr(err))
	}
	q := u.Query()
	if q.Get("sslmode") == "" {
		q.Set("sslmode", "disable")
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}
