// Package cell provides the orchestration glue for the `de cell` command group:
// a timed drainer, per-cell Helm values builder, and move/rebalance helpers.
//
// The CLI uses a nil Fencer and nil EventBarrier (routing-plane moves only).
// Stateful services run the controller in-process with the SDK's GormFencer and
// OutboxEventBarrier wired; the CLI cannot reach in-cluster gates and relies on
// the epoch fence and in-cluster service-side admission gates for correctness.
package cell

import (
	"context"
	"time"

	"github.com/infobloxopen/devedge-sdk/cells"
)

// TimedDrainer implements cells.Drainer for the CLI: it waits the configured
// drain window (or ctx cancellation) and returns a clean Cutoff. Correctness
// is provided by the route epoch fence and in-cluster L2/L3 admission gates
// — the CLI cannot reach those gates directly.
type TimedDrainer struct {
	window time.Duration
}

// NewTimedDrainer returns a Drainer that waits up to window before declaring
// the drain complete. Use a window ≤ the drain deadline configured on the
// MoveController.
func NewTimedDrainer(window time.Duration) *TimedDrainer {
	return &TimedDrainer{window: window}
}

// CloseAndDrain implements cells.Drainer: waits window or ctx, returns Cutoff.
func (d *TimedDrainer) CloseAndDrain(ctx context.Context, _ string, _ uint64) (cells.Cutoff, error) {
	select {
	case <-time.After(d.window):
		return cells.Cutoff{Drained: true}, nil
	case <-ctx.Done():
		return cells.Cutoff{Forced: true}, ctx.Err()
	}
}

// CellValues builds the Helm values map for a per-cell "service" chart instance.
// The release name convention is "<service>-<cell>" (e.g. "myapi-canary").
func CellValues(service, cell, image string, replicas int) map[string]any {
	if replicas <= 0 {
		replicas = 1
	}
	svc := map[string]any{
		"name":     service + "-" + cell,
		"image":    image,
		"replicas": replicas,
		"cell":     cell,
	}
	return map[string]any{
		"service":      svc,
		"dependencies": []map[string]any{},
	}
}

// ReleaseName returns the Helm release name for a cell deployment.
func ReleaseName(service, cell string) string {
	return service + "-" + cell
}
