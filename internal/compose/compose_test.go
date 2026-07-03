package compose

import (
	"os"
	"path/filepath"
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
	gen, err := Generate(c, "1.26.0", "example.com/control-plane/cmd/control-plane", GenerateOptions{})
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
		// WS-012 finding 079: the composed host builds each member over one shared
		// DB via NewModule(db) — NOT a zero-arg Module().
		".NewModule(db),",
		".Models()...)",
		"gorm.Open(sqlite.Open(dsn)",
		"gormtx.MigrateModule(",
		`GRPCAddr:`,
		`":9090"`,
		`HTTPAddr:`,
		`":8080"`,
		"servicekit.DatabaseConfig{",
		`"postgres"`,
		`servicekit.IsolationPolicy("schema-preferred")`,
		"FailurePolicies: map[string]servicekit.FailurePolicy{",
		`servicekit.FailurePolicy("fail-host")`,
		`servicekit.FailurePolicy("degraded")`,
	} {
		if !strings.Contains(main, want) {
			t.Errorf("generated main.go missing %q\n---\n%s", want, main)
		}
	}
	// The regression this fixes: a bare zero-arg Module() constructor call.
	if strings.Contains(main, ".Module(),") {
		t.Errorf("generated main.go still calls the zero-arg Module() (finding 079):\n%s", main)
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
	gen, err := Generate(c, "1.26.0", "example.com/control-plane/cmd/control-plane", GenerateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"module example.com/control-plane/cmd/control-plane",
		"go 1.26.0",
		// No local --path here, so the SDK pin falls back to the default.
		"github.com/infobloxopen/devedge-sdk " + DefaultSDKVersion,
		gormtxModule + " " + DefaultSDKVersion,
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
	gen, err := Generate(c, "1.26.0", "example.com/x", GenerateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"lockVersion: " + LockVersion,
		"composition: control-plane",
		"sdk: " + DefaultSDKVersion,
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

// TestGenerate_UnpinnedPublishedModuleErrors: a published member with no version
// and no --path has nothing to build against — `de compose build` must fail loud
// rather than write the invalid token `latest` into go.mod (finding 083).
func TestGenerate_UnpinnedPublishedModuleErrors(t *testing.T) {
	doc := `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: x }
spec:
  modules:
    - { name: a, module: github.com/acme/a }
`
	c := mustComposition(t, doc)
	_, err := Generate(c, "1.26.0", "example.com/x", GenerateOptions{})
	if err == nil {
		t.Fatal("expected an error for an unpinned published member, got nil")
	}
	if !strings.Contains(err.Error(), "has no version") {
		t.Errorf("error should explain the missing version, got: %v", err)
	}
}

// TestGenerate_LocalPathReplaceAndDerivedSDK: a member added with --path is
// required at the local pseudo-version behind a replace, and the composed SDK pin
// is derived from the member's own go.mod (findings 080 + 082 + 083).
func TestGenerate_LocalPathReplaceAndDerivedSDK(t *testing.T) {
	compDir := t.TempDir()
	memberDir := filepath.Join(compDir, "member")
	if err := os.MkdirAll(memberDir, 0o755); err != nil {
		t.Fatal(err)
	}
	memberGoMod := "module github.com/acme/orders\n\ngo 1.26.0\n\nrequire github.com/infobloxopen/devedge-sdk v0.51.0\n"
	if err := os.WriteFile(filepath.Join(memberDir, "go.mod"), []byte(memberGoMod), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Composition
metadata: { name: suite }
spec:
  modules:
    - { name: orders, module: github.com/acme/orders/module, path: member }
`
	c := mustComposition(t, doc)
	gen, err := Generate(c, "1.26.0", "example.com/suite/cmd/suite", GenerateOptions{
		CompositionDir: compDir,
		OutBaseDir:     compDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gen.SDK != "v0.51.0" {
		t.Errorf("derived SDK = %q, want v0.51.0 (from the member go.mod)", gen.SDK)
	}
	for _, want := range []string{
		"github.com/infobloxopen/devedge-sdk v0.51.0",
		// require uses the member's MODULE path (not the package import path) at the
		// local pseudo-version, and a replace points at the checkout.
		"github.com/acme/orders " + localReplaceVersion,
		"replace (",
		"github.com/acme/orders => ../../member",
	} {
		if !strings.Contains(gen.GoMod, want) {
			t.Errorf("generated go.mod missing %q\n---\n%s", want, gen.GoMod)
		}
	}
	if strings.Contains(gen.GoMod, "latest") {
		t.Errorf("generated go.mod must not contain the invalid token `latest`:\n%s", gen.GoMod)
	}
}
