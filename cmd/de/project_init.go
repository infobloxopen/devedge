package main

import (
	"fmt"

	"github.com/infobloxopen/devedge/internal/scaffold"
	"github.com/spf13/cobra"
)

func projectInitCmd() *cobra.Command {
	var dirFlag, moduleFlag, hostFlag string

	cmd := &cobra.Command{
		Use:   "init NAME",
		Short: "Scaffold a new service project",
		Long: `Scaffold a new service project ready for 'de project up'.

The generated project contains everything needed to develop and run a
devedge-managed service:

  - devedge.yaml       devedge Service config (routes, dependencies)
  - proto/             annotated .proto with fail-closed authz annotations
  - authz/             generated fail-closed authz enforcement server
  - migrations/        database migration stubs (SQL + seed)
  - Dockerfile         multi-stage build for the service

After scaffolding, the project is immediately usable:

  cd NAME
  make generate        # run protoc + authz codegen
  de project up        # register routes and start dependencies

For a full walk-through of the generated layout see AGENTS.md inside the
generated project.

Flags:
  --dir     parent directory to create the project in (default: current dir)
  --module  Go module path for the generated go.mod (default: service name)
  --host    dev edge host for devedge.yaml + the curl examples (default: app.dev.test)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := scaffold.ValidateName(name); err != nil {
				return err
			}
			if err := scaffold.Render(scaffold.Params{
				Name:      name,
				Module:    moduleFlag,
				ParentDir: dirFlag,
				Host:      hostFlag,
			}); err != nil {
				return err
			}
			fmt.Printf("%s %s\n", colorLabel.Sprint("scaffolded"), colorHost.Sprint(name))
			fmt.Printf("\n%s\n", colorHeader.Sprint("Next steps:"))
			fmt.Printf("  cd %s\n", name)
			fmt.Printf("  make generate\n")
			fmt.Printf("  de project up\n")
			fmt.Printf("  %s\n", colorLabel.Sprint("# see AGENTS.md for the full walk-through"))
			return nil
		},
	}
	cmd.Flags().StringVar(&dirFlag, "dir", ".", "parent directory to create the project in")
	cmd.Flags().StringVar(&moduleFlag, "module", "", "Go module path (default: service name)")
	cmd.Flags().StringVar(&hostFlag, "host", scaffold.DefaultHost, "dev edge host for devedge.yaml + the curl examples")
	return cmd
}
