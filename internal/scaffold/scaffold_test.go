package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infobloxopen/devedge/pkg/config"
	"github.com/infobloxopen/devedge/pkg/types"
)

// TestValidateName checks that ValidateName accepts valid DNS labels and
// rejects invalid ones with a non-empty error message.
func TestValidateName(t *testing.T) {
	longValid := strings.Repeat("a", 63)

	valid := []string{
		"webhooks",
		"my-svc2",
		"a",
		longValid,
	}
	for _, name := range valid {
		t.Run("valid/"+name, func(t *testing.T) {
			if err := ValidateName(name); err != nil {
				t.Errorf("ValidateName(%q) = %v, want nil", name, err)
			}
		})
	}

	longInvalid := strings.Repeat("a", 64)

	type invalidCase struct {
		name   string
		reason string // human description of why it should fail
	}
	invalid := []invalidCase{
		{"", "empty"},
		{"Webhooks", "uppercase"},
		{"my svc", "spaces"},
		{"9lives", "leading digit"},
		{"-leading", "leading hyphen"},
		{"trailing-", "trailing hyphen"},
		{"under_score", "underscore"},
		{"dot.name", "dot"},
		{longInvalid, "64+ chars"},
	}
	for _, tc := range invalid {
		t.Run("invalid/"+tc.reason, func(t *testing.T) {
			err := ValidateName(tc.name)
			if err == nil {
				t.Errorf("ValidateName(%q) = nil, want non-nil error (%s)", tc.name, tc.reason)
				return
			}
			if err.Error() == "" {
				t.Errorf("ValidateName(%q) returned empty error message", tc.name)
			}
		})
	}
}

// TestRender_RefusesNonEmptyTarget verifies that Render returns an error when
// the target directory (ParentDir/Name) already exists and is non-empty, and
// that it does not modify the directory's contents.
func TestRender_RefusesNonEmptyTarget(t *testing.T) {
	parent := t.TempDir()
	projectDir := filepath.Join(parent, "webhooks")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(projectDir, "existing.txt")
	if err := os.WriteFile(sentinel, []byte("leave me alone"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := Params{
		Name:      "webhooks",
		ParentDir: parent,
		GoVersion: "1.25",
	}
	err := Render(p)
	if err == nil {
		t.Fatal("Render into non-empty target: expected error, got nil")
	}
	if !strings.Contains(err.Error(), projectDir) {
		t.Errorf("error %q should mention the target path %q", err.Error(), projectDir)
	}

	// Verify the sentinel file was not removed or overwritten.
	data, readErr := os.ReadFile(sentinel)
	if readErr != nil {
		t.Fatalf("sentinel file was removed: %v", readErr)
	}
	if string(data) != "leave me alone" {
		t.Errorf("sentinel file was modified: %q", string(data))
	}
}

// TestRender_AllowsEmptyExistingDir verifies that Render succeeds when the
// target directory already exists but is empty (pre-created by mkdir).
// This test is expected to fail against the stub — that is intentional.
func TestRender_AllowsEmptyExistingDir(t *testing.T) {
	parent := t.TempDir()
	projectDir := filepath.Join(parent, "webhooks")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	p := Params{
		Name:      "webhooks",
		ParentDir: parent,
		GoVersion: "1.25",
	}
	if err := Render(p); err != nil {
		t.Errorf("Render into empty existing dir: unexpected error: %v", err)
	}
}

// TestRender_ValidatesBeforeWriting verifies that an invalid name causes Render
// to return an error without creating any files under ParentDir.
func TestRender_ValidatesBeforeWriting(t *testing.T) {
	parent := t.TempDir()

	p := Params{
		Name:      "BadName!", // invalid: uppercase + special char
		ParentDir: parent,
		GoVersion: "1.25",
	}
	if err := Render(p); err == nil {
		t.Fatal("Render with invalid name: expected error, got nil")
	}

	// Nothing should have been created under parent.
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("Render with invalid name created %d entries under ParentDir, want 0", len(entries))
	}
}

// TestRender_RendersTree checks generic invariants on the rendered output tree
// without tying the test to a specific final file list.
// Expected to fail against the stub — that is intentional.
func TestRender_RendersTree(t *testing.T) {
	parent := t.TempDir()

	p := Params{
		Name:      "webhooks",
		ParentDir: parent,
		GoVersion: "1.25",
	}
	if err := Render(p); err != nil {
		t.Fatalf("Render: %v", err)
	}

	root := filepath.Join(parent, "webhooks")

	// Project root must exist.
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("project root %q does not exist: %v", root, err)
	}

	// Walk the tree and apply invariants to every regular file.
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// No file should retain a ".tmpl" suffix.
		if strings.HasSuffix(info.Name(), ".tmpl") {
			t.Errorf("rendered tree contains a .tmpl file: %q", path)
		}

		// Every regular file must be non-empty.
		if info.Size() == 0 {
			t.Errorf("rendered file is empty: %q", path)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking rendered tree: %v", err)
	}

	// devedge.yaml must exist, keep the service name, and default the edge host
	// to the generic app.dev.test (NOT <name>.dev.test).
	devedgeYAML := filepath.Join(root, "devedge.yaml")
	checkFileContains(t, devedgeYAML, "name: webhooks")
	checkFileContains(t, devedgeYAML, "hostname: app.dev.test")

	// go.mod must exist and contain the default module path (= Name when Module is "").
	goMod := filepath.Join(root, "go.mod")
	checkFileContains(t, goMod, "module webhooks")
}

