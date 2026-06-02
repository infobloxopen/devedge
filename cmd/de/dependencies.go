package main

import (
	"context"
	"fmt"

	"github.com/infobloxopen/devedge/internal/client"
	"github.com/infobloxopen/devedge/internal/cluster"
	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/pkg/config"
)

// provisionDependencies starts a service's declared dependencies via the daemon
// on the resolved cluster target, waits for readiness, and reports the connection
// env var + DSN file per dependency. Per-dependency failures are surfaced and
// cause a non-zero exit, but the daemon leaves nothing half-provisioned that
// blocks a retry (FR-009).
func provisionDependencies(c *client.Client, service string, deps []config.Dependency, target cluster.ClusterTarget) error {
	if err := requireDependencyTools(); err != nil {
		return err
	}

	reqs := make([]daemon.DependencyRequest, 0, len(deps))
	for _, d := range deps {
		reqs = append(reqs, daemon.DependencyRequest{
			Name:      d.Name,
			Engine:    d.Engine,
			Version:   d.Version,
			Port:      d.DefaultedPort(),
			Dedicated: d.Dedicated,
		})
	}

	results, err := c.ApplyDependencies(context.Background(), service, daemon.ApplyRequest{
		KubeContext:  target.KubeContext,
		Namespace:    target.Namespace,
		Dependencies: reqs,
	})
	if err != nil {
		return fmt.Errorf("provision dependencies: %w", err)
	}

	failed := 0
	for _, r := range results {
		if r.Ready() {
			fmt.Printf("dependency %s (%s) %s\n", colorHost.Sprint(r.Name), r.Engine, colorLabel.Sprint("ready"))
			fmt.Printf("  %s=%s\n", r.EnvVarName, r.EnvVarValue)
			fmt.Printf("  real DSN written to %s\n", r.DSNFilePath)
		} else {
			failed++
			fmt.Printf("dependency %s (%s) %s: %s\n", colorHost.Sprint(r.Name), r.Engine, colorLabel.Sprint("failed"), r.Err)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d dependency(ies) failed to start", failed)
	}
	return nil
}
