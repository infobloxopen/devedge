package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge/internal/client"
	"github.com/infobloxopen/devedge/internal/cluster"
	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/internal/version"
	"github.com/infobloxopen/devedge/pkg/config"
)

// clusterMode is the human-readable topology mode for the `cluster: <name> (<mode>)`
// line printed by `de project up` (FR-003).
func clusterMode(t cluster.ClusterTarget) string {
	switch {
	case t.Ephemeral:
		return "ephemeral"
	case t.Dedicated:
		return "dedicated"
	default:
		return "shared dev"
	}
}

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newClient() *client.Client {
	return client.NewDefault()
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "de",
		Short: "Devedge — local development edge router",
	}

	root.AddCommand(
		versionCmd(),
		installCmd(),
		startCmd(),
		stopCmd(),
		doctorCmd(),
		registerCmd(),
		unregisterCmd(),
		renewCmd(),
		lsCmd(),
		statusCmd(),
		inspectCmd(),
		newCmd(),
		ufeCmd(),
		cliCmd(),
		terraformCmd(),
		projectCmd(),
		migrateCmd(),
		composeCmd(),
		cellCmd(),
		clusterCmd(),
		ciCmd(),
		k3dAliasCmd(),
		uiCmd(),
		apiCmd(),
	)

	// WS-023: `de` as the hermetic build authority. These build verbs operate on
	// a service project dir and run the pinned toolchain (internal/toolchain);
	// `de sync` writes the managed Makefile shim. Kept in a separate AddCommand
	// block to minimize reconciliation with the concurrent `de migrate` work.
	root.AddCommand(
		generateCmd(),
		buildCmd(),
		testCmd(),
		lintCmd(),
		imageCmd(),
		syncCmd(),
	)

	applyColoredHelp(root)
	return root
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version.String())
		},
	}
}

func registerCmd() *cobra.Command {
	var project, owner, ttl, protocol, path string
	var backendTLS, stripPrefix bool

	cmd := &cobra.Command{
		Use:   "register HOST UPSTREAM",
		Short: "Register a route",
		Long: `Register a route. For HTTP services, UPSTREAM is a URL like
http://127.0.0.1:3000. For TCP services (databases, gRPC, binary protocols),
use --protocol tcp and specify the backend as host:port.

One host can hold several HTTP routes distinguished by URL path prefix
(--path). A request is matched to the route with the longest matching prefix;
a route with no --path is the host's catch-all. Use --strip-prefix when the
backend serves paths without the prefix (e.g. an "/api" route to a gateway
that answers on "/v1/...").

Examples:
  de register api.foo.dev.test http://127.0.0.1:3000
  de register app.dev.test http://127.0.0.1:3000                       # shell (catch-all)
  de register app.dev.test http://127.0.0.1:8080 --path /api --strip-prefix
  de register postgres.foo.dev.test 127.0.0.1:5432 --protocol tcp
  de register redis.foo.dev.test 127.0.0.1:6379 --protocol tcp
  de register secure-db.foo.dev.test 127.0.0.1:5432 --protocol tcp --backend-tls`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			return c.Register(context.Background(), daemon.RegisterRequest{
				Host:        args[0],
				Upstream:    args[1],
				Protocol:    protocol,
				BackendTLS:  backendTLS,
				Path:        path,
				StripPrefix: stripPrefix,
				Project:     project,
				Owner:       owner,
				TTL:         ttl,
			})
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&owner, "owner", "", "owner identifier")
	cmd.Flags().StringVar(&ttl, "ttl", "", "lease TTL (e.g. 30s)")
	cmd.Flags().StringVar(&protocol, "protocol", "", "routing protocol: http (default) or tcp")
	cmd.Flags().BoolVar(&backendTLS, "backend-tls", false, "use TLS to connect to upstream")
	cmd.Flags().StringVar(&path, "path", "", "URL path prefix; empty is the host's catch-all")
	cmd.Flags().BoolVar(&stripPrefix, "strip-prefix", false, "strip --path from the request path before forwarding")
	return cmd
}

func unregisterCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "unregister HOST",
		Short: "Remove a route",
		Long: `Remove routes for HOST. Without --path this removes ALL routes
registered under HOST; --path removes only that one (host, path) route.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			return c.Deregister(context.Background(), args[0], path)
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "remove only the route registered under this path prefix")
	return cmd
}

func renewCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "renew HOST",
		Short: "Renew a route's lease",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			// Lookup the current route and re-register it to renew the lease.
			// --path selects a specific (host, path) route when the host holds
			// several; without it, the host's catch-all is renewed.
			route, err := c.Lookup(context.Background(), args[0], path)
			if err != nil {
				return fmt.Errorf("lookup %s: %w", args[0], err)
			}
			err = c.Register(context.Background(), daemon.RegisterRequest{
				Host:        route.Host,
				Upstream:    route.Upstream,
				Protocol:    string(route.Protocol),
				BackendTLS:  route.BackendTLS,
				Path:        route.Path,
				StripPrefix: route.StripPrefix,
				Project:     route.Project,
				Owner:       route.Owner,
				TTL:         route.TTL.String(),
			})
			if err != nil {
				return err
			}
			fmt.Printf("renewed %s\n", colorHost.Sprint(args[0]))
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "renew the route registered under this path prefix")
	return cmd
}

func lsCmd() *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List active routes",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			routes, err := c.List(context.Background())
			if err != nil {
				return err
			}

			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(routes)
			}

			if len(routes) == 0 {
				fmt.Println(colorWarning.Sprint("No active routes."))
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, colorHeader.Sprint("HOST\tUPSTREAM\tPROTO\tPROJECT\tSOURCE"))
			for _, r := range routes {
				proto := string(r.EffectiveProtocol())
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", colorHost.Sprint(r.Host), r.Upstream, proto, r.Project, r.Source)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "output as JSON")
	return cmd
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			st, err := c.Status(context.Background())
			if err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(st)
		},
	}
}

func inspectCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "inspect HOST",
		Short: "Show details for a route",
		Long: `Show details for a route on HOST. When a host holds several routes,
