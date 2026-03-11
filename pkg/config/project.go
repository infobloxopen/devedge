// Package config handles parsing of devedge project configuration files.
//
// The configuration follows the Kubernetes resource API structure:
//
//	apiVersion: devedge.infoblox.dev/v1alpha1
//	kind: Config
//	metadata:
//	  name: foo
//	spec:
//	  defaults:
//	    ttl: 30s
//	    tls: true
//	  routes:
//	    - host: web.foo.dev.test
//	      upstream: http://127.0.0.1:3000
package config

import (
	"fmt"
	"os"
	"time"

	"github.com/infobloxopen/devedge/pkg/types"
	"gopkg.in/yaml.v3"
)

const (
	// APIVersion is the current config API version.
	APIVersion = "devedge.infoblox.dev/v1alpha1"
	// Kind is the resource kind for project configs.
	Kind = "Config"
)

// ProjectConfig represents a devedge.yaml project file following the
// Kubernetes resource API structure.
type ProjectConfig struct {
	APIVersion string          `yaml:"apiVersion"`
	Kind       string          `yaml:"kind"`
	Metadata   ObjectMeta      `yaml:"metadata"`
	Spec       ProjectSpec     `yaml:"spec"`
}

// ObjectMeta follows the Kubernetes metadata convention.
type ObjectMeta struct {
	Name        string            `yaml:"name"`
	Namespace   string            `yaml:"namespace,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

// ProjectSpec holds the desired state for the project's edge configuration.
type ProjectSpec struct {
	Defaults RouteDefaults `yaml:"defaults,omitempty"`
	Routes   []RouteEntry  `yaml:"routes"`
}

// RouteDefaults holds default values applied to all routes in the project.
type RouteDefaults struct {
	TTL string `yaml:"ttl,omitempty"`
	TLS bool   `yaml:"tls,omitempty"`
}

// RouteEntry represents a single route in the project config.
type RouteEntry struct {
	Host       string `yaml:"host"`
	Upstream   string `yaml:"upstream"`
	Protocol   string `yaml:"protocol,omitempty"`   // "http" (default) or "tcp"
	BackendTLS bool   `yaml:"backendTLS,omitempty"` // TLS to upstream
	Mode       string `yaml:"mode,omitempty"`
}

// LoadProject reads and parses a devedge.yaml file.
func LoadProject(path string) (*ProjectConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read project config: %w", err)
	}
	return ParseProject(data)
}

// ParseProject parses devedge.yaml content.
func ParseProject(data []byte) (*ProjectConfig, error) {
	var cfg ProjectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse project config: %w", err)
	}

	// Validate required fields.
	if cfg.APIVersion == "" {
		return nil, fmt.Errorf("project config: 'apiVersion' is required (use %q)", APIVersion)
	}
	if cfg.Kind == "" {
		return nil, fmt.Errorf("project config: 'kind' is required (use %q)", Kind)
	}
	if cfg.Kind != Kind {
		return nil, fmt.Errorf("project config: unsupported kind %q (expected %q)", cfg.Kind, Kind)
	}
	if cfg.Metadata.Name == "" {
		return nil, fmt.Errorf("project config: 'metadata.name' is required")
	}

	return &cfg, nil
}

// Project returns the project name from metadata.
func (c *ProjectConfig) Project() string {
	return c.Metadata.Name
}

// ToRoutes converts the project config into domain Route objects.
func (c *ProjectConfig) ToRoutes() ([]types.Route, error) {
	var ttl time.Duration
	if c.Spec.Defaults.TTL != "" {
		var err error
		ttl, err = time.ParseDuration(c.Spec.Defaults.TTL)
		if err != nil {
			return nil, fmt.Errorf("parse default TTL %q: %w", c.Spec.Defaults.TTL, err)
		}
	}

	routes := make([]types.Route, 0, len(c.Spec.Routes))
	for _, entry := range c.Spec.Routes {
		routes = append(routes, types.Route{
			Host:       entry.Host,
			Upstream:   entry.Upstream,
			Protocol:   types.Protocol(entry.Protocol),
			BackendTLS: entry.BackendTLS,
			Project:    c.Metadata.Name,
			Source:     "project-file",
			TTL:        ttl,
		})
	}
	return routes, nil
}
