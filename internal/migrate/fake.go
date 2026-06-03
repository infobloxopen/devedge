package migrate

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// dbState is the modeled per-DSN state held by FakeApplier.
type dbState struct {
	// version is the highest successfully applied migration version (0 = pristine).
	version uint

	// dirty is true when a migration started but did not complete successfully.
	// Mirrors the schema_migrations dirty flag (§2 data-model.md).
	dirty bool

	// dirtyVersion is the version that failed and left the DB dirty.
	dirtyVersion uint

	// downStore maps version → down SQL content persisted in memory (always) and
	// optionally to DownStore.Dir on disk (§3 data-model.md).
	downStore map[uint]string

	// seeded is the in-memory devedge_seed marker (§4 data-model.md). True after
	// a successful Seed; cleared by Reset (simulating DropDatabase / --clean).
	seeded bool
}

// FakeApplier is an in-memory Applier for unit tests. It models the real behaviors
// described in migrations-contract.md §C4 and data-model.md §§2–4 without using a
// real database. Mirrors the style of internal/depruntime/fake_test.go.
type FakeApplier struct {
	mu  sync.Mutex
	dbs map[string]*dbState // keyed by DSN
}

// NewFakeApplier returns a FakeApplier ready for use.
func NewFakeApplier() *FakeApplier {
	return &FakeApplier{dbs: make(map[string]*dbState)}
}

// db returns (creating if absent) the modeled state for the given DSN. Caller must hold mu.
func (f *FakeApplier) db(dsn string) *dbState {
	s, ok := f.dbs[dsn]
	if !ok {
		s = &dbState{downStore: make(map[uint]string)}
		f.dbs[dsn] = s
	}
	return s
}

// dbState returns a copy of the modeled DB state for the given DSN (test introspection helper).
func (f *FakeApplier) dbState(dsn string) dbState {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.db(dsn)
	cp := *s
	return cp
}

// Reset wipes all modeled state for the given DSN, simulating a DropDatabase / --clean.
// This is a fake-only helper; it is NOT part of the Applier interface.
func (f *FakeApplier) Reset(dsn string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.dbs, dsn)
}

// migrationFile records a parsed migration file from src.Path.
type migrationFile struct {
	version uint
	name    string // e.g. "001_init"
	upSQL   string
	downSQL string // may be empty if no down file exists
}

// scanMigrations reads *.up.sql files from dir, matches them with *.down.sql siblings,
// and returns a sorted (ascending by version) slice. An error from ReadFile is returned;
// missing down files are allowed (downSQL left empty).
func scanMigrations(dir string) ([]migrationFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("scan migrations %s: %w", dir, err)
	}

	byVersion := make(map[uint]*migrationFile)

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		base := strings.TrimSuffix(name, ".up.sql")
		// base looks like "001_name" — leading digits are the version.
		ver, err := parseVersion(base)
		if err != nil {
			continue // skip non-standard names
		}
		upContent, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		mf := &migrationFile{version: ver, name: base, upSQL: string(upContent)}

		// Try the matching down file.
		downName := base + ".down.sql"
		downContent, derr := os.ReadFile(filepath.Join(dir, downName))
		if derr == nil {
			mf.downSQL = string(downContent)
		}

		byVersion[ver] = mf
	}

	out := make([]migrationFile, 0, len(byVersion))
	for _, mf := range byVersion {
		out = append(out, *mf)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// parseVersion extracts the leading decimal version from a migration base name like "001_init".
func parseVersion(base string) (uint, error) {
	idx := strings.IndexFunc(base, func(r rune) bool { return r < '0' || r > '9' })
	var numStr string
	if idx < 0 {
		numStr = base
	} else {
		numStr = base[:idx]
	}
	if numStr == "" {
		return 0, fmt.Errorf("no leading digits in %q", base)
	}
	v, err := strconv.ParseUint(numStr, 10, 64)
	if err != nil {
		return 0, err
	}
	return uint(v), nil
}

// isBadSQL returns true if the SQL content contains the sentinel "-- FAIL", which the
// fake treats as a migration that will fail at apply time.
func isBadSQL(sql string) bool {
	for _, line := range strings.Split(sql, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "--") && strings.Contains(line, "FAIL") {
			return true
		}
	}
	return false
}

// persistDown writes a down SQL string to DownStore.Dir on disk (if Dir != "").
func persistDown(storeDir string, version uint, downSQL string) error {
	if storeDir == "" {
		return nil
	}
	fname := filepath.Join(storeDir, fmt.Sprintf("%03d.down.sql", version))
	return os.WriteFile(fname, []byte(downSQL), 0o644)
}

// loadDownFromStore reads a down SQL string from DownStore.Dir on disk.
func loadDownFromStore(storeDir string, version uint) (string, error) {
	if storeDir == "" {
		return "", fmt.Errorf("no store configured")
	}
	fname := filepath.Join(storeDir, fmt.Sprintf("%03d.down.sql", version))
	b, err := os.ReadFile(fname)
	if err != nil {
		return "", fmt.Errorf("load down v%d from store: %w", version, err)
	}
	return string(b), nil
}

