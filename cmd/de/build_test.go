package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infobloxopen/devedge/internal/makefrag"
)

// runDe executes `de <args...>` against a fresh root command, returning combined
// output. It mirrors runAPI in api_test.go.
func runDe(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := rootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestSync_CreatesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	// First run: creates the managed fragment.
	out, err := runDe(t, "sync", "-C", dir)
	if err != nil {
		t.Fatalf("de sync: %v\n%s", err, out)
	}
	path := makefrag.Path(dir)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fragment not written: %v", err)
	}
	if !makefrag.IsCurrent(got) {
		t.Errorf("written fragment is not canonical:\n%s", got)
	}
	if !strings.Contains(out, "wrote") {
		t.Errorf("first run should report 'wrote', got: %s", out)
	}

	// Second run: idempotent — reports unchanged, bytes identical.
	out2, err := runDe(t, "sync", "-C", dir)
	if err != nil {
		t.Fatalf("de sync (2nd): %v\n%s", err, out2)
	}
	if !strings.Contains(out2, "unchanged") {
		t.Errorf("second run should report 'unchanged', got: %s", out2)
	}
	got2, _ := os.ReadFile(path)
	if !bytes.Equal(got, got2) {
		t.Error("idempotent regeneration changed the fragment bytes")
	}
}

func TestSync_LeavesTopLevelMakefileUntouched(t *testing.T) {
	dir := t.TempDir()
	userMakefile := "-include .devedge/make/devedge.mk\n\nrun:\n\t./app\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(userMakefile), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runDe(t, "sync", "-C", dir); err != nil {
		t.Fatalf("de sync: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "Makefile"))
	if string(got) != userMakefile {
		t.Errorf("de sync must not touch the hand-owned Makefile; got:\n%s", got)
	}
}

func TestHasBufConfig(t *testing.T) {
	dir := t.TempDir()
	if hasBufConfig(dir) {
		t.Error("empty dir should have no buf config")
	}
	if err := os.WriteFile(filepath.Join(dir, "buf.gen.yaml"), []byte("version: v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !hasBufConfig(dir) {
		t.Error("dir with buf.gen.yaml should be detected")
	}
}

func TestLintConfigured(t *testing.T) {
	dir := t.TempDir()
	if lintConfigured(dir) {
		t.Error("no golangci config should mean go vet fallback")
	}
	if err := os.WriteFile(filepath.Join(dir, ".golangci.yml"), []byte("run:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !lintConfigured(dir) {
		t.Error(".golangci.yml should be detected")
	}
}

func TestSyncGeneratedMigrations(t *testing.T) {
	dir := t.TempDir()
	genMig := filepath.Join(dir, "gen", "migrations")
	if err := os.MkdirAll(genMig, 0o755); err != nil {
		t.Fatal(err)
	}
	// The storage plugin emits reserved-band (9001+) files under gen/migrations/.
	generated := map[string]string{
		"9001_gizmos_search_vector.up.sql":   "ALTER TABLE gizmos ADD COLUMN search_vector tsvector;",
		"9001_gizmos_search_vector.down.sql": "ALTER TABLE gizmos DROP COLUMN search_vector;",
		"9002_gizmos_search_gin.up.sql":      "CREATE INDEX CONCURRENTLY ...;",
		"9002_gizmos_search_gin.down.sql":    "DROP INDEX ...;",
	}
	for name, body := range generated {
		if err := os.WriteFile(filepath.Join(genMig, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A stray below-band file in gen/migrations/ must be ignored (defensive: the
	// embedded dir only ever receives generated reserved-band files).
	if err := os.WriteFile(filepath.Join(genMig, "0002_handwritten.up.sql"), []byte("-- nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A hand-authored migration already living in the embedded dir must survive.
	modMig := filepath.Join(dir, "module", "migrations")
	if err := os.MkdirAll(modMig, 0o755); err != nil {
		t.Fatal(err)
	}
	handAuthored := filepath.Join(modMig, "0002_domain.up.sql")
	if err := os.WriteFile(handAuthored, []byte("-- my domain table"), 0o644); err != nil {
		t.Fatal(err)
	}

	// First run copies exactly the four reserved-band generated files.
	copied, err := syncGeneratedMigrations(dir)
	if err != nil {
		t.Fatalf("syncGeneratedMigrations: %v", err)
	}
	if len(copied) != 4 {
		t.Fatalf("copied %d files, want 4: %v", len(copied), copied)
	}
	for name, want := range generated {
		got, err := os.ReadFile(filepath.Join(modMig, name))
		if err != nil {
			t.Fatalf("generated migration %s not embedded: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("embedded %s = %q, want %q", name, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(modMig, "0002_handwritten.up.sql")); err == nil {
		t.Error("below-band gen file must NOT be written into the embedded dir")
	}
	if got, _ := os.ReadFile(handAuthored); string(got) != "-- my domain table" {
		t.Errorf("hand-authored migration was clobbered: %q", got)
	}

	// Second run is idempotent: identical content → nothing copied.
	copied2, err := syncGeneratedMigrations(dir)
	if err != nil {
		t.Fatalf("syncGeneratedMigrations (2nd): %v", err)
	}
	if len(copied2) != 0 {
		t.Errorf("second run should copy nothing, got: %v", copied2)
	}
}

func TestSyncGeneratedMigrations_NoGenDir(t *testing.T) {
	// A service with no INDEXED searchable resource has no gen/migrations/ — a
	// clean no-op, and the embedded dir is never created.
	dir := t.TempDir()
	copied, err := syncGeneratedMigrations(dir)
	if err != nil {
		t.Fatalf("syncGeneratedMigrations: %v", err)
	}
	if copied != nil {
		t.Errorf("no gen/migrations should copy nothing, got: %v", copied)
	}
	if _, err := os.Stat(filepath.Join(dir, "module", "migrations")); err == nil {
		t.Error("module/migrations must not be created when there is nothing to sync")
	}
}

func TestResolveProjectDir(t *testing.T) {
	if got, _ := resolveProjectDir("/some/dir"); got != "/some/dir" {
		t.Errorf("explicit dir should win, got %q", got)
	}
	wd, _ := os.Getwd()
	if got, _ := resolveProjectDir(""); got != wd {
		t.Errorf("empty dir should default to cwd %q, got %q", wd, got)
	}
}
