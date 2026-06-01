package depruntime

import (
	"context"
	"fmt"
	"strings"

	"github.com/infobloxopen/devedge/internal/cluster"
	"github.com/infobloxopen/devedge/internal/helm"
)

// HelmProvisioner is the real Provisioner: it installs shared instances with Helm
// and provisions per-service isolation by exec'ing psql / redis-cli inside the
// instance pod. Its live behavior is exercised by the k3d e2e tests; the unit
// suite uses the in-memory fake instead.
type HelmProvisioner struct {
	helm        *helm.Helm
	kubeContext string
	namespace   string
}

// NewHelmProvisioner targets the given kube context (empty = current context),
// installing shared instances into helm.DefaultNamespace.
func NewHelmProvisioner(kubeContext string) *HelmProvisioner {
	return &HelmProvisioner{
		helm:        helm.New(kubeContext),
		kubeContext: kubeContext,
		namespace:   helm.DefaultNamespace,
	}
}

func chartFor(e Engine) (chart, release, target string, port int, err error) {
	switch e {
	case EnginePostgres:
		return helm.ChartPostgres, "devedge-postgres", "statefulset/devedge-postgres", 5432, nil
	case EngineRedis:
		return helm.ChartRedis, "devedge-redis", "statefulset/devedge-redis", 6379, nil
	default:
		return "", "", "", 0, fmt.Errorf("engine %q has no runtime support", e)
	}
}

// stableHost is the dev hostname a service uses to reach the engine (resolved to
// the EdgeIP and forwarded by the per-engine Traefik TCP entrypoint).
func stableHost(e Engine) string { return string(e) + ".dev.test" }

func (p *HelmProvisioner) EnsureInstance(ctx context.Context, engine Engine, version string) (Instance, error) {
	chart, release, _, port, err := chartFor(engine)
	if err != nil {
		return Instance{}, err
	}
	var values map[string]any
	if version != "" {
		values = map[string]any{"image": map[string]any{"tag": version}}
	}
	if err := p.helm.Install(ctx, chart, release, p.namespace, values); err != nil {
		return Instance{}, err
	}
	return Instance{Engine: engine, Host: stableHost(engine), Port: port}, nil
}

func (p *HelmProvisioner) Ready(ctx context.Context, engine Engine) error {
	_, _, target, _, err := chartFor(engine)
	if err != nil {
		return err
	}
	switch engine {
	case EnginePostgres:
		_, err = cluster.KubectlExec(ctx, p.kubeContext, p.namespace, target, "", "pg_isready", "-U", "postgres")
	case EngineRedis:
		var out string
		out, err = cluster.KubectlExec(ctx, p.kubeContext, p.namespace, target, "", "redis-cli", "PING")
		if err == nil && !strings.Contains(out, "PONG") {
			err = fmt.Errorf("redis not responding to PING")
		}
	}
	return err
}

func (p *HelmProvisioner) EnsureDatabase(ctx context.Context, b Binding) error {
	switch b.Engine {
	case EnginePostgres:
		return p.ensurePostgres(ctx, b)
	case EngineRedis:
		return p.ensureRedis(ctx, b)
	default:
		return fmt.Errorf("engine %q has no runtime support", b.Engine)
	}
}

func (p *HelmProvisioner) ensurePostgres(ctx context.Context, b Binding) error {
	_, _, target, _, _ := chartFor(EnginePostgres)
	// Idempotent role create + password sync (names are sanitized identifiers,
	// password is hex — safe to inline).
	roleSQL := fmt.Sprintf(
		`DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='%[1]s') THEN CREATE ROLE %[1]s LOGIN; END IF; END $$; ALTER ROLE %[1]s WITH LOGIN PASSWORD '%[2]s';`,
		b.User, b.Password)
	if _, err := p.psql(ctx, target, "-v", "ON_ERROR_STOP=1", "-c", roleSQL); err != nil {
		return err
	}
	// Create the database if absent (CREATE DATABASE cannot run in a DO block).
	out, err := p.psql(ctx, target, "-tAc", fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname='%s'", b.Database))
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "1" {
		if _, err := p.psql(ctx, target, "-c", fmt.Sprintf("CREATE DATABASE %s OWNER %s", b.Database, b.User)); err != nil {
			return err
		}
	}
	_, err = p.psql(ctx, target, "-c", fmt.Sprintf("GRANT ALL PRIVILEGES ON DATABASE %s TO %s", b.Database, b.User))
	return err
}

func (p *HelmProvisioner) ensureRedis(ctx context.Context, b Binding) error {
	_, _, target, _, _ := chartFor(EngineRedis)
	// ACL user scoped to the binding's key namespace (FR-002 isolation).
	_, err := cluster.KubectlExec(ctx, p.kubeContext, p.namespace, target, "",
		"redis-cli", "ACL", "SETUSER", b.User, "on", ">"+b.Password, "~"+b.KeyNamespace+"*", "+@all")
	return err
}

func (p *HelmProvisioner) DropDatabase(ctx context.Context, b Binding) error {
	switch b.Engine {
	case EnginePostgres:
		_, _, target, _, _ := chartFor(EnginePostgres)
		if _, err := p.psql(ctx, target, "-c", fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", b.Database)); err != nil {
			return err
		}
		_, err := p.psql(ctx, target, "-c", fmt.Sprintf("DROP ROLE IF EXISTS %s", b.User))
		return err
	case EngineRedis:
		_, _, target, _, _ := chartFor(EngineRedis)
		// Remove the binding's keys, then the ACL user. KEYS is acceptable for dev.
		_, _ = cluster.KubectlExec(ctx, p.kubeContext, p.namespace, target, "",
			"redis-cli", "EVAL", "for _,k in ipairs(redis.call('keys', ARGV[1])) do redis.call('del', k) end", "0", b.KeyNamespace+"*")
		_, err := cluster.KubectlExec(ctx, p.kubeContext, p.namespace, target, "", "redis-cli", "ACL", "DELUSER", b.User)
		return err
	default:
		return fmt.Errorf("engine %q has no runtime support", b.Engine)
	}
}

func (p *HelmProvisioner) psql(ctx context.Context, target string, args ...string) (string, error) {
	full := append([]string{"psql", "-U", "postgres"}, args...)
	return cluster.KubectlExec(ctx, p.kubeContext, p.namespace, target, "", full...)
}

// compile-time check.
var _ Provisioner = (*HelmProvisioner)(nil)
