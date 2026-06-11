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
func provisionDependencies(c *client.Client, service string, deps []config.Dependency, migs []config.DependencyMigrations, env cluster.Environment, target cluster.ClusterTarget) error {
	// Resolved (absolute) migration/seed sources, by dependency name (006).
	migByDep := make(map[string]config.DependencyMigrations, len(migs))
	for _, m := range migs {
		migByDep[m.Dependency] = m
	}

	reqs := make([]daemon.DependencyRequest, 0, len(deps))
	for _, d := range deps {
		req := daemon.DependencyRequest{
			Name:      d.Name,
			Engine:    d.Engine,
			Version:   d.Version,
			Port:      d.DefaultedPort(),
			Dedicated: d.Dedicated,
		}
		if m, ok := migByDep[d.Name]; ok {
			req.Migrations = m.Dir
			req.Seed = m.Seed
		}
		reqs = append(reqs, req)
	}

	results, err := c.ApplyDependencies(context.Background(), service, daemon.ApplyRequest{
		KubeContext:  target.KubeContext,
		Namespace:    target.Namespace,
		Environment:  string(env),
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
			// Report the schema-migration outcome when migrations were declared (FR-010).
			if m := r.Migration; m != nil {
				switch {
				case m.AlreadyCurrent:
					fmt.Printf("  migrations: already current (v%d)\n", m.ToVersion)
				case m.ToVersion < m.FromVersion:
					fmt.Printf("  migrations: rolled back (v%d → v%d)\n", m.FromVersion, m.ToVersion)
				default:
					fmt.Printf("  migrations: applied %d (v%d → v%d)\n", m.Applied, m.FromVersion, m.ToVersion)
				}
			}
			// Report the dev-seed outcome when a seed was declared (FR-010/FR-013).
			if s := r.Seed; s != nil {
				switch {
				case s.SkippedCI:
					fmt.Printf("  seed: skipped (CI)\n")
				case s.Applied:
					fmt.Printf("  seed: seeded\n")
				default:
					fmt.Printf("  seed: already seeded\n")
				}
			}
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
