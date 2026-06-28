package compose

import (
	"strings"
	"testing"

	"github.com/infobloxopen/devedge/pkg/config"
)

// compositionWithRoutes returns a Composition whose members declare HTTP routes
// so topology assertions (hostnames, ingress) are non-trivial.
func compositionWithRoutes(t *testing.T) *config.Composition {
	t.Helper()
	doc := `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: suite }
spec:
  runtime: { mode: single-binary, grpc: ":9090", http: ":8080" }
  database: { engine: postgres, dsnRef: DATABASE_URL, isolation: schema-preferred }
  modules:
    - name: greeter
      module: github.com/acme/greeter/module@v0.1.0
      configPrefix: greeter
      database: { schema: greeter }
      routes:
        - { host: greeter.dev.test, upstream: "http://127.0.0.1:3001" }
    - name: echo
      module: github.com/acme/echo/module@v0.2.0
      configPrefix: echo
      database: { schema: echo }
      routes:
        - { host: echo.dev.test, upstream: "http://127.0.0.1:3002" }
`
	c, err := config.ParseComposition([]byte(doc))
	if err != nil {
		t.Fatalf("parse composition: %v", err)
	}
	return c
}

// --- resolveMode ---

func TestResolveMode_FlagOverridesSpec(t *testing.T) {
	c := mustComposition(t, twoModuleDoc)
	// spec declares nothing; flag overrides to multi-daemon
	got := resolveMode(c, ModeMultiDaemon)
	if got != ModeMultiDaemon {
		t.Errorf("resolveMode with flag=multi-daemon got %q", got)
	}
}

func TestResolveMode_SpecFallback(t *testing.T) {
	doc := `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: x }
spec:
  runtime: { mode: multi-daemon }
  modules:
    - { name: a, module: github.com/a/a }
`
	c := mustComposition(t, doc)
	got := resolveMode(c, ModeAuto)
	if got != ModeMultiDaemon {
		t.Errorf("resolveMode auto from spec got %q, want multi-daemon", got)
	}
}

func TestResolveMode_DefaultSingleBinary(t *testing.T) {
	c := mustComposition(t, twoModuleDoc) // twoModuleDoc has no mode set
	got := resolveMode(c, ModeAuto)
	if got != ModeSingleBinary {
		t.Errorf("resolveMode with no spec default got %q, want single-binary", got)
	}
}

// --- single-binary topology ---

// T-P6-01: single-binary mode produces exactly ONE Deployment workload.
func TestComposeChart_SingleBinary_OneDeployment(t *testing.T) {
	c := compositionWithRoutes(t)
	plan, err := ComposeChart(c, ModeSingleBinary)
	if err != nil {
		t.Fatalf("ComposeChart single-binary: %v", err)
	}
	if plan.Mode != ModeSingleBinary {
		t.Errorf("plan.Mode = %q, want single-binary", plan.Mode)
	}
	if len(plan.Workloads) != 1 {
		t.Errorf("single-binary: want 1 workload, got %d", len(plan.Workloads))
	}
}