// TestRender_ModuleOverride verifies that an explicit Module value overrides
// the default (which is Name).
// Expected to fail against the stub — that is intentional.
func TestRender_ModuleOverride(t *testing.T) {
	parent := t.TempDir()

	p := Params{
		Name:      "webhooks",
		Module:    "github.com/acme/webhooks",
		ParentDir: parent,
		GoVersion: "1.25",
	}
	if err := Render(p); err != nil {
		t.Fatalf("Render: %v", err)
	}

	goMod := filepath.Join(parent, "webhooks", "go.mod")
	checkFileContains(t, goMod, "module github.com/acme/webhooks")
}

// TestRender_HostOverride verifies an explicit Host overrides the default
// app.dev.test edge host in both devedge.yaml (hostname + route host) and the
// README curl examples, while leaving the service name (resource) untouched.
// This is the knob the Infoblox-CTO Go tooling flips to csp.dev.test.
func TestRender_HostOverride(t *testing.T) {
	parent := t.TempDir()

	p := Params{
		Name:      "webhooks",
		ParentDir: parent,
		GoVersion: "1.25",
		Host:      "csp.dev.test",
	}
	if err := Render(p); err != nil {
		t.Fatalf("Render: %v", err)
	}

	root := filepath.Join(parent, "webhooks")

	devedgeYAML := filepath.Join(root, "devedge.yaml")
	checkFileContains(t, devedgeYAML, "hostname: csp.dev.test")
	checkFileContains(t, devedgeYAML, "host: csp.dev.test")
	// The service name (used for resource/metadata) is unchanged by --host.
	checkFileContains(t, devedgeYAML, "name: webhooks")

	// The README curl examples use the overridden host at the product-rest path.
	readme := filepath.Join(root, "README.md")
	checkFileContains(t, readme, "https://csp.dev.test/api/webhooks/v1/webhook-endpoints")
	if data, err := os.ReadFile(readme); err == nil && strings.Contains(string(data), "app.dev.test") {
		t.Errorf("README still references the default host app.dev.test despite --host override")
	}
}

