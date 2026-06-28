package compose

// Package compose — P6 deployment rendering: composition → chart inputs.
//
// ComposeChart maps a Composition resource into per-workload chart inputs that
// the caller feeds into internal/helm's ChartService (helm.Render / helm.WriteChart).
// Three topologies are supported, driven by spec.runtime.mode / a --mode override:
//
//   - single-binary  — ONE Deployment running the composed binary (cmd/<name>); one
//     Ingress/route per member route (aggregated via Composition.ToRoutes()).
//   - multi-daemon   — one Deployment per member module; member-owned routes only.
//   - hybrid         — members default to the composed binary unless marked
//     standalone via their failurePolicy ("dedicated-required" maps to standalone;
//     all others stay in the composed binary). This keeps the topology flag in the
//     deployment descriptor, not in the source.
//
// Each topology shape returns a ChartPlan: a shared DB workload (when
// spec.database is set) plus one or more service workloads rendered via the
// existing "service" embedded chart.
//
// The shared DB is provisioned ONCE (one Postgres claim for the whole composition)
// — the module-namespace isolation is runtime (spec.database.isolation), not a
// per-module DB deployment. Per-module schema wiring is reflected in the values so
// operator tooling can read it; the runtime performs the actual namespacing.

import (
	"fmt"
	"strings"

	"github.com/infobloxopen/devedge/pkg/config"
)

// TopologyMode selects how the composition is deployed.
type TopologyMode string

const (
	// ModeAuto resolves to the mode declared in spec.runtime.mode (the default).
	ModeAuto TopologyMode = ""
	// ModeSingleBinary composes all modules into ONE Deployment.
	ModeSingleBinary TopologyMode = "single-binary"
	// ModeMultiDaemon deploys one Deployment per member module.
	ModeMultiDaemon TopologyMode = "multi-daemon"
	// ModeHybrid groups members into the composed binary unless they are marked
	// standalone (failurePolicy: dedicated-required → own Deployment).
	ModeHybrid TopologyMode = "hybrid"
)

// resolveMode returns the effective topology mode: the --mode flag (if set)
// overrides the spec; a blank spec defaults to single-binary.
func resolveMode(c *config.Composition, flag TopologyMode) TopologyMode {
	if flag != ModeAuto {
		return flag
	}
	switch c.Spec.Runtime.Mode {
	case string(ModeSingleBinary):
		return ModeSingleBinary
	case string(ModeMultiDaemon):
		return ModeMultiDaemon
	case string(ModeHybrid):
		return ModeHybrid
	default:
		return ModeSingleBinary // safe default
	}
}

// WorkloadValues is the helm "service" values block for one Deployment workload.
// It maps directly to internal/helm's ChartService values shape (service + dependencies).
type WorkloadValues struct {
	// Name is the Kubernetes workload name (used as Helm release + Deployment name).
	Name string
	// Image is the container image reference (empty — the user supplies it).
	Image string
	// Port is the primary HTTP port the workload listens on.
	Port int
	// GRPCPort is the gRPC port (0 if not exposed as a separate port).
	GRPCPort int
	// Replicas is the desired replica count.
	Replicas int
	// Hostname is the per-member Ingress host (one per member route). When a
	// workload serves multiple members' routes, multiple Hostnames are present
	// (rendered as multiple Ingress rules under one Ingress or split — the caller
	// decides; for now we render one values block per workload).
	Hostnames []string
	// Modules lists the member names this workload hosts (for annotation / labeling).
	Modules []string
	// DatabaseEnvVar is the env var name for the shared DB DSN (empty if no shared DB).
	DatabaseEnvVar string
	// DatabaseSchema lists the per-module schemas wired into this workload (informational;
	// the servicekit host runtime performs the actual namespacing at boot).
	DatabaseSchemas []string
}

// ChartPlan is the result of mapping a Composition onto chart inputs. It contains
// one entry per Deployment workload, plus optional shared-dependency workloads.
type ChartPlan struct {
	// CompositionName is the metadata.name of the source composition.
	CompositionName string
	// Mode is the effective topology mode that produced this plan.
	Mode TopologyMode
	// Workloads are the service workloads to render via ChartService — one per
	// Deployment, regardless of how many modules are co-resident in it.
	Workloads []WorkloadValues
	// SharedDB is non-nil when spec.database is set; it carries the values for
	// provisioning the ONE shared Postgres instance (rendered as a DependencyClaim
	// in each workload's chart, not as a separate Deployment).
	SharedDB *SharedDBValues
}

// SharedDBValues describes the ONE shared database dependency for the composition.
type SharedDBValues struct {
	// Engine is "postgres" / "redis" etc.
	Engine string
	// DSNRef is the env var name the host reads the connection string from.
	DSNRef string
	// Isolation is the module-namespace policy (schema-preferred etc.).
	Isolation string
	// Name is the logical dependency name used in chart wiring ("db" by default).
	Name string
}