--path selects one by its path prefix; without it the host's catch-all (or its
"/" match) is shown.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			route, err := c.Lookup(context.Background(), args[0], path)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(route)
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "inspect the route registered under this path prefix")
	return cmd
}

func projectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage project routes",
	}
	cmd.AddCommand(projectUpCmd(), projectDownCmd(), projectChartCmd(), projectInitCmd())
	return cmd
}

func projectUpCmd() *cobra.Command {
	var file string
	var watch bool
	var envOverride string
	var deployFlag bool

	cmd := &cobra.Command{
		Use:   "up",
		Short: "Register all routes from devedge.yaml",
		Long: `Register all routes from devedge.yaml.

With --watch, the command stays running and sends heartbeats to renew
leases. This keeps routes alive for as long as the project is active.
Press Ctrl-C to stop and let leases expire naturally.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := config.LoadResource(file)
			if err != nil {
				return err
			}
			// Shared bring-up sequencing (cluster resolve + dependency provision +
			// optional deploy + health gate + route register + heartbeat). The same
			// path serves `de compose up` over a Composition resource.
			return runResourceUp(cmd, res, file, envOverride, deployFlag, watch)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "devedge.yaml", "project config file")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "stay running and send lease heartbeats")
	cmd.Flags().StringVar(&envOverride, "env", "", "environment override: dev|ci|ephemeral (default: auto-detect from CI)")
	cmd.Flags().BoolVar(&deployFlag, "deploy", false, "also deploy the service workload into the resolved cluster (opt-in; default is local-run)")
	return cmd
}

func projectDownCmd() *cobra.Command {
	var project string
	var clean bool
	var file string

	cmd := &cobra.Command{
		Use:   "down [PROJECT]",
		Short: "Remove all routes for a project",
		// Footprint-only (FR-005): down releases ONLY the requesting project's
		// routes and dependency bindings on whatever cluster they were applied to.
		// It never deletes the shared cluster or another project's footprint. The
		// one destructive exception is a project on its OWN dedicated cluster:
		// `--clean` then removes that dedicated cluster (US4 AS3) — never the shared.
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load the local config (best-effort) to learn the project name and
			// whether it opted into a dedicated cluster.
			var res config.Resource
			if loaded, err := config.LoadResource(file); err == nil {
				res = loaded
			}
			if len(args) > 0 {
				project = args[0]
			}
			if project == "" {
				if res == nil {
					return fmt.Errorf("project name required (pass as argument or use %s)", file)
				}
				project = res.Project()
			}

			c := newClient()
			n, err := c.DeregisterProject(context.Background(), project)
			if err != nil {
				return err
			}
			fmt.Printf("removed %d route(s) for project %s\n", n, colorHost.Sprint(project))

			// Release any declared dependencies for this service. Default keeps
			// data (FR-005/FR-007); --clean drops this service's data (FR-006).
			// A no-op for projects that declared none.
			if err := c.ReleaseDependencies(context.Background(), project, clean); err != nil {
				return fmt.Errorf("release dependencies: %w", err)
			}
			if clean {
				fmt.Printf("dropped dependency data for project %s\n", colorHost.Sprint(project))
			}

			// Remove a deployed workload, if any (005). Footprint-only and a no-op
			// for never-deployed projects; best-effort so down still succeeds if the
			// cluster is already gone.
			if res != nil && project == res.Project() && workloadOf(res) != nil {
				provider := defaultProvider()
				target := cluster.Resolve(provider, cluster.DetectEnvironment(""), project, isDedicated(res))
				if err := removeWorkload(res, target); err != nil {
					fmt.Printf("%s could not remove workload for %s: %v\n", colorWarning.Sprint("warning:"), colorHost.Sprint(project), err)
				} else {
					fmt.Printf("removed workload for project %s\n", colorHost.Sprint(project))
				}
			}

			// Destructive teardown of a DEDICATED cluster: the cluster is this
			// project's alone, so `--clean` removes it. Guarded so it only fires for
			// the local config's own dedicated project — never the shared cluster
			// and never another project (FR-005/FR-010/US4 AS3).
			if clean && res != nil && project == res.Project() && isDedicated(res) {
				provider := defaultProvider()
				target := cluster.Resolve(provider, cluster.DetectEnvironment(""), project, true)
				if err := cluster.NewEnsurer(provider).Teardown(target.Name); err != nil {
					return fmt.Errorf("remove dedicated cluster %q: %w", target.Name, err)
				}
				fmt.Printf("removed dedicated cluster %s\n", colorHost.Sprint(target.Name))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&clean, "clean", false, "also destroy this project's dependency data; for a dedicated-cluster project, remove its cluster")
	cmd.Flags().StringVarP(&project, "project", "p", "", "project name")
	cmd.Flags().StringVarP(&file, "file", "f", "devedge.yaml", "project config file (to detect a dedicated cluster)")
	return cmd
}

// isDedicated reports whether a loaded resource opted into a dedicated cluster.
func isDedicated(res config.Resource) bool {
	cp, ok := res.(config.ClusterPreferrer)
	return ok && cp.ClusterDedicated()
}
