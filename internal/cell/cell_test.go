package cell_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/infobloxopen/devedge-sdk/cells"
	"github.com/infobloxopen/devedge/internal/cell"
)

// newTempTable creates a FileTable backed by a temp file. Returned cleanup
// removes the temp directory.
func newTempTable(t *testing.T) (*cells.FileTable, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "routes.json")
	return cells.NewFileTable(path, cells.WithPollInterval(50*time.Millisecond)), func() {
		os.RemoveAll(dir)
	}
}

func TestCellValues(t *testing.T) {
	vals := cell.CellValues("myapi", "canary", "registry/myapi:v1.2.0", 2)
	svc, ok := vals["service"].(map[string]any)
	if !ok {
		t.Fatalf("values[service] is not map[string]any")
	}
	if svc["name"] != "myapi-canary" {
		t.Errorf("name = %v, want myapi-canary", svc["name"])
	}
	if svc["image"] != "registry/myapi:v1.2.0" {
		t.Errorf("image = %v, want registry/myapi:v1.2.0", svc["image"])
	}
	if svc["cell"] != "canary" {
		t.Errorf("cell = %v, want canary", svc["cell"])
	}
	if svc["replicas"] != 2 {
		t.Errorf("replicas = %v, want 2", svc["replicas"])
	}
}

func TestReleaseName(t *testing.T) {
	if got := cell.ReleaseName("myapi", "canary"); got != "myapi-canary" {
		t.Errorf("ReleaseName = %q, want myapi-canary", got)
	}
}

