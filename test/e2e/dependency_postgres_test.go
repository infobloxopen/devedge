// Package e2e holds k3d-backed end-to-end tests for dependency runtime. They are
// gated behind DEVEDGE_E2E=1 (and the helm/kubectl/k3d/docker/psql CLIs) so the
// normal `go test ./...` stays fast; CI sets DEVEDGE_E2E=1. Per the constitution
// these exercise the real cross-boundary flow and are skipped-with-reason — never
// silently passed — when the runtime is unavailable.
package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/depruntime"
	"github.com/infobloxopen/devedge/internal/dsn"
)

func requireE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("DEVEDGE_E2E") == "" {
		t.Skip("skipping k3d e2e: set DEVEDGE_E2E=1 to run")
	}
	for _, tool := range []string{"k3d", "kubectl", "helm", "docker", "psql"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("skipping k3d e2e: %q not on PATH", tool)
		}
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("skipping k3d e2e: docker not running")
	}
}

// ephemeralCluster creates a dedicated k3d cluster for the test and deletes it on
// cleanup (co-existence-safe: never touches a shared/dev cluster).
func ephemeralCluster(t *testing.T) string {
	t.Helper()
	name := "devedge-e2e"
	_ = exec.Command("k3d", "cluster", "delete", name).Run() // clean any leftover

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "k3d", "cluster", "create", name, "--wait", "--timeout", "180s").CombinedOutput()
	if err != nil {
		t.Fatalf("k3d cluster create: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("k3d", "cluster", "delete", name).Run() })
	return "k3d-" + name
}

// TestPostgresDependency_e2e: install the shared Postgres via Helm, provision an
// isolated service database, and connect over the reported DSN to write+read a
// row — proving the real provisioner + ephemeral port-forward end to end (US1).
func TestPostgresDependency_e2e(t *testing.T) {
	requireE2E(t)
	kubeCtx := ephemeralCluster(t)

	prov := depruntime.NewHelmProvisioner(kubeCtx)
	t.Cleanup(prov.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	inst, err := prov.EnsureInstance(ctx, depruntime.EnginePostgres, "16")
	if err != nil {
		t.Fatalf("EnsureInstance: %v", err)
	}
	if inst.Host != "127.0.0.1" || inst.Port == 0 {
		t.Fatalf("expected a 127.0.0.1:<dynPort> instance, got %+v", inst)
	}

	// Wait for readiness (helm --wait already gates pod-ready; this confirms the
	// server accepts connections).
	ready := false
	for i := 0; i < 30; i++ {
		if err := prov.Ready(ctx, depruntime.EnginePostgres); err == nil {
			ready = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !ready {
		t.Fatal("postgres did not become ready")
	}

	b, err := depruntime.NewBinding("e2esvc", depruntime.Dep{Name: "db", Engine: depruntime.EnginePostgres, Port: 5432})
	if err != nil {
		t.Fatal(err)
	}
	if err := prov.EnsureDatabase(ctx, b); err != nil {
		t.Fatalf("EnsureDatabase: %v", err)
	}

	// Build the real DSN exactly as devedge would (pointing at the forwarded port)
	// and connect over it from the host with psql — a faithful "connect over the
	// reported DSN" check.
	realDSN, err := dsn.RealDSN(dsn.Conn{
		Engine: "postgres", Host: inst.Host, Port: inst.Port,
		Database: b.Database, User: b.User, Password: b.Password,
	})
	if err != nil {
		t.Fatal(err)
	}
	connStr := realDSN + "?sslmode=disable"

	sql := "CREATE TABLE IF NOT EXISTS e2e(id int); INSERT INTO e2e VALUES (42); SELECT id FROM e2e;"
	out, err := exec.CommandContext(ctx, "psql", connStr, "-v", "ON_ERROR_STOP=1", "-tAc", sql).CombinedOutput()
	if err != nil {
		t.Fatalf("psql connect/write/read over reported DSN failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "42") {
		t.Fatalf("expected to read back 42, got: %s", out)
	}

	// Sanity: the binding really isolates — a different service derives a distinct db.
	b2, _ := depruntime.NewBinding("other", depruntime.Dep{Name: "db", Engine: depruntime.EnginePostgres, Port: 5432})
	if b2.Database == b.Database {
		t.Errorf("distinct services must derive distinct databases")
	}
	fmt.Printf("e2e: connected over %s and round-tripped a row\n", connStr)
}
