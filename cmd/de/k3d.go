package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/internal/k3d"
)

func k3dCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "k3d",
		Short: "Manage k3d cluster integrations",
	}
	cmd.AddCommand(k3dAttachCmd(), k3dDetachCmd(), k3dLsCmd())
	return cmd
}

func k3dAttachCmd() *cobra.Command {
	var ingress string
	var hosts []string

	cmd := &cobra.Command{
		Use:   "attach CLUSTER",
		Short: "Register routes for a k3d cluster's ingress",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cluster := args[0]

			// Auto-detect ingress port if not specified.
			if ingress == "" {
				port, err := k3d.FindIngressPort(cluster)
				if err != nil {
					return fmt.Errorf("auto-detect ingress: %w\nUse --ingress to specify manually", err)
				}
				ingress = fmt.Sprintf("http://127.0.0.1:%s", port)
				fmt.Printf("Auto-detected ingress: %s\n", ingress)
			}

			if len(hosts) == 0 {
				return fmt.Errorf("at least one --host is required (e.g. --host api.foo.dev.test)")
			}

			c := newClient()
			for _, h := range hosts {
				err := c.Register(context.Background(), daemon.RegisterRequest{
					Host:     h,
					Upstream: ingress,
					Project:  cluster,
					Owner:    "k3d:" + cluster,
					TTL:      "30s",
				})
				if err != nil {
					return fmt.Errorf("register %s: %w", h, err)
				}
				fmt.Printf("attached %s -> %s (cluster: %s)\n", h, ingress, cluster)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&ingress, "ingress", "", "ingress URL (auto-detected if omitted)")
	cmd.Flags().StringSliceVar(&hosts, "host", nil, "hostnames to register (repeatable)")
	return cmd
}

func k3dDetachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detach CLUSTER",
		Short: "Remove all routes for a k3d cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			n, err := c.DeregisterProject(context.Background(), args[0])
			if err != nil {
				return err
			}
			fmt.Printf("detached %d route(s) for cluster %q\n", n, args[0])
			return nil
		},
	}
}

func k3dLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List k3d clusters",
		RunE: func(cmd *cobra.Command, args []string) error {
			clusters, err := k3d.ListClusters()
			if err != nil {
				return err
			}
			if len(clusters) == 0 {
				fmt.Println("No k3d clusters found.")
				return nil
			}
			for _, c := range clusters {
				fmt.Printf("  %s", c.Name)
				if len(c.Ports) > 0 {
					fmt.Printf(" (ports:")
					for _, p := range c.Ports {
						fmt.Printf(" %s->%s", p.HostPort, p.ContainerPort)
					}
					fmt.Print(")")
				}
				fmt.Println()
			}
			return nil
		},
	}
}
