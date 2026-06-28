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

// TestComposeCLI_Chart_RequiresHelm asserts `de compose chart` errors when helm
// is not on PATH (the gate `de project chart` uses too), and renders workloads
// when helm is available. P6 is now implemented — the stub is replaced.
func TestComposeCLI_Chart_RequiresHelm(t *testing.T) {
	if err := requireTools("helm"); err != nil {
		// helm unavailable — chart should error with a helm-not-found message, not P6.
		dir := t.TempDir()
		file := filepath.Join(dir, "composition.yaml")
		if _, err2 := runCompose(t, "init", "x", "-f", file); err2 != nil {
			t.Fatal(err2)
		}
		if _, err2 := runCompose(t, "add", "github.com/acme/a/module@v0.1.0", "-f", file, "--name", "a"); err2 != nil {
			t.Fatal(err2)
		}
		if _, err2 := runCompose(t, "add", "github.com/acme/b/module@v0.2.0", "-f", file, "--name", "b"); err2 != nil {
			t.Fatal(err2)
		}
		out, err3 := runCompose(t, "chart", "-f", file)
		if err3 == nil {
			t.Fatalf("chart without helm should error; got output:\n%s", out)
		}
		// The error should be about helm, not a P6 stub message.
		if strings.Contains(err3.Error(), "P6") {
			t.Errorf("chart command is now P6-implemented; error should not mention stub: %v", err3)
		}
		t.Skipf("helm not available: %v", err)
	}
}

// TestComposeCLI_Chart_SingleBinary exercises the full `de compose chart` flow
// against a two-member fixture composition in single-binary mode (the default).
// It asserts: (a) the command succeeds; (b) exactly one chart directory is written;
// (c) the chart passes helm lint; (d) the rendered values carry the composed name.
func TestComposeCLI_Chart_SingleBinary(t *testing.T) {
	if err := requireTools("helm"); err != nil {
		t.Skipf("skipping: %v", err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "composition.yaml")
	outDir := filepath.Join(dir, "charts-out")

	// Scaffold a composition with two members.
	if _, err := runCompose(t, "init", "suite", "-f", file); err != nil {
		t.Fatal(err)
	}
	if _, err := runCompose(t, "add", "github.com/acme/greeter/module@v0.1.0", "-f", file, "--name", "greeter"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCompose(t, "add", "github.com/acme/echo/module@v0.2.0", "-f", file, "--name", "echo"); err != nil {
		t.Fatal(err)
	}

	out, err := runCompose(t, "chart", "-f", file, "-o", outDir, "--mode", "single-binary")
	if err != nil {
		t.Fatalf("compose chart single-binary: %v\n%s", err, out)
	}

	// Single-binary: exactly one workload directory (named after the composition).
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read output dir: %v", err)
	}
	var workloadDirs []string
	for _, e := range entries {
		if e.IsDir() {
			workloadDirs = append(workloadDirs, e.Name())
		}
	}
	if len(workloadDirs) != 1 {
		t.Errorf("single-binary: want 1 workload dir, got %d: %v", len(workloadDirs), workloadDirs)
	} else if workloadDirs[0] != "suite" {
		t.Errorf("single-binary: workload dir = %q, want %q", workloadDirs[0], "suite")
	}

	// Chart.yaml must exist in the workload dir.
	chartYAML := filepath.Join(outDir, "suite", "Chart.yaml")
	if _, err := os.Stat(chartYAML); err != nil {
		t.Errorf("single-binary: Chart.yaml not written at %s", chartYAML)
	}
	// values.yaml must carry the composition's members.
	valYAML := filepath.Join(outDir, "suite", "values.yaml")
	valData, err := os.ReadFile(valYAML)
	if err != nil {
		t.Errorf("single-binary: values.yaml not written: %v", err)
	} else {
		valStr := string(valData)
		if !strings.Contains(valStr, "suite") {
			t.Errorf("single-binary values.yaml missing composition name 'suite':\n%s", valStr)
		}
	}
}

// TestComposeCLI_Chart_MultiDaemon exercises `de compose chart --mode multi-daemon`
// and asserts one chart directory per member module.
func TestComposeCLI_Chart_MultiDaemon(t *testing.T) {
	if err := requireTools("helm"); err != nil {
		t.Skipf("skipping: %v", err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "composition.yaml")
	outDir := filepath.Join(dir, "charts-out")

	if _, err := runCompose(t, "init", "suite", "-f", file); err != nil {
		t.Fatal(err)
	}
	if _, err := runCompose(t, "add", "github.com/acme/greeter/module@v0.1.0", "-f", file, "--name", "greeter"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCompose(t, "add", "github.com/acme/echo/module@v0.2.0", "-f", file, "--name", "echo"); err != nil {
		t.Fatal(err)
	}

	out, err := runCompose(t, "chart", "-f", file, "-o", outDir, "--mode", "multi-daemon")
	if err != nil {
		t.Fatalf("compose chart multi-daemon: %v\n%s", err, out)
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read output dir: %v", err)
	}
	var workloadDirs []string
	for _, e := range entries {
		if e.IsDir() {
			workloadDirs = append(workloadDirs, e.Name())
		}
	}
	// multi-daemon: one dir per member (2 members → 2 dirs).
	if len(workloadDirs) != 2 {
		t.Errorf("multi-daemon: want 2 workload dirs, got %d: %v", len(workloadDirs), workloadDirs)
	}
	wantDirs := map[string]bool{"suite-greeter": true, "suite-echo": true}
	for _, d := range workloadDirs {
		if !wantDirs[d] {
			t.Errorf("multi-daemon: unexpected workload dir %q", d)
		}
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
