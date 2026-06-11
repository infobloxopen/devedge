package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T002: spec.workload strict-decode + validation (image XOR build, port required,
// build requires context, replicas default 1).
func serviceWithWorkload(workload string) []byte {
	return []byte(`apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: my-svc
spec:
  dev:
    hostname: my-svc.dev.test
  workload:
` + workload)
}

func TestParseService_workloadImage(t *testing.T) {
	cfg, err := ParseService(serviceWithWorkload("    image: ghcr.io/acme/my-svc:dev\n    port: 8080\n"))
	if err != nil {
		t.Fatalf("ParseService: %v", err)
	}
	w := cfg.Workload()
	if w == nil || w.Image != "ghcr.io/acme/my-svc:dev" || w.Port != 8080 {
		t.Fatalf("workload not decoded: %+v", w)
	}
	if w.EffectiveReplicas() != 1 {
		t.Errorf("replicas default = %d, want 1", w.EffectiveReplicas())
	}
	if w.Build != nil {
		t.Errorf("build should be nil for an image workload")
	}
}

func TestParseService_workloadBuild(t *testing.T) {
	cfg, err := ParseService(serviceWithWorkload("    build:\n      context: .\n    port: 8080\n    replicas: 3\n"))
	if err != nil {
		t.Fatalf("ParseService: %v", err)
	}
	w := cfg.Workload()
	if w == nil || w.Build == nil || w.Build.Context != "." {
		t.Fatalf("build workload not decoded: %+v", w)
	}
	if w.EffectiveReplicas() != 3 {
		t.Errorf("replicas = %d, want 3", w.EffectiveReplicas())
	}
}

func TestParseService_workloadValidation(t *testing.T) {
	bad := map[string]string{
		"image and build both set": "    image: x:y\n    build:\n      context: .\n    port: 8080\n",
		"neither image nor build":  "    port: 8080\n",
		"missing port":             "    image: x:y\n",
		"port out of range":        "    image: x:y\n    port: 70000\n",
		"build missing context":    "    build:\n      dockerfile: Dockerfile\n    port: 8080\n",
	}
	for name, wl := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseService(serviceWithWorkload(wl)); err == nil {
				t.Errorf("expected validation error for %q", name)
			}
		})
	}
}

func TestParseService_workloadStrictUnknownField(t *testing.T) {
	if _, err := ParseService(serviceWithWorkload("    image: x:y\n    port: 8080\n    bogus: true\n")); err == nil {
		t.Error("expected strict decode to reject unknown spec.workload field")
	}
}

func TestParseService_noWorkload(t *testing.T) {
	// A service with no workload is valid (local-run only).
	cfg, err := ParseService([]byte(`apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: my-svc
spec:
  dev:
    hostname: my-svc.dev.test
`))
	if err != nil {
		t.Fatalf("ParseService: %v", err)
	}
	if cfg.Workload() != nil {
		t.Errorf("expected nil workload, got %+v", cfg.Workload())
	}
}

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
		"valid":              {"webhooks.dev.test", false},
		"valid single":       {"webhooks", false},
		"empty":              {"", true},
		"leading hyphen":     {"-bad.dev.test", true},
		"trailing dot-label": {"bad..dev.test", true},
		"space":              {"bad host.dev.test", true},
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

// T003: migrations/seed fields — strict-decode, engine-gate, and Migrations().

func TestParseService_migrationsDecode(t *testing.T) {
	// migrations: and seed: on a postgres dependency decode into the new fields.
	doc := []byte(`apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: widgets
spec:
  dev:
    hostname: widgets.dev.test
  dependencies:
    - name: db
      engine: postgres
      port: 5432
      migrations: db/migrations
      seed: db/seed/dev.sql
`)
	cfg, err := ParseService(doc)
	if err != nil {
		t.Fatalf("ParseService: %v", err)
	}
	if len(cfg.Spec.Dependencies) != 1 {
		t.Fatalf("dependencies len = %d, want 1", len(cfg.Spec.Dependencies))
	}
	d := cfg.Spec.Dependencies[0]
	if d.Migrations != "db/migrations" {
		t.Errorf("Migrations = %q, want %q", d.Migrations, "db/migrations")
	}
	if d.Seed != "db/seed/dev.sql" {
		t.Errorf("Seed = %q, want %q", d.Seed, "db/seed/dev.sql")
	}
}

func TestParseService_migrationsEngineGate(t *testing.T) {
	// migrations on a non-postgres engine must be rejected.
	doc := []byte(`apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: widgets
spec:
  dev:
    hostname: widgets.dev.test
  dependencies:
    - name: cache
      engine: redis
      port: 6379
      migrations: db/migrations
`)
	_, err := ParseService(doc)
	if err == nil {
		t.Fatal("expected error for migrations on redis dependency, got nil")
	}
	if !strings.Contains(err.Error(), "cache") {
		t.Errorf("error should name the dependency %q: %v", "cache", err)
	}
	if !strings.Contains(err.Error(), "redis") {
		t.Errorf("error should name the engine %q: %v", "redis", err)
	}
}

