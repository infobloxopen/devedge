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
