package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infobloxopen/devedge/pkg/config"
)

// runUFE executes `de ufe <args...>` against a fresh root command, returning
// combined output and the error. Mirrors runCompose.
func runUFE(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := rootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"ufe"}, args...))
	err := root.Execute()
	return buf.String(), err
}

// loadShell reads + strictly parses a shell file for assertions.
func loadShell(t *testing.T, file string) *config.Shell {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read shell %q: %v", file, err)
	}
	s, err := config.ParseShell(data)
	if err != nil {
		t.Fatalf("parse shell %q: %v", file, err)
	}
	return s
}

// findUFE returns the roster entry with the given id (and how many carry it).
func findUFE(s *config.Shell, id string) (config.ShellUFE, int) {
	var got config.ShellUFE
	n := 0
	for _, u := range s.Spec.UFEs {
		if u.ID == id {
			got = u
			n++
		}
	}
	return got, n
}

// TestUFENew_CreatesDefaultShell verifies that when the shell file is absent,
// `de ufe new` creates a sensible default kind: Shell built around the one uFE,
// with the derived host + name and the route/port defaults.
func TestUFENew_CreatesDefaultShell(t *testing.T) {
	dir := t.TempDir()
	shellFile := filepath.Join(dir, "shell.yaml")

	out, err := runUFE(t, "new", "discovery", "--dir", dir, "--shell", shellFile)
	if err != nil {
		t.Fatalf("ufe new: %v\n%s", err, out)
	}
	if _, err := os.Stat(shellFile); err != nil {
		t.Fatalf("default shell not created: %v", err)
	}
	if !strings.Contains(out, "created shell") {
		t.Errorf("output does not report creating the shell:\n%s", out)
	}

	s := loadShell(t, shellFile)
	// Generic create-default shell identity + host (NOT derived per-uFE).
	if s.Project() != "app" {
		t.Errorf("shell name = %q, want app", s.Project())
	}
	if s.Spec.Host != "app.dev.test" {
		t.Errorf("shell host = %q, want app.dev.test", s.Spec.Host)
	}
	if s.Spec.CDN.Host != "cdn.dev.test" {
		t.Errorf("cdn host = %q, want cdn.dev.test", s.Spec.CDN.Host)
	}
	if s.Spec.API.Method != 1 || s.Spec.API.Prefix != "/api" || s.Spec.API.Upstream != "http://127.0.0.1:8080" {
		t.Errorf("default api = %+v, want method 1 /api -> :8080", s.Spec.API)
	}

	// Exactly the one uFE with route/port defaults.
	if len(s.Spec.UFEs) != 1 {
		t.Fatalf("roster = %d, want 1", len(s.Spec.UFEs))
	}
	u, n := findUFE(s, "discovery")
	if n != 1 {
		t.Fatalf("discovery appears %d times, want 1", n)
	}
	if u.Route != "discovery" { // --route defaults to NAME
		t.Errorf("route = %q, want discovery (default = NAME)", u.Route)
	}
	if u.Upstream != "http://127.0.0.1:4201" { // --dev-port defaults to 4201
		t.Errorf("upstream = %q, want http://127.0.0.1:4201 (default dev-port)", u.Upstream)
	}
}

