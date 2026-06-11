package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/infobloxopen/devedge/pkg/types"
	"gopkg.in/yaml.v3"
)

// ServiceConfig represents a devedge.yaml `kind: Service` document. It follows
// the same Kubernetes resource envelope as ProjectConfig but is decoded
// strictly (unknown fields are rejected) and may declare runtime dependencies.
type ServiceConfig struct {
	APIVersion string      `yaml:"apiVersion"`
	Kind       string      `yaml:"kind"`
	Metadata   ObjectMeta  `yaml:"metadata"`
	Spec       ServiceSpec `yaml:"spec"`
}

// ServiceSpec holds the desired state for a service.
type ServiceSpec struct {
	Dev          ServiceDev    `yaml:"dev"`
	Cluster      ClusterSpec   `yaml:"cluster,omitempty"`
	Workload     *WorkloadSpec `yaml:"workload,omitempty"`
	Dependencies []Dependency  `yaml:"dependencies,omitempty"`
	Routes       []RouteEntry  `yaml:"routes,omitempty"`
}

// WorkloadSpec declares the service's deployable workload (005). Optional — absent
// means the service is local-run only. Exactly one of Image / Build is set.
type WorkloadSpec struct {
	// Image is a pre-built container image reference to deploy as-is.
	Image string `yaml:"image,omitempty"`
	// Build, when set instead of Image, builds the image from the project (FR-011).
	Build *BuildSpec `yaml:"build,omitempty"`
	// Port the container listens on (required).
	Port int `yaml:"port"`
	// Replicas to run (default 1).
	Replicas int `yaml:"replicas,omitempty"`
}

// BuildSpec declares how to build the workload image from the project.
type BuildSpec struct {
	Context    string `yaml:"context"`
	Dockerfile string `yaml:"dockerfile,omitempty"`
}

// EffectiveReplicas returns the declared replica count, defaulting to 1.
func (w *WorkloadSpec) EffectiveReplicas() int {
	if w.Replicas <= 0 {
		return 1
	}
	return w.Replicas
}

// ServiceDev describes the service's development surface.
type ServiceDev struct {
	Hostname string `yaml:"hostname"`
}

// ClusterSpec is the optional cluster-placement block for a service (004).
type ClusterSpec struct {
	// Dedicated opts the service onto its own cluster (devedge-proj-<slug>) instead
	// of the shared dev cluster (FR-010). Default false.
	Dedicated bool `yaml:"dedicated,omitempty"`
}

// Dependency is a runtime dependency declared by a service.
type Dependency struct {
	Name    string `yaml:"name"`
	Engine  string `yaml:"engine"`
	Version string `yaml:"version,omitempty"`
	Port    int    `yaml:"port"`
	// Dedicated requests an isolated, per-service instance of the engine instead of
	// attaching to the shared per-engine instance (FR-016, rare). Default false;
	// only meaningful for a recognized engine.
	Dedicated bool `yaml:"dedicated,omitempty"`
	// Migrations is a project-relative path to the migrations directory
	// (golang-migrate-style NNN_name.up.sql / .down.sql). Only valid for
	// engine: postgres (FR-011). Optional.
	Migrations string `yaml:"migrations,omitempty"`
	// Seed is a project-relative path to a seed file or directory (plain SQL).
	// Only valid for engine: postgres (FR-011). Optional; seed without migrations
	// is allowed.
	Seed string `yaml:"seed,omitempty"`
}

