package ufescaffold

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/infobloxopen/devedge/pkg/config"
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
	if !strings.Contains(err.Error(), "does-not-exist") || !strings.Contains(err.Error(), "--preset-dir") {
		t.Errorf("preset error should name the preset and point at --preset-dir: %v", err)
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

// TestRender_ServeTargetAndPort guards the two scaffold-bootstrap traps that a
// fresh developer hits on the very first `pnpm start`:
//   - the serve target option must be `browserTarget` (what the pinned
//     @angular-builders/custom-webpack@15 dev-server schema requires), never
//     `buildTarget` (Angular 16+), which the schema rejects; and
//   - the dev-server port must be the requested DevPort, so the shell-roster
//     upstream and the real listener agree.
func TestRender_ServeTargetAndPort(t *testing.T) {
	// Default port.
	parent := t.TempDir()
	if err := Render(Params{Name: "demo", ParentDir: parent}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	ng := readFile(t, filepath.Join(parent, "demo", "angular.json"))
	if strings.Contains(ng, "buildTarget") {
		t.Error("angular.json serve uses buildTarget — @angular-builders/custom-webpack@15 rejects it; use browserTarget")
	}
	if !strings.Contains(ng, `"browserTarget": "demo:build"`) {
		t.Error("angular.json serve does not set browserTarget")
	}
	if !strings.Contains(ng, `"port": 4201`) {
		t.Errorf("angular.json serve port is not the default DevPort (4201):\n%s", ng)
	}

	// Explicit port must reach angular.json (finding 088).
	parent2 := t.TempDir()
	if err := Render(Params{Name: "widgets", ParentDir: parent2, DevPort: 4207}); err != nil {
		t.Fatalf("Render(DevPort:4207): %v", err)
	}
	ng2 := readFile(t, filepath.Join(parent2, "widgets", "angular.json"))
	if !strings.Contains(ng2, `"port": 4207`) {
		t.Errorf("angular.json serve port did not follow DevPort=4207:\n%s", ng2)
	}
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

// TestRenderShell renders a shell from a small in-memory roster and asserts the
// proven pieces survive templatization: the roster uFE gets a single-spa
// registration + loadMfe interop shim, the index.html importmap points at the
// uFE's CDN bundle, the .npmrc scopes @infobloxopen to GitHub Packages, and the
// package.json serves on the roster's shell port.
func TestRenderShell(t *testing.T) {
	parent := t.TempDir()
	roster := &config.Shell{
		APIVersion: config.APIVersion,
		Kind:       config.KindShell,
		Metadata:   config.ObjectMeta{Name: "app"},
		Spec: config.ShellSpec{
			Host:          "app.dev.test",
			ShellUpstream: "http://127.0.0.1:4200",
			CDN:           config.ShellCDN{Host: "cdn.dev.test"},
			UFEs: []config.ShellUFE{
				{ID: "demo", Route: "demo", Upstream: "http://127.0.0.1:4201"},
			},
		},
	}
	if err := RenderShell(ShellParams{ParentDir: parent, Name: "app-shell", Roster: roster}); err != nil {
		t.Fatalf("RenderShell: %v", err)
	}
	root := filepath.Join(parent, "app-shell")

	// No .tmpl files leak into the rendered tree.
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".tmpl") {
			t.Errorf("rendered shell contains a .tmpl file: %q", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking shell tree: %v", err)
	}

	// root-config.ts: the roster uFE is registered by hash route and loaded via
	// the UMD-interop loadMfe shim (specifier == global == app id).
	rc := readFile(t, filepath.Join(root, "root-config.ts"))
	if !strings.Contains(rc, "loadMfe('demo', 'demo')") {
		t.Errorf("root-config.ts missing loadMfe registration for demo:\n%s", rc)
	}
	if !strings.Contains(rc, "activeOnHash('#demo')") {
		t.Errorf("root-config.ts missing hash route for demo:\n%s", rc)
	}
	if !strings.Contains(rc, "name: 'demo'") {
		t.Errorf("root-config.ts missing single-spa app name for demo:\n%s", rc)
	}
	// The generic shell drops the notes-specific nav-contribution assertion.
	if strings.Contains(rc, "assertNavContributions") {
		t.Errorf("root-config.ts should not carry the uFE-specific nav assertion:\n%s", rc)
	}

	// index.html: importmap points the uFE specifier at its CDN bundle.
	idx := readFile(t, filepath.Join(root, "index.html"))
	if !strings.Contains(idx, `"demo": "https://cdn.dev.test/demo/main.js"`) {
		t.Errorf("index.html importmap missing demo bundle URL:\n%s", idx)
	}
	// The left side-nav lists the uFE with a title-cased label, and the content
	// area pre-creates the uFE's single-spa mount element so it renders in-place.
	if !strings.Contains(idx, `href="#demo">Demo</a>`) {
		t.Errorf("index.html side-nav missing the demo nav item labelled 'Demo':\n%s", idx)
	}
	if !strings.Contains(idx, `id="single-spa-application:demo"`) {
		t.Errorf("index.html missing the pre-created mount element for demo:\n%s", idx)
	}

	// .npmrc scopes @infobloxopen to GitHub Packages (open-seam install).
	npmrc := readFile(t, filepath.Join(root, ".npmrc"))
	if !strings.Contains(npmrc, "@infobloxopen:registry=https://npm.pkg.github.com") {
		t.Errorf(".npmrc does not scope @infobloxopen to GitHub Packages:\n%s", npmrc)
	}
	if !strings.Contains(npmrc, "//npm.pkg.github.com/:_authToken=${GITHUB_TOKEN}") {
		t.Errorf(".npmrc missing GitHub Packages token auth:\n%s", npmrc)
	}

	// package.json: has the start script serving on the roster's shell port (4200
	// parsed from shellUpstream).
	pkg := readFile(t, filepath.Join(root, "package.json"))
	if !strings.Contains(pkg, `"start":`) || !strings.Contains(pkg, "sirv-cli") {
		t.Errorf("package.json missing start script:\n%s", pkg)
	}
	if !strings.Contains(pkg, "--port 4200") {
		t.Errorf("package.json start does not serve on the shellUpstream port 4200:\n%s", pkg)
	}
	if !strings.Contains(pkg, `"name": "app-shell"`) {
		t.Errorf("package.json name is not app-shell:\n%s", pkg)
	}
}

// TestRenderShell_DefaultsPortWhenUnparseable verifies the shell falls back to
// the default serve port when the roster's shellUpstream carries no port.
func TestRenderShell_DefaultsPortWhenUnparseable(t *testing.T) {
	parent := t.TempDir()
	roster := &config.Shell{
		Spec: config.ShellSpec{
			Host:          "app.dev.test",
			ShellUpstream: "http://shell.example", // no port
			CDN:           config.ShellCDN{Host: "cdn.dev.test"},
			UFEs:          []config.ShellUFE{{ID: "demo", Route: "demo", Upstream: "http://127.0.0.1:4201"}},
		},
	}
	if got := ShellServePort(roster); got != defaultShellPort {
		t.Fatalf("ShellServePort with no port = %d, want %d", got, defaultShellPort)
	}
	if err := RenderShell(ShellParams{ParentDir: parent, Name: "app-shell", Roster: roster}); err != nil {
		t.Fatalf("RenderShell: %v", err)
	}
	pkg := readFile(t, filepath.Join(parent, "app-shell", "package.json"))
	if !strings.Contains(pkg, "--port 9000") {
		t.Errorf("package.json did not fall back to default port 9000:\n%s", pkg)
	}
}

// TestRenderShell_RefusesNonEmptyTarget verifies RenderShell never overwrites.
func TestRenderShell_RefusesNonEmptyTarget(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "app-shell")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	roster := &config.Shell{
		Spec: config.ShellSpec{
			Host:          "app.dev.test",
			ShellUpstream: "http://127.0.0.1:4200",
			CDN:           config.ShellCDN{Host: "cdn.dev.test"},
			UFEs:          []config.ShellUFE{{ID: "demo", Route: "demo", Upstream: "http://127.0.0.1:4201"}},
		},
	}
	if err := RenderShell(ShellParams{ParentDir: parent, Name: "app-shell", Roster: roster}); err == nil {
		t.Fatal("RenderShell into non-empty target: expected error, got nil")
	}
	if data, err := os.ReadFile(filepath.Join(dir, "keep.txt")); err != nil || string(data) != "keep" {
		t.Errorf("sentinel modified/removed: %v %q", err, string(data))
	}
}

// TestRenderShell_PresetDirOverlays verifies `de ufe shell --preset-dir`
// applies a preset overlay on top of the rendered shell, rendered against the
// shell's own template data (shellData): it overrides a base shell file
// (environment.ts) and can reference shell fields (Name, ShellPort, UFEs) that a
// uFE templateData does not carry. This is the mechanism a downstream shell
// preset overlay rides on.
func TestRenderShell_PresetDirOverlays(t *testing.T) {
	presetDir := t.TempDir()
	writeFile(t, filepath.Join(presetDir, "preset.json"), `{
	  "name": "shell-fixture",
	  "description": "test shell overlay",
	  "files": [
	    { "path": "environment.ts", "template": "environment.ts" },
	    { "path": "BRANDING.md", "template": "branding.tmpl" }
	  ]
	}`)
	// Override environment.ts and reference shell-only template fields so the
	// overlay proves it renders against shellData, not a uFE templateData.
	writeFile(t, filepath.Join(presetDir, "environment.ts"),
		"export const environment = { useDevSession: false, port: {{ .ShellPort }} };\n")
	writeFile(t, filepath.Join(presetDir, "branding.tmpl"),
		"# {{ .Name }} — custom shell\n{{- range .UFEs }}\n- {{ .Label }} (#{{ .Route }})\n{{- end }}\n")

	parent := t.TempDir()
	roster := &config.Shell{
		APIVersion: config.APIVersion,
		Kind:       config.KindShell,
		Metadata:   config.ObjectMeta{Name: "app"},
		Spec: config.ShellSpec{
			Host:          "app.dev.test",
			ShellUpstream: "http://127.0.0.1:4200",
			CDN:           config.ShellCDN{Host: "cdn.dev.test"},
			UFEs:          []config.ShellUFE{{ID: "demo", Route: "demo", Upstream: "http://127.0.0.1:4201"}},
		},
	}
	if err := RenderShell(ShellParams{ParentDir: parent, Name: "app-shell", Roster: roster, PresetDir: presetDir}); err != nil {
		t.Fatalf("RenderShell with --preset-dir: %v", err)
	}
	root := filepath.Join(parent, "app-shell")

	// environment.ts was overridden by the (rendered) overlay, with the shell's
	// own port field substituted.
	env := readFile(t, filepath.Join(root, "environment.ts"))
	if !strings.Contains(env, "useDevSession: false") {
		t.Errorf("environment.ts not overridden by shell preset overlay: %q", env)
	}
	if !strings.Contains(env, "port: 4200") {
		t.Errorf("shell overlay did not render the ShellPort field (want 4200): %q", env)
	}
	// A new file was added, rendered against the shell's Name + UFEs.
	branding := readFile(t, filepath.Join(root, "BRANDING.md"))
	if !strings.Contains(branding, "app-shell — custom shell") {
		t.Errorf("shell overlay did not render Name into the added file: %q", branding)
	}
	if !strings.Contains(branding, "- Demo (#demo)") {
		t.Errorf("shell overlay did not range over UFEs: %q", branding)
	}
	// A base shell file the preset did NOT touch still exists.
	if _, err := os.Stat(filepath.Join(root, "root-config.ts")); err != nil {
		t.Errorf("shell overlay removed an untouched base file: %v", err)
	}
}

// TestRenderShell_PresetDirMalformed verifies a malformed shell preset.json
// fails loud and removes the partial shell (same atomic guarantee as Render).
func TestRenderShell_PresetDirMalformed(t *testing.T) {
	presetDir := t.TempDir()
	writeFile(t, filepath.Join(presetDir, "preset.json"), `{ "name": "x", "files": [] }`)
	parent := t.TempDir()
	roster := &config.Shell{
		Spec: config.ShellSpec{
			Host:          "app.dev.test",
			ShellUpstream: "http://127.0.0.1:4200",
			CDN:           config.ShellCDN{Host: "cdn.dev.test"},
			UFEs:          []config.ShellUFE{{ID: "demo", Route: "demo", Upstream: "http://127.0.0.1:4201"}},
		},
	}
	err := RenderShell(ShellParams{ParentDir: parent, Name: "app-shell", Roster: roster, PresetDir: presetDir})
	if err == nil {
		t.Fatal("expected error for empty files, got nil")
	}
	if !strings.Contains(err.Error(), `"files" is empty`) {
		t.Errorf("error should name the malformed field: %v", err)
	}
	// The partial shell was removed.
	if _, statErr := os.Stat(filepath.Join(parent, "app-shell")); !os.IsNotExist(statErr) {
		t.Errorf("malformed shell preset left the partial shell behind (want removed): %v", statErr)
	}
}

// TestRenderShell_PresetAndPresetDirExclusive verifies both flags together error.
func TestRenderShell_PresetAndPresetDirExclusive(t *testing.T) {
	parent := t.TempDir()
	roster := &config.Shell{
		Spec: config.ShellSpec{
			Host: "app.dev.test", ShellUpstream: "http://127.0.0.1:4200",
			CDN:  config.ShellCDN{Host: "cdn.dev.test"},
			UFEs: []config.ShellUFE{{ID: "demo", Route: "demo", Upstream: "http://127.0.0.1:4201"}},
		},
	}
	err := RenderShell(ShellParams{ParentDir: parent, Name: "app-shell", Roster: roster, Preset: "x", PresetDir: parent})
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

// TestRender_NpmrcScopesGitHubPackages asserts the generated .npmrc points the
// @infobloxopen scope at GitHub Packages (where the open-core SDK is published)
// with token auth, while everything else still defaults to npmjs. This is what
// lets a bare `pnpm install` resolve @infobloxopen/devedge-ufe-* on the open
// seam without any Infoblox-org access (only a read:packages GitHub PAT).
func TestRender_NpmrcScopesGitHubPackages(t *testing.T) {
	parent := t.TempDir()
	if err := Render(Params{Name: "demo", ParentDir: parent}); err != nil {
		t.Fatal(err)
	}
	npmrc := filepath.Join(parent, "demo", ".npmrc")
	checkFileContains(t, npmrc, "@infobloxopen:registry=https://npm.pkg.github.com")
	checkFileContains(t, npmrc, "//npm.pkg.github.com/:_authToken=${GITHUB_TOKEN}")
	checkFileContains(t, npmrc, "registry=https://registry.npmjs.org/")
}
