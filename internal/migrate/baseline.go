package migrate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// frameworkBaselineModule is the SDK's nested migration-engine module (WS-022).
// When a service module depends on it, the SDK owns the framework migration
// baseline (0001_framework_init: outbox incl. event_seq/event_epoch, idempotency,
// dispatch cursor + dead-letter, and the cell tenant_fence/tenant_event_seq/
// tenant_event_policy tables), which the SDK's persistence/migrate.Apply composes
// AHEAD of the module's on-disk migrations (0002+) at Serve. devedge's own
// appliers MUST compose the SAME baseline so `de migrate` and `de project up`
// converge on ONE version space (1=framework, 2+=domain) with the running
// service — never a second, conflicting version space.
const frameworkBaselineModule = "github.com/infobloxopen/devedge-sdk/persistence/migrate"

// resolveBaselineDir reports the on-disk baseline/ directory of the SDK
// persistence/migrate module IF the service module enclosing queryDir depends on
// it, by running `go list -m -f '{{.Dir}}' <module>` in queryDir. It returns
// ("", false) when that module is not a dependency (a non-SDK / raw dep) or when
// the query cannot run (no go toolchain, not inside a module) — the caller then
// applies the on-disk migrations as-is with NO baseline, preserving pre-WS-022
// behavior for raw deps.
func resolveBaselineDir(queryDir string) (string, bool) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", frameworkBaselineModule)
	cmd.Dir = queryDir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	modDir := strings.TrimSpace(string(out))
	if modDir == "" {
		return "", false
	}
	baselineDir := filepath.Join(modDir, "baseline")
	// The baseline must actually ship at least one *.up.sql; otherwise treat it as
	// absent (a version mismatch that predates the baseline, or a stripped module).
	if ups, _ := filepath.Glob(filepath.Join(baselineDir, "*.up.sql")); len(ups) == 0 {
		return "", false
	}
	return baselineDir, true
}

// FrameworkBaselinePresent reports whether the service module enclosing queryDir
// depends on the SDK persistence/migrate module and thus reserves version 0001
// for the framework baseline. `de migrate lint`/`new` use it to require the first
// on-disk (domain) migration to be 0002 for an SDK service and 0001 otherwise.
func FrameworkBaselinePresent(queryDir string) bool {
	_, present := resolveBaselineDir(queryDir)
	return present
}

// ComposeSource returns the migration Source to apply for the on-disk module
// migrations at migrationsDir. When the enclosing service module depends on the
// SDK persistence/migrate module, it materializes [framework baseline 0001] +
// [module 0002+] into a fresh temp dir (mirroring the SDK's materialize: strict
// numeric order, fail-loud on a duplicate version) and returns that dir; the
// returned cleanup removes it. For a non-SDK / raw dep it returns migrationsDir
// unchanged with a no-op cleanup, so today's behavior is preserved. composed
// reports whether a baseline was composed.
//
// Both devedge appliers (the CLI `de migrate` and the `de project up` reconcile)
// route through this, so both apply the exact set the service applies at Serve.
// Because infobloxopen/migrate is idempotent (it skips versions already recorded
// in schema_migrations), whichever path runs first applies and the others no-op.
func ComposeSource(migrationsDir string) (src Source, cleanup func(), composed bool, err error) {
	noop := func() {}
	baselineDir, present := resolveBaselineDir(migrationsDir)
	if !present {
		return Source{Path: migrationsDir}, noop, false, nil
	}
	tmp, err := os.MkdirTemp("", "devedge-migrate-composed-")
	if err != nil {
		return Source{}, noop, false, fmt.Errorf("compose migrations: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }
	if err := materializeComposed(tmp, baselineDir, migrationsDir); err != nil {
		cleanup()
		return Source{}, noop, false, err
	}
	return Source{Path: tmp}, cleanup, true, nil
}

// materializeComposed copies the *.sql migration files from the ordered source
// dirs (framework baseline first, then the module dir) into destDir, validating
// names and failing loud on a duplicate version — the framework baseline owns
// 0001, so module migrations must start at 0002. This mirrors the SDK's
// persistence/migrate materialize() so both appliers agree on the version space.
func materializeComposed(destDir string, sets ...string) error {
	seen := map[uint]string{}
	for _, dir := range sets {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("read migrations %s: %w", dir, err)
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if !e.IsDir() {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			if !migrationFileRE.MatchString(name) {
				// A stray .sql with a non-conforming name is a mistake — fail loud;
				// other files (README, .gitkeep) are ignored.
				if strings.HasSuffix(name, ".sql") {
					return fmt.Errorf("malformed migration file name %q (want NNNN_<desc>.{up,down}.sql)", name)
				}
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return fmt.Errorf("read %s: %w", name, err)
			}
			if err := os.WriteFile(filepath.Join(destDir, name), b, 0o644); err != nil {
				return fmt.Errorf("stage %s: %w", name, err)
			}
			if strings.HasSuffix(name, ".up.sql") {
				v, err := parseVersion(strings.TrimSuffix(name, ".up.sql"))
				if err != nil {
					return err
				}
				if prev, dup := seen[v]; dup {
					return fmt.Errorf("duplicate migration version %d (%q and %q) — the framework baseline owns 0001; module migrations must start at 0002", v, prev, name)
				}
				seen[v] = name
			}
		}
	}
	return nil
}
