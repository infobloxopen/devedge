package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateName checks that ValidateName accepts valid DNS labels and
// rejects invalid ones with a non-empty error message.
func TestValidateName(t *testing.T) {
	longValid := strings.Repeat("a", 63)

	valid := []string{
		"webhooks",
		"my-svc2",
		"a",
		longValid,
	}
	for _, name := range valid {
		t.Run("valid/"+name, func(t *testing.T) {
			if err := ValidateName(name); err != nil {
				t.Errorf("ValidateName(%q) = %v, want nil", name, err)
			}
		})
	}

	longInvalid := strings.Repeat("a", 64)

	type invalidCase struct {
		name   string
		reason string // human description of why it should fail
	}
	invalid := []invalidCase{
		{"", "empty"},
		{"Webhooks", "uppercase"},
		{"my svc", "spaces"},
		{"9lives", "leading digit"},
		{"-leading", "leading hyphen"},
		{"trailing-", "trailing hyphen"},
		{"under_score", "underscore"},
		{"dot.name", "dot"},
		{longInvalid, "64+ chars"},
	}
	for _, tc := range invalid {
		t.Run("invalid/"+tc.reason, func(t *testing.T) {
			err := ValidateName(tc.name)
			if err == nil {
				t.Errorf("ValidateName(%q) = nil, want non-nil error (%s)", tc.name, tc.reason)
				return
			}
			if err.Error() == "" {
				t.Errorf("ValidateName(%q) returned empty error message", tc.name)
			}
		})
	}
}

// TestRender_RefusesNonEmptyTarget verifies that Render returns an error when
// the target directory (ParentDir/Name) already exists and is non-empty, and
// that it does not modify the directory's contents.
func TestRender_RefusesNonEmptyTarget(t *testing.T) {
	parent := t.TempDir()
	projectDir := filepath.Join(parent, "webhooks")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(projectDir, "existing.txt")
	if err := os.WriteFile(sentinel, []byte("leave me alone"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := Params{
		Name:      "webhooks",
		ParentDir: parent,
		GoVersion: "1.25",
	}
	err := Render(p)
	if err == nil {
		t.Fatal("Render into non-empty target: expected error, got nil")
	}
	if !strings.Contains(err.Error(), projectDir) {
		t.Errorf("error %q should mention the target path %q", err.Error(), projectDir)
	}

	// Verify the sentinel file was not removed or overwritten.
	data, readErr := os.ReadFile(sentinel)
	if readErr != nil {
		t.Fatalf("sentinel file was removed: %v", readErr)
	}
	if string(data) != "leave me alone" {
		t.Errorf("sentinel file was modified: %q", string(data))
	}
}

// TestRender_AllowsEmptyExistingDir verifies that Render succeeds when the
// target directory already exists but is empty (pre-created by mkdir).
// This test is expected to fail against the stub — that is intentional.
func TestRender_AllowsEmptyExistingDir(t *testing.T) {
	parent := t.TempDir()
	projectDir := filepath.Join(parent, "webhooks")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	p := Params{
		Name:      "webhooks",
		ParentDir: parent,
		GoVersion: "1.25",
	}
	if err := Render(p); err != nil {
		t.Errorf("Render into empty existing dir: unexpected error: %v", err)
	}
}

// TestRender_ValidatesBeforeWriting verifies that an invalid name causes Render
// to return an error without creating any files under ParentDir.
func TestRender_ValidatesBeforeWriting(t *testing.T) {
	parent := t.TempDir()

	p := Params{
		Name:      "BadName!", // invalid: uppercase + special char
		ParentDir: parent,
		GoVersion: "1.25",
	}
	if err := Render(p); err == nil {
		t.Fatal("Render with invalid name: expected error, got nil")
	}

	// Nothing should have been created under parent.
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("Render with invalid name created %d entries under ParentDir, want 0", len(entries))
	}
}

// TestRender_RendersTree checks generic invariants on the rendered output tree
// without tying the test to a specific final file list.
// Expected to fail against the stub — that is intentional.
func TestRender_RendersTree(t *testing.T) {
	parent := t.TempDir()

	p := Params{
		Name:      "webhooks",
		ParentDir: parent,
		GoVersion: "1.25",
	}
	if err := Render(p); err != nil {
		t.Fatalf("Render: %v", err)
	}

	root := filepath.Join(parent, "webhooks")

	// Project root must exist.
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("project root %q does not exist: %v", root, err)
	}

	// Walk the tree and apply invariants to every regular file.
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// No file should retain a ".tmpl" suffix.
		if strings.HasSuffix(info.Name(), ".tmpl") {
			t.Errorf("rendered tree contains a .tmpl file: %q", path)
		}

		// Every regular file must be non-empty.
		if info.Size() == 0 {
			t.Errorf("rendered file is empty: %q", path)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking rendered tree: %v", err)
	}

	// devedge.yaml must exist and contain the service name and hostname.
	devedgeYAML := filepath.Join(root, "devedge.yaml")
	checkFileContains(t, devedgeYAML, "name: webhooks")
	checkFileContains(t, devedgeYAML, "hostname: webhooks.dev.test")

	// go.mod must exist and contain the default module path (= Name when Module is "").
	goMod := filepath.Join(root, "go.mod")
	checkFileContains(t, goMod, "module webhooks")
}

// TestRender_ModuleOverride verifies that an explicit Module value overrides
// the default (which is Name).
// Expected to fail against the stub — that is intentional.
func TestRender_ModuleOverride(t *testing.T) {
	parent := t.TempDir()

	p := Params{
		Name:      "webhooks",
		Module:    "github.com/acme/webhooks",
		ParentDir: parent,
		GoVersion: "1.25",
	}
	if err := Render(p); err != nil {
		t.Fatalf("Render: %v", err)
	}

	goMod := filepath.Join(parent, "webhooks", "go.mod")
	checkFileContains(t, goMod, "module github.com/acme/webhooks")
}

// checkFileContains is a test helper that reads path and fails the test if the
// file does not exist or does not contain the given substring.
func checkFileContains(t *testing.T, path, substr string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected file %q to exist: %v", path, err)
	}
	if !strings.Contains(string(data), substr) {
		t.Errorf("file %q does not contain %q\ncontent:\n%s", path, substr, string(data))
	}
}
