package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

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
	Dev          ServiceDev   `yaml:"dev"`
	Dependencies []Dependency `yaml:"dependencies,omitempty"`
	Routes       []RouteEntry `yaml:"routes,omitempty"`
}

// ServiceDev describes the service's development surface.
type ServiceDev struct {
	Hostname string `yaml:"hostname"`
}

// Dependency is a runtime dependency declared by a service. It is validated and
// reported here but started by a later runtime feature.
type Dependency struct {
	Name    string `yaml:"name"`
	Engine  string `yaml:"engine"`
	Version string `yaml:"version,omitempty"`
	Port    int    `yaml:"port"`
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
		if d.Port < 1 || d.Port > 65535 {
			return fmt.Errorf("service config: dependency %q has port %d out of range (must be 1-65535)", d.Name, d.Port)
		}
	}
	return nil
}

// FormatDependencies renders a human-readable report of a service's declared
// runtime dependencies for `de project up`. It returns "" when there are none.
// Dependencies are validated but not started by this feature, so the report
// states that explicitly.
func FormatDependencies(deps []Dependency) string {
	if len(deps) == 0 {
		return ""
	}
	names := make([]string, len(deps))
	for i, d := range deps {
		names[i] = d.Name
	}
	return fmt.Sprintf("%d dependenc%s declared: %s\nstarting dependencies is not yet supported",
		len(deps), plural(len(deps)), strings.Join(names, ", "))
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
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
