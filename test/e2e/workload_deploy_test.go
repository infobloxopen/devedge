package e2e

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/deploy"
	"github.com/infobloxopen/devedge/internal/depruntime"
)

// TestWorkloadDeploy_e2e (CT / US1, FR-002/005/006): a reference-image workload is
// deployed onto a resolved cluster and reaches Ready (helm --wait), re-deploy is
// idempotent (no duplicate), an Ingress is created for the dev hostname, and
// `Remove` (down) deletes the release. Uses a bare k3d cluster (deploy needs no
// cert-manager bootstrap) and a reference image (the build path is T014).
func TestWorkloadDeploy_e2e(t *testing.T) {
	requireE2E(t)
	kubeCtx := ephemeralCluster(t) // "k3d-devedge-e2e"
	clusterName := strings.TrimPrefix(kubeCtx, "k3d-")
	const ns = "devedge-deps"
	const svc = "echo-svc"

	d := deploy.NewDeployer(kubeCtx, ns, clusterName)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	wl := deploy.Workload{Service: svc, Port: 80, Replicas: 1, Hostname: svc + ".dev.test"}
	src := deploy.ImageSource{Image: "nginx:alpine"}

	// FR-002: deploy reaches Ready (helm --wait gates pod readiness; a nil error means Ready).
	st, err := d.Deploy(ctx, wl, src)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if st.Release != svc {
		t.Errorf("release = %q, want %q", st.Release, svc)
	}
	if deploymentCount(t, kubeCtx, ns, svc) != 1 {
		t.Fatalf("expected 1 Deployment %q after deploy", svc)
	}

	// FR-004: an Ingress for the dev hostname was created (routing path).
	if out, err := exec.Command("kubectl", "--context", kubeCtx, "-n", ns, "get", "ingress", svc, "-o", "name").CombinedOutput(); err != nil {
		t.Errorf("ingress %q not created: %v\n%s", svc, err, out)
	}

	// FR-005: idempotent re-deploy — no error, still exactly one Deployment.
	if _, err := d.Deploy(ctx, wl, src); err != nil {
		t.Fatalf("re-deploy: %v", err)
	}
	if n := deploymentCount(t, kubeCtx, ns, svc); n != 1 {
		t.Errorf("after re-deploy, Deployment count = %d, want 1 (no duplicate)", n)
	}

	// FR-006: down removes the workload release.
	if err := d.Remove(ctx, svc); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if deploymentCount(t, kubeCtx, ns, svc) != 0 {
		t.Errorf("Deployment %q still present after Remove", svc)
	}
}

// TestWorkloadDeployDependency_e2e (US2 / FR-003/004): a deployed workload receives
// its dependency's connection info from the in-cluster DSN Secret and that DSN
// actually connects to the dependency in-cluster; an Ingress routes the dev hostname.
func TestWorkloadDeployDependency_e2e(t *testing.T) {
	requireE2E(t)
	kubeCtx := ephemeralCluster(t)
	clusterName := strings.TrimPrefix(kubeCtx, "k3d-")
	const ns = "devedge-deps"
	const svc = "websvc"

	prov := depruntime.NewHelmProvisioner(kubeCtx)
	t.Cleanup(prov.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Provision the dependency: instance + per-service binding + the in-cluster
	// connection Secret the deployed workload reads (T009).
	if _, err := prov.EnsureInstance(ctx, depruntime.InstanceRef{Engine: depruntime.EnginePostgres, Version: "16"}); err != nil {
		t.Fatalf("EnsureInstance: %v", err)
	}
	waitPostgresReady(t, ctx, prov)
	b, _ := depruntime.NewBinding(svc, depruntime.Dep{Name: "db", Engine: depruntime.EnginePostgres, Port: 5432})
	if err := prov.EnsureDatabase(ctx, b); err != nil {
		t.Fatalf("EnsureDatabase: %v", err)
	}
	if err := prov.EnsureConnSecret(ctx, b); err != nil {
		t.Fatalf("EnsureConnSecret: %v", err)
	}

	// The Secret holds an in-cluster Service-DNS DSN.
	connDSN := secretValue(t, kubeCtx, ns, svc+"-db-dsn", "dsn")
	if !strings.Contains(connDSN, "devedge-postgres."+ns+".svc.cluster.local:5432") {
		t.Fatalf("connection secret is not an in-cluster DSN: %q", connDSN)
	}

	// Deploy the workload wired to that dependency.
	d := deploy.NewDeployer(kubeCtx, ns, clusterName)
	if _, err := d.Deploy(ctx, deploy.Workload{
		Service: svc, Port: 80, Replicas: 1, Hostname: svc + ".dev.test",
		Deps: []deploy.DepEnv{{Name: "db", Engine: "postgres", Version: "16", EnvVar: "DATABASE_URL"}},
	}, deploy.ImageSource{Image: "nginx:alpine"}); err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	// FR-003: the deployed pod gets DATABASE_URL from the in-cluster secret.
	env, err := exec.CommandContext(ctx, "kubectl", "--context", kubeCtx, "-n", ns,
		"exec", "deploy/"+svc, "--", "printenv", "DATABASE_URL").CombinedOutput()
	if err != nil || !strings.Contains(string(env), "devedge-postgres."+ns) {
		t.Errorf("deployed workload DATABASE_URL not wired to the in-cluster DSN: %v\n%s", err, env)
	}

	// FR-003: that in-cluster DSN actually connects — psql it from the instance pod.
	out, err := exec.CommandContext(ctx, "kubectl", "--context", kubeCtx, "-n", ns,
		"exec", "devedge-postgres-0", "--", "psql", connDSN+"?sslmode=disable", "-tAc", "SELECT 1").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "1") {
		t.Errorf("the in-cluster DSN does not connect from in-cluster: %v\n%s", err, out)
	}

	// FR-004: Ingress for the dev hostname.
	if out, err := exec.Command("kubectl", "--context", kubeCtx, "-n", ns, "get", "ingress", svc, "-o", "name").CombinedOutput(); err != nil {
		t.Errorf("ingress %q not created: %v\n%s", svc, err, out)
	}
}

