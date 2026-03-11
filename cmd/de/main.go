package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge/internal/client"
	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/internal/version"
	"github.com/infobloxopen/devedge/pkg/config"
)

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
		registerCmd(),
		unregisterCmd(),
		lsCmd(),
		statusCmd(),
		inspectCmd(),
		projectCmd(),
	)

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
	var project, owner, ttl string

	cmd := &cobra.Command{
		Use:   "register HOST UPSTREAM",
		Short: "Register a route",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			return c.Register(context.Background(), daemon.RegisterRequest{
				Host:     args[0],
				Upstream: args[1],
				Project:  project,
				Owner:    owner,
				TTL:      ttl,
			})
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&owner, "owner", "", "owner identifier")
	cmd.Flags().StringVar(&ttl, "ttl", "", "lease TTL (e.g. 30s)")
	return cmd
}

func unregisterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unregister HOST",
		Short: "Remove a route",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			return c.Deregister(context.Background(), args[0])
		},
	}
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
				fmt.Println("No active routes.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "HOST\tUPSTREAM\tPROJECT\tSOURCE")
			for _, r := range routes {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Host, r.Upstream, r.Project, r.Source)
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
	return &cobra.Command{
		Use:   "inspect HOST",
		Short: "Show details for a route",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			route, err := c.Lookup(context.Background(), args[0])
			if err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(route)
		},
	}
}

func projectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage project routes",
	}
	cmd.AddCommand(projectUpCmd(), projectDownCmd())
	return cmd
}

func projectUpCmd() *cobra.Command {
	var file string

	cmd := &cobra.Command{
		Use:   "up",
		Short: "Register all routes from devedge.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadProject(file)
			if err != nil {
				return err
			}
			routes, err := cfg.ToRoutes()
			if err != nil {
				return err
			}

			c := newClient()
			for _, r := range routes {
				err := c.Register(context.Background(), daemon.RegisterRequest{
					Host:     r.Host,
					Upstream: r.Upstream,
					Project:  r.Project,
					Owner:    "project-file",
					TTL:      r.TTL.String(),
				})
				if err != nil {
					return fmt.Errorf("register %s: %w", r.Host, err)
				}
				fmt.Printf("registered %s -> %s\n", r.Host, r.Upstream)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "devedge.yaml", "project config file")
	return cmd
}

func projectDownCmd() *cobra.Command {
	var project string

	cmd := &cobra.Command{
		Use:   "down [PROJECT]",
		Short: "Remove all routes for a project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				project = args[0]
			}
			if project == "" {
				// Try to read from devedge.yaml.
				cfg, err := config.LoadProject("devedge.yaml")
				if err != nil {
					return fmt.Errorf("project name required (pass as argument or use devedge.yaml)")
				}
				project = cfg.Project
			}

			c := newClient()
			n, err := c.DeregisterProject(context.Background(), project)
			if err != nil {
				return err
			}
			fmt.Printf("removed %d route(s) for project %q\n", n, project)
			return nil
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "project name")
	return cmd
}
