package cluster

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// defaultEnsureTimeout bounds first-time create+bootstrap (D-timing): k3d's own
// `--timeout 180s` bounds the create; this bounds the bootstrap/probe steps.
const defaultEnsureTimeout = 300 * time.Second

// Ensurer ensures a cluster exists and is bootstrapped for devedge, idempotently
// and race-safely. It depends only on the Provider adapter (Principle IV); the
// bootstrap and bootstrap-probe seams are injectable so the policy is unit-tested
// against a fake Provider without kubectl/helm.
type Ensurer struct {
	Provider Provider
	LockDir  string        // dir for the host lock file (default: devedge home)
	Timeout  time.Duration // first-time ensure budget (default 300s)
	HostPort string        // shared-cluster ingress host port (default 8081)
	Force    bool          // bypass the local-cluster safeguard (rare)
	Logger   *slog.Logger  // observability (resolve/ensure/teardown); defaults to slog.Default()

	// Bootstrap installs devedge integration into a created/unbootstrapped
	// cluster. Defaults to the real Bootstrap.
	Bootstrap func(ctx context.Context, p Provider, name string, force bool) error
	// IsBootstrapped reports whether a present cluster already carries the devedge
	// integration. Defaults to a kubectl probe; any probe error is treated as
	// "not bootstrapped" so reconcile (not failure) is the safe default.
	IsBootstrapped func(ctx context.Context, kubeContext string) (bool, error)
}

// NewEnsurer builds an Ensurer over the given provider with real bootstrap seams.
func NewEnsurer(p Provider) *Ensurer {
	return &Ensurer{Provider: p}
}

// EnsureCluster makes the named cluster present and bootstrapped, returning only
// when it is ready to use. It never re-creates a present cluster (FR-011 fast
// path), reconciles a present-but-unbootstrapped one, serializes first-time
// creation across concurrent invocations with a host lock (FR-011 edge case),
// and leaves nothing half-created on failure (FR-012). It never mutates the
// user's global kube context (FR-013) — every operation is context-scoped.
func (e *Ensurer) EnsureCluster(ctx context.Context, name string) error {
	kubeContext := e.Provider.KubeContext(name)

	present, err := e.present(name)
	if err != nil {
		return fmt.Errorf("cannot list %s clusters (is %s installed and the container runtime running?): %w",
			e.Provider.Name(), e.Provider.Name(), err)
	}
	if present {
		return e.reconcileIfNeeded(ctx, name, kubeContext)
	}

	// Absent: serialize first-time creation so racing `de project up` invocations
	// yield exactly one cluster.
	lock, err := acquireHostLock(e.lockDir(), name)
	if err != nil {
		return fmt.Errorf("acquire cluster lock for %q: %w", name, err)
	}
	defer lock.release()

	// Re-check under the lock — a racer may have created it while we waited.
	present, err = e.present(name)
	if err != nil {
		return fmt.Errorf("cannot list %s clusters: %w", e.Provider.Name(), err)
	}
	if present {
		return e.reconcileIfNeeded(ctx, name, kubeContext)
	}

	e.log().Info("ensuring cluster", "cluster", name, "context", kubeContext)
	if err := e.create(name, e.hostPort()); err != nil {
		// Best-effort cleanup so a retry starts from a clean slate (FR-012).
		_ = e.Provider.Delete(name)
		return fmt.Errorf("create cluster %q failed: %w\nretry `de project up` once the cause is resolved", name, err)
	}
	// Bootstrap installs cert-manager, the devedge ClusterIssuer, and external-dns
	// into the (local) cluster (FR-002). On a cluster devedge created this is fatal
	// on failure — ensure must yield a fully bootstrapped cluster — but the cluster
	// already exists, so a rerun reconciles rather than recreates.
	if err := e.bootstrap(ctx, name); err != nil {
		return fmt.Errorf("bootstrap cluster %q failed: %w\nthe cluster exists — retry `de project up` to reconcile, or run `de cluster bootstrap %s`",
			name, err, name)
	}
	e.log().Info("cluster ready", "cluster", name)
	return nil
}

// EnsureEphemeral creates and bootstraps a fresh, per-run-unique cluster for CI
// (FR-007/FR-008). It allocates a free host port so concurrent runs never collide
// and returns the resolved target. A failed create/bootstrap is torn down so no
// broken cluster is left behind.
func (e *Ensurer) EnsureEphemeral(ctx context.Context) (ClusterTarget, error) {
	target := Resolve(e.Provider, EnvEphemeral, "", false)
	e.log().Info("creating ephemeral cluster", "cluster", target.Name, "context", target.KubeContext)

	port, err := freePort()
	if err != nil {
		return ClusterTarget{}, fmt.Errorf("allocate ingress host port: %w", err)
	}
	if err := e.create(target.Name, port); err != nil {
		_ = e.Provider.Delete(target.Name)
		return ClusterTarget{}, fmt.Errorf("create ephemeral cluster %q: %w", target.Name, err)
	}
	if err := e.bootstrap(ctx, target.Name); err != nil {
		_ = e.Provider.Delete(target.Name)
		return ClusterTarget{}, fmt.Errorf("bootstrap ephemeral cluster %q: %w", target.Name, err)
	}
	return target, nil
}

