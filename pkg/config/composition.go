package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/infobloxopen/devedge/pkg/types"
	"gopkg.in/yaml.v3"
)

// KindComposition is the resource kind for a composed "suite" binary: a set of
// importable service modules (WS-012) composed into ONE host process. It is the
// devedge surface over servicekit.Run (devedge-sdk): `de compose build` turns a
// Composition into a generated cmd/<name>/main.go that imports the member modules
// and calls servicekit.Run(HostConfig{...}). Static composition only — the
// generated binary imports the modules; there are no Go plugins (proposal §10-B).
const KindComposition = "Composition"

// Composition represents a devedge.yaml `kind: Composition` document. Like
// ServiceConfig it follows the Kubernetes resource envelope and is decoded
// strictly (unknown fields rejected). It maps directly onto a
// servicekit.HostConfig: spec.runtime -> GRPCAddr/HTTPAddr, spec.database ->
// DatabaseConfig, spec.modules -> Modules + per-module FailurePolicies +
// ConfigDescriptor.Prefix + DatabaseDescriptor.Schema.
type Composition struct {
	APIVersion string          `yaml:"apiVersion"`
	Kind       string          `yaml:"kind"`
	Metadata   ObjectMeta      `yaml:"metadata"`
	Spec       CompositionSpec `yaml:"spec"`
}

// CompositionSpec is the desired state for a composed host.
type CompositionSpec struct {
	// Runtime is the host's process-level shape (mode + listen addresses).
	Runtime RuntimeSpec `yaml:"runtime,omitempty"`
	// Database is the SHARED database the composed host's modules namespace
	// themselves within (proposal §5.4). Optional — absent means no shared DB.
	Database *CompositionDatabase `yaml:"database,omitempty"`
	// Modules are the member service modules to compose into this host. At least
	// two is the composed-suite case the resource exists for; one is allowed
	// (a standalone host described declaratively).
	Modules []ModuleEntry `yaml:"modules"`
}

// RuntimeSpec is the composed host's process topology + listen addresses. It maps
// onto servicekit.HostConfig.{GRPCAddr,HTTPAddr}.
type RuntimeSpec struct {
	// Mode is the deploy topology hint: "single-binary" (default) composes all
	// modules into one host; multi-daemon/hybrid are P6 deploy choices. It does
	// not change the generated build (always one host) — it is a deploy hint.
	Mode string `yaml:"mode,omitempty"`
	// GRPC is the shared gRPC listen address (e.g. ":9090").
	GRPC string `yaml:"grpc,omitempty"`
	// HTTP is the shared HTTP gateway address (e.g. ":8080"). Empty disables it.
	HTTP string `yaml:"http,omitempty"`
}

// CompositionDatabase declares the shared database all member modules namespace
// themselves within. Maps onto servicekit.DatabaseConfig.
type CompositionDatabase struct {
	// Engine is the shared DB engine (e.g. "postgres").
	Engine string `yaml:"engine"`
	// DSNRef is the env var name carrying the shared DSN (e.g. "DATABASE_URL").
	DSNRef string `yaml:"dsnRef,omitempty"`
	// Isolation is the composition's default module-namespacing policy
	// (schema-required | schema-preferred | prefix-required | dedicated-required).
	// Empty defers to the host default (schema-preferred).
	Isolation string `yaml:"isolation,omitempty"`
}

