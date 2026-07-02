package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// hasProblem reports whether any problem contains substr.
func hasProblem(problems []string, substr string) bool {
	for _, p := range problems {
		if strings.Contains(p, substr) {
			return true
		}
	}
	return false
}

func TestLintSequence_clean(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "0001_init.up.sql", "CREATE TABLE a (id INT);")
	writeFile(t, dir, "0001_init.down.sql", "DROP TABLE a;")
	writeFile(t, dir, "0002_idx.up.sql", "CREATE INDEX CONCURRENTLY IF NOT EXISTS a_id ON a (id);")
	writeFile(t, dir, "0002_idx.down.sql", "DROP INDEX CONCURRENTLY IF EXISTS a_id;")
	// A coexisting non-SQL file must not trip the linter.
	writeFile(t, dir, "README.md", "notes")

	problems, warnings := lintSequence(dir, 1)
	if len(problems) != 0 {
		t.Fatalf("expected no problems, got %v", problems)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestLintSequence_gap(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "0001_a.up.sql", "SELECT 1;")
	writeFile(t, dir, "0001_a.down.sql", "SELECT 1;")
	writeFile(t, dir, "0003_c.up.sql", "SELECT 1;") // 0002 missing
	writeFile(t, dir, "0003_c.down.sql", "SELECT 1;")

	problems, _ := lintSequence(dir, 1)
	if !hasProblem(problems, "gap in migration sequence") {
		t.Fatalf("expected a gap problem, got %v", problems)
	}
}

func TestLintSequence_missingDown(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "0001_a.up.sql", "SELECT 1;")
	// no down for 0001

	problems, _ := lintSequence(dir, 1)
	if !hasProblem(problems, "no matching .down.sql") {
		t.Fatalf("expected a missing-down problem, got %v", problems)
	}
}

func TestLintSequence_duplicate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "0001_a.up.sql", "SELECT 1;")
	writeFile(t, dir, "0001_b.up.sql", "SELECT 1;") // duplicate version 1 up
	writeFile(t, dir, "0001_a.down.sql", "SELECT 1;")

	problems, _ := lintSequence(dir, 1)
	if !hasProblem(problems, "duplicate up migration for version 0001") {
		t.Fatalf("expected a duplicate problem, got %v", problems)
	}
}

func TestLintSequence_badName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "0001_a.up.sql", "SELECT 1;")
	writeFile(t, dir, "0001_a.down.sql", "SELECT 1;")
	writeFile(t, dir, "add_thing.sql", "SELECT 1;") // no version prefix / direction

	problems, _ := lintSequence(dir, 1)
	if !hasProblem(problems, "unrecognized migration file") {
		t.Fatalf("expected an unrecognized-file problem, got %v", problems)
	}
}

func TestLintSequence_mixedConcurrently(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "0001_a.up.sql", "SET lock_timeout = '2s';\nCREATE INDEX CONCURRENTLY IF NOT EXISTS a_id ON a (id);")
	writeFile(t, dir, "0001_a.down.sql", "DROP INDEX CONCURRENTLY IF EXISTS a_id;")

	problems, _ := lintSequence(dir, 1)
	if !hasProblem(problems, "CONCURRENTLY must be the ONLY statement") {
		t.Fatalf("expected a mixed-CONCURRENTLY problem, got %v", problems)
	}
}

func TestLintSequence_concurrentlyAloneOK(t *testing.T) {
	dir := t.TempDir()
	// CONCURRENTLY alone (with a trailing comment) must NOT be flagged.
	writeFile(t, dir, "0001_a.up.sql", "-- add index\nCREATE INDEX CONCURRENTLY IF NOT EXISTS a_id ON a (id);")
	writeFile(t, dir, "0001_a.down.sql", "DROP INDEX CONCURRENTLY IF EXISTS a_id;")

	problems, _ := lintSequence(dir, 1)
	if hasProblem(problems, "CONCURRENTLY") {
		t.Fatalf("did not expect a CONCURRENTLY problem, got %v", problems)
	}
}

func TestCountStatements(t *testing.T) {
	cases := []struct {
		sql  string
		want int
	}{
		{"CREATE INDEX CONCURRENTLY x ON t (c);", 1},
		{"-- comment only\n", 0},
		{"SET lock_timeout='2s';\nCREATE INDEX CONCURRENTLY x ON t (c);", 2},
		{"/* block */ SELECT 1; SELECT 2;", 2},
	}
	for _, c := range cases {
		if got := countStatements(c.sql); got != c.want {
			t.Errorf("countStatements(%q) = %d, want %d", c.sql, got, c.want)
		}
	}
}

