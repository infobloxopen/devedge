package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTerraformMultiResource proves that repeated `de terraform add` calls
// accumulate into one multi-resource provider (the Terraform composition model,
// the mirror of TestCLIMultiDomain for the CLI seam). It scaffolds a provider
// with `de terraform new`, adds two resources from two separate enriched OpenAPI
// v3 specs — widget from the devedge-sdk toy golden and order from this repo's
// fixture — and asserts both constructors are registered in resources_gen.go and
// the provider builds serving both. This guards the fix where `de terraform add`
// re-derives resources_gen.go from every generated resource (tfgen alone
// registers only its current invocation).
//
// Requires network (Go module downloads + `go run tfgen`, which pulls the
// terraform-plugin-framework stack) and `go` on PATH; skipped in -short mode,
// when `go` is missing, or when the sibling devedge-sdk toy golden is absent.
func TestTerraformMultiResource(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode (needs network + go run tfgen)")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}

	repoRoot := repoRootDir(t)
	toyGolden := toyGoldenSpec(t, repoRoot)
	ordersSpec := filepath.Join(repoRoot, "testdata", "multidomain", "orders.openapi.yaml")
	if _, err := os.Stat(ordersSpec); err != nil {
		t.Fatalf("orders fixture missing at %s: %v", ordersSpec, err)
	}

	de := filepath.Join(t.TempDir(), "de")
	build := exec.Command("go", "build", "-o", de, "./cmd/de")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build de: %v\n%s", err, out)
	}

	work := t.TempDir()
	run := func(dir, name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s %v (in %s): %v\n%s", name, args, dir, err, out.String())
		}
		return out.String()
	}

	// Scaffold the provider, then add two resources, each from its own spec.
	run(work, de, "terraform", "new", "toy", "--module", "github.com/example/terraform-provider-toy", "--org", "infobloxopen")
	prov := filepath.Join(work, "terraform-provider-toy")
	run(work, de, "terraform", "add", "--dir", prov, "--input", toyGolden, "--resource", "widget")
	run(work, de, "terraform", "add", "--dir", prov, "--input", ordersSpec, "--resource", "order")

	// resources_gen.go must register BOTH resources — it is re-derived from the
	// provider directory on every add, so N adds accumulate.
	resourcesGen, err := os.ReadFile(filepath.Join(prov, "internal", "provider", "resources_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NewWidgetResource", "NewOrderResource"} {
		if !strings.Contains(string(resourcesGen), want) {
			t.Fatalf("resources_gen.go does not register %q (adds did not accumulate):\n%s", want, resourcesGen)
		}
	}

	// The provider must build serving both resources.
	run(prov, "go", "mod", "tidy")
	bin := filepath.Join(prov, "terraform-provider-toy")
	run(prov, "go", "build", "-o", bin, ".")
	t.Logf("multi-resource provider composed; resources_gen.go registers both:\n%s", resourcesGen)
}