// ModuleEntry is one member module of a composition: its import path + version,
// its config prefix, its DB schema, its failure policy, and any HTTP routes it
// serves through the edge.
type ModuleEntry struct {
	// Name is the module's short name within the composition (e.g. "orders").
	// It defaults the configPrefix and the DB schema when those are unset.
	Name string `yaml:"name"`
	// Module is the Go import path of the module package, optionally suffixed
	// with "@<version>" (e.g. "github.com/acme/orders/module@v0.4.1"). The
	// version pins the member in composition.lock.
	Module string `yaml:"module"`
	// ConfigPrefix is the module's config namespace (proposal §5.6). Defaults to
	// Name when empty.
	ConfigPrefix string `yaml:"configPrefix,omitempty"`
	// Database is the per-module DB namespace override (schema). Optional.
	Database *ModuleDatabase `yaml:"database,omitempty"`
	// FailurePolicy is the module's failure posture in the composed host
	// ("fail-host" | "degraded"). Empty defers to the host default. Maps onto
	// servicekit.HostConfig.FailurePolicies[name] (proposal §5.9).
	FailurePolicy string `yaml:"failurePolicy,omitempty"`
	// Routes are the module's HTTP routes through the edge (host -> upstream),
	// aggregated into the composition's ToRoutes(). Optional.
	Routes []RouteEntry `yaml:"routes,omitempty"`
}

// ModuleDatabase is a per-module DB namespace override.
type ModuleDatabase struct {
	// Schema is the Postgres schema the module is namespaced into (proposal §5.4).
	// Defaults to the module Name when empty.
	Schema string `yaml:"schema,omitempty"`
}

// recognizedIsolations is the set of database.isolation policies a composition
// may declare, in a stable order for error messages (proposal §5.4).
var recognizedIsolations = []string{
	"schema-required", "schema-preferred", "prefix-required", "dedicated-required",
}

func isolationRecognized(p string) bool {
	for _, i := range recognizedIsolations {
		if i == p {
			return true
		}
	}
	return false
}

// recognizedFailurePolicies is the set of per-module failurePolicy values, in a
// stable order for error messages (proposal §5.9).
var recognizedFailurePolicies = []string{"fail-host", "degraded"}

func failurePolicyRecognized(p string) bool {
	for _, f := range recognizedFailurePolicies {
		if f == p {
			return true
		}
	}
	return false
}

// ParseComposition strictly decodes a `kind: Composition` document (unknown
// fields are rejected) and validates it as a COMPLETE composition — including the
// at-least-one-module rule a buildable/runnable composition must satisfy.
func ParseComposition(data []byte) (*Composition, error) {
	c, err := decodeComposition(data)
	if err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// ParseCompositionForEdit decodes + validates a composition for in-place editing
// (`de compose add`/`remove`), tolerating an as-yet-empty member set so the FIRST
// member can be added to a freshly-scaffolded (zero-module) file. Every other
// spec rule (envelope, database, per-module shape) is still enforced.
func ParseCompositionForEdit(data []byte) (*Composition, error) {
	c, err := decodeComposition(data)
	if err != nil {
		return nil, err
	}
	if err := c.validateSpec(false); err != nil {
		return nil, err
	}
	return c, nil
}

// decodeComposition strictly decodes the YAML + checks the resource envelope,
// without spec-level validation (the callers choose strict vs edit-tolerant).
func decodeComposition(data []byte) (*Composition, error) {
	var c Composition
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("composition config: document is empty")
		}
		return nil, fmt.Errorf("parse composition config: %w", err)
	}

	if c.APIVersion == "" {
		return nil, fmt.Errorf("composition config: 'apiVersion' is required (use %q)", APIVersion)
	}
	if c.APIVersion != APIVersion {
		return nil, fmt.Errorf("composition config: unsupported apiVersion %q (use %q)", c.APIVersion, APIVersion)
	}
	if c.Kind != KindComposition {
		// Dispatch already guarantees this; guard for direct callers.
		return nil, fmt.Errorf("composition config: unsupported kind %q (expected %q)", c.Kind, KindComposition)
	}
	if c.Metadata.Name == "" {
		return nil, fmt.Errorf("composition config: 'metadata.name' is required")
	}
	return &c, nil
}

// Validate checks the composition spec as a COMPLETE composition (at least one
// module). Each failure names the specific offending field so the error is
// actionable.
func (c *Composition) Validate() error {
	return c.validateSpec(true)
}