// ComposeChart maps c onto a ChartPlan using the given topology mode.
// It is pure (no I/O, no helm subprocess) and returns an error only when the
// composition is structurally invalid for chart rendering (e.g. no modules).
func ComposeChart(c *config.Composition, modeFlag TopologyMode) (*ChartPlan, error) {
	if len(c.Spec.Modules) == 0 {
		return nil, fmt.Errorf("composition %q: no member modules to render", c.Project())
	}

	mode := resolveMode(c, modeFlag)

	var sharedDB *SharedDBValues
	if d := c.Spec.Database; d != nil {
		sharedDB = &SharedDBValues{
			Engine:    d.Engine,
			DSNRef:    d.DSNRef,
			Isolation: d.Isolation,
			Name:      "db",
		}
	}

	var workloads []WorkloadValues
	switch mode {
	case ModeSingleBinary:
		workloads = singleBinaryWorkloads(c, sharedDB)
	case ModeMultiDaemon:
		workloads = multiDaemonWorkloads(c, sharedDB)
	case ModeHybrid:
		workloads = hybridWorkloads(c, sharedDB)
	default:
		return nil, fmt.Errorf("unsupported topology mode %q", mode)
	}

	return &ChartPlan{
		CompositionName: c.Project(),
		Mode:            mode,
		Workloads:       workloads,
		SharedDB:        sharedDB,
	}, nil
}

// httpPort returns the HTTP port from spec.runtime.http (e.g. ":8080" → 8080),
// defaulting to 8080 when unset or unparseable.
func httpPort(c *config.Composition) int {
	addr := c.Spec.Runtime.HTTP
	if addr == "" {
		return 8080
	}
	addr = strings.TrimPrefix(addr, ":")
	var port int
	if _, err := fmt.Sscanf(addr, "%d", &port); err != nil || port <= 0 {
		return 8080
	}
	return port
}

// grpcPort returns the gRPC port from spec.runtime.grpc (e.g. ":9090" → 9090),
// returning 0 when unset.
func grpcPort(c *config.Composition) int {
	addr := c.Spec.Runtime.GRPC
	if addr == "" {
		return 0
	}
	addr = strings.TrimPrefix(addr, ":")
	var port int
	if _, err := fmt.Sscanf(addr, "%d", &port); err != nil || port <= 0 {
		return 0
	}
	return port
}

// dbEnvVar returns the DATABASE_URL env var for the shared DB, using DSNRef when
// set, falling back to "DATABASE_URL".
func dbEnvVar(db *SharedDBValues) string {
	if db == nil {
		return ""
	}
	if db.DSNRef != "" {
		return db.DSNRef
	}
	return "DATABASE_URL"
}

// memberHostnames collects the route hostnames from a ModuleEntry.
func memberHostnames(m config.ModuleEntry) []string {
	var hs []string
	for _, r := range m.Routes {
		if r.Host != "" {
			hs = append(hs, r.Host)
		}
	}
	return hs
}

// allHostnames aggregates route hostnames from all member modules.
func allHostnames(c *config.Composition) []string {
	var hs []string
	for _, m := range c.Spec.Modules {
		hs = append(hs, memberHostnames(m)...)
	}
	return hs
}

// allSchemas aggregates the effective DB schema for all member modules.
func allSchemas(c *config.Composition) []string {
	schemas := make([]string, 0, len(c.Spec.Modules))
	for _, m := range c.Spec.Modules {
		schemas = append(schemas, m.EffectiveSchema())
	}
	return schemas
}

// memberNames returns the short names of all member modules.
func memberNames(c *config.Composition) []string {
	names := make([]string, 0, len(c.Spec.Modules))
	for _, m := range c.Spec.Modules {
		names = append(names, m.Name)
	}
	return names
}

// --- Topology implementations ---

// singleBinaryWorkloads produces ONE Deployment for the whole composed binary.
// Routes from all members are aggregated on that one workload.
//
//	assert: len(workloads) == 1
//	assert: one Ingress/route per member route (hostnames carry all member hosts)
func singleBinaryWorkloads(c *config.Composition, sharedDB *SharedDBValues) []WorkloadValues {
	return []WorkloadValues{
		{
			Name:            c.Project(),
			Port:            httpPort(c),
			GRPCPort:        grpcPort(c),
			Replicas:        1,
			Hostnames:       allHostnames(c),
			Modules:         memberNames(c),
			DatabaseEnvVar:  dbEnvVar(sharedDB),
			DatabaseSchemas: allSchemas(c),
		},
	}
}

