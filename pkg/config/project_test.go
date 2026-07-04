package config

import (
	"testing"
	"time"
)

func TestParseProject(t *testing.T) {
	input := []byte(`
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Config
metadata:
  name: foo
  labels:
    team: platform
spec:
  defaults:
    ttl: 30s
    tls: true
  routes:
    - host: web.foo.dev.test
      upstream: http://127.0.0.1:3000
    - host: api.foo.dev.test
      upstream: http://127.0.0.1:8081
      mode: host-header-pass
`)

	cfg, err := ParseProject(input)
	if err != nil {
		t.Fatalf("ParseProject: %v", err)
	}

	if cfg.APIVersion != "devedge.infoblox.dev/v1alpha1" {
		t.Errorf("APIVersion = %q", cfg.APIVersion)
	}
	if cfg.Kind != "Config" {
		t.Errorf("Kind = %q", cfg.Kind)
	}
	if cfg.Project() != "foo" {
		t.Errorf("Project() = %q, want foo", cfg.Project())
	}
	if cfg.Metadata.Labels["team"] != "platform" {
		t.Errorf("Labels = %v", cfg.Metadata.Labels)
	}
	if len(cfg.Spec.Routes) != 2 {
		t.Fatalf("Routes len = %d, want 2", len(cfg.Spec.Routes))
	}
	if cfg.Spec.Routes[0].Host != "web.foo.dev.test" {
		t.Errorf("Routes[0].Host = %q", cfg.Spec.Routes[0].Host)
	}
	if cfg.Spec.Routes[1].Mode != "host-header-pass" {
		t.Errorf("Routes[1].Mode = %q", cfg.Spec.Routes[1].Mode)
	}
}

