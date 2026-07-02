package migrate

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestMaterializeComposed_OrdersBaselineAheadOfModule verifies the composed set
// is [baseline 0001] + [module 0002+] and that every file lands in destDir.
func TestMaterializeComposed_OrdersBaselineAheadOfModule(t *testing.T) {
	baseline := t.TempDir()
	writeSQL(t, baseline, "0001_framework_init.up.sql", "CREATE TABLE outbox (id text);")
	writeSQL(t, baseline, "0001_framework_init.down.sql", "DROP TABLE outbox;")
	// A non-migration file in the baseline dir must be ignored, not fail.
	writeSQL2(t, baseline, "atlas.sum", "checksum")

	module := t.TempDir()
	writeSQL(t, module, "0002_widgets.up.sql", "CREATE TABLE widgets (id text);")
	writeSQL(t, module, "0002_widgets.down.sql", "DROP TABLE widgets;")
	writeSQL(t, module, "0003_idx.up.sql", "CREATE INDEX CONCURRENTLY i ON widgets (id);")
	writeSQL(t, module, "0003_idx.down.sql", "DROP INDEX CONCURRENTLY i;")

	dest := t.TempDir()
	if err := materializeComposed(dest, baseline, module); err != nil {
		t.Fatalf("materializeComposed: %v", err)
	}

	got := upNames(t, dest)
	want := []string{"0001_framework_init.up.sql", "0002_widgets.up.sql", "0003_idx.up.sql"}
	if len(got) != len(want) {
		t.Fatalf("composed up files = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("composed[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// The atlas.sum non-migration file must not have been copied.
	if _, err := os.Stat(filepath.Join(dest, "atlas.sum")); err == nil {
		t.Error("non-migration file atlas.sum should not be composed into the target")
	}
}

// TestMaterializeComposed_DuplicateVersionFailsLoud verifies that a module file
// colliding with the framework baseline's 0001 fails loud (the reserved-0001
// rule the SDK's materialize enforces).
func TestMaterializeComposed_DuplicateVersionFailsLoud(t *testing.T) {
	baseline := t.TempDir()
	writeSQL(t, baseline, "0001_framework_init.up.sql", "CREATE TABLE outbox (id text);")
	writeSQL(t, baseline, "0001_framework_init.down.sql", "DROP TABLE outbox;")

	module := t.TempDir()
	// A domain 0001 collides with the reserved framework baseline.
	writeSQL(t, module, "0001_widgets.up.sql", "CREATE TABLE widgets (id text);")
	writeSQL(t, module, "0001_widgets.down.sql", "DROP TABLE widgets;")

	err := materializeComposed(t.TempDir(), baseline, module)
	if err == nil {
		t.Fatal("expected a duplicate-version failure, got nil")
	}
	if !containsAll(err.Error(), "duplicate migration version", "must start at 0002") {
		t.Errorf("error = %q, want it to mention the reserved 0001 / start-at-0002 rule", err)
	}
}

// TestMaterializeComposed_MalformedNameFailsLoud verifies a stray .sql with a
// non-conforming name is rejected (parity with the SDK materialize).
func TestMaterializeComposed_MalformedNameFailsLoud(t *testing.T) {
	module := t.TempDir()
	writeSQL(t, module, "0002_widgets.up.sql", "CREATE TABLE widgets (id text);")
	writeSQL(t, module, "widgets.sql", "OOPS no version prefix")

	if err := materializeComposed(t.TempDir(), module); err == nil {
		t.Fatal("expected a malformed-name failure, got nil")
	}
}

// TestComposeSource_NonModuleDirFallsBack verifies that a plain (non-Go-module)
// migrations dir composes nothing and returns the dir unchanged — the raw / non-
// SDK dep path preserving pre-WS-022 behavior.
func TestComposeSource_NonModuleDirFallsBack(t *testing.T) {
	dir := t.TempDir()
	writeSQL(t, dir, "0001_init.up.sql", "CREATE TABLE a (id int);")
	writeSQL(t, dir, "0001_init.down.sql", "DROP TABLE a;")

	src, cleanup, composed, err := ComposeSource(dir)
	if err != nil {
		t.Fatalf("ComposeSource: %v", err)
	}
	defer cleanup()
	if composed {
		t.Error("a non-module dir must not compose a baseline")
	}
	if src.Path != dir {
		t.Errorf("src.Path = %q, want the original dir %q", src.Path, dir)
	}
}

// --- helpers -----------------------------------------------------------------

func writeSQL2(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func upNames(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, filepath.Base(m))
	}
	sort.Strings(names)
	return names
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
