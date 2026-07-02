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

func TestResolveProjectDir(t *testing.T) {
	if got, _ := resolveProjectDir("/some/dir"); got != "/some/dir" {
		t.Errorf("explicit dir should win, got %q", got)
	}
	wd, _ := os.Getwd()
	if got, _ := resolveProjectDir(""); got != wd {
		t.Errorf("empty dir should default to cwd %q, got %q", wd, got)
	}
}
