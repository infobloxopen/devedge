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
	Name    string
	Engine  Engine
	Version string
	Port    int
}

// Binding is a service's isolated slice of a shared instance — the unit a wipe
// targets. For Postgres: a database + role + password. For Redis: an ACL user +
// password + key namespace / logical DB index.
type Binding struct {
	Service      string
	Dependency   string
	Engine       Engine
	Database     string // postgres db name, or redis logical DB index as a string
	User         string
	Password     string
	KeyNamespace string // redis only
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
	// EnsureInstance ensures the shared instance for engine (version optional) is
	// installed and returns how to reach it.
	EnsureInstance(ctx context.Context, engine Engine, version string) (Instance, error)
	// Ready returns nil once the engine's instance accepts connections.
	Ready(ctx context.Context, engine Engine) error
	// EnsureDatabase idempotently provisions the binding's isolated db/role/ACL.
	EnsureDatabase(ctx context.Context, b Binding) error
	// DropDatabase removes only this binding's isolation slice (never the instance).
	DropDatabase(ctx context.Context, b Binding) error
}