// ParseService strictly decodes a `kind: Service` document (unknown fields are
// rejected) and validates it. Strictness gives typo protection that the lenient
// Config decoder intentionally does not.
func ParseService(data []byte) (*ServiceConfig, error) {
	var cfg ServiceConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("service config: document is empty")
		}
		return nil, fmt.Errorf("parse service config: %w", err)
	}

	if cfg.APIVersion == "" {
		return nil, fmt.Errorf("service config: 'apiVersion' is required (use %q)", APIVersion)
	}
	if cfg.APIVersion != APIVersion {
		return nil, fmt.Errorf("service config: unsupported apiVersion %q (use %q)", cfg.APIVersion, APIVersion)
	}
	if cfg.Kind != KindService {
		// Dispatch already guarantees this; guard for direct ParseService callers.
		return nil, fmt.Errorf("service config: unsupported kind %q (expected %q)", cfg.Kind, KindService)
	}
	if cfg.Metadata.Name == "" {
		return nil, fmt.Errorf("service config: 'metadata.name' is required")
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// recognizedEngines is the set of dependency engines devedge understands, in a
// stable order for error messages.
var recognizedEngines = []string{"postgres", "redis"}

func engineRecognized(engine string) bool {
	for _, e := range recognizedEngines {
		if e == engine {
			return true
		}
	}
	return false
}

// hostnameLabel matches a single DNS label: alphanumeric, internal hyphens
// allowed, no leading/trailing hyphen, 1-63 chars.
var hostnameLabel = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

// validHostname reports whether h is a non-empty dotted DNS hostname.
func validHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if !hostnameLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// Validate checks the service spec beyond the required envelope fields. Each
// failure names the specific offending field so the error is actionable.
func (c *ServiceConfig) Validate() error {
	if c.Spec.Dev.Hostname == "" {
		return fmt.Errorf("service config: 'spec.dev.hostname' is required")
	}
	if !validHostname(c.Spec.Dev.Hostname) {
		return fmt.Errorf("service config: invalid 'spec.dev.hostname' %q", c.Spec.Dev.Hostname)
	}

	if w := c.Spec.Workload; w != nil {
		hasImage := w.Image != ""
		hasBuild := w.Build != nil
		if hasImage == hasBuild {
			return fmt.Errorf("service config: 'spec.workload' must set exactly one of 'image' or 'build'")
		}
		if hasBuild && w.Build.Context == "" {
			return fmt.Errorf("service config: 'spec.workload.build.context' is required")
		}
		if w.Port < 1 || w.Port > 65535 {
			return fmt.Errorf("service config: 'spec.workload.port' %d out of range (must be 1-65535)", w.Port)
		}
	}

	seen := make(map[string]struct{}, len(c.Spec.Dependencies))
	for i, d := range c.Spec.Dependencies {
		// Identify the dependency by name when present, else by position.
		who := d.Name
		if who == "" {
			who = fmt.Sprintf("dependency #%d", i+1)
		}
		if d.Name == "" {
			return fmt.Errorf("service config: %s is missing required 'name'", who)
		}
		if d.Engine == "" {
			return fmt.Errorf("service config: dependency %q is missing required 'engine'", d.Name)
		}
		if d.Port == 0 {
			return fmt.Errorf("service config: dependency %q is missing required 'port'", d.Name)
		}
		if _, dup := seen[d.Name]; dup {
			return fmt.Errorf("service config: duplicate dependency name %q", d.Name)
		}
		seen[d.Name] = struct{}{}
		if !engineRecognized(d.Engine) {
			return fmt.Errorf("service config: dependency %q has unrecognized engine %q (recognized engines: %s)",
				d.Name, d.Engine, strings.Join(recognizedEngines, ", "))
		}
		if (d.Migrations != "" || d.Seed != "") && d.Engine != "postgres" {
			return fmt.Errorf("service config: dependency %q declares migrations/seed but engine is %q (only postgres is supported)",
				d.Name, d.Engine)
		}
		if d.Port < 1 || d.Port > 65535 {
			return fmt.Errorf("service config: dependency %q has port %d out of range (must be 1-65535)", d.Name, d.Port)
		}
	}

	for i, r := range c.Spec.Routes {
		if r.Readiness == nil {
			continue
		}
		rd := r.Readiness
		if rd.Path == "" || rd.Path[0] != '/' {
			return fmt.Errorf("service config: spec.routes[%d].readiness.path must be a non-empty path starting with '/' (got %q)", i, rd.Path)
		}
		var timeout, interval time.Duration
		if rd.Timeout != "" {
			d, err := time.ParseDuration(rd.Timeout)
			if err != nil || d <= 0 {
				return fmt.Errorf("service config: spec.routes[%d].readiness.timeout %q is not a valid positive duration: %v", i, rd.Timeout, err)
			}
			timeout = d
		}
		if rd.Interval != "" {
			d, err := time.ParseDuration(rd.Interval)
			if err != nil || d <= 0 {
				return fmt.Errorf("service config: spec.routes[%d].readiness.interval %q is not a valid positive duration: %v", i, rd.Interval, err)
			}
			interval = d
		}
		if timeout > 0 && interval > 0 && timeout <= interval {
			return fmt.Errorf("service config: spec.routes[%d].readiness: timeout (%s) must be greater than interval (%s)", i, timeout, interval)
		}
	}
	return nil
}

// Project returns the service name from metadata.
func (c *ServiceConfig) Project() string {
	return c.Metadata.Name
}

// ToRoutes converts the service's declared routes into domain Route objects,
// mirroring ProjectConfig.ToRoutes (Source "project-file", Project metadata.name).
func (c *ServiceConfig) ToRoutes() ([]types.Route, error) {
	routes := make([]types.Route, 0, len(c.Spec.Routes))
	for _, entry := range c.Spec.Routes {
		routes = append(routes, types.Route{
			Host:       entry.Host,
			Upstream:   entry.Upstream,
			Protocol:   types.Protocol(entry.Protocol),
			BackendTLS: entry.BackendTLS,
			Project:    c.Metadata.Name,
			Source:     "project-file",
		})
	}
	return routes, nil
}

// Dependencies returns the service's declared runtime dependencies, satisfying
// DependencyDeclarer.
func (c *ServiceConfig) Dependencies() []Dependency {
	return c.Spec.Dependencies
}

// ClusterDedicated reports whether the service opted into a dedicated cluster
// (spec.cluster.dedicated), satisfying ClusterPreferrer (FR-010).
func (c *ServiceConfig) ClusterDedicated() bool {
	return c.Spec.Cluster.Dedicated
}

// Workload returns the service's declared deployable workload, or nil if none,
// satisfying WorkloadDeclarer (005).
func (c *ServiceConfig) Workload() *WorkloadSpec {
	return c.Spec.Workload
}

// enginePorts maps a recognized engine to its standard port.
var enginePorts = map[string]int{"postgres": 5432, "redis": 6379}

// DefaultedPort returns the declared port, falling back to the engine's standard
// port when unset. Returns 0 for an unrecognized engine.
func (d Dependency) DefaultedPort() int {
	if d.Port != 0 {
		return d.Port
	}
	return enginePorts[d.Engine]
}

// resolveMigrationPath resolves a project-relative path against projectDir,
// rejects paths that escape the project tree (relative segment starts with ".."),
// and checks that the resolved path exists. Returns the absolute path or an error.
func resolveMigrationPath(projectDir, rel, field, depName string) (string, error) {
	// The resolved path crosses a process boundary (CLI -> daemon, different
	// cwd), so it MUST be absolute. With the default `-f devedge.yaml`,
	// projectDir arrives as "." — anchor it to the CLI's cwd first.
	projectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return "", fmt.Errorf("service config: resolve project dir: %w", err)
	}
	abs := filepath.Join(projectDir, rel)
	// Reject paths that escape projectDir.
	relCheck, err := filepath.Rel(projectDir, abs)
	if err != nil || strings.HasPrefix(relCheck, "..") {
		return "", fmt.Errorf("service config: dependency %q %s path %q escapes the project directory",
			depName, field, rel)
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("service config: dependency %q %s path %q does not exist",
				depName, field, rel)
		}
		return "", fmt.Errorf("service config: dependency %q %s path %q: %w", depName, field, rel, err)
	}
	return abs, nil
}