func TestSlugifyMigrationName(t *testing.T) {
	cases := map[string]string{
		"Add Email Index":   "add_email_index",
		"add-email":         "add_email",
		"  Weird!!Name  ":   "weird_name",
		"0002_already_slug": "0002_already_slug",
	}
	for in, want := range cases {
		if got := slugifyMigrationName(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNextVersion(t *testing.T) {
	dir := t.TempDir()
	if v, _ := nextVersion(dir, 1); v != 1 {
		t.Fatalf("empty dir next = %d, want 1", v)
	}
	writeFile(t, dir, "0001_a.up.sql", "")
	writeFile(t, dir, "0002_b.up.sql", "")
	if v, _ := nextVersion(dir, 1); v != 3 {
		t.Fatalf("next after 0002 = %d, want 3", v)
	}
}

// TestLintSequence_sdkServiceRequires0002 verifies that when the framework
// baseline reserves 0001 (firstExpected=2), a domain 0001 is rejected with the
// reserved-baseline message and a domain sequence starting at 0002 is clean.
func TestLintSequence_sdkServiceRequires0002(t *testing.T) {
	// SDK service: first on-disk migration is 0001 -> collision with baseline.
	bad := t.TempDir()
	writeFile(t, bad, "0001_widgets.up.sql", "SELECT 1;")
	writeFile(t, bad, "0001_widgets.down.sql", "SELECT 1;")
	problems, _ := lintSequence(bad, 2)
	if !hasProblem(problems, "0001 is reserved for the SDK framework baseline") {
		t.Fatalf("expected the reserved-0001 problem, got %v", problems)
	}

	// SDK service: a clean 0002,0003 domain sequence has no problems.
	ok := t.TempDir()
	writeFile(t, ok, "0002_widgets.up.sql", "SELECT 1;")
	writeFile(t, ok, "0002_widgets.down.sql", "SELECT 1;")
	writeFile(t, ok, "0003_idx.up.sql", "SELECT 1;")
	writeFile(t, ok, "0003_idx.down.sql", "SELECT 1;")
	if problems, _ := lintSequence(ok, 2); len(problems) != 0 {
		t.Fatalf("expected no problems for a clean 0002+ SDK sequence, got %v", problems)
	}
}

// TestLintSequence_noUintUnderflow guards the sequence math against a uint
// underflow/overflow when a version 0 is present or the first version is below
// firstExpected: the reported "missing"/"found" numbers must be the real values,
// never a wrapped-around giant like 18446744073709551615.
func TestLintSequence_noUintUnderflow(t *testing.T) {
	const wrapped = "18446744073709551615" // ^uint64(0)

	// A version-0 file with a gap to 2 (raw service, firstExpected=1).
	dir := t.TempDir()
	writeFile(t, dir, "0000_zero.up.sql", "SELECT 1;")
	writeFile(t, dir, "0000_zero.down.sql", "SELECT 1;")
	writeFile(t, dir, "0002_two.up.sql", "SELECT 1;")
	writeFile(t, dir, "0002_two.down.sql", "SELECT 1;")
	problems, _ := lintSequence(dir, 1)
	for _, p := range problems {
		if strings.Contains(p, wrapped) {
			t.Fatalf("uint wrap in lint output: %q", p)
		}
	}
	if !hasProblem(problems, "found 0000") {
		t.Errorf("expected 'first migration must be 0001, found 0000', got %v", problems)
	}
	if !hasProblem(problems, "missing 0001") {
		t.Errorf("expected the gap to report 'missing 0001', got %v", problems)
	}

	// SDK service (firstExpected=2) with a first version BELOW the expected first
	// — the case a naive firstExpected-versions[0] subtraction would underflow.
	sdk := t.TempDir()
	writeFile(t, sdk, "0000_zero.up.sql", "SELECT 1;")
	writeFile(t, sdk, "0000_zero.down.sql", "SELECT 1;")
	for _, p := range func() []string { pp, _ := lintSequence(sdk, 2); return pp }() {
		if strings.Contains(p, wrapped) {
			t.Fatalf("uint wrap in SDK lint output: %q", p)
		}
	}
}

// TestNextVersion_sdkServiceStartsAt0002 verifies an empty SDK-service dir
// scaffolds 0002 first (0001 reserved), while a raw service starts at 0001.
func TestNextVersion_sdkServiceStartsAt0002(t *testing.T) {
	empty := t.TempDir()
	if v, _ := nextVersion(empty, 2); v != 2 {
		t.Errorf("empty SDK dir next = %d, want 2", v)
	}
	if v, _ := nextVersion(empty, 1); v != 1 {
		t.Errorf("empty raw dir next = %d, want 1", v)
	}
	writeFile(t, empty, "0002_a.up.sql", "")
	if v, _ := nextVersion(empty, 2); v != 3 {
		t.Errorf("next after 0002 = %d, want 3", v)
	}
}

func TestRunMigrateNew_4digitAndNoLockTimeout(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "0001_init.up.sql", "")
	writeFile(t, dir, "0001_init.down.sql", "")

	cmd := migrateNewCmd()
	o := &migrateOpts{dir: dir}
	if err := runMigrateNew(cmd, o, "Add Email Index"); err != nil {
		t.Fatalf("runMigrateNew: %v", err)
	}
	upPath := filepath.Join(dir, "0002_add_email_index.up.sql")
	downPath := filepath.Join(dir, "0002_add_email_index.down.sql")
	up, err := os.ReadFile(upPath)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", upPath, err)
	}
	if _, err := os.Stat(downPath); err != nil {
		t.Fatalf("expected %s to exist: %v", downPath, err)
	}
	// The scaffolded file must NOT contain an actual `SET lock_timeout` SQL
	// statement (timeouts go on the connection; a SET line would break
	// CONCURRENTLY DDL). Prose in the header comment is fine — check non-comment
	// lines only.
	for _, line := range strings.Split(string(up), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		if strings.Contains(strings.ToLower(line), "lock_timeout") || strings.Contains(strings.ToLower(line), "statement_timeout") {
			t.Fatalf("scaffolded migration must not contain a timeout SET statement, found: %q", line)
		}
	}
	// A second call advances to the next number rather than overwriting.
	if err := runMigrateNew(cmd, o, "another change"); err != nil {
		t.Fatalf("second runMigrateNew: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "0003_another_change.up.sql")); err != nil {
		t.Fatalf("expected 0003_another_change.up.sql: %v", err)
	}
}
