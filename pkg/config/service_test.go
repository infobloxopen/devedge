package config

import (
	"fmt"
	"strings"
	"testing"
)

// T020: spec.cluster.dedicated and dependencies[].dedicated strict-decode + defaults.
func TestParseService_dedicatedOptIns(t *testing.T) {
	doc := `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: my-svc
spec:
  dev:
    hostname: my-svc.dev.test
  cluster:
    dedicated: true
  dependencies:
    - name: db
      engine: postgres
      port: 5432
      dedicated: true
`
	cfg, err := ParseService([]byte(doc))
	if err != nil {
		t.Fatalf("ParseService: %v", err)
	}
	if !cfg.Spec.Cluster.Dedicated || !cfg.ClusterDedicated() {
		t.Error("spec.cluster.dedicated did not decode to true")
	}
	if len(cfg.Spec.Dependencies) != 1 || !cfg.Spec.Dependencies[0].Dedicated {
		t.Errorf("dependencies[0].dedicated did not decode: %+v", cfg.Spec.Dependencies)
	}
}

// T020: both opt-ins default to false when omitted.
func TestParseService_dedicatedDefaultsFalse(t *testing.T) {
	doc := `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: my-svc
spec:
  dev:
    hostname: my-svc.dev.test
  dependencies:
    - name: db
      engine: postgres
      port: 5432
`
	cfg, err := ParseService([]byte(doc))
	if err != nil {
		t.Fatalf("ParseService: %v", err)
	}
	if cfg.ClusterDedicated() {
		t.Error("cluster.dedicated should default to false")
	}
	if cfg.Spec.Dependencies[0].Dedicated {
		t.Error("dependencies[].dedicated should default to false")
	}
}

// T020: strict decode still rejects unknown fields inside the new cluster block.
func TestParseService_rejectsUnknownClusterField(t *testing.T) {
	doc := `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: my-svc
spec:
  dev:
    hostname: my-svc.dev.test
  cluster:
    bogus: true
`
	if _, err := ParseService([]byte(doc)); err == nil {
		t.Error("expected strict decode to reject unknown spec.cluster field")
	}
}

func serviceWithDeps(deps string) []byte {
	return []byte(`
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: webhooks
spec:
  dev:
    hostname: webhooks.dev.test
  dependencies:
` + deps)
}

func TestParseService_dependency_missing_attrs(t *testing.T) {
	cases := map[string]struct {
		deps string
		want string // substring the error must name
	}{
		"missing name":   {"    - engine: postgres\n      port: 5432\n", "name"},
		"missing engine": {"    - name: db\n      port: 5432\n", "engine"},
		"missing port":   {"    - name: db\n      engine: postgres\n", "port"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseService(serviceWithDeps(tc.deps))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should name %q", err, tc.want)
			}
		})
	}
}

func TestParseService_dependency_duplicate_name(t *testing.T) {
	deps := "    - name: db\n      engine: postgres\n      port: 5432\n" +
		"    - name: db\n      engine: redis\n      port: 6379\n"
	_, err := ParseService(serviceWithDeps(deps))
	if err == nil || !strings.Contains(err.Error(), "db") {
		t.Fatalf("expected duplicate-name error naming \"db\", got %v", err)
	}
}

func TestParseService_dependency_unrecognized_engine(t *testing.T) {
	deps := "    - name: db\n      engine: mongo\n      port: 27017\n"
	_, err := ParseService(serviceWithDeps(deps))
	if err == nil {
		t.Fatal("expected error for unrecognized engine")
	}
	for _, eng := range []string{"postgres", "redis"} {
		if !strings.Contains(err.Error(), eng) {
			t.Errorf("error %q should list recognized engine %q", err, eng)
		}
	}
}

func TestParseService_dependency_port_out_of_range(t *testing.T) {
	// Port 0 is indistinguishable from "missing" with an int field, so it is
	// reported as missing (covered above); -1 and 70000 are genuinely out of range.
	for _, port := range []string{"-1", "70000"} {
		deps := "    - name: db\n      engine: postgres\n      port: " + port + "\n"
		_, err := ParseService(serviceWithDeps(deps))
		if err == nil || !strings.Contains(err.Error(), port) {
			t.Errorf("port %s: expected error naming the bad port, got %v", port, err)
		}
	}
}


func TestParseService_missing_name(t *testing.T) {
	input := []byte(`
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata: {}
spec:
  dev:
    hostname: webhooks.dev.test
`)
	_, err := ParseService(input)
	if err == nil || !strings.Contains(err.Error(), "metadata.name") {
		t.Fatalf("expected error naming metadata.name, got %v", err)
	}
}

