package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge-sdk/cells"
	icell "github.com/infobloxopen/devedge/internal/cell"
	"github.com/infobloxopen/devedge/internal/helm"
	"github.com/infobloxopen/devedge/pkg/config"
)

// defaultRoutesFile is the default path for the cell routing table.
// Override with --routes-file.
const defaultRoutesFile = ".devedge/cells/routes.json"

func cellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cell",
		Short: "Manage cell-based deployments and tenant routing",
		Long: `Manage cell-based deployments: create and tear down cell instances,
assign tenants to cells, move tenants between cells, and rebalance
the tenant population across a cell fleet.

A cell is a version-pinned deployment of a service. Tenants are routed
to exactly one cell; the routing table (a local JSON file) is the
single source of truth for the current assignment.`,
	}
	cmd.AddCommand(
		cellCreateCmd(),
		cellDownCmd(),
		cellStatusCmd(),
		cellAssignCmd(),
		cellMoveCmd(),
		cellRebalanceCmd(),
	)
	return cmd
}

// cellCreateCmd renders and installs a per-cell "service" chart instance.
func cellCreateCmd() *cobra.Command {
	var (
		service  string
		image    string
		version  string
		cellID   string
		replicas int
		fromFile string
		dryRun   bool
		ns       string
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Deploy a cell instance (helm upgrade --install)",
		Long: `Render and install a per-cell "service" Helm chart instance.

A cell instance is a version-pinned deployment of a service identified by
<service>-<cell> (e.g. myapi-canary). Provide flags or a 'kind: Cell' file.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var cfg *config.Cell

			if fromFile != "" {
				data, err := os.ReadFile(fromFile)
				if err != nil {
					return fmt.Errorf("read cell file: %w", err)
				}
				cfg, err = config.ParseCell(data)
				if err != nil {
					return err
				}
			} else {
				if service == "" {
					return fmt.Errorf("--service is required (or use --from-file)")
				}
				if cellID == "" {
					return fmt.Errorf("--cell is required (or use --from-file)")
				}
				if image == "" && version == "" {
					return fmt.Errorf("--image or --version is required (or use --from-file)")
				}
				img := image
				if img == "" {
					img = service + ":" + version
				}
				cfg = &config.Cell{
					APIVersion: config.APIVersion,
					Kind:       config.KindCell,
					Metadata:   config.ObjectMeta{Name: service + "-" + cellID},
					Spec: config.CellSpec{
						Service:  service,
						Image:    img,
						Replicas: replicas,
						Cell:     cellID,
					},
				}
			}
			if cfg.Spec.Replicas <= 0 {
				cfg.Spec.Replicas = 1
			}

			img := cfg.Spec.Image
			if img == "" && cfg.Spec.Version != "" {
				img = cfg.Spec.Service + ":" + cfg.Spec.Version
			}

			releaseName := icell.ReleaseName(cfg.Spec.Service, cfg.Spec.Cell)
			values := icell.CellValues(cfg.Spec.Service, cfg.Spec.Cell, img, cfg.Spec.Replicas)
			namespace := ns
			if namespace == "" {
				namespace = helm.DefaultNamespace
			}

			h := helm.New("")
			if dryRun {
				out, err := h.Render(cmd.Context(), helm.ChartService, releaseName, namespace, values)
				if err != nil {
					return fmt.Errorf("render: %w", err)
				}
				fmt.Fprint(cmd.OutOrStdout(), out)
				return nil
			}

			if err := requireTools("helm"); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s cell %s (release: %s)\n",
				colorLabel.Sprint("creating"), colorHost.Sprint(cfg.Spec.Cell), releaseName)
			if err := h.Install(cmd.Context(), helm.ChartService, releaseName, namespace, values); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", colorSuccess.Sprint("created"), colorHost.Sprint(releaseName))
			return nil
		},
	}

	cmd.Flags().StringVar(&service, "service", "", "service name")
	cmd.Flags().StringVar(&image, "image", "", "full container image reference")
	cmd.Flags().StringVar(&version, "version", "", "image version tag (used when --image is not set)")
	cmd.Flags().StringVar(&cellID, "cell", "", "cell ID (e.g. canary, v2)")
	cmd.Flags().IntVar(&replicas, "replicas", 1, "replica count")
	cmd.Flags().StringVarP(&fromFile, "from-file", "f", "", "cell config file (kind: Cell)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "render chart and print; do not install")
	cmd.Flags().StringVarP(&ns, "namespace", "n", "", "Kubernetes namespace (default: devedge-deps)")
	return cmd
}

// cellDownCmd uninstalls a cell release and optionally purges its routes.
func cellDownCmd() *cobra.Command {
	var (
		service     string
		cellID      string
		purgeRoutes bool
		routesFile  string
		ns          string
	)

	cmd := &cobra.Command{
		Use:   "down",
		Short: "Uninstall a cell instance",
		Long: `Uninstall the Helm release for a cell instance (<service>-<cell>).

With --purge-routes, every tenant route whose active cell matches <cell>
is deleted from the routing table, reverting those tenants to the
fail-safe default cell.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if service == "" {
				return fmt.Errorf("--service is required")
			}
			if cellID == "" {
				return fmt.Errorf("--cell is required")
			}
			if err := requireTools("helm"); err != nil {
				return err
			}

			releaseName := icell.ReleaseName(service, cellID)
			namespace := ns
			if namespace == "" {
				namespace = helm.DefaultNamespace
			}

			h := helm.New("")
			fmt.Fprintf(cmd.OutOrStdout(), "%s cell %s (release: %s)\n",
				colorLabel.Sprint("removing"), colorHost.Sprint(cellID), releaseName)
			if err := h.Uninstall(cmd.Context(), releaseName, namespace); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", colorSuccess.Sprint("removed"), colorHost.Sprint(releaseName))

			if purgeRoutes {
				rf := resolveRoutesFile(routesFile)
				if err := os.MkdirAll(filepath.Dir(rf), 0o755); err != nil {
					return fmt.Errorf("create routes dir: %w", err)
				}
				table := cells.NewFileTable(rf)
				routes, err := table.List(cmd.Context())
				if err != nil {
					return fmt.Errorf("list routes: %w", err)
				}
				purged := 0
				for _, r := range routes {
					if r.ActiveCell == cellID {
						if err := table.Delete(cmd.Context(), r.TenantID); err != nil {
							fmt.Fprintf(cmd.OutOrStdout(), "%s purge route for tenant %s: %v\n",
								colorWarning.Sprint("warning:"), r.TenantID, err)
						} else {
							purged++
						}
					}
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s purged %d route(s) for cell %s\n",
					colorSuccess.Sprint("ok"), purged, colorHost.Sprint(cellID))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&service, "service", "", "service name")
	cmd.Flags().StringVar(&cellID, "cell", "", "cell ID")
	cmd.Flags().BoolVar(&purgeRoutes, "purge-routes", false, "delete routing table entries for this cell (tenants revert to default)")
	cmd.Flags().StringVar(&routesFile, "routes-file", "", "routing table file (default: "+defaultRoutesFile+")")
	cmd.Flags().StringVarP(&ns, "namespace", "n", "", "Kubernetes namespace (default: devedge-deps)")
	return cmd
}

// cellStatusCmd prints routing table state and (best-effort) deployed releases.
func cellStatusCmd() *cobra.Command {
	var (
		routesFile string
		service    string
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show tenant routes and per-tenant budget",
		Long: `List tenant routes from the routing table, grouped by cell.
Shows each tenant's state, epoch, and remaining unavailability budget.

Deployed Helm releases are listed if a cluster is reachable (best-effort;
skipped cleanly when not reachable).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rf := resolveRoutesFile(routesFile)
			if _, err := os.Stat(rf); os.IsNotExist(err) {
				fmt.Fprintf(cmd.OutOrStdout(), "%s routes file %s does not exist (no tenants assigned yet)\n",
					colorWarning.Sprint("note:"), rf)
				return nil
			}

			table := cells.NewFileTable(rf)
			routes, err := table.List(cmd.Context())
			if err != nil {
				return fmt.Errorf("list routes: %w", err)
			}

			if len(routes) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), colorWarning.Sprint("No tenant routes."))
				return nil
			}

			budget := cells.NewBudgetMeter()

			// Group by cell.
			byCells := map[string][]cells.TenantRoute{}
			for _, r := range routes {
				if service != "" && !strings.HasSuffix(r.ActiveCell, service) {
					// service filter: best-effort (cell IDs don't encode service name)
				}
				byCells[r.ActiveCell] = append(byCells[r.ActiveCell], r)
			}
			cellNames := make([]string, 0, len(byCells))
			for c := range byCells {
				cellNames = append(cellNames, c)
			}
			sort.Strings(cellNames)

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			for _, c := range cellNames {
				fmt.Fprintf(w, "%s\n", colorHost.Sprint("cell: "+c))
				fmt.Fprintf(w, "  %s\n", colorHeader.Sprint("TENANT\tEPOCH\tSTATE\tBUDGET_REMAINING"))
				rs := byCells[c]
				sort.Slice(rs, func(i, j int) bool { return rs[i].TenantID < rs[j].TenantID })
				for _, r := range rs {
					rem := budget.Remaining(r.TenantID)
					fmt.Fprintf(w, "  %s\t%d\t%s\t%s\n",
						r.TenantID, r.RouteEpoch, r.State.String(), rem.Round(time.Millisecond))
				}
			}
			if err := w.Flush(); err != nil {
				return err
			}

			// Best-effort: list deployed cell releases via helm ls.
			if requireTools("helm") == nil {
				fmt.Fprintln(cmd.OutOrStdout())
				fmt.Fprintln(cmd.OutOrStdout(), colorLabel.Sprint("deployed releases (best-effort):"))
				if out, err := helmLsList(cmd.Context()); err == nil {
					fmt.Fprint(cmd.OutOrStdout(), out)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "%s cluster not reachable, skipping release list\n",
						colorWarning.Sprint("note:"))
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&routesFile, "routes-file", "", "routing table file (default: "+defaultRoutesFile+")")
	cmd.Flags().StringVar(&service, "service", "", "filter output by service name (informational)")
	return cmd
}

// cellAssignCmd assigns a single tenant to a cell.
func cellAssignCmd() *cobra.Command {
	var (
		tenantID   string
		cellID     string
		routesFile string
		operator   string
	)

	cmd := &cobra.Command{
		Use:   "assign",
		Short: "Assign a tenant to a cell",
		RunE: func(cmd *cobra.Command, args []string) error {
			if tenantID == "" {
				return fmt.Errorf("--tenant is required")
			}
			if cellID == "" {
				return fmt.Errorf("--cell is required")
			}

			rf := resolveRoutesFile(routesFile)
			if err := os.MkdirAll(filepath.Dir(rf), 0o755); err != nil {
				return fmt.Errorf("create routes dir: %w", err)
			}
			table := cells.NewFileTable(rf)
			ctrl := cells.NewMoveController(table)

			op := operator
			if op == "" {
				op = "de-cell-assign"
			}

			if err := ctrl.Assign(cmd.Context(), tenantID, cellID, op); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s tenant %s → cell %s\n",
				colorSuccess.Sprint("assigned"), colorHost.Sprint(tenantID), colorHost.Sprint(cellID))
			return nil
		},
	}

	cmd.Flags().StringVar(&tenantID, "tenant", "", "tenant ID")
	cmd.Flags().StringVar(&cellID, "cell", "", "target cell ID")
	cmd.Flags().StringVar(&routesFile, "routes-file", "", "routing table file (default: "+defaultRoutesFile+")")
	cmd.Flags().StringVar(&operator, "operator", "", "operator identifier (for audit)")
	return cmd
}

// cellMoveCmd moves a tenant from one cell to another.
func cellMoveCmd() *cobra.Command {
	var (
		tenantID    string
		toCell      string
		fromCell    string
		force       bool
		drainWindow time.Duration
		routesFile  string
		operator    string
	)

	cmd := &cobra.Command{
		Use:   "move",
		Short: "Move a tenant to a different cell",
		Long: `Move a tenant from its current cell to a target cell.

A timed drain window is observed before committing the cut; the epoch
fence and in-cluster admission gates enforce correctness. The budget
gate refuses the move if the tenant's monthly unavailability allowance
is exhausted — use --force to override.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if tenantID == "" {
				return fmt.Errorf("--tenant is required")
			}
			if toCell == "" {
				return fmt.Errorf("--to is required")
			}

			rf := resolveRoutesFile(routesFile)
			if err := os.MkdirAll(filepath.Dir(rf), 0o755); err != nil {
				return fmt.Errorf("create routes dir: %w", err)
			}
			table := cells.NewFileTable(rf)

			window := drainWindow
			if window <= 0 {
				window = 5 * time.Second
			}

			drainer := icell.NewTimedDrainer(window)
			meter := cells.NewBudgetMeter()
			ctrl := cells.NewMoveController(table,
				cells.WithDrainer(drainer),
				cells.WithDrainDeadline(window+2*time.Second),
				cells.WithBudgetMeter(meter),
			)

			// Resolve fromCell when not provided.
			from := fromCell
			if from == "" {
				cur, err := table.Get(cmd.Context(), tenantID)
				if err != nil {
					return fmt.Errorf("resolve current cell: %w", err)
				}
				from = cur.ActiveCell
			}

			op := operator
			if op == "" {
				op = "de-cell-move"
			}

			plan := cells.MovePlan{
				TenantID: tenantID,
				FromCell: from,
				ToCell:   toCell,
				Operator: op,
				Force:    force,
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%s tenant %s: %s → %s (drain window: %s)\n",
				colorLabel.Sprint("moving"), colorHost.Sprint(tenantID),
				colorHost.Sprint(from), colorHost.Sprint(toCell), window)

			if err := ctrl.Move(cmd.Context(), plan); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s tenant %s moved to cell %s\n",
				colorSuccess.Sprint("ok"), colorHost.Sprint(tenantID), colorHost.Sprint(toCell))
			return nil
		},
	}

	cmd.Flags().StringVar(&tenantID, "tenant", "", "tenant ID")
	cmd.Flags().StringVar(&toCell, "to", "", "target cell ID")
	cmd.Flags().StringVar(&fromCell, "from", "", "source cell ID (auto-detected from routing table when not set)")
	cmd.Flags().BoolVar(&force, "force", false, "bypass budget gate")
	cmd.Flags().DurationVar(&drainWindow, "drain-window", 5*time.Second, "time to wait for in-flight work to drain")
	cmd.Flags().StringVar(&routesFile, "routes-file", "", "routing table file (default: "+defaultRoutesFile+")")
	cmd.Flags().StringVar(&operator, "operator", "", "operator identifier (for audit)")
	return cmd
}

