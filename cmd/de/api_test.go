package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// parseAPIID
// ---------------------------------------------------------------------------

func TestParseAPIID_ValidFull(t *testing.T) {
	cases := []struct {
		input        string
		wantDomain   string
		wantSvc      string
		wantLine     string
	}{
		{"openapi/platform.data/orders/v1", "platform.data", "orders", "v1"},
		{"openapi/iam/authz/v2", "iam", "authz", "v2"},
		// Without the leading "openapi/" prefix — also accepted.
		{"platform.data/orders/v1", "platform.data", "orders", "v1"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseAPIID(tc.input)
			if err != nil {
				t.Fatalf("parseAPIID(%q) error: %v", tc.input, err)
			}
			if got.domain != tc.wantDomain {
				t.Errorf("domain = %q, want %q", got.domain, tc.wantDomain)
			}
			if got.svc != tc.wantSvc {
				t.Errorf("svc = %q, want %q", got.svc, tc.wantSvc)
			}
			if got.line != tc.wantLine {
				t.Errorf("line = %q, want %q", got.line, tc.wantLine)
			}
		})
	}
}

func TestParseAPIID_Invalid(t *testing.T) {
	cases := []string{
		"",
		"openapi/",
		"openapi/platform.data",
		"openapi/platform.data/",
		"openapi//orders/v1",
		"just-one-segment",
	}
	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			_, err := parseAPIID(id)
			if err == nil {
				t.Errorf("parseAPIID(%q): expected error, got nil", id)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// arrangeSpec
// ---------------------------------------------------------------------------

func TestArrangeSpec_CopiesFile(t *testing.T) {
	dir := t.TempDir()

	// Write a fake spec in the flat location.
	srcDir := filepath.Join(dir, "openapi")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(srcDir, "orders.openapi.yaml")
	const content = "openapi: 3.0.3\ninfo:\n  title: orders\n  version: v0.1.0\n"
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Arrange into the apx layout.
	destDir := filepath.Join(dir, "openapi", "platform.data", "orders", "v1")
	dest := filepath.Join(destDir, "orders.openapi.yaml")
	if err := arrangeSpec(src, destDir, dest); err != nil {
		t.Fatalf("arrangeSpec: %v", err)
	}

	// The destination file must exist with the same content.
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest spec: %v", err)
	}
	if string(got) != content {
		t.Errorf("arranged spec content mismatch:\ngot:  %s\nwant: %s", got, content)
	}

	// The source flat file must still exist (arrangeSpec is non-destructive).
	if _, err := os.Stat(src); err != nil {
		t.Errorf("source file removed after arrangeSpec: %v", err)
	}
}

func TestArrangeSpec_MissingSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "openapi", "missing.openapi.yaml")
	dest := filepath.Join(dir, "openapi", "x", "missing", "v1", "missing.openapi.yaml")
	err := arrangeSpec(src, filepath.Dir(dest), dest)
	if err == nil {
		t.Fatal("expected error for missing source spec")
	}
	if !strings.Contains(err.Error(), "de generate") {
		t.Errorf("error should mention 'de generate', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// de api publish — CLI wiring
// ---------------------------------------------------------------------------

// runAPI executes `de api <args...>` against a fresh root command.
func runAPI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := rootCmd()
	var buf strings.Builder
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"api"}, args...))
	err := root.Execute()
	return buf.String(), err
}

// TestAPIPublishHelp checks that `de api publish --help` succeeds and surfaces
// the key flags. This is a lightweight smoke test that doesn't shell out to apx.
func TestAPIPublishHelp(t *testing.T) {
	out, err := runAPI(t, "publish", "--help")
	if err != nil {
		t.Fatalf("api publish --help: %v\n%s", err, out)
	}
	for _, keyword := range []string{
		"--api-id", "--version", "--canonical-repo", "--lifecycle", "--submit", "--skip-generate",
		"--client", "--client-out", "--client-scope", "--publish-client",
	} {
		if !strings.Contains(out, keyword) {
			t.Errorf("--help output missing flag %q:\n%s", keyword, out)
		}
	}
}

// TestAPIPublishMissingRequiredFlags checks that omitting a required flag
// returns an error rather than panicking.
func TestAPIPublishMissingRequiredFlags(t *testing.T) {
	_, err := runAPI(t, "publish", "--api-id", "openapi/x/y/v1", "--version", "v0.1.0")
	// --canonical-repo is required; cobra should error.
	if err == nil {
		t.Fatal("expected error when --canonical-repo is missing")
	}
}
