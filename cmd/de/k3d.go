package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/internal/k3d"
)

func k3dCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "k3d",
		Short: "Manage k3d cluster integrations",
	}
	cmd.AddCommand(
		k3dCreateCmd(),
		k3dDeleteCmd(),
		k3dAttachCmd(),
		k3dDetachCmd(),
		k3dLsCmd(),
		k3dBootstrapCmd(),
		k3dWatchCmd(),
	)
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

func k3dBootstrapCmd() *cobra.Command {
	var ctx, ns string

	cmd := &cobra.Command{
		Use:   "bootstrap CLUSTER",
		Short: "Set up a k3d cluster for seamless devedge integration",
		Long: `Bootstrap installs the devedge CA, cert-manager issuer, and
external-dns webhook into a k3d cluster. After bootstrapping, any Ingress
object with standard cert-manager and external-dns annotations will
automatically get locally-trusted TLS and host-resolvable DNS names.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Bootstrapping cluster %q for devedge...\n", args[0])
			return k3d.Bootstrap(k3d.BootstrapOptions{
				ClusterName: args[0],
				Context:     ctx,
				Namespace:   ns,
			})
		},
	}
	cmd.Flags().StringVar(&ctx, "context", "", "kubectl context (default: k3d-CLUSTER)")
	cmd.Flags().StringVar(&ns, "namespace", "cert-manager", "namespace for CA secret")
	return cmd
}

func k3dWatchCmd() *cobra.Command {
	var kubectx, ns, ingressPort, devedgeURL string

	cmd := &cobra.Command{
		Use:   "watch CLUSTER",
		Short: "Watch Ingress objects and auto-register with devedge",
		Long: `Watch Kubernetes Ingress objects annotated with
devedge.io/expose=true and automatically register/deregister their
hostnames with the devedge daemon. This is a lightweight alternative
to running the full external-dns stack.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cluster := args[0]
			if kubectx == "" {
				kubectx = "k3d-" + cluster
			}
			if devedgeURL == "" {
				devedgeURL = "http://127.0.0.1:" + daemon.DefaultTCPAddr()[len("127.0.0.1:"):]
			}

			// Auto-detect ingress port.
			if ingressPort == "" {
				port, err := k3d.FindIngressPort(cluster)
				if err != nil {
					return fmt.Errorf("auto-detect ingress port: %w", err)
				}
				ingressPort = port
			}

			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

			ctx, cancel := signal.NotifyContext(context.Background(),
				os.Interrupt, syscall.SIGTERM)
			defer cancel()

			fmt.Printf("Watching Ingress objects in context %q (Ctrl-C to stop)\n", kubectx)

			return k3d.WatchIngresses(ctx, k3d.IngressWatcherConfig{
				Context:     kubectx,
				Namespace:   ns,
				DevedgeURL:  devedgeURL,
				IngressPort: ingressPort,
				Logger:      logger,
			})
		},
	}
	cmd.Flags().StringVar(&kubectx, "context", "", "kubectl context (default: k3d-CLUSTER)")
	cmd.Flags().StringVar(&ns, "namespace", "", "namespace to watch (default: all)")
	cmd.Flags().StringVar(&ingressPort, "ingress-port", "", "host port for k3d ingress (auto-detected)")
	cmd.Flags().StringVar(&devedgeURL, "devedge-url", "", "devedge daemon URL")
	return cmd
}

func k3dCreateCmd() *cobra.Command {
	var port string
	var agents int
	var image string

	cmd := &cobra.Command{
		Use:   "create CLUSTER",
		Short: "Create a k3d cluster pre-configured for devedge",
		Long: `Create a k3d cluster with ingress port mapping, mkcert CA,
cert-manager issuer, and external-dns webhook pre-installed.

This is equivalent to running k3d cluster create with the right flags
followed by de k3d bootstrap.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return k3d.Create(k3d.CreateOptions{
				ClusterName: args[0],
				HostPort:    port,
				Agents:      agents,
				Image:       image,
			})
		},
	}
	cmd.Flags().StringVar(&port, "port", "8081", "host port for ingress load balancer")
	cmd.Flags().IntVar(&agents, "agents", 0, "number of agent nodes")
	cmd.Flags().StringVar(&image, "image", "", "k3s image (default: k3d default)")
	return cmd
}

func k3dDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete CLUSTER",
		Short: "Delete a k3d cluster and clean up devedge routes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return k3d.Delete(args[0])
		},
	}
}
