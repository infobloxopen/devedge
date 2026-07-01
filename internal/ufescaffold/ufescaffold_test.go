package ufescaffold

import (
	"os"
	"path/filepath"
	"regexp"
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

	// HomeComponent (routed by app-routing) MUST be declared in the AppModule,
	// or `ng build` (AOT) fails "HomeComponent is not part of any NgModule".
	assertHomeComponentDeclared(t, root)

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

// TestRender_HomeComponentDeclared is a focused structural guard for the AOT
// regression: HomeComponent is routed, so it must be declared in the AppModule.
func TestRender_HomeComponentDeclared(t *testing.T) {
	parent := t.TempDir()
	if err := Render(Params{Name: "widgets", ParentDir: parent}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	assertHomeComponentDeclared(t, filepath.Join(parent, "widgets"))
}

// declarationsRe extracts the AppModule @NgModule declarations array contents.
var declarationsRe = regexp.MustCompile(`declarations:\s*\[([^\]]*)\]`)

// assertHomeComponentDeclared fails unless AppModule imports HomeComponent AND
// lists it in the @NgModule declarations array.
func assertHomeComponentDeclared(t *testing.T, root string) {
	t.Helper()
	mod := readFile(t, filepath.Join(root, "src/app/app.module.ts"))
	if !strings.Contains(mod, `import { HomeComponent } from './home.component'`) {
		t.Error("app.module.ts: does not import HomeComponent")
	}
	m := declarationsRe.FindStringSubmatch(mod)
	if m == nil {
		t.Fatalf("app.module.ts: no @NgModule declarations array found\n%s", mod)
	}
	if !strings.Contains(m[1], "HomeComponent") {
		t.Errorf("app.module.ts: HomeComponent not in declarations %q — ng build (AOT) would fail", strings.TrimSpace(m[1]))
	}
}

// writeFile is a test helper that writes a file, creating parent dirs.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRender_PresetDirOverlays verifies --preset-dir renders a fixture preset
// through the base template data and overrides a base file at the target path.
func TestRender_PresetDirOverlays(t *testing.T) {
	presetDir := t.TempDir()
	writeFile(t, filepath.Join(presetDir, "preset.json"), `{
	  "name": "fixture",
	  "description": "test overlay",
	  "files": [
	    { "path": "README.md", "template": "readme.tmpl" },
	    { "path": "src/extra.ts", "template": "extra.ts" }
	  ]
	}`)
	// A templated overlay file (sees the same data as the base).
	writeFile(t, filepath.Join(presetDir, "readme.tmpl"), "# {{ .TitleName }} preset overlay\n")
	// A verbatim overlay file.
	writeFile(t, filepath.Join(presetDir, "extra.ts"), "export const EXTRA = true;\n")

	parent := t.TempDir()
	if err := Render(Params{Name: "over", ParentDir: parent, PresetDir: presetDir}); err != nil {
		t.Fatalf("Render with --preset-dir: %v", err)
	}
	root := filepath.Join(parent, "over")

	// README.md was overridden by the (rendered) overlay.
	readme := readFile(t, filepath.Join(root, "README.md"))
	if !strings.Contains(readme, "Over preset overlay") {
		t.Errorf("README.md not overridden by preset overlay (TitleName not rendered): %q", readme)
	}
	// The new file was added.
	extra := readFile(t, filepath.Join(root, "src/extra.ts"))
	if !strings.Contains(extra, "EXTRA = true") {
		t.Errorf("preset did not add src/extra.ts: %q", extra)
	}
	// A base file the preset did NOT touch still exists.
	if _, err := os.Stat(filepath.Join(root, "package.json")); err != nil {
		t.Errorf("preset removed an untouched base file: %v", err)
	}
}

// TestRender_PresetDirMalformed verifies a malformed/missing preset.json fails
// loud and writes nothing.
func TestRender_PresetDirMalformed(t *testing.T) {
	cases := []struct {
		name, json, wantSub string
	}{
		{"badjson", `{ not json`, "parsing preset manifest"},
		{"noname", `{ "files": [ {"path":"a","template":"a"} ] }`, `missing "name"`},
		{"emptyfiles", `{ "name": "x", "files": [] }`, `"files" is empty`},
		{"nopath", `{ "name": "x", "files": [ {"template":"a"} ] }`, `missing "path"`},
		{"escape", `{ "name": "x", "files": [ {"path":"../evil","template":"a"} ] }`, "stay within"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			presetDir := t.TempDir()
			writeFile(t, filepath.Join(presetDir, "preset.json"), tc.json)
			parent := t.TempDir()
			err := Render(Params{Name: "over", ParentDir: parent, PresetDir: presetDir})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
			if entries, _ := os.ReadDir(parent); len(entries) != 0 {
				t.Errorf("malformed preset left %d entries behind, want 0", len(entries))
			}
		})
	}
}

// TestRender_PresetDirMissingManifest verifies a preset dir without preset.json
// fails loud.
func TestRender_PresetDirMissingManifest(t *testing.T) {
	presetDir := t.TempDir() // no preset.json
	parent := t.TempDir()
	err := Render(Params{Name: "over", ParentDir: parent, PresetDir: presetDir})
	if err == nil {
		t.Fatal("expected error for missing preset.json, got nil")
	}
	if !strings.Contains(err.Error(), "reading preset manifest") {
		t.Errorf("error should name the missing manifest: %v", err)
	}
}

// TestRender_PresetAndPresetDirExclusive verifies both flags together error.
func TestRender_PresetAndPresetDirExclusive(t *testing.T) {
	parent := t.TempDir()
	err := Render(Params{Name: "over", ParentDir: parent, Preset: "x", PresetDir: parent})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually-exclusive error, got %v", err)
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
