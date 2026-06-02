package helm

import (
	"context"
	"strings"
	"testing"
)

// requireHelm skips (with a reason) when the helm CLI is unavailable, per the
// constitution: never claim a helm-dependent test passed when it could not run.
func requireHelm(t *testing.T) {
	t.Helper()
	if err := Available(); err != nil {
		t.Skipf("skipping: %v", err)
	}
}

// T029: the service chart renders the service Deployment plus a separable abstract
// claim + DSN secret per dependency, and is valid with no dependencies.
func TestServiceChart_render(t *testing.T) {
	requireHelm(t)
	h := New("")
	values := map[string]any{
		"service": map[string]any{"name": "webhooks", "image": "example/webhooks:dev", "port": 8080, "replicas": 1},
		"dependencies": []any{
			map[string]any{"name": "db", "engine": "postgres", "version": "16", "envVar": "DATABASE_URL"},
		},
	}
	out, err := h.Render(context.Background(), ChartService, "webhooks", "default", values)
	if err != nil {
		t.Fatalf("render service chart: %v", err)
	}
	// The per-dependency connection Secret is created by devedge (the daemon) at
	// deploy time with the real in-cluster DSN — not by the chart — so the chart
	// references it (secretKeyRef) but does not render a "kind: Secret" (005).
	for _, want := range []string{"kind: Deployment", "kind: DependencyClaim", "DATABASE_URL", "engine: postgres", "webhooks-db-dsn"} {
		if !strings.Contains(out, want) {
			t.Errorf("service chart render missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "kind: Secret") {
		t.Errorf("service chart must NOT render the connection Secret (daemon owns it):\n%s", out)
	}
}

func TestServiceChart_noDeps(t *testing.T) {
	requireHelm(t)
	h := New("")
	values := map[string]any{"service": map[string]any{"name": "webhooks", "image": "x", "port": 8080, "replicas": 1}}
	out, err := h.Render(context.Background(), ChartService, "webhooks", "default", values)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "kind: Deployment") || !strings.Contains(out, "kind: Service") {
		t.Errorf("no-deps service chart should still have a Deployment + Service:\n%s", out)
	}
	if strings.Contains(out, "DependencyClaim") {
		t.Errorf("no-deps service chart must not emit a DependencyClaim")
	}
}

// T029: a written-out service chart passes `helm lint`.
func TestWriteChart_lints(t *testing.T) {
	requireHelm(t)
	dir := t.TempDir()
	if err := WriteChart(ChartService, dir); err != nil {
		t.Fatalf("WriteChart: %v", err)
	}
	if err := New("").Lint(context.Background(), dir); err != nil {
		t.Fatalf("emitted chart failed helm lint: %v", err)
	}
}

func TestMaterializeChart_unknown(t *testing.T) {
	if _, _, err := MaterializeChart("nope"); err == nil {
		t.Fatal("expected error for unknown chart")
	}
}

func TestRender_sharedInstanceCharts(t *testing.T) {
	requireHelm(t)
	ctx := context.Background()
	h := New("")

	for _, tc := range []struct{ chart, port string }{
		{ChartPostgres, "5432"},
		{ChartRedis, "6379"},
	} {
		t.Run(tc.chart, func(t *testing.T) {
			out, err := h.Render(ctx, tc.chart, "testrelease", DefaultNamespace, nil)
			if err != nil {
				t.Fatalf("render %s: %v", tc.chart, err)
			}
			for _, want := range []string{"kind: StatefulSet", "volumeClaimTemplates:", "kind: Service", "clusterIP: None", tc.port} {
				if !strings.Contains(out, want) {
					t.Errorf("%s render missing %q\n---\n%s", tc.chart, want, out)
				}
			}

			// Determinism: a second render must be byte-identical.
			out2, err := h.Render(ctx, tc.chart, "testrelease", DefaultNamespace, nil)
			if err != nil {
				t.Fatalf("render %s (2): %v", tc.chart, err)
			}
			if out != out2 {
				t.Errorf("%s render is not deterministic", tc.chart)
			}
		})
	}
}