func TestParseService_seedEngineGate(t *testing.T) {
	// seed on a non-postgres engine must also be rejected.
	doc := []byte(`apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: widgets
spec:
  dev:
    hostname: widgets.dev.test
  dependencies:
    - name: cache
      engine: redis
      port: 6379
      seed: db/seed/dev.sql
`)
	_, err := ParseService(doc)
	if err == nil {
		t.Fatal("expected error for seed on redis dependency, got nil")
	}
	if !strings.Contains(err.Error(), "cache") {
		t.Errorf("error should name the dependency %q: %v", "cache", err)
	}
	if !strings.Contains(err.Error(), "redis") {
		t.Errorf("error should name the engine %q: %v", "redis", err)
	}
}

func TestParseService_seedWithoutMigrationsAllowed(t *testing.T) {
	// seed without migrations on postgres is explicitly allowed.
	doc := []byte(`apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: widgets
spec:
  dev:
    hostname: widgets.dev.test
  dependencies:
    - name: db
      engine: postgres
      port: 5432
      seed: db/seed/dev.sql
`)
	cfg, err := ParseService(doc)
	if err != nil {
		t.Fatalf("seed-without-migrations should be allowed: %v", err)
	}
	if cfg.Spec.Dependencies[0].Seed != "db/seed/dev.sql" {
		t.Errorf("Seed = %q, want %q", cfg.Spec.Dependencies[0].Seed, "db/seed/dev.sql")
	}
	if cfg.Spec.Dependencies[0].Migrations != "" {
		t.Errorf("Migrations should be empty, got %q", cfg.Spec.Dependencies[0].Migrations)
	}
}

// helpers for Migrations() tests — build a minimal valid *ServiceConfig without a real YAML parse.
func makeServiceCfg(depName, engine, migrations, seed string) *ServiceConfig {
	return &ServiceConfig{
		APIVersion: APIVersion,
		Kind:       KindService,
		Metadata:   ObjectMeta{Name: "widgets"},
		Spec: ServiceSpec{
			Dev: ServiceDev{Hostname: "widgets.dev.test"},
			Dependencies: []Dependency{
				{Name: depName, Engine: engine, Port: 5432, Migrations: migrations, Seed: seed},
			},
		},
	}
}

