// Package config handles parsing of devedge project configuration files.
package config

import (
	"fmt"
	"os"
	"time"

	"github.com/infobloxopen/devedge/pkg/types"
	"gopkg.in/yaml.v3"
)

// ProjectConfig represents a devedge.yaml project file.
type ProjectConfig struct {
	Version  int             `yaml:"version"`
	Project  string          `yaml:"project"`
	Defaults ProjectDefaults `yaml:"defaults"`
	Routes   []RouteEntry    `yaml:"routes"`
}

// ProjectDefaults holds default values for routes in the project.
type ProjectDefaults struct {
	TTL string `yaml:"ttl"`
	TLS bool   `yaml:"tls"`
}

// RouteEntry represents a single route in the project config.
type RouteEntry struct {
	Host     string `yaml:"host"`
	Upstream string `yaml:"upstream"`
	Mode     string `yaml:"mode,omitempty"`
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
	if cfg.Project == "" {
		return nil, fmt.Errorf("project config: 'project' field is required")
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	return &cfg, nil
}

// ToRoutes converts the project config into domain Route objects.
func (c *ProjectConfig) ToRoutes() ([]types.Route, error) {
	var ttl time.Duration
	if c.Defaults.TTL != "" {
		var err error
		ttl, err = time.ParseDuration(c.Defaults.TTL)
		if err != nil {
			return nil, fmt.Errorf("parse default TTL %q: %w", c.Defaults.TTL, err)
		}
	}

	routes := make([]types.Route, 0, len(c.Routes))
	for _, entry := range c.Routes {
		routes = append(routes, types.Route{
			Host:     entry.Host,
			Upstream: entry.Upstream,
			Project:  c.Project,
			Source:   "project-file",
			TTL:      ttl,
		})
	}
	return routes, nil
}