// T-P6-02: single-binary workload carries all member routes as Ingress hostnames.
func TestComposeChart_SingleBinary_AggregatesRoutes(t *testing.T) {
	c := compositionWithRoutes(t)
	plan, err := ComposeChart(c, ModeSingleBinary)
	if err != nil {
		t.Fatalf("ComposeChart single-binary: %v", err)
	}
	w := plan.Workloads[0]
	// Both member hostnames must appear on the single workload.
	wantHosts := []string{"greeter.dev.test", "echo.dev.test"}
	for _, h := range wantHosts {
		found := false
		for _, wh := range w.Hostnames {
			if wh == h {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("single-binary workload missing hostname %q; got %v", h, w.Hostnames)
		}
	}
	// Both member modules listed.
	if len(w.Modules) != 2 {
		t.Errorf("single-binary workload Modules = %v, want 2", w.Modules)
	}
}

// T-P6-03: single-binary workload wires the shared DB env var.
func TestComposeChart_SingleBinary_SharedDBEnvVar(t *testing.T) {
	c := compositionWithRoutes(t)
	plan, err := ComposeChart(c, ModeSingleBinary)
	if err != nil {
		t.Fatalf("ComposeChart single-binary: %v", err)
	}
	w := plan.Workloads[0]
	if w.DatabaseEnvVar != "DATABASE_URL" {
		t.Errorf("single-binary DatabaseEnvVar = %q, want DATABASE_URL", w.DatabaseEnvVar)
	}
	// Both per-module schemas present.
	schemas := strings.Join(w.DatabaseSchemas, ",")
	for _, s := range []string{"greeter", "echo"} {
		if !strings.Contains(schemas, s) {
			t.Errorf("single-binary DatabaseSchemas missing %q: %v", s, w.DatabaseSchemas)
		}
	}
}

// T-P6-04: single-binary shared DB plan is set.
func TestComposeChart_SingleBinary_SharedDB(t *testing.T) {
	c := compositionWithRoutes(t)
	plan, err := ComposeChart(c, ModeSingleBinary)
	if err != nil {
		t.Fatalf("ComposeChart single-binary: %v", err)
	}
	if plan.SharedDB == nil {
		t.Fatal("single-binary: SharedDB is nil, want non-nil (composition declares database)")
	}
	if plan.SharedDB.Engine != "postgres" {
		t.Errorf("SharedDB.Engine = %q, want postgres", plan.SharedDB.Engine)
	}
}

// --- multi-daemon topology ---

// T-P6-05: multi-daemon mode produces one Deployment per member.
func TestComposeChart_MultiDaemon_OnePerMember(t *testing.T) {
	c := compositionWithRoutes(t)
	plan, err := ComposeChart(c, ModeMultiDaemon)
	if err != nil {
		t.Fatalf("ComposeChart multi-daemon: %v", err)
	}
	if plan.Mode != ModeMultiDaemon {
		t.Errorf("plan.Mode = %q, want multi-daemon", plan.Mode)
	}
	wantCount := len(c.Spec.Modules) // 2
	if len(plan.Workloads) != wantCount {
		t.Errorf("multi-daemon: want %d workloads (one per member), got %d", wantCount, len(plan.Workloads))
	}
}

// T-P6-06: each multi-daemon workload owns only its own member's routes.
func TestComposeChart_MultiDaemon_MemberRoutesOwned(t *testing.T) {
	c := compositionWithRoutes(t)
	plan, err := ComposeChart(c, ModeMultiDaemon)
	if err != nil {
		t.Fatalf("ComposeChart multi-daemon: %v", err)
	}
	// greeter workload should have greeter.dev.test; echo workload echo.dev.test.
	for _, w := range plan.Workloads {
		if len(w.Modules) != 1 {
			t.Errorf("multi-daemon workload %q: want 1 module, got %v", w.Name, w.Modules)
		}
		switch w.Modules[0] {
		case "greeter":
			if len(w.Hostnames) != 1 || w.Hostnames[0] != "greeter.dev.test" {
				t.Errorf("greeter workload hostnames = %v, want [greeter.dev.test]", w.Hostnames)
			}
		case "echo":
			if len(w.Hostnames) != 1 || w.Hostnames[0] != "echo.dev.test" {
				t.Errorf("echo workload hostnames = %v, want [echo.dev.test]", w.Hostnames)
			}
		default:
			t.Errorf("unexpected module %q in multi-daemon plan", w.Modules[0])
		}
	}
}

// T-P6-07: multi-daemon workloads all carry the shared DB env var.
func TestComposeChart_MultiDaemon_SharedDBEnvVar(t *testing.T) {
	c := compositionWithRoutes(t)
	plan, err := ComposeChart(c, ModeMultiDaemon)
	if err != nil {
		t.Fatalf("ComposeChart multi-daemon: %v", err)
	}
	for _, w := range plan.Workloads {
		if w.DatabaseEnvVar != "DATABASE_URL" {
			t.Errorf("multi-daemon workload %q DatabaseEnvVar = %q, want DATABASE_URL", w.Name, w.DatabaseEnvVar)
		}
	}
}

// T-P6-08: each multi-daemon workload carries only its own module's schema.
func TestComposeChart_MultiDaemon_PerMemberSchema(t *testing.T) {
	c := compositionWithRoutes(t)
	plan, err := ComposeChart(c, ModeMultiDaemon)
	if err != nil {
		t.Fatalf("ComposeChart multi-daemon: %v", err)
	}
	for _, w := range plan.Workloads {
		if len(w.DatabaseSchemas) != 1 {
			t.Errorf("multi-daemon workload %q: want 1 schema, got %v", w.Name, w.DatabaseSchemas)
		}
	}
}

// --- hybrid topology ---

// T-P6-09: hybrid groups non-standalone members into one composed Deployment;
// members with failurePolicy: degraded get their own Deployment (they are
// optional — their failure should not take down the composed binary, so they
// deploy independently in hybrid mode).
func TestComposeChart_Hybrid_StandaloneIsolated(t *testing.T) {
	doc := `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: suite }
spec:
  runtime: { mode: hybrid, grpc: ":9090", http: ":8080" }
  modules:
    - { name: orders,    module: github.com/acme/orders/module@v0.4.1 }
    - { name: billing,   module: github.com/acme/billing/module@v0.7.0 }
    - { name: analytics, module: github.com/acme/analytics/module@v0.1.0, failurePolicy: degraded }
`
	c := mustComposition(t, doc)
	plan, err := ComposeChart(c, ModeAuto) // spec says hybrid
	if err != nil {
		t.Fatalf("ComposeChart hybrid: %v", err)
	}
	if plan.Mode != ModeHybrid {
		t.Errorf("plan.Mode = %q, want hybrid", plan.Mode)
	}
	// Expected: 1 composed Deployment (orders+billing) + 1 standalone (analytics) = 2.
	if len(plan.Workloads) != 2 {
		t.Errorf("hybrid: want 2 workloads, got %d: %v", len(plan.Workloads), workloadNames(plan))
	}
	// Composed workload hosts 2 modules; standalone hosts 1.
	for _, w := range plan.Workloads {
		switch {
		case len(w.Modules) == 2:
			// composed group
			for _, m := range w.Modules {
				if m == "analytics" {
					t.Errorf("hybrid: analytics should be standalone, found in composed workload %q", w.Name)
				}
			}
		case len(w.Modules) == 1 && w.Modules[0] == "analytics":
			// standalone — expected
		default:
			t.Errorf("hybrid: unexpected workload %q modules %v", w.Name, w.Modules)
		}
	}
}

// T-P6-10: hybrid with no standalone members equals single-binary.
func TestComposeChart_Hybrid_AllComposed_EqualsSingleBinary(t *testing.T) {
	c := compositionWithRoutes(t)
	// compositionWithRoutes has no failurePolicy: degraded → all stay in composed binary
	plan, err := ComposeChart(c, ModeHybrid)
	if err != nil {
		t.Fatalf("ComposeChart hybrid (all composed): %v", err)
	}
	if len(plan.Workloads) != 1 {
		t.Errorf("hybrid with no standalone: want 1 workload, got %d", len(plan.Workloads))
	}
	if len(plan.Workloads[0].Modules) != len(c.Spec.Modules) {
		t.Errorf("hybrid (all composed) workload.Modules = %v, want %d", plan.Workloads[0].Modules, len(c.Spec.Modules))
	}
}

// T-P6-11: hybrid with ALL members standalone (failurePolicy: degraded) equals multi-daemon.
func TestComposeChart_Hybrid_AllStandalone_EqualsMultiDaemon(t *testing.T) {
	doc := `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: suite }
spec:
  runtime: { mode: hybrid }
  modules:
    - { name: a, module: github.com/acme/a, failurePolicy: degraded }
    - { name: b, module: github.com/acme/b, failurePolicy: degraded }
`
	c := mustComposition(t, doc)
	plan, err := ComposeChart(c, ModeHybrid)
	if err != nil {
		t.Fatalf("ComposeChart hybrid (all standalone): %v", err)
	}
	if len(plan.Workloads) != 2 {
		t.Errorf("hybrid (all standalone): want 2 workloads, got %d", len(plan.Workloads))
	}
	for _, w := range plan.Workloads {
		if len(w.Modules) != 1 {
			t.Errorf("hybrid standalone workload %q: want 1 module, got %v", w.Name, w.Modules)
		}
	}
}

// --- ToHelmValues ---

// T-P6-12: ToHelmValues maps a WorkloadValues to the shape helm.Render expects.
func TestWorkloadValues_ToHelmValues_SingleBinaryShape(t *testing.T) {
	w := WorkloadValues{
		Name:            "suite",
		Port:            8080,
		GRPCPort:        9090,
		Replicas:        1,
		Hostnames:       []string{"suite.dev.test"},
		Modules:         []string{"greeter", "echo"},
		DatabaseEnvVar:  "DATABASE_URL",
		DatabaseSchemas: []string{"greeter", "echo"},
	}
	vals := w.ToHelmValues()
	svc, ok := vals["service"].(map[string]any)
	if !ok {
		t.Fatalf("ToHelmValues: service is %T, want map[string]any", vals["service"])
	}
	if svc["name"] != "suite" {
		t.Errorf("service.name = %v, want suite", svc["name"])
	}
	if svc["port"] != 8080 {
		t.Errorf("service.port = %v, want 8080", svc["port"])
	}
	if svc["hostname"] != "suite.dev.test" {
		t.Errorf("service.hostname = %v, want suite.dev.test", svc["hostname"])
	}
	deps, ok := vals["dependencies"].([]map[string]any)
	if !ok {
		t.Fatalf("dependencies is %T", vals["dependencies"])
	}
	if len(deps) != 1 || deps[0]["envVar"] != "DATABASE_URL" {
		t.Errorf("dependencies = %v, want [{envVar: DATABASE_URL}]", deps)
	}
}

// T-P6-13: ToHelmValues with no DB produces empty deps slice.
func TestWorkloadValues_ToHelmValues_NoDB(t *testing.T) {
	w := WorkloadValues{
		Name:     "suite",
		Port:     8080,
		Replicas: 1,
	}
	vals := w.ToHelmValues()
	deps := vals["dependencies"].([]map[string]any)
	if len(deps) != 0 {
		t.Errorf("no-DB workload should have empty deps, got %v", deps)
	}
}

// --- edge cases ---

// T-P6-14: ComposeChart rejects a composition with no modules.
func TestComposeChart_NoModules_Error(t *testing.T) {
	// Use ParseCompositionForEdit to bypass the at-least-one-module rule.
	doc := `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: empty }
spec:
  modules: []
`
	c, err := config.ParseCompositionForEdit([]byte(doc))
	if err != nil {
		t.Fatalf("ParseCompositionForEdit: %v", err)
	}
	if _, err := ComposeChart(c, ModeSingleBinary); err == nil {
		t.Error("ComposeChart with 0 modules: expected error, got nil")
	}
}

// T-P6-15: No shared DB → SharedDB is nil; workloads have empty DatabaseEnvVar.
func TestComposeChart_NoDB_SharedDBNil(t *testing.T) {
	doc := `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: nodbs }
spec:
  modules:
    - { name: a, module: github.com/acme/a }
    - { name: b, module: github.com/acme/b }
`
	c := mustComposition(t, doc)
	plan, err := ComposeChart(c, ModeSingleBinary)
	if err != nil {
		t.Fatalf("ComposeChart no-DB: %v", err)
	}
	if plan.SharedDB != nil {
		t.Errorf("no shared DB declared: SharedDB should be nil, got %+v", plan.SharedDB)
	}
	for _, w := range plan.Workloads {
		if w.DatabaseEnvVar != "" {
			t.Errorf("workload %q: DatabaseEnvVar = %q, want empty (no DB)", w.Name, w.DatabaseEnvVar)
		}
	}
}

// T-P6-16: DSNRef defaults to "DATABASE_URL" when spec omits it.
func TestComposeChart_DSNRef_Default(t *testing.T) {
	doc := `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: x }
spec:
  database: { engine: postgres }
  modules:
    - { name: a, module: github.com/acme/a }
`
	c := mustComposition(t, doc)
	plan, err := ComposeChart(c, ModeSingleBinary)
	if err != nil {
		t.Fatalf("ComposeChart DSNRef default: %v", err)
	}
	if plan.Workloads[0].DatabaseEnvVar != "DATABASE_URL" {
		t.Errorf("DatabaseEnvVar = %q, want DATABASE_URL (default)", plan.Workloads[0].DatabaseEnvVar)
	}
}

// workloadNames returns all workload names from a plan for error messages.
func workloadNames(plan *ChartPlan) []string {
	names := make([]string, len(plan.Workloads))
	for i, w := range plan.Workloads {
		names[i] = w.Name
	}
	return names
}
