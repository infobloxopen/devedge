package k3d

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
)

// CreateOptions configures k3d cluster creation.
type CreateOptions struct {
	ClusterName string
	HostPort    string   // host port for the ingress load balancer (default "8081")
	Agents      int      // number of agent nodes (default 0)
	Image       string   // k3s image (empty = k3d default)
	ExtraArgs   []string // additional args passed to k3d cluster create
}

func (o *CreateOptions) hostPort() string {
	if o.HostPort != "" {
		return o.HostPort
	}
	return "8081"
}

// Create creates a k3d cluster pre-configured for devedge. It:
//  1. Creates the cluster with port mapping for the ingress
//  2. Runs bootstrap via kubectl apply (CA secret, cert-manager issuer)
//
// No k3s-specific volume mounts or auto-deploy paths are used. All
// post-create setup goes through standard kubectl apply, so the same
// bootstrap works with any Kubernetes distribution reachable via kubectl.
func Create(opts CreateOptions) error {
	port := opts.hostPort()
	args := []string{
		"cluster", "create", opts.ClusterName,
		"-p", port + ":80@loadbalancer",
	}
	if opts.Agents > 0 {
		args = append(args, "--agents", fmt.Sprintf("%d", opts.Agents))
	}
	if opts.Image != "" {
		args = append(args, "--image", opts.Image)
	}
	args = append(args, opts.ExtraArgs...)

	fmt.Printf("Creating k3d cluster %q (ingress on :%s)...\n", opts.ClusterName, port)
	cmd := exec.Command("k3d", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("k3d cluster create: %w", err)
	}

	// Bootstrap via kubectl apply — works with any k8s cluster.
	fmt.Println("Bootstrapping devedge integration...")
	if err := Bootstrap(BootstrapOptions{ClusterName: opts.ClusterName}); err != nil {
		fmt.Printf("Warning: bootstrap partially failed: %v\n", err)
		fmt.Println("You can retry with: de k3d bootstrap", opts.ClusterName)
	}

	fmt.Printf("\nCluster %q ready. Ingress available at http://127.0.0.1:%s\n", opts.ClusterName, port)
	fmt.Println("Deploy your app and annotate Ingress with devedge.io/expose=true")
	fmt.Printf("Or attach manually: de k3d attach %s --host myapp.dev.test\n", opts.ClusterName)
	return nil
}

// Delete removes a k3d cluster and cleans up its devedge routes.
func Delete(clusterName string) error {
	fmt.Printf("Removing devedge routes for %q...\n", clusterName)
	req, _ := newHTTPRequest(context.Background(), "DELETE",
		"http://127.0.0.1:15353/v1/projects/"+clusterName, nil)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}

	fmt.Printf("Deleting k3d cluster %q...\n", clusterName)
	cmd := exec.Command("k3d", "cluster", "delete", clusterName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
