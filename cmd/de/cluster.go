package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge/internal/cluster"
	"github.com/infobloxopen/devedge/internal/daemon"
	k3dpkg "github.com/infobloxopen/devedge/internal/k3d"
)

func defaultProvider() cluster.Provider {
	return &cluster.K3dProvider{}
}

func clusterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Manage local Kubernetes clusters with devedge integration",
	}
	cmd.AddCommand(
		clusterCreateCmd(),
		clusterDeleteCmd(),
		clusterBootstrapCmd(),
		clusterAttachCmd(),
		clusterDetachCmd(),
		clusterLsCmd(),
		clusterWatchCmd(),
	)
	return cmd
}

// k3dAliasCmd returns "de k3d" as an alias for "de cluster" for backwards compat.
func k3dAliasCmd() *cobra.Command {
	cmd := clusterCmd()
	cmd.Use = "k3d"
	cmd.Short = "Alias for 'de cluster' (k3d provider)"
	return cmd
}

func clusterCreateCmd() *cobra.Command {
	var port string
	var agents int
	var image string

	cmd := &cobra.Command{
		Use:   "create CLUSTER",
		Short: "Create a local cluster pre-configured for devedge",
		Long: `Create a k3d cluster with ingress port mapping, then bootstrap
it with mkcert CA, cert-manager issuer, and external-dns webhook.

Safety: refuses to target non-local clusters unless --force is used.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider := defaultProvider()
			return cluster.CreateAndBootstrap(provider, cluster.CreateOptions{
				Name:     args[0],
				HostPort: port,
				Agents:   agents,
				Image:    image,
			})
		},
	}
	cmd.Flags().StringVar(&port, "port", "8081", "host port for ingress load balancer")
	cmd.Flags().IntVar(&agents, "agents", 0, "number of agent nodes")
	cmd.Flags().StringVar(&image, "image", "", "k3s image (default: k3d default)")
	return cmd
}

func clusterDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete CLUSTER",
		Short: "Delete a cluster and clean up devedge routes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cluster.DeleteAndCleanup(defaultProvider(), args[0])
		},
	}
}

func clusterBootstrapCmd() *cobra.Command {
	var ns string
	var force bool

	cmd := &cobra.Command{
		Use:   "bootstrap CLUSTER",
		Short: "Set up a cluster for seamless devedge integration",
		Long: `Install devedge CA, cert-manager issuer, and external-dns webhook.

Safety: validates the cluster is local (loopback API server, known context
name pattern). Use --force to bypass if you know what you're doing.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider := defaultProvider()
			rt := cluster.DetectRuntime()
			fmt.Printf("Container runtime: %s\n", rt)
			fmt.Printf("Host gateway: %s\n", provider.HostGateway())

			return cluster.Bootstrap(cluster.BootstrapOptions{
				Provider:    provider,
				ClusterName: args[0],
				Namespace:   ns,
				Force:       force,
			})
		},
	}
	cmd.Flags().StringVar(&ns, "namespace", "cert-manager", "namespace for CA secret")
	cmd.Flags().BoolVar(&force, "force", false, "bypass local-cluster safety checks (DANGEROUS)")
	return cmd
}

func clusterAttachCmd() *cobra.Command {
	var ingress string
	var hosts []string

	cmd := &cobra.Command{
		Use:   "attach CLUSTER",
		Short: "Register routes for a cluster's ingress",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider := defaultProvider()
			name := args[0]

			if ingress == "" {
				port, err := provider.FindIngressPort(name)
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
					Project:  name,
					Owner:    provider.Name() + ":" + name,
					TTL:      "30s",
				})
				if err != nil {
					return fmt.Errorf("register %s: %w", h, err)
				}
				fmt.Printf("attached %s -> %s (cluster: %s)\n", h, ingress, name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ingress, "ingress", "", "ingress URL (auto-detected if omitted)")
	cmd.Flags().StringSliceVar(&hosts, "host", nil, "hostnames to register (repeatable)")
	return cmd
}

func clusterDetachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detach CLUSTER",
		Short: "Remove all routes for a cluster",
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

func clusterLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List clusters",
		RunE: func(cmd *cobra.Command, args []string) error {
			provider := defaultProvider()
			clusters, err := provider.List()
			if err != nil {
				return err
			}
			if len(clusters) == 0 {
				fmt.Println("No clusters found.")
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

func clusterWatchCmd() *cobra.Command {
	var kubectx, ns, ingressPort, devedgeURL string

	cmd := &cobra.Command{
		Use:   "watch CLUSTER",
		Short: "Watch Ingress objects and auto-register with devedge",
		Long: `Watch Kubernetes Ingress objects annotated with
devedge.io/expose=true and automatically register/deregister their
hostnames with the devedge daemon.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider := defaultProvider()
			name := args[0]

			if kubectx == "" {
				kubectx = provider.KubeContext(name)
			}
			if devedgeURL == "" {
				devedgeURL = "http://127.0.0.1:" + daemon.DefaultTCPAddr()[len("127.0.0.1:"):]
			}
			if ingressPort == "" {
				port, err := provider.FindIngressPort(name)
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

			return k3dpkg.WatchIngresses(ctx, k3dpkg.IngressWatcherConfig{
				Context:     kubectx,
				Namespace:   ns,
				DevedgeURL:  devedgeURL,
				IngressPort: ingressPort,
				Logger:      logger,
			})
		},
	}
	cmd.Flags().StringVar(&kubectx, "context", "", "kubectl context (default: provider-specific)")
	cmd.Flags().StringVar(&ns, "namespace", "", "namespace to watch (default: all)")
	cmd.Flags().StringVar(&ingressPort, "ingress-port", "", "host port for ingress (auto-detected)")
	cmd.Flags().StringVar(&devedgeURL, "devedge-url", "", "devedge daemon URL")
	return cmd
}