// Migrate applies all pending migrations from src to the modeled DB at dsn, persisting
// applied down steps to store. It is idempotent (SC-002) and recovers a dirty DB after
// the bad file is corrected (FR-007/SC-004). Implements Applier.
func (f *FakeApplier) Migrate(_ context.Context, dsn string, src Source, store DownStore) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	s := f.db(dsn)

	migrations, err := scanMigrations(src.Path)
	if err != nil {
		return Result{}, err
	}

	// Determine the highest version available in the source.
	var latestVersion uint
	if len(migrations) > 0 {
		latestVersion = migrations[len(migrations)-1].version
	}

	fromVersion := s.version

	// If dirty, the DB is stuck at dirtyVersion-1 (the last clean state). The fork's
	// handleDirtyState() re-runs the dirty version on the next Migrate (FR-007). We model
	// this by re-attempting the dirty version; if the source file no longer fails we
	// continue; if it still fails we return the error again.
	startFrom := s.version
	if s.dirty {
		// Retry from the version that dirtied the DB (it was the next pending one).
		startFrom = s.dirtyVersion - 1
	}

	applied := 0
	for _, mf := range migrations {
		if mf.version <= startFrom {
			continue // already applied (or below recovery point)
		}

		// Check for bad SQL sentinel.
		if isBadSQL(mf.upSQL) {
			// Mark dirty at this version (transition: start → dirty=true).
			s.dirty = true
			s.dirtyVersion = mf.version
			return Result{}, fmt.Errorf("migration v%d failed: %s", mf.version, mf.name)
		}

		// Apply: model success.
		s.version = mf.version
		s.dirty = false
		s.dirtyVersion = 0
		applied++

		// Persist down step to in-memory store.
		s.downStore[mf.version] = mf.downSQL

		// Persist down step to disk store if configured.
		if store.Dir != "" {
			if err := persistDown(store.Dir, mf.version, mf.downSQL); err != nil {
				return Result{}, fmt.Errorf("persist down v%d: %w", mf.version, err)
			}
		}
	}

	alreadyCurrent := applied == 0 && s.version == latestVersion && !s.dirty

	return Result{
		FromVersion:    fromVersion,
		ToVersion:      s.version,
		Applied:        applied,
		AlreadyCurrent: alreadyCurrent,
	}, nil
}

// Seed applies the seed source once per DSN, keyed by the devedge_seed marker (§4 data-model.md).
// Returns seeded=true on first apply, false if already applied. Implements Applier.
func (f *FakeApplier) Seed(_ context.Context, dsn string, seed Source) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	s := f.db(dsn)
	if s.seeded {
		return false, nil
	}

	// Verify the seed source is accessible.
	if _, err := os.Stat(seed.Path); err != nil {
		return false, fmt.Errorf("seed source %s: %w", seed.Path, err)
	}

	// Model applying: read all SQL files (or the single file) to confirm they exist.
	info, err := os.Stat(seed.Path)
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		if err := filepath.WalkDir(seed.Path, func(_ string, de fs.DirEntry, err error) error {
			return err
		}); err != nil {
			return false, fmt.Errorf("seed walk %s: %w", seed.Path, err)
		}
	}

	// Mark the devedge_seed marker.
	s.seeded = true
	return true, nil
}

// Down rolls back all applied versions in the modeled DB down to (but not including) toVersion,
// using the persisted down steps from store. This is a fake-only helper; it is NOT on the
// Applier interface. It models the fork's rollback path and is used by contract tests to
// assert FR-012/SC-007 (persisted-down survives source-file removal).
func (f *FakeApplier) Down(_ context.Context, dsn string, store DownStore, toVersion uint) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	s := f.db(dsn)

	// Collect versions to roll back (descending order).
	var vers []uint
	for v := range s.downStore {
		if v > toVersion {
			vers = append(vers, v)
		}
	}
	// Also try disk store for any versions not in the in-memory map.
	if store.Dir != "" {
		entries, err := os.ReadDir(store.Dir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".down.sql") {
					continue
				}
				base := strings.TrimSuffix(e.Name(), ".down.sql")
				// base is e.g. "001", "002" — parse directly.
				v64, err := strconv.ParseUint(base, 10, 64)
				if err != nil {
					continue
				}
				v := uint(v64)
				if v > toVersion {
					// Only add if not already listed.
					found := false
					for _, ev := range vers {
						if ev == v {
							found = true
							break
						}
					}
					if !found {
						vers = append(vers, v)
					}
				}
			}
		}
	}

	sort.Slice(vers, func(i, j int) bool { return vers[i] > vers[j] }) // descending

	for _, v := range vers {
		// Fetch down SQL: prefer in-memory, fall back to disk store.
		downSQL, ok := s.downStore[v]
		if !ok || downSQL == "" {
			var err error
			downSQL, err = loadDownFromStore(store.Dir, v)
			if err != nil {
				return fmt.Errorf("down v%d: %w", v, err)
			}
		}
		// Model applying the down step.
		delete(s.downStore, v)
		if v-1 > s.version {
			// shouldn't happen in normal flow, but guard
		}
		if s.version == v {
			if v == 0 || v-1 <= toVersion {
				s.version = toVersion
			} else {
				s.version = v - 1
			}
		}
	}

	if len(vers) > 0 {
		s.version = toVersion
	}

	return nil
}
