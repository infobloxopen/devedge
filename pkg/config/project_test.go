package config

import (
	"testing"
	"time"
)

func TestParseProject(t *testing.T) {
	input := []byte(`
version: 1
project: foo
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

	if cfg.Project != "foo" {
		t.Errorf("Project = %q, want foo", cfg.Project)
	}
	if cfg.Version != 1 {
		t.Errorf("Version = %d, want 1", cfg.Version)
	}
	if len(cfg.Routes) != 2 {
		t.Fatalf("Routes len = %d, want 2", len(cfg.Routes))
	}
	if cfg.Routes[0].Host != "web.foo.dev.test" {
		t.Errorf("Routes[0].Host = %q", cfg.Routes[0].Host)
	}
	if cfg.Routes[1].Mode != "host-header-pass" {
		t.Errorf("Routes[1].Mode = %q", cfg.Routes[1].Mode)
	}
}

func TestParseProject_missing_project(t *testing.T) {
	input := []byte(`version: 1
routes:
  - host: x.dev.test
    upstream: http://127.0.0.1:1
`)
	_, err := ParseProject(input)
	if err == nil {
		t.Fatal("expected error for missing project")
	}
}

func TestToRoutes(t *testing.T) {
	cfg := &ProjectConfig{
		Project: "foo",
		Defaults: ProjectDefaults{
			TTL: "30s",
		},
		Routes: []RouteEntry{
			{Host: "web.foo.dev.test", Upstream: "http://127.0.0.1:3000"},
			{Host: "api.foo.dev.test", Upstream: "http://127.0.0.1:4000"},
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