// TestUFENew_PresetShellHostOverride verifies a preset directory whose manifest
// declares shellHost overrides the public app.dev.test create-default shell
// host (the mechanism the private infoblox-cto preset uses to serve at
// csp.dev.test). The override is data-driven — the public core never hardcodes
// the product host.
func TestUFENew_PresetShellHostOverride(t *testing.T) {
	// A minimal, valid preset directory: a manifest with shellHost + one overlay
	// file (a manifest must overlay at least one file).
	presetDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(presetDir, "preset.json"), []byte(`{
	  "name": "test-cto",
	  "description": "test preset that overrides the shell host",
	  "shellHost": "csp.dev.test",
	  "files": [ { "path": "PRESET.txt", "template": "preset.txt" } ]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(presetDir, "preset.txt"), []byte("applied\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	shellFile := filepath.Join(dir, "shell.yaml")

	out, err := runUFE(t, "new", "discovery", "--dir", dir, "--shell", shellFile, "--preset-dir", presetDir)
	if err != nil {
		t.Fatalf("ufe new --preset-dir: %v\n%s", err, out)
	}

	s := loadShell(t, shellFile)
	if s.Spec.Host != "csp.dev.test" {
		t.Errorf("shell host = %q, want csp.dev.test (preset shellHost override)", s.Spec.Host)
	}
	// The shell identity stays the generic create-default name.
	if s.Project() != "app" {
		t.Errorf("shell name = %q, want app", s.Project())
	}
	// The preset overlay was actually applied to the scaffolded uFE.
	if _, err := os.Stat(filepath.Join(dir, "discovery", "PRESET.txt")); err != nil {
		t.Errorf("preset overlay not applied: %v", err)
	}
}

// TestUFENew_UpsertExistingShell verifies adding a NEW uFE appends without
// touching the existing entry, and re-adding an EXISTING id updates in place
// (idempotent — never duplicated).
func TestUFENew_UpsertExistingShell(t *testing.T) {
	dir := t.TempDir()
	shellFile := filepath.Join(dir, "shell.yaml")

	// Seed a shell that already has one uFE ("notes").
	seed := `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: notesapp
spec:
  host: notesapp.dev.test
  shellUpstream: http://127.0.0.1:4200
  cdn:
    host: cdn.dev.test
  api:
    method: 1
    prefix: /api
    upstream: http://127.0.0.1:8080
  ufes:
    - id: notes
      route: notes
      upstream: http://127.0.0.1:4201
`
	if err := os.WriteFile(shellFile, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	// Add a NEW uFE with an explicit route + dev-port → appended.
	out, err := runUFE(t, "new", "tags", "--dir", dir, "--shell", shellFile, "--route", "tags", "--dev-port", "4202")
	if err != nil {
		t.Fatalf("ufe new tags: %v\n%s", err, out)
	}
	if !strings.Contains(out, "added uFE to") {
		t.Errorf("expected 'added uFE to' for a new entry:\n%s", out)
	}
	s := loadShell(t, shellFile)
	if len(s.Spec.UFEs) != 2 {
		t.Fatalf("after adding new uFE, roster = %d, want 2", len(s.Spec.UFEs))
	}
	// The pre-existing entry is untouched.
	if u, n := findUFE(s, "notes"); n != 1 || u.Route != "notes" || u.Upstream != "http://127.0.0.1:4201" {
		t.Errorf("pre-existing notes entry changed: %+v (n=%d)", u, n)
	}
	if u, n := findUFE(s, "tags"); n != 1 || u.Route != "tags" || u.Upstream != "http://127.0.0.1:4202" {
		t.Errorf("new tags entry wrong: %+v (n=%d)", u, n)
	}

	// Re-run for the SAME id "tags" with different route/port → updated in place,
	// no duplicate. (Scaffold into a fresh dir so Render does not hit a non-empty
	// target; the roster wiring is what we assert.)
	dir2 := t.TempDir()
	out, err = runUFE(t, "new", "tags", "--dir", dir2, "--shell", shellFile, "--route", "tags-v2", "--dev-port", "4299")
	if err != nil {
		t.Fatalf("ufe new tags (re-run): %v\n%s", err, out)
	}
	if !strings.Contains(out, "updated uFE in") {
		t.Errorf("expected 'updated uFE in' for an existing id:\n%s", out)
	}
	s = loadShell(t, shellFile)
	if len(s.Spec.UFEs) != 2 {
		t.Fatalf("after re-upsert, roster = %d, want 2 (no duplicate)", len(s.Spec.UFEs))
	}
	u, n := findUFE(s, "tags")
	if n != 1 {
		t.Fatalf("tags appears %d times, want 1", n)
	}
	if u.Route != "tags-v2" || u.Upstream != "http://127.0.0.1:4299" {
		t.Errorf("tags not updated in place: %+v", u)
	}
}

// TestUFENew_SkipRosterWiring verifies --shell "" scaffolds only, writing no
// shell file and printing no roster/import-map output.
func TestUFENew_SkipRosterWiring(t *testing.T) {
	dir := t.TempDir()
	// A default shell.yaml would land in the CWD; run from the temp dir so we can
	// assert none is created anywhere we control.
	shellFile := filepath.Join(dir, "shell.yaml")

	out, err := runUFE(t, "new", "widgets", "--dir", dir, "--shell", "")
	if err != nil {
		t.Fatalf("ufe new --shell '': %v\n%s", err, out)
	}
	// The scaffold still happened.
	if _, err := os.Stat(filepath.Join(dir, "widgets", "package.json")); err != nil {
		t.Errorf("scaffold did not run with --shell '': %v", err)
	}
	// No roster wiring output, no shell file.
	if strings.Contains(out, "import map") || strings.Contains(out, "created shell") || strings.Contains(out, "added uFE") {
		t.Errorf("--shell '' should skip roster wiring; output:\n%s", out)
	}
	if _, err := os.Stat(shellFile); !os.IsNotExist(err) {
		t.Errorf("--shell '' wrote a shell file: %v", err)
	}
}

// TestUFENew_ReportsImportMapAndRoute verifies the roster wiring prints the
// import-map entry (id -> https://<cdn>/<route>/) and the hash mount (#<route>).
func TestUFENew_ReportsImportMapAndRoute(t *testing.T) {
	dir := t.TempDir()
	shellFile := filepath.Join(dir, "shell.yaml")

	out, err := runUFE(t, "new", "discovery", "--dir", dir, "--shell", shellFile, "--route", "disco")
	if err != nil {
		t.Fatalf("ufe new: %v\n%s", err, out)
	}
	// import map: discovery -> https://cdn.dev.test/disco/
	if !strings.Contains(out, "https://cdn.dev.test/disco/") {
		t.Errorf("output missing import-map URL:\n%s", out)
	}
	// hash mount: #disco
	if !strings.Contains(out, "#disco") {
		t.Errorf("output missing hash route #disco:\n%s", out)
	}
	// hint to route it.
	if !strings.Contains(out, "de project up -f "+shellFile) {
		t.Errorf("output missing 'de project up' hint:\n%s", out)
	}
}
