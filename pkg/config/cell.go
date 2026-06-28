package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/infobloxopen/devedge/pkg/types"
	"gopkg.in/yaml.v3"
)

// KindCell is the resource kind for a cell deployment descriptor.
const KindCell = "Cell"

// Cell represents a devedge cell deployment: a version-pinned instance of a
// service running on one named cell, used with the cells routing table to
// direct a subset of tenants to the new instance.
type Cell struct {
	APIVersion string     `yaml:"apiVersion"`
	Kind       string     `yaml:"kind"`
	Metadata   ObjectMeta `yaml:"metadata"`
	Spec       CellSpec   `yaml:"spec"`
}

// CellSpec describes the cell deployment.
type CellSpec struct {
	// Service is the service name (matches the Helm release base name).
	Service string `yaml:"service"`
	// Image is the full container image reference (e.g. "registry/svc:v1.2.0").
	// Takes precedence over Version when both are set.
	Image string `yaml:"image,omitempty"`
	// Version is a short version tag appended to the service's base image.
	// Ignored when Image is set.
	Version string `yaml:"version,omitempty"`
	// Replicas is the desired replica count (default 1).
	Replicas int `yaml:"replicas,omitempty"`
	// Cell is the cell ID this deployment belongs to.
	Cell string `yaml:"cell"`
	// DefaultCell is the fail-safe cell ID (used by down --purge-routes to
	// revert tenants; set on the routing table, not in the cluster).
	DefaultCell string `yaml:"defaultCell,omitempty"`
	// ControllerClass shards move orchestration like an IngressClass. Empty
	// binds to the default class.
	ControllerClass string `yaml:"controllerClass,omitempty"`
}

// Project satisfies Resource; returns the metadata name.
func (c *Cell) Project() string { return c.Metadata.Name }

// ToRoutes satisfies Resource; a Cell has no edge routes (it is a cluster
// workload descriptor, not a route declaration).
func (c *Cell) ToRoutes() ([]types.Route, error) { return nil, nil }

// ParseCell strictly decodes a `kind: Cell` document (unknown fields rejected)
// and validates required fields.
func ParseCell(data []byte) (*Cell, error) {
	var c Cell
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("cell config: document is empty")
		}
		return nil, fmt.Errorf("parse cell config: %w", err)
	}
	if c.APIVersion == "" {
		return nil, fmt.Errorf("cell config: 'apiVersion' is required (use %q)", APIVersion)
	}
	if c.APIVersion != APIVersion {
		return nil, fmt.Errorf("cell config: unsupported apiVersion %q (use %q)", c.APIVersion, APIVersion)
	}
	if c.Kind != KindCell {
		return nil, fmt.Errorf("cell config: unsupported kind %q (expected %q)", c.Kind, KindCell)
	}
	if c.Metadata.Name == "" {
		return nil, fmt.Errorf("cell config: 'metadata.name' is required")
	}
	if c.Spec.Service == "" {
		return nil, fmt.Errorf("cell config: 'spec.service' is required")
	}
	if c.Spec.Cell == "" {
		return nil, fmt.Errorf("cell config: 'spec.cell' is required")
	}
	if c.Spec.Replicas == 0 {
		c.Spec.Replicas = 1
	}
	return &c, nil
}

// MarshalCell serializes a Cell back to YAML.
func MarshalCell(c *Cell) ([]byte, error) {
	data, err := yaml.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("marshal cell config: %w", err)
	}
	return data, nil
}
