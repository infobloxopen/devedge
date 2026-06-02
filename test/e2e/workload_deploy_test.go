package e2e

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/deploy"
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