// cellRebalanceCmd redistributes tenants across a set of cells using a policy.
func cellRebalanceCmd() *cobra.Command {
	var (
		cellList      string
		policy        string
		maxConcurrent int
		routesFile    string
		operator      string
	)

	cmd := &cobra.Command{
		Use:   "rebalance",
		Short: "Redistribute tenants across cells using a placement policy",
		Long: `Redistribute tenants across cells.

Reads the current tenant list from the routing table, builds a placement
plan using the chosen policy (round-robin, least-loaded, sticky), and
drives moves via the Campaign API. Budget-aware: over-budget tenants are
skipped (shown in output).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cellList == "" {
				return fmt.Errorf("--cells is required (comma-separated list, e.g. a,b,c)")
			}

			cellIDs := strings.Split(cellList, ",")
			for i, c := range cellIDs {
				cellIDs[i] = strings.TrimSpace(c)
			}
			if len(cellIDs) == 0 {
				return fmt.Errorf("--cells must list at least one cell")
			}

			rf := resolveRoutesFile(routesFile)
			if err := os.MkdirAll(filepath.Dir(rf), 0o755); err != nil {
				return fmt.Errorf("create routes dir: %w", err)
			}
			table := cells.NewFileTable(rf)

			routes, err := table.List(cmd.Context())
			if err != nil {
				return fmt.Errorf("list routes: %w", err)
			}

			tenantIDs := make([]string, 0, len(routes))
			for _, r := range routes {
				tenantIDs = append(tenantIDs, r.TenantID)
			}
			if len(tenantIDs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), colorWarning.Sprint("No tenants in routing table to rebalance."))
				return nil
			}

			pl := resolvePlacementPolicy(policy, cellIDs)

			plan, err := cells.PlanFromPolicy(cmd.Context(), tenantIDs, cellIDs, pl)
			if err != nil {
				return fmt.Errorf("build plan: %w", err)
			}

			if maxConcurrent > 0 {
				plan.MaxConcurrent = maxConcurrent
			}
			op := operator
			if op == "" {
				op = "de-cell-rebalance"
			}
			plan.Operator = op

			meter := cells.NewBudgetMeter()
			ctrl := cells.NewMoveController(table, cells.WithBudgetMeter(meter))
			campaign := cells.NewCampaign(ctrl)

			fmt.Fprintf(cmd.OutOrStdout(), "%s %d tenant(s) across %d cell(s) (policy: %s, concurrency: %d)\n",
				colorLabel.Sprint("rebalancing"), len(tenantIDs), len(cellIDs),
				policy, plan.MaxConcurrent)

			result, err := campaign.Run(cmd.Context(), plan)

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "%s\t%d\n", colorSuccess.Sprint("moved:"), len(result.Moved))
			fmt.Fprintf(w, "%s\t%d\n", colorLabel.Sprint("skipped:"), len(result.Skipped))
			fmt.Fprintf(w, "%s\t%d\n", colorWarning.Sprint("failed:"), len(result.Failed))
			_ = w.Flush()

			for tid, reason := range result.Skipped {
				fmt.Fprintf(cmd.OutOrStdout(), "  skipped %s: %s\n", colorHost.Sprint(tid), reason)
			}
			for tid, ferr := range result.Failed {
				fmt.Fprintf(cmd.OutOrStdout(), "  failed  %s: %v\n", colorHost.Sprint(tid), ferr)
			}

			if len(result.Moved) > 0 {
				fmt.Fprintln(cmd.OutOrStdout(), colorLabel.Sprint("budget remaining after rebalance:"))
				for _, tid := range result.Moved {
					rem := meter.Remaining(tid)
					fmt.Fprintf(cmd.OutOrStdout(), "  %s: %s\n", tid, rem.Round(time.Millisecond))
				}
			}

			return err
		},
	}

	cmd.Flags().StringVar(&cellList, "cells", "", "comma-separated list of cell IDs (e.g. a,b,c)")
	cmd.Flags().StringVar(&policy, "policy", "round-robin", "placement policy: round-robin | least-loaded | sticky")
	cmd.Flags().IntVar(&maxConcurrent, "max-concurrent", 1, "maximum simultaneous moves (<=1 ⇒ sequential)")
	cmd.Flags().StringVar(&routesFile, "routes-file", "", "routing table file (default: "+defaultRoutesFile+")")
	cmd.Flags().StringVar(&operator, "operator", "", "operator identifier (for audit)")
	return cmd
}

// resolveRoutesFile returns the effective routes file path.
func resolveRoutesFile(override string) string {
	if override != "" {
		return override
	}
	return defaultRoutesFile
}

// resolvePlacementPolicy returns the PlacementPolicy for the named policy.
func resolvePlacementPolicy(policy string, cellIDs []string) cells.PlacementPolicy {
	switch policy {
	case "sticky":
		defaultCell := cells.DefaultCellID
		if len(cellIDs) > 0 {
			defaultCell = cellIDs[0]
		}
		return cells.StickyDefaultPolicy(defaultCell)
	case "least-loaded":
		return cells.LeastLoadedPolicy(func(_ string) int { return 0 })
	default: // "round-robin"
		return cells.RoundRobinPolicy()
	}
}

// helmLsList runs `helm list` and returns its stdout (best-effort).
func helmLsList(ctx context.Context) (string, error) {
	var out bytes.Buffer
	c := exec.CommandContext(ctx, "helm", "list", "--all-namespaces")
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}
