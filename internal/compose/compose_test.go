package compose

import (
	"strings"
	"testing"

	"github.com/infobloxopen/devedge/pkg/config"
)

func mustComposition(t *testing.T, doc string) *config.Composition {
	t.Helper()
	c, err := config.ParseComposition([]byte(doc))
	if err != nil {
		t.Fatalf("parse composition: %v", err)
	}
	return c
}

const twoModuleDoc = `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: control-plane }
spec:
  runtime: { grpc: ":9090", http: ":8080" }
  database: { engine: postgres, isolation: schema-preferred }
  modules:
    - { name: orders, module: github.com/acme/orders/module@v0.4.1, failurePolicy: fail-host }
    - { name: billing, module: github.com/acme/billing/module@v0.7.0, failurePolicy: degraded }
`

func TestParseModuleRef(t *testing.T) {
	cases := []struct {
		ref, path, ver string
		wantErr        bool
	}{
		{"github.com/a/b@v1.2.3", "github.com/a/b", "v1.2.3", false},
		{"github.com/a/b", "github.com/a/b", "", false},
		{"", "", "", true},
		{"@v1", "", "", true},
	}
	for _, c := range cases {
		p, v, err := ParseModuleRef(c.ref)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseModuleRef(%q) err=%v wantErr=%v", c.ref, err, c.wantErr)
			continue
		}
		if !c.wantErr && (p != c.path || v != c.ver) {
			t.Errorf("ParseModuleRef(%q) = (%q,%q), want (%q,%q)", c.ref, p, v, c.path, c.ver)
		}
	}
}

func TestResolveModuleRefs_UniqueAliases(t *testing.T) {
	doc := `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: x }
spec:
  modules:
    - { name: "orders", module: github.com/a/orders }
    - { name: "orders-v2", module: github.com/a/orders2 }
    - { name: "3rd", module: github.com/a/third }
`
	c := mustComposition(t, doc)
	refs, err := ResolveModuleRefs(c)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range refs {
		if seen[r.Alias] {
			t.Errorf("duplicate alias %q", r.Alias)
		}
		seen[r.Alias] = true
		// alias must be a valid Go identifier start
		first := r.Alias[0]
		if !((first >= 'a' && first <= 'z') || first == '_') {
			t.Errorf("alias %q does not start with a valid identifier char", r.Alias)
		}
	}
	// "3rd" -> leading digit prefixed with "m".
	if refs[2].Alias[0] == '3' {
		t.Errorf("numeric-leading name produced invalid alias %q", refs[2].Alias)
	}
}

func TestGenerate_MainGoShape(t *testing.T) {
	c := mustComposition(t, twoModuleDoc)
	gen, err := Generate(c, "1.26.0", "example.com/control-plane/cmd/control-plane")
	if err != nil {
		t.Fatal(err)
	}
	main := gen.MainGo
	for _, want := range []string{
		"package main",
		"github.com/infobloxopen/devedge-sdk/servicekit",
		"github.com/acme/orders/module",
		"github.com/acme/billing/module",
		"servicekit.Run(hc)",
		"servicekit.HostConfig{",
		".Module(),",
		`GRPCAddr: ":9090"`,
		`HTTPAddr: ":8080"`,
		"servicekit.DatabaseConfig{",
		`Engine: "postgres"`,
		`servicekit.IsolationPolicy("schema-preferred")`,
		"FailurePolicies: map[string]servicekit.FailurePolicy{",
		`servicekit.FailurePolicy("fail-host")`,
		`servicekit.FailurePolicy("degraded")`,
	} {
		if !strings.Contains(main, want) {
			t.Errorf("generated main.go missing %q\n---\n%s", want, main)
		}
	}
	// Static composition: NO plugin loading.
	if strings.Contains(main, "plugin.") || strings.Contains(main, "plugin/") {
		t.Error("generated main.go must not use Go plugins")
	}
	if gen.Dir != "cmd/control-plane" {
		t.Errorf("Dir = %q", gen.Dir)
	}
}

func TestGenerate_GoModShape(t *testing.T) {
	c := mustComposition(t, twoModuleDoc)
	gen, err := Generate(c, "1.26.0", "example.com/control-plane/cmd/control-plane")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"module example.com/control-plane/cmd/control-plane",
		"go 1.26.0",
		"github.com/infobloxopen/devedge-sdk " + SDKVersion,
		"github.com/acme/orders/module v0.4.1",
		"github.com/acme/billing/module v0.7.0",
	} {
		if !strings.Contains(gen.GoMod, want) {
			t.Errorf("generated go.mod missing %q\n---\n%s", want, gen.GoMod)
		}
	}
}

func TestGenerate_LockShape(t *testing.T) {
	c := mustComposition(t, twoModuleDoc)
	gen, err := Generate(c, "1.26.0", "example.com/x")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"lockVersion: " + LockVersion,
		"composition: control-plane",
		"sdk: " + SDKVersion,
		"go: 1.26.0",
		"name: orders",
		"module: github.com/acme/orders/module",
		"version: v0.4.1",
		"name: billing",
	} {
		if !strings.Contains(gen.Lock, want) {
			t.Errorf("generated composition.lock missing %q\n---\n%s", want, gen.Lock)
		}
	}
}

func TestGenerate_UnpinnedModuleRequiresLatest(t *testing.T) {
	doc := `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: x }
spec:
  modules:
    - { name: a, module: github.com/acme/a }
`
	c := mustComposition(t, doc)
	gen, err := Generate(c, "1.26.0", "example.com/x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gen.GoMod, "github.com/acme/a latest") {
		t.Errorf("unpinned module should require @latest:\n%s", gen.GoMod)
	}
}