func TestParseService_missing_apiVersion(t *testing.T) {
	input := []byte(`
kind: Service
metadata:
  name: webhooks
spec:
  dev:
    hostname: webhooks.dev.test
`)
	_, err := ParseService(input)
	if err == nil || !strings.Contains(err.Error(), "apiVersion") {
		t.Fatalf("expected error naming apiVersion, got %v", err)
	}
}

func TestParseService_hostname(t *testing.T) {
	cases := map[string]struct {
		hostname string
		wantErr  bool
	}{
		"valid":             {"webhooks.dev.test", false},
		"valid single":      {"webhooks", false},
		"empty":             {"", true},
		"leading hyphen":    {"-bad.dev.test", true},
		"trailing dot-label": {"bad..dev.test", true},
		"space":             {"bad host.dev.test", true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			input := []byte(`
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: webhooks
spec:
  dev:
    hostname: ` + fmt.Sprintf("%q", tc.hostname) + `
`)
			_, err := ParseService(input)
			if tc.wantErr && err == nil {
				t.Errorf("hostname %q: expected error, got nil", tc.hostname)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("hostname %q: unexpected error %v", tc.hostname, err)
			}
		})
	}
}

func TestParseService_invalid_yaml(t *testing.T) {
	_, err := ParseService([]byte("this: : : not valid yaml\n  - x"))
	if err == nil {
		t.Error("expected parse error for invalid YAML (no panic)")
	}
}

func TestParseService_empty(t *testing.T) {
	_, err := ParseService([]byte(""))
	if err == nil {
		t.Error("expected error for empty document (no panic)")
	}
}

func TestParseService_wellformed(t *testing.T) {
	input := []byte(`
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: webhooks
spec:
  dev:
    hostname: webhooks.dev.test
  dependencies:
    - name: db
      engine: postgres
      version: "16"
      port: 5432
    - name: cache
      engine: redis
      port: 6379
  routes:
    - host: webhooks.dev.test
      upstream: http://127.0.0.1:8080
`)

	cfg, err := ParseService(input)
	if err != nil {
		t.Fatalf("ParseService: %v", err)
	}
	if cfg.Project() != "webhooks" {
		t.Errorf("Project() = %q, want webhooks", cfg.Project())
	}
	if cfg.Spec.Dev.Hostname != "webhooks.dev.test" {
		t.Errorf("dev.hostname = %q", cfg.Spec.Dev.Hostname)
	}
	if len(cfg.Spec.Dependencies) != 2 {
		t.Fatalf("dependencies = %d, want 2", len(cfg.Spec.Dependencies))
	}
	if cfg.Spec.Dependencies[0].Name != "db" ||
		cfg.Spec.Dependencies[0].Engine != "postgres" ||
		cfg.Spec.Dependencies[0].Version != "16" ||
		cfg.Spec.Dependencies[0].Port != 5432 {
		t.Errorf("dependencies[0] = %+v", cfg.Spec.Dependencies[0])
	}
	if len(cfg.Spec.Routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(cfg.Spec.Routes))
	}
	if cfg.Spec.Routes[0].Host != "webhooks.dev.test" {
		t.Errorf("routes[0].Host = %q", cfg.Spec.Routes[0].Host)
	}
}

func TestParseService_unknown_field_rejected(t *testing.T) {
	input := []byte(`
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: webhooks
spec:
  dev:
    hostname: webhooks.dev.test
  bogusField: nope
`)
	_, err := ParseService(input)
	if err == nil {
		t.Fatal("expected error for unknown field (strict decode)")
	}
}

func TestServiceToRoutes(t *testing.T) {
	cfg := &ServiceConfig{
		APIVersion: APIVersion,
		Kind:       KindService,
		Metadata:   ObjectMeta{Name: "webhooks"},
		Spec: ServiceSpec{
			Dev: ServiceDev{Hostname: "webhooks.dev.test"},
			Routes: []RouteEntry{
				{Host: "webhooks.dev.test", Upstream: "http://127.0.0.1:8080"},
			},
		},
	}

	routes, err := cfg.ToRoutes()
	if err != nil {
		t.Fatalf("ToRoutes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("len = %d, want 1", len(routes))
	}
	if routes[0].Project != "webhooks" {
		t.Errorf("Project = %q, want webhooks", routes[0].Project)
	}
	if routes[0].Source != "project-file" {
		t.Errorf("Source = %q, want project-file", routes[0].Source)
	}
	if routes[0].Host != "webhooks.dev.test" {
		t.Errorf("Host = %q", routes[0].Host)
	}
}

func TestServiceToRoutes_empty(t *testing.T) {
	cfg := &ServiceConfig{
		Metadata: ObjectMeta{Name: "webhooks"},
		Spec:     ServiceSpec{Dev: ServiceDev{Hostname: "webhooks.dev.test"}},
	}

	routes, err := cfg.ToRoutes()
	if err != nil {
		t.Fatalf("ToRoutes: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("len = %d, want 0", len(routes))
	}
}
