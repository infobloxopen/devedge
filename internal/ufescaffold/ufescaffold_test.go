package ufescaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateName checks that ValidateName accepts valid DNS labels and
// rejects invalid ones with a non-empty error message.
func TestValidateName(t *testing.T) {
	valid := []string{"discovery", "my-ufe2", "a", strings.Repeat("a", 63)}
	for _, name := range valid {
		t.Run("valid/"+name, func(t *testing.T) {
			if err := ValidateName(name); err != nil {
				t.Errorf("ValidateName(%q) = %v, want nil", name, err)
			}
		})
	}

	invalid := []struct{ name, reason string }{
		{"", "empty"},
		{"Discovery", "uppercase"},
		{"my ufe", "spaces"},
		{"9lives", "leading digit"},
		{"-leading", "leading hyphen"},
		{"trailing-", "trailing hyphen"},
		{"under_score", "underscore"},
		{strings.Repeat("a", 64), "64+ chars"},
	}
	for _, tc := range invalid {
		t.Run("invalid/"+tc.reason, func(t *testing.T) {
			if err := ValidateName(tc.name); err == nil {
				t.Errorf("ValidateName(%q) = nil, want error (%s)", tc.name, tc.reason)
			}
		})
	}
}

// placeholders are the boilerplate strings the scaffold must NOT emit — the
// whole point is that there is nothing to rename.
var placeholders = []string{
	"APPNAME", "NAME_OF_REPO", "URL-TO-APP", "csp-discovery-ufe",
	"__name__", "__protopkg__", "<no value>", "group:'manage'", `group: 'manage'`,
}

// TestRender_PlaceholderFreeTree renders a demo uFE and asserts the tree is
// placeholder-free, has the expected files, and honors the correctness
// guarantees (matching route, valid default nav group).
func TestRender_PlaceholderFreeTree(t *testing.T) {
	parent := t.TempDir()
	if err := Render(Params{Name: "demo", ParentDir: parent}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	root := filepath.Join(parent, "demo")

	// Every regular file: non-empty, no .tmpl suffix, no placeholder strings.
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(info.Name(), ".tmpl") {
			t.Errorf("rendered tree contains a .tmpl file: %q", path)
		}
		if info.Size() == 0 {
			t.Errorf("rendered file is empty: %q", path)
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, ph := range placeholders {
			if strings.Contains(string(data), ph) {
				t.Errorf("file %q contains placeholder %q", path, ph)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking tree: %v", err)
	}

	// Expected key files exist.
	expected := []string{
		"package.json",
		"angular.json",
		"extra-webpack.config.js",
		"tsconfig.json",
		"tsconfig.app.json",
		"src/main.ufe.ts",
		"src/metadata.ts",
		"src/polyfills.ts",
		"src/app/app.module.ts",
		"src/app/app.component.ts",
		"src/app/app-routing.module.ts",
		"README.md",
		"Dockerfile",
		"deploy/manifest.yaml",
		"charts/demo-ufe/Chart.yaml",
	}
	for _, rel := range expected {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("expected file %q missing: %v", rel, err)
		}
	}

	// package.json carries the correct slug.
	checkFileContains(t, filepath.Join(root, "package.json"), `"name": "csp-demo-ufe"`)

	// metadata.ts: the default nav group is the app id AND is registered in the
	// dev registry (so validation passes), and the route matches.
	meta := readFile(t, filepath.Join(root, "src/metadata.ts"))
	if !strings.Contains(meta, "staticGroupRegistry([APP_GROUP])") {
		t.Error("metadata.ts: dev registry does not include the default group")
	}
	if !strings.Contains(meta, `assertNavContributions(navItems, devRegistry)`) {
		t.Error("metadata.ts: missing loud assertNavContributions call")
	}
	if !strings.Contains(meta, `export const APP_PATH = '/demo'`) {
		t.Error("metadata.ts: APP_PATH is not '/demo'")
	}

	// app-routing registers the matching path (not path:'').
	routing := readFile(t, filepath.Join(root, "src/app/app-routing.module.ts"))
	if !strings.Contains(routing, `path: 'demo'`) {
		t.Error("app-routing.module.ts: app route does not match 'demo'")
	}

	// The chart dir is named from the uFE name (no orphaned hardcoded dir).
	checkFileContains(t, filepath.Join(root, "charts/demo-ufe/Chart.yaml"), "name: demo-ufe")

	// No committed lockfile.
	for _, lf := range []string{"pnpm-lock.yaml", "package-lock.json"} {
		if _, err := os.Stat(filepath.Join(root, lf)); err == nil {
			t.Errorf("scaffold committed a lockfile it should not: %q", lf)
		}
	}
}

// TestRender_RefusesNonEmptyTarget verifies Render never overwrites.
func TestRender_RefusesNonEmptyTarget(t *testing.T) {
	parent := t.TempDir()
	projectDir := filepath.Join(parent, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(projectDir, "existing.txt")
	if err := os.WriteFile(sentinel, []byte("leave me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Render(Params{Name: "demo", ParentDir: parent}); err == nil {
		t.Fatal("Render into non-empty target: expected error, got nil")
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "leave me" {
		t.Errorf("sentinel modified/removed: %v %q", err, string(data))
	}
}

// TestRender_UnknownPresetFails verifies an unknown preset fails with a clear
// error and writes nothing.
func TestRender_UnknownPresetFails(t *testing.T) {
	parent := t.TempDir()
	err := Render(Params{Name: "demo", ParentDir: parent, Preset: "does-not-exist"})
	if err == nil {
		t.Fatal("Render with unknown preset: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "does-not-exist") || !strings.Contains(err.Error(), "infoblox-cto") {
		t.Errorf("preset error should name the preset and point at infoblox-cto: %v", err)
	}
	// Nothing written.
	if entries, _ := os.ReadDir(parent); len(entries) != 0 {
		t.Errorf("unknown preset created %d entries, want 0", len(entries))
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return string(data)
}

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