// multiDaemonWorkloads produces ONE Deployment per member module. Each workload
// carries only its own member's routes; the shared DB env var appears in each.
//
//	assert: len(workloads) == len(c.Spec.Modules)
func multiDaemonWorkloads(c *config.Composition, sharedDB *SharedDBValues) []WorkloadValues {
	out := make([]WorkloadValues, 0, len(c.Spec.Modules))
	for _, m := range c.Spec.Modules {
		out = append(out, WorkloadValues{
			Name:            c.Project() + "-" + m.Name,
			Port:            httpPort(c),
			GRPCPort:        grpcPort(c),
			Replicas:        1,
			Hostnames:       memberHostnames(m),
			Modules:         []string{m.Name},
			DatabaseEnvVar:  dbEnvVar(sharedDB),
			DatabaseSchemas: []string{m.EffectiveSchema()},
		})
	}
	return out
}

// hybridWorkloads splits members into two groups:
//   - standalone: members whose failurePolicy is "degraded" get their own
//     Deployment. The "degraded" policy means the module is optional — if it
//     fails, the host stays up without it. Giving such a module its own Deployment
//     in hybrid mode respects that semantic: it fails independently without
//     dragging down the composed binary.
//   - composed: all other members (failurePolicy: fail-host or unset) share ONE
//     composed-binary Deployment. Core modules (fail-host) must be co-resident
//     because their failure should cascade to the host anyway.
//
// This mapping preserves the `failurePolicy` semantics as a deploy-time topology
// hint without adding a new per-member field: core modules stay together; optional
// modules get their own process. The rule is documented in the spec (P6 section).
//
// If ALL members are standalone (all "degraded") the result equals multi-daemon.
// If NO members are standalone the result equals single-binary.
func hybridWorkloads(c *config.Composition, sharedDB *SharedDBValues) []WorkloadValues {
	var composed []config.ModuleEntry
	var standalone []config.ModuleEntry
	for _, m := range c.Spec.Modules {
		if m.FailurePolicy == "degraded" {
			standalone = append(standalone, m)
		} else {
			composed = append(composed, m)
		}
	}

	var out []WorkloadValues

	// Composed group: one Deployment for all non-standalone members.
	if len(composed) > 0 {
		names := make([]string, 0, len(composed))
		hostnames := []string{}
		schemas := make([]string, 0, len(composed))
		for _, m := range composed {
			names = append(names, m.Name)
			hostnames = append(hostnames, memberHostnames(m)...)
			schemas = append(schemas, m.EffectiveSchema())
		}
		out = append(out, WorkloadValues{
			Name:            c.Project(),
			Port:            httpPort(c),
			GRPCPort:        grpcPort(c),
			Replicas:        1,
			Hostnames:       hostnames,
			Modules:         names,
			DatabaseEnvVar:  dbEnvVar(sharedDB),
			DatabaseSchemas: schemas,
		})
	}

	// Standalone group: one Deployment per standalone member.
	for _, m := range standalone {
		out = append(out, WorkloadValues{
			Name:            c.Project() + "-" + m.Name,
			Port:            httpPort(c),
			GRPCPort:        grpcPort(c),
			Replicas:        1,
			Hostnames:       memberHostnames(m),
			Modules:         []string{m.Name},
			DatabaseEnvVar:  dbEnvVar(sharedDB),
			DatabaseSchemas: []string{m.EffectiveSchema()},
		})
	}

	return out
}

// ToHelmValues converts a WorkloadValues into the map[string]any shape that
// internal/helm's ChartService (helm.Render) expects for the "service" chart.
// Callers feed the returned map directly into helm.Render / helm.WriteChart.
func (w WorkloadValues) ToHelmValues() map[string]any {
	svc := map[string]any{
		"name":     w.Name,
		"image":    w.Image,
		"port":     w.Port,
		"replicas": w.Replicas,
	}
	// Hostname: use the first member hostname for the Ingress rule (the service chart
	// renders one Ingress per values block; multi-hostname is a future iteration).
	if len(w.Hostnames) > 0 {
		svc["hostname"] = w.Hostnames[0]
	}

	// Compose-specific annotations on the values so helm templates / operators can
	// read the membership without needing the composition file.
	if len(w.Modules) > 0 {
		svc["compositionModules"] = strings.Join(w.Modules, ",")
	}
	if len(w.DatabaseSchemas) > 0 {
		svc["compositionSchemas"] = strings.Join(w.DatabaseSchemas, ",")
	}

	deps := []map[string]any{}
	if w.DatabaseEnvVar != "" {
		deps = append(deps, map[string]any{
			"name":    "db",
			"engine":  "postgres",
			"version": "",
			"envVar":  w.DatabaseEnvVar,
		})
	}

	return map[string]any{
		"service":      svc,
		"dependencies": deps,
	}
}
