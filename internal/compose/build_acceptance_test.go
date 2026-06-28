package compose_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/infobloxopen/devedge/internal/compose"
	"github.com/infobloxopen/devedge/testdata/composefixtures/echomod"
	"github.com/infobloxopen/devedge/testdata/composefixtures/greetermod"
	"github.com/infobloxopen/devedge/pkg/config"
)

// repoRoot returns the absolute path to the devedge module root, derived from this
// test file's location (internal/compose/build_acceptance_test.go).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// .../internal/compose/build_acceptance_test.go -> up 2 dirs to repo root.
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// TestBuild_ComposedBinaryCompiles is the WS-012 P4 SC-001 acceptance: a real
// `kind: Composition` with TWO real modules (the greetermod + echomod fixtures,
// each exposing a zero-arg Module()) -> compose.Generate produces a
// cmd/<name>/main.go + go.mod + composition.lock that COMPILE into a single
// composed binary.
//
// The generated go.mod is replaced with one wired to the local devedge checkout
// (so the fixture import paths resolve) + the cached devedge-sdk v0.28.0; the
// generated main.go + lock are used verbatim. This proves the static-composition
// generator emits a buildable composed host (no Go plugins).
func TestBuild_ComposedBinaryCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping compile acceptance in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	doc := `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: suite }
spec:
  runtime: { grpc: ":9090", http: ":8080" }
  database: { engine: postgres, isolation: schema-preferred }
  modules:
    - name: greeter
      module: ` + greetermod.ImportPath + `
      failurePolicy: fail-host
    - name: echo
      module: ` + echomod.ImportPath + `
      failurePolicy: degraded
`
	c, err := config.ParseComposition([]byte(doc))
	if err != nil {
		t.Fatalf("parse composition: %v", err)
	}

	tmp := t.TempDir()
	modulePath := "example.com/suite/cmd/suite"
	gen, err := compose.Generate(c, "1.26.0", modulePath)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Write the generated files into a flat module dir (tmp acts as the cmd/<name>
	// dir for the build).
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte(gen.MainGo), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "composition.lock"), []byte(gen.Lock), 0o644); err != nil {
		t.Fatal(err)
	}

	// Replace the generated go.mod with one wired to the LOCAL devedge checkout so
	// the fixture import paths (under devedge's testdata/composefixtures) resolve,
	// plus the SDK (resolved from the module cache via devedge's own require).
	root := repoRoot(t)
	goMod := "module " + modulePath + "\n\n" +
		"go 1.26.0\n\n" +
		"require (\n" +
		"\tgithub.com/infobloxopen/devedge-sdk " + compose.SDKVersion + "\n" +
		"\tgithub.com/infobloxopen/devedge v0.0.0\n" +
		")\n\n" +
		"replace github.com/infobloxopen/devedge => " + root + "\n"
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	// go mod tidy resolves devedge-sdk (+ its transitive deps) from the module
	// cache; the local devedge supplies the fixture packages via the replace.
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("go", args...)
		cmd.Dir = tmp
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("mod", "tidy")
	run("build", "-o", filepath.Join(tmp, "suite"), ".")

	// The single composed binary must exist.
	if fi, err := os.Stat(filepath.Join(tmp, "suite")); err != nil || fi.Size() == 0 {
		t.Fatalf("composed binary not produced: err=%v", err)
	}
}
