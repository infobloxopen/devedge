package config

import (
	"strings"
	"testing"
)

const validComposition = `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata:
  name: control-plane
spec:
  runtime:
    mode: single-binary
    grpc: ":9090"
    http: ":8080"
  database:
    engine: postgres
    dsnRef: DATABASE_URL
    isolation: schema-preferred
  modules:
    - name: orders
      module: github.com/acme/orders/module@v0.4.1
      configPrefix: orders
      database: { schema: orders }
      failurePolicy: fail-host
      routes:
        - host: orders.dev.test
          upstream: http://127.0.0.1:8080
    - name: billing
      module: github.com/acme/billing/module@v0.7.0
      configPrefix: billing
      database: { schema: billing }
      failurePolicy: degraded
`

func TestParseComposition_Valid(t *testing.T) {
	c, err := ParseComposition([]byte(validComposition))
	if err != nil {
		t.Fatalf("ParseComposition: %v", err)
	}
	if c.Project() != "control-plane" {
		t.Errorf("Project() = %q, want control-plane", c.Project())
	}
	if len(c.Spec.Modules) != 2 {
		t.Fatalf("want 2 modules, got %d", len(c.Spec.Modules))
	}
	if c.Spec.Runtime.GRPC != ":9090" || c.Spec.Runtime.HTTP != ":8080" {
		t.Errorf("runtime addrs not parsed: %+v", c.Spec.Runtime)
	}
	if c.Spec.Database == nil || c.Spec.Database.Engine != "postgres" || c.Spec.Database.Isolation != "schema-preferred" {
		t.Errorf("database not parsed: %+v", c.Spec.Database)
	}
	if c.Spec.Modules[0].FailurePolicy != "fail-host" {
		t.Errorf("orders failurePolicy = %q", c.Spec.Modules[0].FailurePolicy)
	}
}

func TestParseComposition_DispatchViaParseResource(t *testing.T) {
	res, err := ParseResource([]byte(validComposition))
	if err != nil {
		t.Fatalf("ParseResource: %v", err)
	}
	if _, ok := res.(*Composition); !ok {
		t.Fatalf("ParseResource returned %T, want *Composition", res)
	}
	// Composition satisfies DependencyDeclarer (shared DB).
	dd, ok := res.(DependencyDeclarer)
	if !ok {
		t.Fatal("Composition does not satisfy DependencyDeclarer")
	}
	deps := dd.Dependencies()
	if len(deps) != 1 || deps[0].Engine != "postgres" {
		t.Errorf("Dependencies() = %+v, want one postgres dep", deps)
	}
}

func TestComposition_ToRoutes_AggregatesMembers(t *testing.T) {
	c, err := ParseComposition([]byte(validComposition))
	if err != nil {
		t.Fatal(err)
	}
	routes, err := c.ToRoutes()
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("want 1 aggregated route, got %d", len(routes))
	}
	if routes[0].Host != "orders.dev.test" || routes[0].Project != "control-plane" {
		t.Errorf("route = %+v", routes[0])
	}
}

func TestParseComposition_Errors(t *testing.T) {
	cases := map[string]string{
		"no modules": `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: x }
spec: { modules: [] }`,
		"module missing path": `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: x }
spec: { modules: [ { name: a } ] }`,
		"duplicate module name": `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: x }
spec: { modules: [ { name: a, module: p }, { name: a, module: q } ] }`,
		"bad isolation": `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: x }
spec: { database: { engine: postgres, isolation: bogus }, modules: [ { name: a, module: p } ] }`,
		"bad failurePolicy": `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: x }
spec: { modules: [ { name: a, module: p, failurePolicy: nope } ] }`,
		"unknown field": `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: x }
spec: { modules: [ { name: a, module: p } ], bogusField: 1 }`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseComposition([]byte(doc)); err == nil {
				t.Fatalf("expected error for %q, got nil", name)
			}
		})
	}
}

func TestParseCompositionForEdit_AllowsEmptyModules(t *testing.T) {
	doc := `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: x }
spec: { modules: [] }`
	if _, err := ParseCompositionForEdit([]byte(doc)); err != nil {
		t.Fatalf("ParseCompositionForEdit should allow zero modules: %v", err)
	}
	// But the strict parser rejects it.
	if _, err := ParseComposition([]byte(doc)); err == nil {
		t.Fatal("ParseComposition should reject zero modules")
	}
}

func TestComposition_Effectives(t *testing.T) {
	m := ModuleEntry{Name: "orders"}
	if m.EffectiveConfigPrefix() != "orders" || m.EffectiveSchema() != "orders" {
		t.Errorf("effectives default to name: prefix=%q schema=%q", m.EffectiveConfigPrefix(), m.EffectiveSchema())
	}
	m2 := ModuleEntry{Name: "orders", ConfigPrefix: "ord", Database: &ModuleDatabase{Schema: "ord_schema"}}
	if m2.EffectiveConfigPrefix() != "ord" || m2.EffectiveSchema() != "ord_schema" {
		t.Errorf("effectives honor overrides: prefix=%q schema=%q", m2.EffectiveConfigPrefix(), m2.EffectiveSchema())
	}
}

func TestSupportedKinds_IncludesComposition(t *testing.T) {
	found := false
	for _, k := range supportedKinds {
		if k == KindComposition {
			found = true
		}
	}
	if !found {
		t.Errorf("supportedKinds %v missing %q", supportedKinds, KindComposition)
	}
	// The dispatch error for an unknown kind should list Composition.
	_, err := ParseResource([]byte("apiVersion: devedge.infoblox.dev/v1alpha1\nkind: Nope\n"))
	if err == nil || !strings.Contains(err.Error(), KindComposition) {
		t.Errorf("unknown-kind error should mention %q: %v", KindComposition, err)
	}
}

func TestMarshalComposition_RoundTrip(t *testing.T) {
	c, err := ParseComposition([]byte(validComposition))
	if err != nil {
		t.Fatal(err)
	}
	data, err := MarshalComposition(c)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := ParseComposition(data)
	if err != nil {
		t.Fatalf("re-parse marshaled composition: %v", err)
	}
	if len(c2.Spec.Modules) != len(c.Spec.Modules) || c2.Project() != c.Project() {
		t.Errorf("round-trip lost data: %+v", c2)
	}
}
