package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCLIMultiDomain proves the WS-024 P3b capstone: N independent API domains
// compose into one CLI. It scaffolds a shell with `de cli new`, then adds two
// domains from two separate enriched OpenAPI v3 specs with `de cli add` — widgets
// from the devedge-sdk toy golden and orders from this repo's fixture — builds
// the composed shell, and asserts the built binary's command tree lists BOTH
// domains. This exercises the catalog-as-roster mechanic: `de cli add`
// regenerates domains_gen.go from every gen/* domain, so adding each service's
// contract accumulates it into one CLI.
//
// Requires network (Go module downloads + `go run cligen`) and `go` on PATH;
// skipped in -short mode, when `go` is missing, or when the sibling devedge-sdk
// toy golden is not checked out alongside this repo (e.g. a CI that clones only
// devedge — set DEVEDGE_SDK_TOY_GOLDEN to point at it).
func TestCLIMultiDomain(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode (needs network + go run cligen)")
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

	// Build the `de` binary under test.
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

	// Scaffold the shell, then add two independent domains, each from its own spec.
	run(work, de, "cli", "new", "shop", "--module", "example.com/shop")
	shop := filepath.Join(work, "shop")
	run(work, de, "cli", "add", "--dir", shop, "--input", toyGolden, "--domain", "widgets")
	run(work, de, "cli", "add", "--dir", shop, "--input", ordersSpec, "--domain", "orders")

	// The regenerated wiring must reference both generated domains — the roster
	// is re-derived from the gen/ directory on every add, so N adds compose.
	domainsGen, err := os.ReadFile(filepath.Join(shop, "domains_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gen/widgets", "gen/orders"} {
		if !strings.Contains(string(domainsGen), want) {
			t.Fatalf("domains_gen.go does not reference %q (roster did not accumulate):\n%s", want, domainsGen)
		}
	}

	// Build the composed shell. `de cli add` intentionally does not tidy; the
	// walk-through's next step (go mod tidy && go build) does.
	run(shop, "go", "mod", "tidy")
	bin := filepath.Join(shop, "shop")
	run(shop, "go", "build", "-o", bin, ".")

	// The built shell's command tree must list BOTH domains.
	help := exec.Command(bin, "--help")
	help.Dir = shop
	helpOut, err := help.CombinedOutput()
	if err != nil {
		t.Fatalf("shop --help: %v\n%s", err, helpOut)
	}
	for _, want := range []string{"widgets", "orders"} {
		if !strings.Contains(string(helpOut), want) {
			t.Fatalf("built shell --help is missing domain %q — N domains did not compose:\n%s", want, helpOut)
		}
	}
	t.Logf("multi-domain CLI composed; built shell --help lists both domains:\n%s", helpOut)
}

// repoRootDir returns the devedge repo root, two levels up from this test file
// (test/integration/), independent of the process working directory.
func repoRootDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

// toyGoldenSpec resolves the devedge-sdk toy golden OpenAPI fixture used as the
// first (widgets) domain. It is normally checked out as a sibling of this repo;
// the test skips when it is absent. An explicit DEVEDGE_SDK_TOY_GOLDEN override
// wins.
func toyGoldenSpec(t *testing.T, repoRoot string) string {
	t.Helper()
	const rel = "testdata/toy/openapi/toy.openapi.yaml"
	candidates := []string{
		os.Getenv("DEVEDGE_SDK_TOY_GOLDEN"),
		filepath.Join(repoRoot, "..", "devedge-sdk", filepath.FromSlash(rel)),
	}
	if gp := goEnvVar("GOPATH"); gp != "" {
		candidates = append(candidates,
			filepath.Join(gp, "src", "github.com", "infobloxopen", "devedge-sdk", filepath.FromSlash(rel)))
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	t.Skip("devedge-sdk toy golden not found alongside this repo (set DEVEDGE_SDK_TOY_GOLDEN)")
	return ""
}

// goEnvVar reads a single `go env` value, returning "" on error.
func goEnvVar(key string) string {
	out, err := exec.Command("go", "env", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