func TestServiceMigrations_resolvesAbsDir(t *testing.T) {
	dir := t.TempDir()
	// create migrations dir with at least one *.up.sql file
	migsDir := dir + "/db/migrations"
	if err := os.MkdirAll(migsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(migsDir+"/001_init.up.sql", []byte("CREATE TABLE x();"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(migsDir+"/001_init.down.sql", []byte("DROP TABLE x;"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := makeServiceCfg("db", "postgres", "db/migrations", "")
	results, err := cfg.Migrations(dir)
	if err != nil {
		t.Fatalf("Migrations: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
	dm := results[0]
	if dm.Dependency != "db" {
		t.Errorf("Dependency = %q, want %q", dm.Dependency, "db")
	}
	if dm.Dir != migsDir {
		t.Errorf("Dir = %q, want %q", dm.Dir, migsDir)
	}
	if dm.Seed != "" {
		t.Errorf("Seed should be empty, got %q", dm.Seed)
	}
}

func TestServiceMigrations_nonExistentPath(t *testing.T) {
	dir := t.TempDir()
	cfg := makeServiceCfg("db", "postgres", "db/does-not-exist", "")
	_, err := cfg.Migrations(dir)
	if err == nil {
		t.Fatal("expected error for non-existent migrations path, got nil")
	}
}

func TestServiceMigrations_emptyDir(t *testing.T) {
	dir := t.TempDir()
	migsDir := dir + "/db/migrations"
	if err := os.MkdirAll(migsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// no *.up.sql files
	cfg := makeServiceCfg("db", "postgres", "db/migrations", "")
	_, err := cfg.Migrations(dir)
	if err == nil {
		t.Fatal("expected error for migrations dir with no *.up.sql files, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention 'empty': %v", err)
	}
}

func TestServiceMigrations_pathEscapesProjectDir(t *testing.T) {
	dir := t.TempDir()
	cfg := makeServiceCfg("db", "postgres", "../outside", "")
	_, err := cfg.Migrations(dir)
	if err == nil {
		t.Fatal("expected error for path escaping project dir, got nil")
	}
}

func TestServiceMigrations_seedFileResolved(t *testing.T) {
	dir := t.TempDir()
	// create migrations dir
	migsDir := dir + "/db/migrations"
	if err := os.MkdirAll(migsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(migsDir+"/001_init.up.sql", []byte("CREATE TABLE x();"), 0o644); err != nil {
		t.Fatal(err)
	}
	// create seed file
	seedDir := dir + "/db/seed"
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seedFile := seedDir + "/dev.sql"
	if err := os.WriteFile(seedFile, []byte("INSERT INTO x VALUES (1);"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := makeServiceCfg("db", "postgres", "db/migrations", "db/seed/dev.sql")
	results, err := cfg.Migrations(dir)
	if err != nil {
		t.Fatalf("Migrations: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
	if results[0].Seed != seedFile {
		t.Errorf("Seed = %q, want %q", results[0].Seed, seedFile)
	}
}

func TestServiceMigrations_noDeclaredDepsExcluded(t *testing.T) {
	// A dependency declaring neither migrations nor seed must NOT appear in the result.
	cfg := makeServiceCfg("db", "postgres", "", "")
	results, err := cfg.Migrations(t.TempDir())
	if err != nil {
		t.Fatalf("Migrations: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty slice, got %d entries", len(results))
	}
}

// T002 (feature 010): readiness block validation in ParseService.
// Cases 1–6 are intentionally RED until T005 adds validation logic to
// ServiceConfig.Validate(). Cases 7–9 must pass immediately (no false
// positives from a missing-but-not-yet-written validator).
func TestParseService_ReadinessValidation(t *testing.T) {
	// baseYAML is a complete valid Service document; the readiness block is
	// injected per-test by replacing the placeholder below.
	const baseWithReadiness = `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: testsvc
spec:
  dev:
    hostname: testsvc.dev.local
  routes:
    - host: testsvc.dev.local
      upstream: localhost:8080
      readiness:
        %s`

	const baseNoReadiness = `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: testsvc
spec:
  dev:
    hostname: testsvc.dev.local
  routes:
    - host: testsvc.dev.local
      upstream: localhost:8080
`

	cases := []struct {
		name        string
		yaml        string
		wantErr     bool
		errContains string
	}{
		{
			name:        "empty path",
			yaml:        fmt.Sprintf(baseWithReadiness, "path: \"\""),
			wantErr:     true,
			errContains: "readiness.path",
		},
		{
			name:        "path without leading slash",
			yaml:        fmt.Sprintf(baseWithReadiness, "path: healthz"),
			wantErr:     true,
			errContains: "readiness.path",
		},
		{
			name: "invalid timeout duration",
			yaml: fmt.Sprintf(baseWithReadiness,
				"path: /healthz\n        timeout: notaduration"),
			wantErr:     true,
			errContains: "readiness.timeout",
		},
		{
			name: "negative timeout",
			yaml: fmt.Sprintf(baseWithReadiness,
				"path: /healthz\n        timeout: \"-5s\""),
			wantErr:     true,
			errContains: "readiness.timeout",
		},
		{
			name: "invalid interval duration",
			yaml: fmt.Sprintf(baseWithReadiness,
				"path: /healthz\n        interval: notaduration"),
			wantErr:     true,
			errContains: "readiness.interval",
		},
		{
			name: "interval longer than timeout",
			yaml: fmt.Sprintf(baseWithReadiness,
				"path: /healthz\n        timeout: 100ms\n        interval: 1s"),
			wantErr:     true,
			errContains: "readiness",
		},
		{
			name: "valid block with all fields",
			yaml: fmt.Sprintf(baseWithReadiness,
				"path: /healthz\n        timeout: 30s\n        interval: 500ms"),
			wantErr: false,
		},
		{
			name:    "valid block path only",
			yaml:    fmt.Sprintf(baseWithReadiness, "path: /healthz"),
			wantErr: false,
		},
		{
			name:    "no readiness block",
			yaml:    baseNoReadiness,
			wantErr: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseService([]byte(tc.yaml))
			if tc.wantErr && err == nil {
				t.Errorf("expected error (containing %q) but got nil", tc.errContains)
				return
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected no error but got: %v", err)
				return
			}
			if tc.wantErr && tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.errContains)
			}
		})
	}
}

// Regression for the onboarding walk-through's friction #6 (007): with the
// default `-f devedge.yaml`, the project dir arrives as "." — the resolved
// migrations path must STILL be absolute, because it crosses the CLI→daemon
// process boundary where the daemon's cwd differs.
func TestServiceMigrations_relativeProjectDirYieldsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	migsDir := dir + "/db/migrations"
	if err := os.MkdirAll(migsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(migsDir+"/001_init.up.sql", []byte("CREATE TABLE x();"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(migsDir+"/001_init.down.sql", []byte("DROP TABLE x;"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run from the project dir with projectDir "." (what filepath.Dir of the
	// default -f produces).
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	cfg := makeServiceCfg("db", "postgres", "db/migrations", "")
	got, err := cfg.Migrations(".")
	if err != nil {
		t.Fatalf("Migrations(.): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	if !filepath.IsAbs(got[0].Dir) {
		t.Fatalf("resolved migrations dir must be absolute (crosses to the daemon), got %q", got[0].Dir)
	}
}
