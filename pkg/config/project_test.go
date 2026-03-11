package config

import (
	"testing"
	"time"
)

func TestParseProject(t *testing.T) {
	input := []byte(`
apiVersion: devedge.infoblox.dev/v1alpha1
kind: DEConfig
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
	if cfg.Kind != "DEConfig" {
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

func TestParseProject_missing_apiVersion(t *testing.T) {
	input := []byte(`
kind: DEConfig
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
kind: DEConfig
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