// validateSpec validates the spec; requireModules toggles the at-least-one-module
// rule (true for a complete composition, false for in-place editing).
func (c *Composition) validateSpec(requireModules bool) error {
	if d := c.Spec.Database; d != nil {
		if d.Engine == "" {
			return fmt.Errorf("composition config: 'spec.database.engine' is required when a database is declared")
		}
		if d.Isolation != "" && !isolationRecognized(d.Isolation) {
			return fmt.Errorf("composition config: spec.database.isolation %q is not recognized (recognized: %s)",
				d.Isolation, strings.Join(recognizedIsolations, ", "))
		}
	}

	if requireModules && len(c.Spec.Modules) == 0 {
		return fmt.Errorf("composition config: 'spec.modules' must declare at least one module")
	}

	seenName := make(map[string]struct{}, len(c.Spec.Modules))
	for i, m := range c.Spec.Modules {
		who := m.Name
		if who == "" {
			who = fmt.Sprintf("module #%d", i+1)
		}
		if m.Name == "" {
			return fmt.Errorf("composition config: %s is missing required 'name'", who)
		}
		if m.Module == "" {
			return fmt.Errorf("composition config: module %q is missing required 'module' (import path)", m.Name)
		}
		if _, dup := seenName[m.Name]; dup {
			return fmt.Errorf("composition config: duplicate module name %q", m.Name)
		}
		seenName[m.Name] = struct{}{}
		if m.FailurePolicy != "" && !failurePolicyRecognized(m.FailurePolicy) {
			return fmt.Errorf("composition config: module %q failurePolicy %q is not recognized (recognized: %s)",
				m.Name, m.FailurePolicy, strings.Join(recognizedFailurePolicies, ", "))
		}
	}
	return nil
}

// Project returns the composition name from metadata, satisfying Resource.
func (c *Composition) Project() string {
	return c.Metadata.Name
}

// ToRoutes aggregates every member module's declared routes into domain Route
// objects, satisfying Resource. The composition is the project the routes belong
// to (one composed host serves all members' routes — proposal §5.8).
func (c *Composition) ToRoutes() ([]types.Route, error) {
	var routes []types.Route
	for _, m := range c.Spec.Modules {
		for _, entry := range m.Routes {
			routes = append(routes, types.Route{
				Host:        entry.Host,
				Upstream:    entry.Upstream,
				Protocol:    types.Protocol(entry.Protocol),
				BackendTLS:  entry.BackendTLS,
				Path:        entry.Path,
				StripPrefix: entry.StripPrefix,
				Project:     c.Metadata.Name,
				Source:      "project-file",
			})
		}
	}
	return routes, nil
}

// Dependencies returns the composition's shared database as a single dependency,
// satisfying DependencyDeclarer so `de compose up` provisions it once and all
// member modules namespace themselves within it (proposal §5.4). A composition
// with no shared database declares no dependencies.
func (c *Composition) Dependencies() []Dependency {
	d := c.Spec.Database
	if d == nil {
		return nil
	}
	dep := Dependency{
		Name:   "db",
		Engine: d.Engine,
		Port:   enginePorts[d.Engine],
	}
	return []Dependency{dep}
}

// EffectiveConfigPrefix returns the module's config prefix, defaulting to Name.
func (m ModuleEntry) EffectiveConfigPrefix() string {
	if m.ConfigPrefix != "" {
		return m.ConfigPrefix
	}
	return m.Name
}

// EffectiveSchema returns the module's DB schema, defaulting to Name.
func (m ModuleEntry) EffectiveSchema() string {
	if m.Database != nil && m.Database.Schema != "" {
		return m.Database.Schema
	}
	return m.Name
}

// MarshalComposition serializes a composition back to YAML (used by
// `de compose add`/`remove` to rewrite the file). It round-trips through
// ParseComposition's strict shape.
func MarshalComposition(c *Composition) ([]byte, error) {
	data, err := yaml.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("marshal composition config: %w", err)
	}
	return data, nil
}