// TestRender_ProductRESTRoute pins the WS-019 default: the service is routed at
// the app host under /api/{domain} (domain defaults to the service name) with
// strip-prefix, so the public URL is product-rest and the service still serves
// /v1/...
func TestRender_ProductRESTRoute(t *testing.T) {
	parent := t.TempDir()
	if err := Render(Params{Name: "webhooks", ParentDir: parent, GoVersion: "1.25"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	root := filepath.Join(parent, "webhooks")

	devedgeYAML := filepath.Join(root, "devedge.yaml")
	checkFileContains(t, devedgeYAML, "host: app.dev.test")
	checkFileContains(t, devedgeYAML, "path: /api/webhooks")
	checkFileContains(t, devedgeYAML, "stripPrefix: true")

	// The emitted devedge.yaml must parse + route as a single strip-prefix route.
	routes := routesOf(t, devedgeYAML)
	if len(routes) != 1 {
		t.Fatalf("want 1 route, got %d: %+v", len(routes), routes)
	}
	if routes[0].Host != "app.dev.test" || routes[0].Path != "/api/webhooks" || !routes[0].StripPrefix {
		t.Errorf("route = %+v, want host app.dev.test path /api/webhooks stripPrefix", routes[0])
	}

	// README curl uses the product-rest public URL.
	checkFileContains(t, filepath.Join(root, "README.md"), "https://app.dev.test/api/webhooks/v1/webhook-endpoints")
}

// TestRender_DomainOverride verifies --domain sets the edge path independent of
// the service name.
func TestRender_DomainOverride(t *testing.T) {
	parent := t.TempDir()
	if err := Render(Params{Name: "webhooks", ParentDir: parent, GoVersion: "1.25", Domain: "hooks"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	routes := routesOf(t, filepath.Join(parent, "webhooks", "devedge.yaml"))
	if len(routes) != 1 || routes[0].Path != "/api/hooks" {
		t.Errorf("route = %+v, want single route path /api/hooks (--domain override)", routes)
	}
}

// TestRender_InvalidLayout rejects an unknown --api-layout without writing files.
func TestRender_InvalidLayout(t *testing.T) {
	parent := t.TempDir()
	if err := Render(Params{Name: "webhooks", ParentDir: parent, GoVersion: "1.25", Layout: "nope"}); err == nil {
		t.Fatal("Render with invalid layout: expected error, got nil")
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("invalid layout created %d entries under ParentDir, want 0", len(entries))
	}
}

// TestRender_TwoServices_NoCollision proves two services scaffolded under the
// same default host get distinct /api/{domain} paths — they coexist on one host.
func TestRender_TwoServices_NoCollision(t *testing.T) {
	parent := t.TempDir()
	if err := Render(Params{Name: "foo", ParentDir: parent, GoVersion: "1.25"}); err != nil {
		t.Fatalf("Render foo: %v", err)
	}
	if err := Render(Params{Name: "bar", ParentDir: parent, GoVersion: "1.25"}); err != nil {
		t.Fatalf("Render bar: %v", err)
	}
	foo := routesOf(t, filepath.Join(parent, "foo", "devedge.yaml"))
	bar := routesOf(t, filepath.Join(parent, "bar", "devedge.yaml"))
	if len(foo) != 1 || len(bar) != 1 {
		t.Fatalf("want one route each, got %d / %d", len(foo), len(bar))
	}
	if foo[0].Host != bar[0].Host {
		t.Errorf("services should share the host; got %q vs %q", foo[0].Host, bar[0].Host)
	}
	if foo[0].Path == bar[0].Path {
		t.Errorf("services collide: both routed at %q on host %q", foo[0].Path, foo[0].Host)
	}
	if foo[0].Path != "/api/foo" || bar[0].Path != "/api/bar" {
		t.Errorf("paths = %q / %q, want /api/foo, /api/bar", foo[0].Path, bar[0].Path)
	}
}

// routesOf reads + parses a scaffolded devedge.yaml and returns its routes.
func routesOf(t *testing.T, path string) []types.Route {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	parsed, err := config.ParseResource(data)
	if err != nil {
		t.Fatalf("parse %q: %v", path, err)
	}
	routes, err := parsed.ToRoutes()
	if err != nil {
		t.Fatalf("ToRoutes %q: %v", path, err)
	}
	return routes
}

// TestDefaultHost pins the public open-core default edge host. If this changes,
// the CLI docs + the Infoblox-CTO preset expectations change with it.
func TestDefaultHost(t *testing.T) {
	if DefaultHost != "app.dev.test" {
		t.Errorf("DefaultHost = %q, want app.dev.test", DefaultHost)
	}
}

// checkFileContains is a test helper that reads path and fails the test if the
// file does not exist or does not contain the given substring.
func checkFileContains(t *testing.T, path, substr string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected file %q to exist: %v", path, err)
	}
	if !strings.Contains(string(data), substr) {
		t.Errorf("file %q does not contain %q\ncontent:\n%s", path, substr, string(data))
	}
}