// Migrations resolves declared migration/seed sources against projectDir
// (the directory containing devedge.yaml), validating existence; one entry per
// declaring dependency. Satisfies MigrationDeclarer.
func (c *ServiceConfig) Migrations(projectDir string) ([]DependencyMigrations, error) {
	var results []DependencyMigrations
	for _, d := range c.Spec.Dependencies {
		if d.Migrations == "" && d.Seed == "" {
			continue
		}
		dm := DependencyMigrations{Dependency: d.Name}

		if d.Migrations != "" {
			abs, err := resolveMigrationPath(projectDir, d.Migrations, "migrations", d.Name)
			if err != nil {
				return nil, err
			}
			info, err := os.Stat(abs)
			if err != nil || !info.IsDir() {
				return nil, fmt.Errorf("service config: dependency %q migrations path %q is not a directory",
					d.Name, d.Migrations)
			}
			// Require at least one *.up.sql file.
			matches, err := filepath.Glob(filepath.Join(abs, "*.up.sql"))
			if err != nil {
				return nil, fmt.Errorf("service config: dependency %q migrations glob: %w", d.Name, err)
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("service config: dependency %q migrations dir %q is declared but empty (no *.up.sql files found)",
					d.Name, d.Migrations)
			}
			dm.Dir = abs
		}

		if d.Seed != "" {
			abs, err := resolveMigrationPath(projectDir, d.Seed, "seed", d.Name)
			if err != nil {
				return nil, err
			}
			dm.Seed = abs
		}

		results = append(results, dm)
	}
	return results, nil
}
