//go:build darwin

package dnsserver

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestDarwinSource_ListsRegularFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"dev.test", "example.com"} {
		writeFile(t, dir, name)
	}
	// Hidden file: skipped.
	writeFile(t, dir, ".DS_Store")
	// Directory inside the dir: skipped (non-regular).
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Invalid DNS name: skipped (with debug log; no error).
	writeFile(t, dir, "Not_A_Domain")

	src := newDarwinSuffixSourceWithDir(dir, nil)
	got, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	names := suffixNames(got)
	sort.Strings(names)
	want := []string{"dev.test", "example.com"}
	if !equalStringSlices(names, want) {
		t.Errorf("List returned %v, want %v", names, want)
	}
}

func TestDarwinSource_MissingDir_ReturnsEmptyNoError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	src := newDarwinSuffixSourceWithDir(dir, nil)
	got, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("List on missing dir returned err=%v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("List on missing dir returned %v, want empty slice", got)
	}
}

func TestDarwinSource_UnreadableDir_ReturnsError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read unreadable dirs; skipping")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	src := newDarwinSuffixSourceWithDir(dir, nil)
	if _, err := src.List(context.Background()); err == nil {
		t.Error("List on unreadable dir returned nil error")
	}
}

func TestDarwinSource_Name(t *testing.T) {
	src := newDarwinSuffixSourceWithDir(t.TempDir(), nil)
	if src.Name() != "darwin-etc-resolver" {
		t.Errorf("Name() = %q", src.Name())
	}
}

func writeFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), fs.FileMode(0o644)); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func suffixNames(in []ConfiguredSuffix) []string {
	out := make([]string, len(in))
	for i, cs := range in {
		out[i] = cs.Name
	}
	return out
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
