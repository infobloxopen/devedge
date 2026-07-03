// Package compose implements the devedge side of WS-012 Phase 4: turning a
// `kind: Composition` resource into a STATIC composed-suite binary built on
// devedge-sdk's servicekit host.
//
// The model: a composition lists member service MODULES (importable Go packages
// each exposing a zero-arg `Module() servicekit.Module` constructor, proposal
// §5.2). `de compose build` generates a cmd/<name>/main.go that IMPORTS those
// packages and calls servicekit.Run(HostConfig{Modules: ...}), a go.mod for the
// composed binary, and a composition.lock pinning the member versions. The
// composition is static — the generated Go imports the modules; there are NO Go
// plugins (proposal §10-B, non-negotiable).
//
// This package owns the pure, I/O-free rendering (main.go / go.mod / lock) and
// the module-ref parsing so it is trivially testable; the cobra command in
// cmd/de/compose.go drives it.
package compose

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/infobloxopen/devedge/pkg/config"
)

// ModuleRef is a resolved member-module reference: its import path, its pinned
// version (empty if the composition left it floating), and the Go package alias
// the generated main.go imports it under.
type ModuleRef struct {
	// Name is the module's short name within the composition (ModuleEntry.Name).
	Name string
	// ImportPath is the Go import path of the module package (no @version).
	ImportPath string
	// Version is the pinned version ("@v0.4.1" suffix), or "" when unpinned.
	Version string
	// Alias is the import alias the generated main.go uses to avoid collisions
	// (derived from Name; always a valid, unique Go identifier).
	Alias string
	// Path is the local checkout directory a member was added with (`de compose
	// add --path`), relative to the composition file (or absolute). Empty for a
	// published member. When set, `de compose build` requires the member at a
	// local pseudo-version behind a `replace` and derives the SDK pin from it.
	Path string
}

// ParseModuleRef splits a "path@version" module reference into its path and
// version. A bare path (no "@") yields an empty version. The path must be
// non-empty.
func ParseModuleRef(ref string) (path, version string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", fmt.Errorf("empty module reference")
	}
	if at := strings.LastIndex(ref, "@"); at >= 0 {
		path = ref[:at]
		version = ref[at+1:]
	} else {
		path = ref
	}
	if path == "" {
		return "", "", fmt.Errorf("module reference %q has no import path", ref)
	}
	return path, version, nil
}

// goIdentRe matches characters NOT allowed in a Go identifier; we replace runs of
// them with "_".
var goIdentRe = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

// aliasFor derives a valid, lower-cased Go identifier from a module name, using
// taken to guarantee uniqueness across the composition (a numeric suffix is
// appended on collision). A leading digit is prefixed with "m".
func aliasFor(name string, taken map[string]struct{}) string {
	base := strings.ToLower(goIdentRe.ReplaceAllString(name, "_"))
	base = strings.Trim(base, "_")
	if base == "" {
		base = "mod"
	}
	if base[0] >= '0' && base[0] <= '9' {
		base = "m" + base
	}
	alias := base
	for i := 2; ; i++ {
		if _, dup := taken[alias]; !dup {
			break
		}
		alias = fmt.Sprintf("%s%d", base, i)
	}
	taken[alias] = struct{}{}
	return alias
}

// ResolveModuleRefs turns a composition's module entries into ModuleRefs with
// unique import aliases, ready for code generation.
func ResolveModuleRefs(c *config.Composition) ([]ModuleRef, error) {
	taken := make(map[string]struct{}, len(c.Spec.Modules))
	refs := make([]ModuleRef, 0, len(c.Spec.Modules))
	for _, m := range c.Spec.Modules {
		path, version, err := ParseModuleRef(m.Module)
		if err != nil {
			return nil, fmt.Errorf("module %q: %w", m.Name, err)
		}
		refs = append(refs, ModuleRef{
			Name:       m.Name,
			ImportPath: path,
			Version:    version,
			Alias:      aliasFor(m.Name, taken),
			Path:       m.Path,
		})
	}
	return refs, nil
}