func TestAssign_FirstPlacement(t *testing.T) {
	table, cleanup := newTempTable(t)
	defer cleanup()

	ctrl := cells.NewMoveController(table)
	ctx := context.Background()

	if err := ctrl.Assign(ctx, "tenant-1", "cell-a", "test"); err != nil {
		t.Fatalf("Assign: %v", err)
	}

	route, err := table.Get(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if route.ActiveCell != "cell-a" {
		t.Errorf("ActiveCell = %q, want cell-a", route.ActiveCell)
	}
	if route.RouteEpoch != 1 {
		t.Errorf("RouteEpoch = %d, want 1", route.RouteEpoch)
	}
	if !route.State.AdmitsNew() {
		t.Errorf("State %v should admit new", route.State)
	}
}

func TestMove_AdvancesEpoch(t *testing.T) {
	table, cleanup := newTempTable(t)
	defer cleanup()

	drainer := cell.NewTimedDrainer(10 * time.Millisecond)
	ctrl := cells.NewMoveController(table,
		cells.WithDrainer(drainer),
		cells.WithDrainDeadline(500*time.Millisecond),
	)
	ctx := context.Background()

	// First placement.
	if err := ctrl.Assign(ctx, "tenant-2", "cell-a", "test"); err != nil {
		t.Fatalf("Assign: %v", err)
	}

	// Move to cell-b.
	if err := ctrl.Move(ctx, cells.MovePlan{
		TenantID: "tenant-2",
		FromCell: "cell-a",
		ToCell:   "cell-b",
		Operator: "test",
	}); err != nil {
		t.Fatalf("Move: %v", err)
	}

	route, err := table.Get(ctx, "tenant-2")
	if err != nil {
		t.Fatalf("Get after move: %v", err)
	}
	if route.ActiveCell != "cell-b" {
		t.Errorf("ActiveCell = %q, want cell-b", route.ActiveCell)
	}
	if route.RouteEpoch < 2 {
		t.Errorf("RouteEpoch = %d, want >= 2", route.RouteEpoch)
	}
}

func TestMove_BudgetRefusal(t *testing.T) {
	table, cleanup := newTempTable(t)
	defer cleanup()

	// Meter with a tiny budget so any move exceeds it.
	meter := cells.NewBudgetMeter(cells.WithTenantBudget(1 * time.Millisecond))
	drainer := cell.NewTimedDrainer(10 * time.Millisecond)
	ctrl := cells.NewMoveController(table,
		cells.WithDrainer(drainer),
		cells.WithDrainDeadline(5*time.Second), // drain deadline > budget → refusal at phase 0
		cells.WithBudgetMeter(meter),
	)
	ctx := context.Background()

	if err := ctrl.Assign(ctx, "tenant-3", "cell-a", "test"); err != nil {
		t.Fatalf("Assign: %v", err)
	}

	err := ctrl.Move(ctx, cells.MovePlan{
		TenantID: "tenant-3",
		FromCell: "cell-a",
		ToCell:   "cell-b",
		Operator: "test",
		Force:    false,
	})
	if err == nil {
		t.Fatal("expected budget exceeded error, got nil")
	}
	if len(err.Error()) == 0 {
		t.Fatal("expected non-empty error message")
	}
}

func TestMove_ForceOverridesBudget(t *testing.T) {
	table, cleanup := newTempTable(t)
	defer cleanup()

	meter := cells.NewBudgetMeter(cells.WithTenantBudget(1 * time.Millisecond))
	drainer := cell.NewTimedDrainer(10 * time.Millisecond)
	ctrl := cells.NewMoveController(table,
		cells.WithDrainer(drainer),
		cells.WithDrainDeadline(5*time.Second),
		cells.WithBudgetMeter(meter),
	)
	ctx := context.Background()

	if err := ctrl.Assign(ctx, "tenant-4", "cell-a", "test"); err != nil {
		t.Fatalf("Assign: %v", err)
	}

	if err := ctrl.Move(ctx, cells.MovePlan{
		TenantID: "tenant-4",
		FromCell: "cell-a",
		ToCell:   "cell-b",
		Operator: "test",
		Force:    true,
	}); err != nil {
		t.Fatalf("forced Move should succeed: %v", err)
	}
}

func TestRebalance_RoundRobin(t *testing.T) {
	table, cleanup := newTempTable(t)
	defer cleanup()

	ctx := context.Background()
	ctrl := cells.NewMoveController(table)

	// Seed all tenants on cell-a.
	for _, tid := range []string{"t1", "t2", "t3", "t4"} {
		if err := ctrl.Assign(ctx, tid, "cell-a", "test"); err != nil {
			t.Fatalf("Assign %s: %v", tid, err)
		}
	}

	// Rebalance across cell-a and cell-b with a small drainer.
	drainer := cell.NewTimedDrainer(10 * time.Millisecond)
	ctrl2 := cells.NewMoveController(table,
		cells.WithDrainer(drainer),
		cells.WithDrainDeadline(500*time.Millisecond),
	)
	campaign := cells.NewCampaign(ctrl2)

	policy := cells.RoundRobinPolicy()
	plan, err := cells.PlanFromPolicy(ctx, []string{"t1", "t2", "t3", "t4"}, []string{"cell-a", "cell-b"}, policy)
	if err != nil {
		t.Fatalf("PlanFromPolicy: %v", err)
	}
	plan.MaxConcurrent = 1
	plan.Operator = "test"

	result, err := campaign.Run(ctx, plan)
	if err != nil {
		t.Logf("campaign error (may be ok if some skipped): %v", err)
	}

	// At least some tenants should be moved or already placed; no unexpected failures.
	if len(result.Failed) > 0 {
		for tid, ferr := range result.Failed {
			t.Errorf("tenant %s failed: %v", tid, ferr)
		}
	}

	// Verify at least one tenant is on cell-b.
	routes, err := table.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	onB := 0
	for _, r := range routes {
		if r.ActiveCell == "cell-b" {
			onB++
		}
	}
	if onB == 0 {
		t.Error("expected some tenants on cell-b after round-robin rebalance")
	}
}

func TestTimedDrainer_Clean(t *testing.T) {
	d := cell.NewTimedDrainer(20 * time.Millisecond)
	ctx := context.Background()
	cut, err := d.CloseAndDrain(ctx, "any", 0)
	if err != nil {
		t.Fatalf("CloseAndDrain: %v", err)
	}
	if !cut.Drained {
		t.Error("expected Drained=true")
	}
	if cut.Forced {
		t.Error("expected Forced=false for clean drain")
	}
}

func TestTimedDrainer_Cancelled(t *testing.T) {
	d := cell.NewTimedDrainer(10 * time.Second) // long enough to force cancel
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cut, err := d.CloseAndDrain(ctx, "any", 0)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	if !cut.Forced {
		t.Error("expected Forced=true on ctx cancellation")
	}
}
