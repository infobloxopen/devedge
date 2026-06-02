package depruntime

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/infobloxopen/devedge/internal/cluster"
	"github.com/infobloxopen/devedge/internal/helm"
)

// HelmProvisioner is the real Provisioner: it installs shared instances with Helm,
// reaches them from the host via a supervised ephemeral port-forward, and
// provisions per-service isolation by exec'ing psql / redis-cli inside the
// instance pod. Its live behavior is exercised by the k3d e2e tests; the unit
// suite uses the in-memory fake instead.
type HelmProvisioner struct {
	helm        *helm.Helm
	kubeContext string
	namespace   string

	mu       sync.Mutex
	forwards map[string]*cluster.PortForward // keyed by release (shared or per-service dedicated)
}

// NewHelmProvisioner targets the given kube context (empty = current context),
// installing shared instances into helm.DefaultNamespace.
func NewHelmProvisioner(kubeContext string) *HelmProvisioner {
	return NewHelmProvisionerNS(kubeContext, helm.DefaultNamespace)
}

// NewHelmProvisionerNS targets a specific kube context and dependency namespace.
// An empty namespace falls back to helm.DefaultNamespace. This is the form the
// daemon uses to build a provisioner per resolved cluster (004).
func NewHelmProvisionerNS(kubeContext, namespace string) *HelmProvisioner {
	if namespace == "" {
		namespace = helm.DefaultNamespace
	}
	return &HelmProvisioner{
		helm:        helm.New(kubeContext),
		kubeContext: kubeContext,
		namespace:   namespace,
		forwards:    map[string]*cluster.PortForward{},
	}
}

// Close stops all supervised port-forwards. Called on daemon shutdown.
func (p *HelmProvisioner) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, pf := range p.forwards {
		pf.Stop()
	}
}

// ensureForward returns the local port of a live forward to the release's pod,
// (re)starting it if absent or dead. Keyed by release so the shared instance and
// any per-service dedicated instances each get their own forward.
func (p *HelmProvisioner) ensureForward(release, target string, remotePort int) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pf := p.forwards[release]; pf != nil && pf.Alive() {
		return pf.LocalPort, nil
	}
	pf, err := cluster.StartPortForward(p.kubeContext, p.namespace, target, remotePort)
	if err != nil {
		return 0, err
	}
	p.forwards[release] = pf
	return pf.LocalPort, nil
}

// instanceFor maps an InstanceRef to its chart, Helm release, kubectl exec/forward
// target, and engine port. The shared instance uses the base release name; a
// dedicated instance (FR-016) appends the service slug, yielding an isolated
// release that coexists with the shared one (the chart names all resources from
// the release name).
func instanceFor(ref InstanceRef) (chart, release, target string, port int, err error) {
	switch ref.Engine {
	case EnginePostgres:
		chart, release, port = helm.ChartPostgres, "devedge-postgres", 5432
	case EngineRedis:
		chart, release, port = helm.ChartRedis, "devedge-redis", 6379
	default:
		return "", "", "", 0, fmt.Errorf("engine %q has no runtime support", ref.Engine)
	}
	if ref.Dedicated {
		release = release + "-" + cluster.ProjectSlug(ref.Service)
	}
	target = "statefulset/" + release
	return chart, release, target, port, nil
}

func (p *HelmProvisioner) EnsureInstance(ctx context.Context, ref InstanceRef) (Instance, error) {
	chart, release, target, port, err := instanceFor(ref)
	if err != nil {
		return Instance{}, err
	}
	var values map[string]any
	if ref.Version != "" {
		values = map[string]any{"image": map[string]any{"tag": ref.Version}}
	}
	if err := p.helm.Install(ctx, chart, release, p.namespace, values); err != nil {
		return Instance{}, err
	}
	// Reach the in-cluster instance from the host via an ephemeral port-forward;
	// the indirect DSN hides the dynamic port from the app (research decision 1).
	localPort, err := p.ensureForward(release, target, port)
	if err != nil {
		return Instance{}, fmt.Errorf("port-forward %s: %w", release, err)
	}
	return Instance{Engine: ref.Engine, Host: "127.0.0.1", Port: localPort}, nil
}

func (p *HelmProvisioner) Ready(ctx context.Context, ref InstanceRef) error {
	_, _, target, _, err := instanceFor(ref)
	if err != nil {
		return err
	}
	switch ref.Engine {
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
	_, _, target, _, _ := instanceFor(b.instanceRef())
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
	_, _, target, _, _ := instanceFor(b.instanceRef())
	// ACL user scoped to the binding's key namespace (FR-002 isolation).
	_, err := cluster.KubectlExec(ctx, p.kubeContext, p.namespace, target, "",
		"redis-cli", "ACL", "SETUSER", b.User, "on", ">"+b.Password, "~"+b.KeyNamespace+"*", "+@all")
	return err
}

func (p *HelmProvisioner) DropDatabase(ctx context.Context, b Binding) error {
	switch b.Engine {
	case EnginePostgres:
		_, _, target, _, _ := instanceFor(b.instanceRef())
		if _, err := p.psql(ctx, target, "-c", fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", b.Database)); err != nil {
			return err
		}
		_, err := p.psql(ctx, target, "-c", fmt.Sprintf("DROP ROLE IF EXISTS %s", b.User))
		return err
	case EngineRedis:
		_, _, target, _, _ := instanceFor(b.instanceRef())
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
