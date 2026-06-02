package deploy

import (
	"context"
	"strings"
	"testing"
)

// T003: chartValues assembles the service-chart values contract from a workload.
func TestChartValues(t *testing.T) {
	v := chartValues("my-svc", "img:tag", 8080, 2, "my-svc.dev.test", []DepEnv{
		{Name: "db", Engine: "postgres", Version: "16", EnvVar: "DATABASE_URL"},
	})
	svc, ok := v["service"].(map[string]any)
	if !ok {
		t.Fatalf("service values missing: %+v", v)
	}
	if svc["name"] != "my-svc" || svc["image"] != "img:tag" || svc["port"] != 8080 ||
		svc["replicas"] != 2 || svc["hostname"] != "my-svc.dev.test" {
		t.Errorf("service values = %+v", svc)
	}
	deps, ok := v["dependencies"].([]map[string]any)
	if !ok || len(deps) != 1 {
		t.Fatalf("dependencies values = %+v", v["dependencies"])
	}
	if deps[0]["name"] != "db" || deps[0]["envVar"] != "DATABASE_URL" {
		t.Errorf("dependency values = %+v", deps[0])
	}
}

// T003: buildTag derives a deterministic, distinct, lowercase tag per service.
func TestBuildTag(t *testing.T) {
	a := buildTag("my-svc")
	if a == "" || !strings.Contains(a, "my-svc") {
		t.Errorf("buildTag(my-svc) = %q", a)
	}
	if a != buildTag("my-svc") {
		t.Error("buildTag must be deterministic")
	}
	if buildTag("alpha") == buildTag("beta") {
		t.Error("distinct services must derive distinct tags")
	}
}

// T003 (T006): a reference image is returned as-is without invoking docker/k3d.
func TestEnsureImage_reference(t *testing.T) {
	got, err := DockerK3dBuilder{}.EnsureImage(context.Background(), ImageSource{Image: "ghcr.io/acme/x:dev"}, "devedge")
	if err != nil {
		t.Fatalf("EnsureImage(reference): %v", err)
	}
	if got != "ghcr.io/acme/x:dev" {
		t.Errorf("reference image = %q, want it returned as-is", got)
	}
}
