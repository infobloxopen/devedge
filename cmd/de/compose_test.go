package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infobloxopen/devedge/pkg/config"
)

// runCompose executes `de compose <args...>` against a fresh root command,
// returning combined output and the error. cobra writes to the buffers we set.
func runCompose(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := rootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"compose"}, args...))
	err := root.Execute()
	return buf.String(), err
}

// TestComposeCLI_InitAddBuildTidy exercises the full compose CLI flow against a
// temp working directory: init a composition, add two members, build the composed
// sources, and tidy. It asserts the generated files exist + are well-formed.
func TestComposeCLI_InitAddBuildTidy(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "composition.yaml")

	// init
	if out, err := runCompose(t, "init", "control-plane", "-f", file); err != nil {
		t.Fatalf("compose init: %v\n%s", err, out)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("init did not write %s: %v", file, err)
	}

	// add first member to a freshly-scaffolded (zero-module) file
	if out, err := runCompose(t, "add", "github.com/acme/orders/module@v0.4.1", "-f", file, "--name", "orders"); err != nil {
		t.Fatalf("compose add #1: %v\n%s", err, out)
	}
	// add second member
	if out, err := runCompose(t, "add", "github.com/acme/billing/module@v0.7.0", "-f", file, "--name", "billing"); err != nil {
		t.Fatalf("compose add #2: %v\n%s", err, out)
	}

	comp, err := config.LoadResource(file)
	if err != nil {
		t.Fatalf("load after add: %v", err)
	}
	c := comp.(*config.Composition)
	if len(c.Spec.Modules) != 2 {
		t.Fatalf("want 2 members after add, got %d", len(c.Spec.Modules))
	}

	// build
	if out, err := runCompose(t, "build", "-f", file, "-o", dir, "--module-path", "example.com/cp/cmd/control-plane"); err != nil {
		t.Fatalf("compose build: %v\n%s", err, out)
	}
	cmdDir := filepath.Join(dir, "cmd", "control-plane")
	for _, fn := range []string{"main.go", "go.mod", "composition.lock"} {
		if _, err := os.Stat(filepath.Join(cmdDir, fn)); err != nil {
			t.Errorf("build did not write %s: %v", fn, err)
		}
	}
	mainGo, _ := os.ReadFile(filepath.Join(cmdDir, "main.go"))
	if !strings.Contains(string(mainGo), "servicekit.Run(hc)") {
		t.Errorf("generated main.go missing servicekit.Run:\n%s", mainGo)
	}

	// remove a member
	if out, err := runCompose(t, "remove", "billing", "-f", file); err != nil {
		t.Fatalf("compose remove: %v\n%s", err, out)
	}
	comp2, _ := config.LoadResource(file)
	if n := len(comp2.(*config.Composition).Spec.Modules); n != 1 {
		t.Errorf("want 1 member after remove, got %d", n)
	}
}

// TestComposeCLI_AddNoFile errors clearly when the composition file is missing.
func TestComposeCLI_AddNoFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "composition.yaml")
	if _, err := runCompose(t, "add", "github.com/x/y@v1", "-f", file); err == nil {
		t.Fatal("expected error adding to a missing composition file")
	}
}

// TestComposeCLI_ChartIsP6Stub asserts `de compose chart` is a P6 stub (errors,
// does not render).
func TestComposeCLI_ChartIsP6Stub(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "composition.yaml")
	if _, err := runCompose(t, "init", "x", "-f", file); err != nil {
		t.Fatal(err)
	}
	out, err := runCompose(t, "chart", "-f", file)
	if err == nil {
		t.Fatal("expected `de compose chart` to error (P6 not implemented)")
	}
	if !strings.Contains(err.Error(), "P6") {
		t.Errorf("chart stub error should mention P6: %v\n%s", err, out)
	}
}

// TestComposeCLI_BuildRejectsNonComposition asserts a kind: Config / Service file
// is rejected by the compose loader with an actionable message.
func TestComposeCLI_BuildRejectsNonComposition(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "devedge.yaml")
	cfg := "apiVersion: " + config.APIVersion + "\nkind: " + config.Kind +
		"\nmetadata: { name: web }\nspec: { routes: [ { host: w.dev.test, upstream: http://127.0.0.1:3000 } ] }\n"
	if err := os.WriteFile(file, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runCompose(t, "build", "-f", file)
	if err == nil || !strings.Contains(err.Error(), "not a Composition") {
		t.Errorf("expected a 'not a Composition' error, got: %v", err)
	}
}
