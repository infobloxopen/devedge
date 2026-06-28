package compose

import (
	"fmt"
	"sort"
	"strings"

	"github.com/infobloxopen/devedge-sdk/servicekit"
	"github.com/infobloxopen/devedge-sdk/servicekittest"
	"github.com/infobloxopen/devedge/pkg/config"
)

// ModuleResolver resolves a composition's member module references to the
// concrete servicekit.Module values needed to validate the composition
// (descriptor union + version compatibility) BEFORE building.
//
// Resolving an arbitrary external module in-process is impossible without
// compiling it (its package must be linked into the running binary), so the
// default resolver ([RegistryResolver]) serves modules that have registered
// themselves into the process (the fixtures, and any module the host already
// links). A future resolver can generate + build a throwaway probe that imports
// the external modules and emits their descriptors — that heavier path is out of
// P4 scope; `de compose tidy` reports honestly which members it could not resolve.
type ModuleResolver interface {
	// Resolve returns the servicekit.Module for an import path, or false when the
	// resolver does not know it.
	Resolve(importPath string) (servicekit.Module, bool)
}

// RegistryResolver is a ModuleResolver backed by an in-process registry of
// import-path -> Module constructors. Modules register themselves (e.g. fixtures
// in init, or a host that links them) via [RegisterModule].
type RegistryResolver struct{}

// moduleRegistry maps a module import path to a zero-arg Module constructor. It
// lets `de compose tidy` validate a composition built from modules linked into
// the running process (the fixtures; a host that already imports its members).
var moduleRegistry = map[string]func() servicekit.Module{}

// RegisterModule registers a module constructor under its import path so the
// default resolver can serve it to `de compose tidy`. Intended for fixtures /
// linked modules to call from init().
func RegisterModule(importPath string, ctor func() servicekit.Module) {
	moduleRegistry[importPath] = ctor
}

// Resolve implements ModuleResolver against the in-process registry.
func (RegistryResolver) Resolve(importPath string) (servicekit.Module, bool) {
	ctor, ok := moduleRegistry[importPath]
	if !ok {
		return nil, false
	}
	return ctor(), true
}

// TidyReport is the outcome of [Tidy]: which members resolved, which did not, and
// the conflicts/incompatibilities found among the resolved set.
type TidyReport struct {
	// Resolved are the import paths whose modules were resolvable in-process.
	Resolved []string
	// Unresolved are the import paths the resolver could not serve (reported, not
	// a hard failure on its own — they can still be built statically).
	Unresolved []string
	// Conflict is the first descriptor-union conflict (duplicate route prefix /
	// gRPC service / permission name; incoherent event graph), or nil.
	Conflict error
	// Incompatible are version-compatibility failures (module Requires vs host).
	Incompatible []error
}

// OK reports whether the composition is conflict-free and compatible.
func (r TidyReport) OK() bool {
	return r.Conflict == nil && len(r.Incompatible) == 0
}

// HostRequires describes the host runtime `de compose tidy` validates module
// Compatibility against (the SDK + Go versions devedge builds with).
func HostRequires(goVersion string) servicekittest.HostRequires {
	return servicekittest.HostRequires{
		SDK: SDKVersion,
		Go:  goVersion,
	}
}

// Tidy resolves a composition's member modules and validates the descriptor union
// (servicekit.ValidateModules — unique IDs; no duplicate gRPC service / HTTP route
// prefix / permission names; coherent event graph) plus version compatibility
// (servicekittest.CompatibleModules). It returns a report; the CLI decides exit
// status from report.OK().
//
// Only the resolvable members are validated against each other — an unresolved
// member is recorded in the report (it cannot contribute its descriptor to the
// in-process union check without being linked).
func Tidy(c *config.Composition, r ModuleResolver, goVersion string) (TidyReport, error) {
	if r == nil {
		r = RegistryResolver{}
	}
	refs, err := ResolveModuleRefs(c)
	if err != nil {
		return TidyReport{}, err
	}

	var report TidyReport
	var mods []servicekit.Module
	for _, ref := range refs {
		m, ok := r.Resolve(ref.ImportPath)
		if !ok {
			report.Unresolved = append(report.Unresolved, ref.ImportPath)
			continue
		}
		report.Resolved = append(report.Resolved, ref.ImportPath)
		mods = append(mods, m)
	}

	if len(mods) > 0 {
		// Descriptor-union conflict detection (ValidateModules reuses
		// ValidateDescriptors): the SDK's source of truth, not a reimplementation.
		report.Conflict = servicekit.ValidateModules(mods)
		// Version compatibility (the non-test form the SDK ships for exactly this).
		report.Incompatible = servicekittest.CompatibleModules(mods, HostRequires(goVersion))
	}

	sort.Strings(report.Resolved)
	sort.Strings(report.Unresolved)
	return report, nil
}

// Format renders a TidyReport into a human-readable, multi-line summary.
func (r TidyReport) Format(comp string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "composition %q: %d module(s) resolved, %d unresolved\n",
		comp, len(r.Resolved), len(r.Unresolved))
	for _, u := range r.Unresolved {
		fmt.Fprintf(&b, "  unresolved: %s (cannot validate descriptors in-process)\n", u)
	}
	if r.Conflict != nil {
		fmt.Fprintf(&b, "  conflict: %v\n", r.Conflict)
	}
	for _, e := range r.Incompatible {
		fmt.Fprintf(&b, "  incompatible: %v\n", e)
	}
	if r.OK() {
		b.WriteString("OK: no conflicts, all resolved modules compatible\n")
	}
	return b.String()
}
