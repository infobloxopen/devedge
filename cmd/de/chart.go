package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/infobloxopen/devedge/internal/depruntime"
	"github.com/infobloxopen/devedge/internal/helm"
	"github.com/infobloxopen/devedge/pkg/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// projectChartCmd implements `de project chart`: emit a Helm chart for the
// service and its declared dependencies (FR-010). Dependencies are expressed
// abstractly (FR-011); the chart is emitted, not deployed.
func projectChartCmd() *cobra.Command {
	var file, out string
	cmd := &cobra.Command{
		Use:   "chart",
		Short: "Generate a Helm chart for the service and its declared dependencies",
		Long: `Generate a Helm chart for the service declared in devedge.yaml and its
dependencies. Dependencies are expressed as abstract claims (a shared logical
database in dev; a dedicated instance via the platform DB abstraction in a real
cluster). The chart is emitted only — it is not deployed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireTools("helm"); err != nil {
				return err
			}
			res, err := config.LoadResource(file)
			if err != nil {
				return err
			}

			values := map[string]any{
				"service": map[string]any{
					"name":     res.Project(),
					"image":    "",
					"port":     8080,
					"replicas": 1,
				},
				"dependencies": chartDependencies(res),
			}

			if err := helm.WriteChart(helm.ChartService, out); err != nil {
				return fmt.Errorf("write chart: %w", err)
			}
			data, err := yaml.Marshal(values)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(out, "values.yaml"), data, 0o644); err != nil {
				return fmt.Errorf("write values.yaml: %w", err)
			}

			if err := helm.New("").Lint(context.Background(), out); err != nil {
				return fmt.Errorf("generated chart failed helm lint: %w", err)
			}

			fmt.Printf("wrote Helm chart for %s to %s (passes helm lint)\n", colorHost.Sprint(res.Project()), out)
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "devedge.yaml", "project config file")
	cmd.Flags().StringVarP(&out, "out", "o", "chart", "output directory for the chart")
	return cmd
}

// chartDependencies builds the chart's `dependencies` values from a Service's
// declared dependencies, deriving the per-dependency env var name.
func chartDependencies(res config.Resource) []map[string]any {
	dd, ok := res.(config.DependencyDeclarer)
	if !ok {
		return []map[string]any{}
	}
	deps := dd.Dependencies()
	engineCount := map[string]int{}
	for _, d := range deps {
		engineCount[d.Engine]++
	}
	out := make([]map[string]any, 0, len(deps))
	for _, d := range deps {
		ambiguous := engineCount[d.Engine] > 1
		out = append(out, map[string]any{
			"name":    d.Name,
			"engine":  d.Engine,
			"version": d.Version,
			"envVar":  depruntime.EnvVarName(depruntime.Engine(d.Engine), d.Name, ambiguous),
		})
	}
	return out
}
