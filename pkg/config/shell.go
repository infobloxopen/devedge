package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/infobloxopen/devedge/pkg/types"
	"gopkg.in/yaml.v3"
)

// KindShell is the resource kind for a micro-frontend shell topology (WS-018).
//
// A Shell describes a single-page-app SHELL (single-spa root-config) that fronts
// many micro-frontends (uFEs). It renders down to the same edge routes + import
// map every other kind produces, so `de project up -f shell.yaml` brings it up
// through the existing path with no new command. The rendered routes exploit the
// path-prefix routing added in Phase A (types.Route Path/StripPrefix):
//
//   - the shell FQDN's catch-all serves the root-config and every non-/api path;
//   - the same origin's /api prefix (method 1) strips to the backend;
//   - a simulated CDN host serves each uFE's bundle under /<route>/, strip-prefixed.
//
// The uFEs themselves are HASH-routed (<host>/#<route>) by single-spa in the
// browser, so they do NOT need edge routes — only their CDN asset paths do.
const KindShell = "Shell"

// apiMethodSameOrigin (1) fronts the backend at a same-origin path prefix
// (default /api); apiMethodPerService (2) fronts each service at its own FQDN.
const (
	apiMethodSameOrigin = 1
	apiMethodPerService = 2

	// defaultAPIPrefix is the method-1 same-origin API path prefix used when
	// spec.api.prefix is omitted.
	defaultAPIPrefix = "/api"
)

// Shell represents a devedge `kind: Shell` document. Like the other kinds it
// follows the Kubernetes resource envelope and is decoded strictly (unknown
// fields rejected).
type Shell struct {
	APIVersion string     `yaml:"apiVersion"`
	Kind       string     `yaml:"kind"`
	Metadata   ObjectMeta `yaml:"metadata"`
	Spec       ShellSpec  `yaml:"spec"`
}

// ShellSpec is the desired state for a micro-frontend shell topology.
type ShellSpec struct {
	// Host is the shell FQDN. The browser loads the single-spa root-config here,
	// and every non-/api path on this host is served by the shell.
	Host string `yaml:"host"`
	// ShellUpstream is where the root-config/shell is served from (a dev server
	// or a static file server).
	ShellUpstream string `yaml:"shellUpstream"`
	// CDN declares the simulated CDN host that serves uFE bundles.
	CDN ShellCDN `yaml:"cdn"`
	// API declares how the shell reaches its backend API(s).
	API ShellAPI `yaml:"api"`
	// UFEs are the micro-frontends composed into the shell. Non-empty.
	UFEs []ShellUFE `yaml:"ufes"`
}

// ShellCDN declares the simulated CDN host for uFE bundles.
type ShellCDN struct {
	// Host is the CDN FQDN (e.g. "cdn.dev.test"). Each uFE's assets load from
	// https://<host>/<route>/.
	Host string `yaml:"host"`
}

// ShellAPI declares the shell's API topology. Method 1 fronts the backend at a
// same-origin path prefix; method 2 fronts each service at its own FQDN.
type ShellAPI struct {
	// Method selects the topology: 1 = same-origin /api prefix, 2 = per-service
	// FQDNs.
	Method int `yaml:"method"`
	// Prefix is the same-origin API path prefix (method 1). Defaults to /api.
	Prefix string `yaml:"prefix,omitempty"`
	// Upstream is the backend behind the prefix (method 1).
	Upstream string `yaml:"upstream,omitempty"`
	// Services are the per-service API FQDNs (method 2).
	Services []ShellAPIService `yaml:"services,omitempty"`
}

// ShellAPIService is one per-service API FQDN (method 2).
type ShellAPIService struct {
	// Host is the service's API FQDN (e.g. "api.notesapp.dev.test").
	Host string `yaml:"host"`
	// Upstream is the backend serving that FQDN.
	Upstream string `yaml:"upstream"`
}

// ShellUFE is one micro-frontend composed into the shell.
type ShellUFE struct {
	// ID is the import-map specifier / single-spa app name (e.g. "notes-ufe").
	ID string `yaml:"id"`
	// Route is the hash route the uFE mounts at (<host>/#<route>) and the CDN
	// path segment its bundle loads from (https://<cdn>/<route>/).
	Route string `yaml:"route"`
	// Upstream is the dev server serving this uFE's bundle.
	Upstream string `yaml:"upstream"`
}

