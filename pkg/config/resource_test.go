package config

import (
	"strings"
	"testing"
)

func TestParseResource_Config_parity(t *testing.T) {
	input := []byte(`
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Config
metadata:
  name: foo
spec:
  defaults:
    ttl: 30s
  routes:
    - host: web.foo.dev.test
      upstream: http://127.0.0.1:3000
`)

	res, err := ParseResource(input)
	if err != nil {
		t.Fatalf("ParseResource: %v", err)
	}
	if res.Project() != "foo" {
		t.Errorf("Project() = %q, want foo", res.Project())
	}

	got, err := res.ToRoutes()
	if err != nil {
		t.Fatalf("Resource.ToRoutes: %v", err)
	}

	// Parity: ParseResource must produce the same routes as ParseProject.
	cfg, err := ParseProject(input)
	if err != nil {
		t.Fatalf("ParseProject: %v", err)
	}
	want, err := cfg.ToRoutes()
	if err != nil {
		t.Fatalf("ProjectConfig.ToRoutes: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("routes len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("route[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Config does not declare runtime dependencies.
	if _, ok := res.(DependencyDeclarer); ok {
		t.Errorf("Config resource should not implement DependencyDeclarer")
	}
}

func TestParseResource_unsupported_kind(t *testing.T) {
	input := []byte(`
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Deployment
metadata:
  name: foo
spec: {}
`)
	_, err := ParseResource(input)
	if err == nil {
		t.Fatal("expected error for unsupported kind")
	}
	for _, k := range []string{"Config", "Service"} {
		if !strings.Contains(err.Error(), k) {
			t.Errorf("error %q should list supported kind %q", err, k)
		}
	}
}

func TestParseResource_missing_apiVersion(t *testing.T) {
	input := []byte(`
kind: Config
metadata:
  name: foo
spec:
  routes: []
`)
	_, err := ParseResource(input)
	if err == nil || !strings.Contains(err.Error(), "apiVersion") {
		t.Fatalf("expected error naming apiVersion, got %v", err)
	}
}

func TestParseResource_missing_kind(t *testing.T) {
	input := []byte(`
apiVersion: devedge.infoblox.dev/v1alpha1
metadata:
  name: foo
spec: {}
`)
	_, err := ParseResource(input)
	if err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("expected error naming kind, got %v", err)
	}
}
