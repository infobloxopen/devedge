package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge/internal/cluster"
	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/internal/health"
	"github.com/infobloxopen/devedge/pkg/config"
)

// runResourceUp is the shared "bring a resource up" sequencing used by both
// `de project up` and `de compose up`. It resolves the target cluster, provisions
// declared dependencies, optionally deploys the workload, health-gates the routes,
// registers them through the edge, and (when watch) heartbeats the leases.
//
// It operates over the polymorphic config.Resource (ProjectConfig / ServiceConfig
// / Composition all satisfy it), so a Composition's aggregated routes + shared DB
// dependency flow through the exact same path as a Service's — no duplicated
// orchestration. file is the resource file (its dir anchors migration paths).
func runResourceUp(cmd *cobra.Command, res config.Resource, file, envOverride string, deployFlag, watch bool) error {
	out := cmd.OutOrStdout()
	routes, err := res.ToRoutes()
	if err != nil {
		return err
	}
	if len(routes) == 0 {
		fmt.Fprintf(out, "no routes declared in %s\n", file)
	}

	c := newClient()

	// Resolve the target cluster from the environment (FR-001/009) and report
	// where this resource lands (FR-003). Resolution is always done (even for a
	// no-deps resource) so the placement is observable; the cluster is only
	// *ensured* when there is something to provision on it.
	provider := defaultProvider()
	env := cluster.DetectEnvironment(envOverride)
	dedicated := false
	if cp, ok := res.(config.ClusterPreferrer); ok {
		dedicated = cp.ClusterDedicated()
	}
	target := cluster.Resolve(provider, env, res.Project(), dedicated)
	fmt.Fprintf(out, "%s %s %s\n", colorLabel.Sprint("cluster:"), colorHost.Sprint(target.Name), colorLabel.Sprintf("(%s)", clusterMode(target)))

	// Start declared dependencies on the resolved cluster (FR-001/002). Engaged
	// only when the resource declares dependencies — kind: Config and no-deps
	// resources are unaffected (FR-013) and skip cluster ensure.
	if dd, ok := res.(config.DependencyDeclarer); ok && len(dd.Dependencies()) > 0 {
		if err := requireDependencyTools(); err != nil {
			return err
		}
		ensurer := cluster.NewEnsurer(provider)
		if err := ensurer.EnsureCluster(context.Background(), target.Name); err != nil {
			return err
		}
		var migs []config.DependencyMigrations
		if md, ok := res.(config.MigrationDeclarer); ok {
			m, err := md.Migrations(filepath.Dir(file))
			if err != nil {
				return err
			}
			migs = m
		}
		if err := provisionDependencies(c, res.Project(), dd.Dependencies(), migs, env, target); err != nil {
			return err
		}
	}

	// Opt-in: deploy the resource workload into the resolved cluster (005).
	if deployFlag {
		if err := cluster.NewEnsurer(provider).EnsureCluster(context.Background(), target.Name); err != nil {
			return err
		}
		if err := deployWorkload(res, target); err != nil {
			return err
		}
	}

	// Health gate: probe each route's upstream before registering (feature 010).
	var routeEntries []config.RouteEntry
	switch v := res.(type) {
	case *config.ProjectConfig:
		routeEntries = v.Spec.Routes
	case *config.ServiceConfig:
		routeEntries = v.Spec.Routes
	}
	healthyRoutes := make(map[string]bool, len(routeEntries))
	for _, r := range routeEntries {
		if r.Readiness == nil || deployFlag {
			continue
		}
		scheme := "http"
		if r.BackendTLS {
			scheme = "https"
		}
		targetURL := scheme + "://" + r.Upstream + r.Readiness.Path
		fmt.Fprintf(out, "%s %s %s\n",
			colorLabel.Sprint("waiting for"),
			colorHost.Sprint(r.Host),
			colorLabel.Sprintf("(%s)", targetURL),
		)
		prober := &health.HTTPProber{TargetURL: targetURL}
		if r.Readiness.Timeout != "" {
			prober.Timeout, _ = time.ParseDuration(r.Readiness.Timeout)
		}
		if r.Readiness.Interval != "" {
			prober.Interval, _ = time.ParseDuration(r.Readiness.Interval)
		}
		ready, err := prober.Probe(context.Background())
		if err != nil {
			return fmt.Errorf("readiness probe for %s: %w", r.Host, err)
		}
		if !ready {
			fmt.Fprintf(out, "%s readiness timeout (%s) — registering route anyway\n",
				colorWarning.Sprint("warning:"), targetURL)
		}
		healthyRoutes[r.Host] = ready
	}

	var reqs []daemon.RegisterRequest
	for _, r := range routes {
		req := daemon.RegisterRequest{
			Host:        r.Host,
			Upstream:    r.Upstream,
			Protocol:    string(r.Protocol),
			BackendTLS:  r.BackendTLS,
			Path:        r.Path,
			StripPrefix: r.StripPrefix,
			Project:     r.Project,
			Owner:       "project-file",
			TTL:         r.TTL.String(),
			Tile:        r.Tile,
		}
		if err := c.Register(context.Background(), req); err != nil {
			return fmt.Errorf("register %s: %w", r.Host, err)
		}
		suffix := ""
		if healthyRoutes[r.Host] {
			suffix = colorLabel.Sprint(" (healthy)")
		}
		fmt.Fprintf(out, "registered %s %s %s%s\n", colorHost.Sprint(r.Host), colorLabel.Sprint("->"), r.Upstream, suffix)
		reqs = append(reqs, req)
	}

	if !watch {
		return nil
	}

	// Heartbeat loop — renew leases at half the TTL interval.
	interval := 15 * time.Second
	for _, r := range routes {
		if r.TTL > 0 {
			interval = r.TTL / 2
			break
		}
	}

	fmt.Fprintf(out, "Watching with heartbeat every %s (Ctrl-C to stop)\n", interval)
	ctx, cancel := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	c.Heartbeat(ctx, reqs, interval, logger)

	fmt.Fprintln(out, "Stopped. Routes will expire when their TTL elapses.")
	return nil
}