// ParseShell strictly decodes a `kind: Shell` document (unknown fields rejected)
// and validates it as a complete shell topology.
func ParseShell(data []byte) (*Shell, error) {
	var s Shell
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("shell config: document is empty")
		}
		return nil, fmt.Errorf("parse shell config: %w", err)
	}

	if s.APIVersion == "" {
		return nil, fmt.Errorf("shell config: 'apiVersion' is required (use %q)", APIVersion)
	}
	if s.APIVersion != APIVersion {
		return nil, fmt.Errorf("shell config: unsupported apiVersion %q (use %q)", s.APIVersion, APIVersion)
	}
	if s.Kind != KindShell {
		// Dispatch already guarantees this; guard for direct callers.
		return nil, fmt.Errorf("shell config: unsupported kind %q (expected %q)", s.Kind, KindShell)
	}
	if s.Metadata.Name == "" {
		return nil, fmt.Errorf("shell config: 'metadata.name' is required")
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Validate checks the shell spec. Each failure names the specific offending
// field so the error is actionable. It also defaults the method-1 API prefix.
func (s *Shell) Validate() error {
	if s.Spec.Host == "" {
		return fmt.Errorf("shell config: 'spec.host' is required")
	}
	if s.Spec.ShellUpstream == "" {
		return fmt.Errorf("shell config: 'spec.shellUpstream' is required")
	}
	if s.Spec.CDN.Host == "" {
		return fmt.Errorf("shell config: 'spec.cdn.host' is required")
	}

	if len(s.Spec.UFEs) == 0 {
		return fmt.Errorf("shell config: 'spec.ufes' must declare at least one micro-frontend")
	}
	seenID := make(map[string]struct{}, len(s.Spec.UFEs))
	seenRoute := make(map[string]struct{}, len(s.Spec.UFEs))
	for i, u := range s.Spec.UFEs {
		who := u.ID
		if who == "" {
			who = fmt.Sprintf("ufe #%d", i+1)
		}
		if u.ID == "" {
			return fmt.Errorf("shell config: %s is missing required 'id'", who)
		}
		if u.Route == "" {
			return fmt.Errorf("shell config: ufe %q is missing required 'route'", u.ID)
		}
		if u.Upstream == "" {
			return fmt.Errorf("shell config: ufe %q is missing required 'upstream'", u.ID)
		}
		if _, dup := seenID[u.ID]; dup {
			return fmt.Errorf("shell config: duplicate ufe id %q", u.ID)
		}
		seenID[u.ID] = struct{}{}
		if _, dup := seenRoute[u.Route]; dup {
			return fmt.Errorf("shell config: duplicate ufe route %q", u.Route)
		}
		seenRoute[u.Route] = struct{}{}
	}

	switch s.Spec.API.Method {
	case apiMethodSameOrigin:
		if s.Spec.API.Prefix == "" {
			s.Spec.API.Prefix = defaultAPIPrefix
		}
		if s.Spec.API.Upstream == "" {
			return fmt.Errorf("shell config: 'spec.api.upstream' is required for api.method 1 (same-origin)")
		}
	case apiMethodPerService:
		if len(s.Spec.API.Services) == 0 {
			return fmt.Errorf("shell config: 'spec.api.services' must declare at least one service for api.method 2 (per-service FQDNs)")
		}
		for i, svc := range s.Spec.API.Services {
			if svc.Host == "" {
				return fmt.Errorf("shell config: spec.api.services #%d is missing required 'host'", i+1)
			}
			if svc.Upstream == "" {
				return fmt.Errorf("shell config: spec.api.services %q is missing required 'upstream'", svc.Host)
			}
		}
	default:
		return fmt.Errorf("shell config: 'spec.api.method' must be 1 (same-origin) or 2 (per-service), got %d", s.Spec.API.Method)
	}
	return nil
}

// Project returns the shell name from metadata, satisfying Resource. All rendered
// routes carry it as their Project so `de project down <name>` releases them as a
// group.
func (s *Shell) Project() string {
	return s.Metadata.Name
}

// ToRoutes renders the shell topology into domain Route objects, satisfying
// Resource:
//
//   - a path-less catch-all on the shell host serves the root-config and every
//     non-/api path (this is <host>/ and <host>/#<route> — hash routes never
//     reach the edge);
//   - method 1 adds a same-origin /api prefix route (strip-prefixed) to the
//     backend; method 2 adds one per-service host route per declared FQDN;
//   - both methods add one CDN route per uFE: /<route> on the CDN host,
//     strip-prefixed to the uFE's upstream, so cdn/<route>/main.js -> <up>/main.js.
//
// Every route carries Project == metadata.name so they can be released as a group.
func (s *Shell) ToRoutes() ([]types.Route, error) {
	proj := s.Metadata.Name
	routes := make([]types.Route, 0, 1+len(s.Spec.API.Services)+len(s.Spec.UFEs)+1)

	// Shell catch-all: path-less route on the shell host.
	routes = append(routes, types.Route{
		Host:     s.Spec.Host,
		Upstream: s.Spec.ShellUpstream,
		Project:  proj,
		Source:   "project-file",
	})

	// API routes.
	switch s.Spec.API.Method {
	case apiMethodSameOrigin:
		routes = append(routes, types.Route{
			Host:        s.Spec.Host,
			Path:        s.Spec.API.Prefix,
			StripPrefix: true,
			Upstream:    s.Spec.API.Upstream,
			Project:     proj,
			Source:      "project-file",
		})
	case apiMethodPerService:
		for _, svc := range s.Spec.API.Services {
			routes = append(routes, types.Route{
				Host:     svc.Host,
				Upstream: svc.Upstream,
				Project:  proj,
				Source:   "project-file",
			})
		}
	}

	// CDN routes: one per uFE, strip-prefixed under /<route>.
	for _, u := range s.Spec.UFEs {
		routes = append(routes, types.Route{
			Host:        s.Spec.CDN.Host,
			Path:        "/" + u.Route,
			StripPrefix: true,
			Upstream:    u.Upstream,
			Project:     proj,
			Source:      "project-file",
		})
	}

	return routes, nil
}

// ImportMap maps each uFE id to the base URL its bundle loads from
// (https://<cdn.host>/<route>/). A later phase points the shell's
// <script type="importmap"> at these entries. It is a pure function of the spec.
func (s *Shell) ImportMap() map[string]string {
	m := make(map[string]string, len(s.Spec.UFEs))
	for _, u := range s.Spec.UFEs {
		m[u.ID] = fmt.Sprintf("https://%s/%s/", s.Spec.CDN.Host, u.Route)
	}
	return m
}

// HashRoute pairs a uFE's single-spa app name (ID) with the hash route it mounts
// at, for a later phase to build the single-spa registration.
type HashRoute struct {
	// ID is the single-spa app name / import-map specifier.
	ID string
	// Route is the hash route the uFE mounts at (<host>/#<route>).
	Route string
}

// HashRoutes returns the shell's uFE hash routes in declaration order, so a later
// phase can build the single-spa registerApplication calls. Pure function of the
// spec.
func (s *Shell) HashRoutes() []HashRoute {
	hr := make([]HashRoute, 0, len(s.Spec.UFEs))
	for _, u := range s.Spec.UFEs {
		hr = append(hr, HashRoute{ID: u.ID, Route: u.Route})
	}
	return hr
}

// UpsertUFE adds ufe to the shell's roster, or updates the existing entry with
// the same ID in place. It is idempotent: adding a uFE whose ID is already a
// member never duplicates it — the existing entry's route + upstream are
// replaced. It reports whether an existing entry was updated (true) versus a new
// one appended (false), so callers can tell the user which happened.
//
// It mirrors `de compose add`'s member upsert: the config type owns the mutation
// so the CLI just loads, calls this, and re-marshals.
func (s *Shell) UpsertUFE(ufe ShellUFE) (updated bool) {
	for i := range s.Spec.UFEs {
		if s.Spec.UFEs[i].ID == ufe.ID {
			s.Spec.UFEs[i] = ufe
			return true
		}
	}
	s.Spec.UFEs = append(s.Spec.UFEs, ufe)
	return false
}

// MarshalShell serializes a Shell back to YAML.
func MarshalShell(s *Shell) ([]byte, error) {
	data, err := yaml.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("marshal shell config: %w", err)
	}
	return data, nil
}
