package cliscaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateName checks that ValidateName accepts valid DNS labels and rejects
// invalid ones with a non-empty error message.
func TestValidateName(t *testing.T) {
	valid := []string{"ib", "my-cli2", "a", strings.Repeat("a", 63)}
	for _, name := range valid {
		t.Run("valid/"+name, func(t *testing.T) {
			if err := ValidateName(name); err != nil {
				t.Errorf("ValidateName(%q) = %v, want nil", name, err)
			}
		})
	}

	invalid := []struct{ name, reason string }{
		{"", "empty"},
		{"IB", "uppercase"},
		{"my cli", "spaces"},
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

// placeholders are strings that must NOT survive into the rendered tree — either
// path/field placeholders or unrendered template braces.
var placeholders = []string{"__name__", "<no value>", "{{"}

// TestRender_PlaceholderFreeTree renders a demo CLI shell and asserts the tree
// is placeholder-free, has the expected files, and carries the correct module,
// app name, and SDK dependency.
func TestRender_PlaceholderFreeTree(t *testing.T) {
	parent := t.TempDir()
	if err := Render(Params{Name: "demo", ParentDir: parent, Module: "example.com/demo"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	root := filepath.Join(parent, "demo")

	// Every regular file: no .tmpl suffix, no placeholder strings, non-empty
	// (except the intentionally-empty gen/.gitkeep dir marker).
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
		if info.Size() == 0 && info.Name() != ".gitkeep" {
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
		"go.mod",
		"main.go",
		"domains_gen.go",
		"README.md",
		".gitignore",
		".goreleaser.yaml",
		".github/workflows/ci.yml",
		"gen/.gitkeep",
	}
	for _, rel := range expected {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("expected file %q missing: %v", rel, err)
		}
	}

	// go.mod carries the module path and the open-core SDK dependency.
	checkFileContains(t, filepath.Join(root, "go.mod"), "module example.com/demo")
	checkFileContains(t, filepath.Join(root, "go.mod"), "github.com/infobloxopen/devedge-cli-sdk "+DefaultVersions.SDK)

	// main.go wires the shell against clikit with the app name.
	main := readFile(t, filepath.Join(root, "main.go"))
	if !strings.Contains(main, `const appName = "demo"`) {
		t.Error("main.go: appName is not \"demo\"")
	}
	for _, want := range []string{
		"clikit.NewApp(appName)",
		"app.SetSessionFactory(",
		"registerDomains(root, app.Runtime())",
		"clikit.NewDoctorCommand(appName)",
	} {
		if !strings.Contains(main, want) {
			t.Errorf("main.go: missing shell wiring %q", want)
		}
	}

	// domains_gen.go starts empty but declares the (idempotently regenerated)
	// registerDomains wiring hook.
	dg := readFile(t, filepath.Join(root, "domains_gen.go"))
	if !strings.Contains(dg, "func registerDomains(root *cobra.Command, rt clikit.Runtime)") {
		t.Error("domains_gen.go: missing registerDomains signature")
	}

	// The goreleaser build targets the CLI binary by name.
	checkFileContains(t, filepath.Join(root, ".goreleaser.yaml"), "binary: demo")

	// No committed go.sum (resolved by go mod tidy in the generated repo).
	if _, err := os.Stat(filepath.Join(root, "go.sum")); err == nil {
		t.Error("scaffold committed a go.sum it should not")
	}
}

// TestRender_DefaultModuleAndAppName verifies empty Module/AppName default to
// the CLI name.
func TestRender_DefaultModuleAndAppName(t *testing.T) {
	parent := t.TempDir()
	if err := Render(Params{Name: "acme", ParentDir: parent}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	root := filepath.Join(parent, "acme")
	checkFileContains(t, filepath.Join(root, "go.mod"), "module acme")
	checkFileContains(t, filepath.Join(root, "main.go"), `const appName = "acme"`)
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

// TestBuiltinPresetsEmpty verifies the public CLI ships NO built-in preset
// (only the contract README under presets/).
func TestBuiltinPresetsEmpty(t *testing.T) {
	if names := BuiltinPresets(); len(names) != 0 {
		t.Errorf("public CLI must ship no built-in preset, got %v", names)
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
	    { "path": "extra.go", "template": "extra.go" }
	  ]
	}`)
	// A templated overlay file (sees the same data as the base).
	writeFile(t, filepath.Join(presetDir, "readme.tmpl"), "# {{ .TitleName }} preset overlay for module {{ .Module }}\n")
	// A verbatim overlay file.
	writeFile(t, filepath.Join(presetDir, "extra.go"), "package main\n\nconst extra = true\n")

	parent := t.TempDir()
	if err := Render(Params{Name: "over", ParentDir: parent, PresetDir: presetDir}); err != nil {
		t.Fatalf("Render with --preset-dir: %v", err)
	}
	root := filepath.Join(parent, "over")

	readme := readFile(t, filepath.Join(root, "README.md"))
	if !strings.Contains(readme, "Over preset overlay") {
		t.Errorf("README.md not overridden by preset overlay (TitleName not rendered): %q", readme)
	}
	extra := readFile(t, filepath.Join(root, "extra.go"))
	if !strings.Contains(extra, "const extra = true") {
		t.Errorf("preset did not add extra.go: %q", extra)
	}
	// A base file the preset did NOT touch still exists.
	if _, err := os.Stat(filepath.Join(root, "main.go")); err != nil {
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