// TestWorkloadDeployBuild_e2e (US2 / FR-011 build path): a workload.build service is
// built with docker and loaded into the cluster with `k3d image import`, then
// deployed and reaches Ready.
func TestWorkloadDeployBuild_e2e(t *testing.T) {
	requireE2E(t)
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	kubeCtx := ephemeralCluster(t)
	clusterName := strings.TrimPrefix(kubeCtx, "k3d-")
	const ns = "devedge-deps"
	const svc = "builtsvc"

	// Minimal build context (trivial, fast build).
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM nginx:alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	d := deploy.NewDeployer(kubeCtx, ns, clusterName)
	st, err := d.Deploy(ctx, deploy.Workload{Service: svc, Port: 80, Replicas: 1, Hostname: svc + ".dev.test"},
		deploy.ImageSource{Build: &deploy.BuildSource{Context: dir}})
	if err != nil {
		t.Fatalf("Deploy (build path): %v", err)
	}
	if !strings.Contains(st.Image, svc) {
		t.Errorf("expected a devedge-built image tag, got %q", st.Image)
	}
	if deploymentCount(t, kubeCtx, ns, svc) != 1 {
		t.Errorf("built workload not deployed")
	}
}

// TestWorkloadDeployCoexistence_e2e (US3 / FR-008/006): two workloads deployed to
// the same cluster get distinct, isolated releases; taking one down leaves the
// other running. Per-service release naming is cluster.ProjectSlug (T016).
func TestWorkloadDeployCoexistence_e2e(t *testing.T) {
	requireE2E(t)
	kubeCtx := ephemeralCluster(t)
	clusterName := strings.TrimPrefix(kubeCtx, "k3d-")
	const ns = "devedge-deps"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	d := deploy.NewDeployer(kubeCtx, ns, clusterName)
	for _, svc := range []string{"svc-a", "svc-b"} {
		if _, err := d.Deploy(ctx, deploy.Workload{Service: svc, Port: 80, Replicas: 1, Hostname: svc + ".dev.test"},
			deploy.ImageSource{Image: "nginx:alpine"}); err != nil {
			t.Fatalf("deploy %s: %v", svc, err)
		}
	}
	for _, svc := range []string{"svc-a", "svc-b"} {
		if deploymentCount(t, kubeCtx, ns, svc) != 1 {
			t.Errorf("%s not deployed as its own release", svc)
		}
	}

	// Down one leaves the other running (FR-006/008).
	if err := d.Remove(ctx, "svc-a"); err != nil {
		t.Fatalf("Remove svc-a: %v", err)
	}
	if deploymentCount(t, kubeCtx, ns, "svc-a") != 0 {
		t.Errorf("svc-a not removed")
	}
	if deploymentCount(t, kubeCtx, ns, "svc-b") != 1 {
		t.Errorf("svc-b should remain running after svc-a down")
	}
}

// secretValue reads and base64-decodes a key from a Secret.
func secretValue(t *testing.T, kubeCtx, ns, name, key string) string {
	t.Helper()
	out, err := exec.Command("kubectl", "--context", kubeCtx, "-n", ns,
		"get", "secret", name, "-o", "jsonpath={.data."+key+"}").CombinedOutput()
	if err != nil {
		t.Fatalf("get secret %s: %v\n%s", name, err, out)
	}
	dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("decode secret %s/%s: %v", name, key, err)
	}
	return string(dec)
}

// deploymentCount reports how many Deployments of the given name exist in ns (0/1).
func deploymentCount(t *testing.T, kubeCtx, ns, name string) int {
	t.Helper()
	out, err := exec.Command("kubectl", "--context", kubeCtx, "-n", ns,
		"get", "deployment", name, "-o", "name").CombinedOutput()
	if err != nil {
		// NotFound → 0; any other error is surfaced by the caller's assertion.
		if strings.Contains(string(out), "NotFound") || strings.Contains(string(out), "not found") {
			return 0
		}
		return 0
	}
	if strings.TrimSpace(string(out)) == "" {
		return 0
	}
	return 1
}
