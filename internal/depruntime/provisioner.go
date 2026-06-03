// Package depruntime is the portable core of dependency runtime: it turns a
// service's declared dependencies into running, reachable, isolated backing
// stores. Cluster-specific behavior is isolated behind the Provisioner adapter
// (a fake backs the unit tests; the real impl wraps Helm + kubectl exec), so the
// desired-state/reconcile logic here is platform-agnostic and cluster-free to test.
package depruntime

import (
	"context"
	"slices"
)

// Engine is a supported dependency engine. The runtime-supported set equals the
// config-recognized set (FR-012).
type Engine string

const (
	EnginePostgres Engine = "postgres"
	EngineRedis    Engine = "redis"
)

// SupportedEngines is the stable set of runtime-supported engines.
var SupportedEngines = []Engine{EnginePostgres, EngineRedis}

// Supported reports whether e has runtime support.
func Supported(e Engine) bool {
	return slices.Contains(SupportedEngines, e)
}

// Dep is a minimal, config-decoupled view of one declared dependency (the daemon
// maps pkg/config.Dependency onto this to avoid an import cycle).
type Dep struct {
	Name      string
	Engine    Engine
	Version   string
	Port      int
	Dedicated bool // FR-016: provision an isolated per-service instance, not the shared one
	// Migrations is the absolute path to this dependency's migrations directory,
	// resolved CLI-side from the project root; "" when none declared (006, postgres only).
	Migrations string
	// Seed is the absolute path to this dependency's dev seed file/dir; "" when none.
	// Applied after migrations, local/dev only, skipped in CI (006/US3).
	Seed string
}

// InstanceRef identifies which engine instance a dependency targets: the shared
// per-engine instance (Dedicated false), or a per-service dedicated instance
// (Dedicated true, named from Service) — FR-016.
type InstanceRef struct {
	Engine    Engine
	Version   string
	Dedicated bool
	Service   string // names the dedicated release; ignored when Dedicated is false
}

// Binding is a service's isolated slice of an instance — the unit a wipe targets.
// For Postgres: a database + role + password. For Redis: an ACL user + password +
// key namespace / logical DB index. Dedicated selects which instance the slice
// lives in (the shared per-engine one, or this service's own — FR-016).
type Binding struct {
	Service      string
	Dependency   string
	Engine       Engine
	Database     string // postgres db name, or redis logical DB index as a string
	User         string
	Password     string
	KeyNamespace string // redis only
	Dedicated    bool   // targets this service's dedicated instance instead of the shared one
}

// instanceRef returns the InstanceRef this binding's slice lives in.
func (b Binding) instanceRef() InstanceRef {
	return InstanceRef{Engine: b.Engine, Dedicated: b.Dedicated, Service: b.Service}
}

// Instance describes a reachable shared instance for an engine.
type Instance struct {
	Engine Engine
	Host   string // stable host the service connects to (e.g. postgres.dev.test)
	Port   int
}

// Provisioner is the cluster adapter. Implementations must be idempotent:
// EnsureInstance/EnsureDatabase create-if-absent and never destroy data.
type Provisioner interface {
	// EnsureInstance ensures the instance identified by ref (shared per-engine, or
	// a per-service dedicated one) is installed and returns how to reach it.
	EnsureInstance(ctx context.Context, ref InstanceRef) (Instance, error)
	// Ready returns nil once the referenced instance accepts connections.
	Ready(ctx context.Context, ref InstanceRef) error
	// EnsureDatabase idempotently provisions the binding's isolated db/role/ACL.
	EnsureDatabase(ctx context.Context, b Binding) error
	// EnsureConnSecret materializes the binding's in-cluster connection as a
	// Secret in the cluster (reachable over the in-cluster Service DNS), so a
	// deployed workload (005) can connect. Idempotent; unused by local-run.
	EnsureConnSecret(ctx context.Context, b Binding) error
	// DropDatabase removes only this binding's isolation slice (never the instance).
	DropDatabase(ctx context.Context, b Binding) error
}
