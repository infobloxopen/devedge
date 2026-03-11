package cluster

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/infobloxopen/devedge/internal/certs"
)

// BootstrapOptions configures what gets installed into a cluster.
type BootstrapOptions struct {
	Provider     Provider
	ClusterName  string
	Namespace    string // namespace for the CA secret (default "cert-manager")
	DevedgeTCP   string // devedge daemon TCP address (default "15353")
	Force        bool   // bypass local-cluster safety checks
}

func (o *BootstrapOptions) namespace() string {
	if o.Namespace != "" {
		return o.Namespace
	}
	return "cert-manager"
}

func (o *BootstrapOptions) devedgePort() string {
	if o.DevedgeTCP != "" {
		return o.DevedgeTCP
	}
	return "15353"
}

// Bootstrap performs the full cluster setup: CA, issuer, and external-dns
// webhook. It uses kubectl apply for all operations, making it portable
// across any Kubernetes distribution.
func Bootstrap(opts BootstrapOptions) error {
	ctx := opts.Provider.KubeContext(opts.ClusterName)

	// Safety check: refuse to bootstrap non-local clusters.
	if !opts.Force {
		if err := ValidateLocalCluster(ctx); err != nil {
			return err
		}
	}

	ns := opts.namespace()
	gateway := opts.Provider.HostGateway()
	port := opts.devedgePort()

	steps := []struct {
		name     string
		manifest string
	}{
		{"create namespace", fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
`, ns)},
		{"install CA secret", mustBuildCASecret(ns)},
		{"install cert-manager ClusterIssuer", fmt.Sprintf(`apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: devedge-local
spec:
  ca:
    secretName: devedge-ca
---
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: devedge-local
  namespace: %s
spec:
  ca:
    secretName: devedge-ca
`, ns)},
		{"install external-dns webhook", fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: external-dns
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: external-dns
  namespace: external-dns
spec:
  replicas: 1
  selector:
    matchLabels:
      app: external-dns
  template:
    metadata:
      labels:
        app: external-dns
    spec:
      containers:
        - name: external-dns
          image: registry.k8s.io/external-dns/external-dns:v0.14.0
          args:
            - --source=ingress
            - --provider=webhook
            - --webhook-provider-url=http://%s:%s
            - --txt-owner-id=devedge
            - --policy=upsert-only
          ports:
            - containerPort: 7979
`, gateway, port)},
	}

	for _, step := range steps {
		fmt.Printf("  %s... ", step.name)
		if err := kubectlApply(ctx, step.manifest); err != nil {
			fmt.Println("FAILED")
			return fmt.Errorf("%s: %w", step.name, err)
		}
		fmt.Println("OK")
	}
	return nil
}

// CreateAndBootstrap creates a cluster and bootstraps it for devedge.
func CreateAndBootstrap(provider Provider, opts CreateOptions) error {
	port := opts.HostPort
	if port == "" {
		port = "8081"
	}

	fmt.Printf("Creating %s cluster %q (ingress on :%s)...\n", provider.Name(), opts.Name, port)
	if err := provider.Create(opts); err != nil {
		return fmt.Errorf("%s cluster create: %w", provider.Name(), err)
	}

	fmt.Println("Bootstrapping devedge integration...")
	if err := Bootstrap(BootstrapOptions{
		Provider:    provider,
		ClusterName: opts.Name,
	}); err != nil {
		fmt.Printf("Warning: bootstrap partially failed: %v\n", err)
		fmt.Printf("You can retry with: de cluster bootstrap %s\n", opts.Name)
	}

	fmt.Printf("\nCluster %q ready. Ingress at http://127.0.0.1:%s\n", opts.Name, port)
	fmt.Println("Deploy your app and annotate Ingress with devedge.io/expose=true")
	fmt.Printf("Or attach manually: de cluster attach %s --host myapp.dev.test\n", opts.Name)
	return nil
}

// DeleteAndCleanup removes a cluster and its devedge routes.
func DeleteAndCleanup(provider Provider, name string) error {
	fmt.Printf("Removing devedge routes for %q...\n", name)
	req, _ := http.NewRequestWithContext(context.Background(), "DELETE",
		"http://127.0.0.1:15353/v1/projects/"+name, nil)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}

	fmt.Printf("Deleting %s cluster %q...\n", provider.Name(), name)
	return provider.Delete(name)
}

func kubectlApply(kubeContext, manifest string) error {
	cmd := exec.Command("kubectl", "--context", kubeContext, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func mustBuildCASecret(ns string) string {
	cert, key, err := certs.ReadCAFiles()
	if err != nil {
		return fmt.Sprintf(`# devedge CA not available — run 'mkcert -install' first
apiVersion: v1
kind: Secret
metadata:
  name: devedge-ca
  namespace: %s
type: kubernetes.io/tls
data:
  tls.crt: ""
  tls.key: ""
`, ns)
	}

	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: devedge-ca
  namespace: %s
type: kubernetes.io/tls
data:
  tls.crt: %s
  tls.key: %s
`, ns,
		base64.StdEncoding.EncodeToString(cert),
		base64.StdEncoding.EncodeToString(key),
	)
}
