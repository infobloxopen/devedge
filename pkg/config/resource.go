package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/infobloxopen/devedge/pkg/types"
	"gopkg.in/yaml.v3"
)

// This file is the kind-dispatch extension point for devedge project files.
//
// A project file carries a Kubernetes-style envelope (apiVersion + kind).
// ParseResource peeks at that envelope and routes the document to the decoder
// for its kind, returning a Resource the CLI can treat uniformly. Adding a new
// kind is additive: implement its decoder + the Resource interface and add one
// case (and one supportedKinds entry) below — nothing else changes.
//
//	Config  -> ParseProject  (existing, lenient decode; back-compat surface)
//	Service -> ParseService  (strict decode; typo protection)

// KindService is the resource kind for service configs. (Kind == "Config" for
// the existing project config, declared in project.go.)
const KindService = "Service"

// supportedKinds is the stable, ordered set of kinds ParseResource accepts.
// It backs the "supported kinds" listing in dispatch error messages; keep it in
// sync with the switch in ParseResource.
var supportedKinds = []string{Kind, KindService}

// Resource is the CLI's polymorphic view over any project-file kind. Both
// ProjectConfig (kind: Config) and ServiceConfig (kind: Service) satisfy it.
type Resource interface {
	// Project returns the project name (metadata.name) the routes belong to.
	Project() string
	// ToRoutes converts the resource into domain Route objects to register.
	ToRoutes() ([]types.Route, error)
}

// DependencyDeclarer is implemented by kinds that declare runtime dependencies.
// Service implements it; Config does not. The CLI uses a type assertion to
// decide whether to report dependencies.
type DependencyDeclarer interface {
	Dependencies() []Dependency
}

// ClusterPreferrer is implemented by kinds that can opt into a dedicated cluster
// (spec.cluster.dedicated). Service implements it; Config does not (a non-Service
// resource resolves to the shared cluster). The CLI type-asserts to read it.
type ClusterPreferrer interface {
	ClusterDedicated() bool
}

// typeMeta is the apiVersion/kind envelope, decoded first to choose a kind
// decoder without committing to a concrete type.
type typeMeta struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
}

// ParseResource reads the apiVersion/kind envelope and decodes the document
// with the matching kind's decoder. Config is decoded by the existing (lenient)
// ParseProject; Service is decoded strictly. An unknown or missing kind, or a
// missing apiVersion, is a clear actionable error.
func ParseResource(data []byte) (Resource, error) {
	var tm typeMeta
	if err := yaml.Unmarshal(data, &tm); err != nil {
		return nil, fmt.Errorf("parse resource config: %w", err)
	}
	if tm.APIVersion == "" {
		return nil, fmt.Errorf("resource config: 'apiVersion' is required (use %q)", APIVersion)
	}
	if tm.Kind == "" {
		return nil, fmt.Errorf("resource config: 'kind' is required (supported kinds: %s)",
			strings.Join(supportedKinds, ", "))
	}

	switch tm.Kind {
	case Kind: // Config — delegate unchanged to the existing decoder.
		cfg, err := ParseProject(data)
		if err != nil {
			return nil, err
		}
		return cfg, nil
	case KindService:
		svc, err := ParseService(data)
		if err != nil {
			return nil, err
		}
		return svc, nil
	default:
		return nil, fmt.Errorf("resource config: unsupported kind %q (supported kinds: %s)",
			tm.Kind, strings.Join(supportedKinds, ", "))
	}
}

// LoadResource reads a project file from disk and parses it via ParseResource.
func LoadResource(path string) (Resource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read resource config: %w", err)
	}
	return ParseResource(data)
}
