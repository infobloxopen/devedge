package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge/internal/sdkscaffold"
)

// newCmd is `de new`, the noun-verb entry point for scaffolding. Today it has
// one subcommand, `service`; the shape leaves room for more later.
func newCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Scaffold a new artifact (service, ...)",
	}
	cmd.AddCommand(newServiceCmd())
	return cmd
}

// newServiceCmd is `de new service <name>` — the long-promised `de init` from
// the product vision, delivered as a thin driver over the devedge-sdk
// apx-native scaffold. devedge orchestrates; devedge-sdk does the heavy
// scaffolding (apx + buf wiring, proto, generated models, server). After the
// scaffold succeeds, `de` emits a devedge.yaml routing the service's HTTP/JSON
// gateway through the local edge — the devedge-native value-add.
func newServiceCmd() *cobra.Command {
	var resource, backend, dir string

	cmd := &cobra.Command{
		Use:   "service NAME [-- DEVEDGE_SDK_FLAGS...]",
		Short: "Scaffold a new apx-native service and route it through the edge",
		Long: `Scaffold a new apx-native, authz-gated, persisting service.

This is a thin driver over the devedge-sdk scaffold: it forwards to
'devedge-sdk new service' for the heavy lifting (apx + buf wiring, an
annotated proto, generated models + repository + server), then emits a
devedge.yaml routing the service's HTTP/JSON gateway through the local
edge so 'de project up' serves it over stable HTTPS.

Requires the devedge-sdk binary on PATH:

    go install github.com/infobloxopen/devedge-sdk/cmd/devedge-sdk@` + sdkscaffold.SDKInstallVersion + `

Flags after a '--' separator are forwarded verbatim to devedge-sdk
(e.g. --module, --org, --force, --no-generate).

Examples:
  de new service orders --resource Order
  de new service notes --resource Note --backend ent
  de new service orders --resource Order --dir ./services/orders -- --module github.com/acme/orders --force`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// args[0] is NAME; anything after a '--' separator is passthrough.
			name := args[0]
			var passthrough []string
			if dash := cmd.ArgsLenAtDash(); dash >= 0 {
				// args[dash:] are the tokens that followed '--'.
				passthrough = args[dash:]
			} else if len(args) > 1 {
				return fmt.Errorf("unexpected arguments %v; forward extra devedge-sdk flags after a '--' separator", args[1:])
			}

			opts := sdkscaffold.Options{
				Name:        name,
				Resource:    resource,
				Backend:     backend,
				Dir:         dir,
				Passthrough: passthrough,
			}

			res, err := sdkscaffold.Run(cmd.Context(), sdkscaffold.DefaultRunner, opts, cmd.OutOrStdout(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "\n%s %s\n", colorSuccess.Sprint("scaffolded"), colorHost.Sprint(name))
			fmt.Fprintf(out, "%s %s %s %s\n", colorLabel.Sprint("routed"), colorHost.Sprint(res.GatewayHost), colorLabel.Sprint("->"), res.Upstream)
			fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("wrote"), res.DevedgeYAML)
			fmt.Fprintf(out, "\n%s\n", colorHeader.Sprint("Next steps:"))
			fmt.Fprintf(out, "  cd %s\n", res.Dir)
			fmt.Fprintf(out, "  make test                 %s\n", colorLabel.Sprint("# build + boot + smoke (devedge-sdk scaffold)"))
			fmt.Fprintf(out, "  de project up             %s\n", colorLabel.Sprint("# register the route through the edge"))
			fmt.Fprintf(out, "  %s\n", colorLabel.Sprintf("# then: https://%s/v1/...", res.GatewayHost))
			return nil
		},
	}
	cmd.Flags().StringVar(&resource, "resource", "", "singular resource type name (e.g. Order); devedge-sdk defaults it from NAME")
	cmd.Flags().StringVar(&backend, "backend", "", "persistence backend: gorm (default) or ent")
	cmd.Flags().StringVar(&dir, "dir", "", "target directory (defaults to the service name)")
	return cmd
}