// Teardown deletes a cluster and cleans up its devedge routes. Used by the CI
// wrapper's deferred cleanup (always attempted, on success/failure/signal).
func (e *Ensurer) Teardown(name string) error {
	e.log().Info("tearing down cluster", "cluster", name)
	return DeleteAndCleanup(e.Provider, name)
}

// reconcileIfNeeded reuses a bootstrapped cluster as-is, or re-bootstraps one that
// is present but not bootstrapped (edge case). It never re-creates the cluster.
func (e *Ensurer) reconcileIfNeeded(ctx context.Context, name, kubeContext string) error {
	if ok, _ := e.probe(ctx, kubeContext); ok {
		e.log().Info("reusing cluster", "cluster", name, "context", kubeContext)
		return nil
	}
	e.log().Info("reconciling cluster (re-bootstrap)", "cluster", name)
	if err := e.bootstrap(ctx, name); err != nil {
		return fmt.Errorf("reconcile (re-bootstrap) cluster %q: %w\nretry `de project up`", name, err)
	}
	return nil
}

func (e *Ensurer) present(name string) (bool, error) {
	clusters, err := e.Provider.List()
	if err != nil {
		return false, err
	}
	for _, c := range clusters {
		if c.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (e *Ensurer) create(name, hostPort string) error {
	// --kubeconfig-switch-context=false: an auto-ensured cluster must NOT hijack
	// the user's current kube context (FR-013/D8); k3d switches it by default.
	return e.Provider.Create(CreateOptions{
		Name:      name,
		HostPort:  hostPort,
		ExtraArgs: []string{"--wait", "--timeout", "180s", "--kubeconfig-switch-context=false"},
	})
}

// log returns the configured logger, or the default. Ensure/teardown lifecycle
// transitions are logged here (Principle V observability).
func (e *Ensurer) log() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}

func (e *Ensurer) bootstrap(ctx context.Context, name string) error {
	bs := e.Bootstrap
	if bs == nil {
		bs = defaultBootstrap
	}
	cctx, cancel := context.WithTimeout(ctx, e.timeout())
	defer cancel()
	return bs(cctx, e.Provider, name, e.Force)
}

func (e *Ensurer) probe(ctx context.Context, kubeContext string) (bool, error) {
	p := e.IsBootstrapped
	if p == nil {
		p = isClusterBootstrapped
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return p(cctx, kubeContext)
}

func (e *Ensurer) timeout() time.Duration {
	if e.Timeout > 0 {
		return e.Timeout
	}
	return defaultEnsureTimeout
}

func (e *Ensurer) hostPort() string {
	if e.HostPort != "" {
		return e.HostPort
	}
	return "8081"
}

func (e *Ensurer) lockDir() string {
	if e.LockDir != "" {
		return e.LockDir
	}
	if dir := os.Getenv("DEVEDGE_HOME"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".devedge")
}

// defaultBootstrap wraps the package Bootstrap as an Ensurer seam.
func defaultBootstrap(_ context.Context, p Provider, name string, force bool) error {
	return Bootstrap(BootstrapOptions{Provider: p, ClusterName: name, Force: force})
}

// isClusterBootstrapped probes for the devedge ClusterIssuer installed by
// Bootstrap. Any error (not-found, unreachable) reports "not bootstrapped" so the
// caller reconciles rather than failing.
func isClusterBootstrapped(ctx context.Context, kubeContext string) (bool, error) {
	out, err := exec.CommandContext(ctx, "kubectl", "--context", kubeContext,
		"get", "clusterissuer", "devedge-local", "-o", "name").CombinedOutput()
	if err != nil {
		return false, nil
	}
	return strings.Contains(string(out), "devedge-local"), nil
}

// freePort asks the OS for an unused TCP port (then releases it), so ephemeral
// clusters get a collision-free ingress host port.
func freePort() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		return "", err
	}
	return port, nil
}

// hostLock is an advisory host-level file lock (flock) that serializes first-time
// cluster creation across processes. The lock releases automatically on process
// exit, so a crash mid-create cannot wedge future ensures.
type hostLock struct{ f *os.File }

func acquireHostLock(dir, name string) (*hostLock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "cluster-"+name+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return &hostLock{f: f}, nil
}

func (l *hostLock) release() {
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
}