func TestParseProject_pathRouting(t *testing.T) {
	input := []byte(`
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Config
metadata:
  name: app
spec:
  routes:
    - host: app.dev.test
      upstream: http://127.0.0.1:3000
    - host: app.dev.test
      upstream: http://127.0.0.1:8080
      path: /api
      stripPrefix: true
`)

	cfg, err := ParseProject(input)
	if err != nil {
		t.Fatalf("ParseProject: %v", err)
	}
	if len(cfg.Spec.Routes) != 2 {
		t.Fatalf("Routes len = %d, want 2", len(cfg.Spec.Routes))
	}

	// Entry 0: no path fields → catch-all defaults.
	if cfg.Spec.Routes[0].Path != "" || cfg.Spec.Routes[0].StripPrefix {
		t.Errorf("Routes[0] path=%q strip=%v, want empty/false (catch-all)",
			cfg.Spec.Routes[0].Path, cfg.Spec.Routes[0].StripPrefix)
	}
	// Entry 1: path + stripPrefix parsed.
	if cfg.Spec.Routes[1].Path != "/api" {
		t.Errorf("Routes[1].Path = %q, want /api", cfg.Spec.Routes[1].Path)
	}
	if !cfg.Spec.Routes[1].StripPrefix {
		t.Errorf("Routes[1].StripPrefix = false, want true")
	}

	// Round-trip into domain routes.
	routes, err := cfg.ToRoutes()
	if err != nil {
		t.Fatalf("ToRoutes: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("routes len = %d, want 2", len(routes))
	}
	if routes[0].Path != "" || routes[0].StripPrefix {
		t.Errorf("routes[0] path=%q strip=%v, want catch-all", routes[0].Path, routes[0].StripPrefix)
	}
	if routes[1].Path != "/api" || !routes[1].StripPrefix {
		t.Errorf("routes[1] path=%q strip=%v, want /api/true", routes[1].Path, routes[1].StripPrefix)
	}
}

func TestParseProject_missing_apiVersion(t *testing.T) {
	input := []byte(`
kind: Config
metadata:
  name: foo
spec:
  routes:
    - host: x.dev.test
      upstream: http://127.0.0.1:1
`)
	_, err := ParseProject(input)
	if err == nil {
		t.Fatal("expected error for missing apiVersion")
	}
}

func TestParseProject_missing_name(t *testing.T) {
	input := []byte(`
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Config
metadata: {}
spec:
  routes:
    - host: x.dev.test
      upstream: http://127.0.0.1:1
`)
	_, err := ParseProject(input)
	if err == nil {
		t.Fatal("expected error for missing metadata.name")
	}
}

func TestParseProject_wrong_kind(t *testing.T) {
	input := []byte(`
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Deployment
metadata:
  name: foo
spec:
  routes: []
`)
	_, err := ParseProject(input)
	if err == nil {
		t.Fatal("expected error for wrong kind")
	}
}

func TestToRoutes(t *testing.T) {
	cfg := &ProjectConfig{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata:   ObjectMeta{Name: "foo"},
		Spec: ProjectSpec{
			Defaults: RouteDefaults{TTL: "30s"},
			Routes: []RouteEntry{
				{Host: "web.foo.dev.test", Upstream: "http://127.0.0.1:3000"},
				{Host: "api.foo.dev.test", Upstream: "http://127.0.0.1:4000"},
			},
		},
	}

	routes, err := cfg.ToRoutes()
	if err != nil {
		t.Fatalf("ToRoutes: %v", err)
	}

	if len(routes) != 2 {
		t.Fatalf("len = %d, want 2", len(routes))
	}
	if routes[0].Project != "foo" {
		t.Errorf("Project = %q", routes[0].Project)
	}
	if routes[0].Source != "project-file" {
		t.Errorf("Source = %q", routes[0].Source)
	}
	if routes[0].TTL != 30*time.Second {
		t.Errorf("TTL = %v", routes[0].TTL)
	}
}

// TestParseProject_NoTile_BackwardCompat proves a devedge.yaml with no tile
// metadata still parses (tile nil) and maps to routes with no tile — the tile
// addition to RouteEntry is backward-compatible.
func TestParseProject_NoTile_BackwardCompat(t *testing.T) {
	input := []byte(`apiVersion: devedge.infoblox.dev/v1alpha1
kind: Config
metadata:
  name: foo
spec:
  routes:
    - host: web.foo.dev.test
      upstream: http://127.0.0.1:3000
`)
	cfg, err := ParseProject(input)
	if err != nil {
		t.Fatalf("ParseProject: %v", err)
	}
	if cfg.Spec.Routes[0].Tile != nil {
		t.Errorf("Routes[0].Tile = %+v, want nil", cfg.Spec.Routes[0].Tile)
	}
	routes, err := cfg.ToRoutes()
	if err != nil {
		t.Fatalf("ToRoutes: %v", err)
	}
	if routes[0].Tile != nil {
		t.Errorf("route Tile = %+v, want nil", routes[0].Tile)
	}
}

// TestParseProject_Tile proves a tile-annotated route parses and the tile flows
// through ToRoutes onto the domain Route.
func TestParseProject_Tile(t *testing.T) {
	input := []byte(`apiVersion: devedge.infoblox.dev/v1alpha1
kind: Config
metadata:
  name: orders
spec:
  routes:
    - host: orders.app.dev.test
      upstream: http://127.0.0.1:3000
      tile:
        displayName: Orders
        description: Manage orders
        launchURL: https://orders.app.dev.test/
`)
	cfg, err := ParseProject(input)
	if err != nil {
		t.Fatalf("ParseProject: %v", err)
	}
	entry := cfg.Spec.Routes[0]
	if entry.Tile == nil || entry.Tile.DisplayName != "Orders" {
		t.Fatalf("Routes[0].Tile = %+v, want DisplayName=Orders", entry.Tile)
	}
	routes, err := cfg.ToRoutes()
	if err != nil {
		t.Fatalf("ToRoutes: %v", err)
	}
	if routes[0].Tile == nil || routes[0].Tile.LaunchURL != "https://orders.app.dev.test/" {
		t.Fatalf("route Tile = %+v, want the declared tile", routes[0].Tile)
	}
}
