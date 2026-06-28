package compose_test

import (
	"strings"
	"testing"

	"github.com/infobloxopen/devedge-sdk/servicekit"
	"github.com/infobloxopen/devedge/internal/compose"
	"github.com/infobloxopen/devedge/testdata/composefixtures/dupmod"
	"github.com/infobloxopen/devedge/testdata/composefixtures/echomod"
	"github.com/infobloxopen/devedge/testdata/composefixtures/futuremod"
	"github.com/infobloxopen/devedge/testdata/composefixtures/greetermod"
	"github.com/infobloxopen/devedge/pkg/config"
)

// registerFixtures wires the fixture modules into the compose in-process resolver
// so Tidy can resolve them by import path. Idempotent (RegisterModule overwrites).
func registerFixtures() {
	compose.RegisterModule(greetermod.ImportPath, greetermod.Module)
	compose.RegisterModule(echomod.ImportPath, echomod.Module)
	compose.RegisterModule(dupmod.ImportPath, dupmod.Module)
	compose.RegisterModule(futuremod.ImportPath, futuremod.Module)
}

func composition(t *testing.T, name string, moduleRefs ...string) *config.Composition {
	t.Helper()
	var b strings.Builder
	b.WriteString("apiVersion: devedge.infoblox.dev/v1alpha1\nkind: Composition\n")
	b.WriteString("metadata: { name: " + name + " }\nspec:\n  modules:\n")
	for i, ref := range moduleRefs {
		b.WriteString("    - { name: m")
		b.WriteByte(byte('0' + i))
		b.WriteString(", module: " + ref + " }\n")
	}
	c, err := config.ParseComposition([]byte(b.String()))
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, b.String())
	}
	return c
}

func TestTidy_CleanComposition(t *testing.T) {
	registerFixtures()
	c := composition(t, "clean", greetermod.ImportPath, echomod.ImportPath)
	report, err := compose.Tidy(c, nil, "1.26.0")
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK() {
		t.Fatalf("clean composition should be OK, got: %s", report.Format(c.Project()))
	}
	if len(report.Resolved) != 2 {
		t.Errorf("want 2 resolved, got %v", report.Resolved)
	}
}

// SC-002: tidy detects a conflict on a deliberately-broken fixture (two modules
// declaring the SAME HTTP route prefix).
func TestTidy_DuplicateRoutePrefixConflict(t *testing.T) {
	registerFixtures()
	c := composition(t, "broken", greetermod.ImportPath, dupmod.ImportPath)
	report, err := compose.Tidy(c, nil, "1.26.0")
	if err != nil {
		t.Fatal(err)
	}
	if report.OK() {
		t.Fatal("broken composition (duplicate route prefix) should NOT be OK")
	}
	if report.Conflict == nil || !strings.Contains(report.Conflict.Error(), "route prefix") {
		t.Errorf("expected a duplicate-route-prefix conflict, got: %v", report.Conflict)
	}
}

// SC-002 (alt): tidy detects an incompatible module version requirement.
func TestTidy_VersionIncompatibility(t *testing.T) {
	registerFixtures()
	c := composition(t, "skew", greetermod.ImportPath, futuremod.ImportPath)
	report, err := compose.Tidy(c, nil, "1.26.0")
	if err != nil {
		t.Fatal(err)
	}
	if report.OK() {
		t.Fatal("composition with a future-SDK module should NOT be OK")
	}
	if len(report.Incompatible) == 0 {
		t.Errorf("expected an incompatibility, got none; report: %s", report.Format(c.Project()))
	}
}

func TestTidy_UnresolvedModuleReported(t *testing.T) {
	registerFixtures()
	c := composition(t, "ext", greetermod.ImportPath, "github.com/external/unknown/module@v1.0.0")
	report, err := compose.Tidy(c, nil, "1.26.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Unresolved) != 1 || report.Unresolved[0] != "github.com/external/unknown/module" {
		t.Errorf("unresolved external module not reported: %v", report.Unresolved)
	}
	// The one resolvable module is still validated (and is conflict-free alone).
	if !report.OK() {
		t.Errorf("single resolvable module should be OK: %s", report.Format(c.Project()))
	}
}

// Sanity: the two clean fixtures compose + boot via the SDK harness path that
// AssertComposition uses (descriptor union + Run boot gate), proving the modules
// are real servicekit.Modules — not just descriptor stubs.
func TestFixtures_ValidateModules(t *testing.T) {
	mods := []servicekit.Module{greetermod.Module(), echomod.Module()}
	if err := servicekit.ValidateModules(mods); err != nil {
		t.Fatalf("fixture modules should validate as a composition: %v", err)
	}
}

// Smoke boots the resolvable members in-process through the SDK boot gate (the
// path `de compose test` drives). The real-DB path does not run (no migrations).
func TestSmoke_Fixtures_BootGate(t *testing.T) {
	registerFixtures()
	c := composition(t, "smoke", greetermod.ImportPath, echomod.ImportPath)
	res, err := compose.Smoke(c, nil)
	if err != nil {
		t.Fatalf("Smoke should boot the clean composition: %v", err)
	}
	if len(res.Resolved) != 2 {
		t.Errorf("want 2 booted, got %v", res.Resolved)
	}
	if res.RealDBRan {
		t.Error("RealDBRan should be false (no migrations, no shared DB)")
	}
}

// A broken composition fails the in-process smoke (boot gate / union validation).
func TestSmoke_Conflict_Fails(t *testing.T) {
	registerFixtures()
	c := composition(t, "smokebroken", greetermod.ImportPath, dupmod.ImportPath)
	if _, err := compose.Smoke(c, nil); err == nil {
		t.Fatal("Smoke should fail on a duplicate-route-prefix composition")
	}
}
