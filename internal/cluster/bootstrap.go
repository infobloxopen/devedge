package cluster

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/infobloxopen/devedge/internal/certs"
)

// certManagerVersion pins the cert-manager release bootstrap installs. The
// ClusterIssuer below is a hard dependency on cert-manager's CRDs + webhook.
const certManagerVersion = "v1.14.5"

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

	// Safety check: refuse to bootstrap non-local clusters. This gate is why it is
	// safe to install cluster-scoped components (cert-manager) below: we only ever
	// do so on a local dev cluster, never a real/remote one.
	if !opts.Force {
		if err := ValidateLocalCluster(ctx); err != nil {
			return err
		}
	}

	// cert-manager is a hard prerequisite for the ClusterIssuer step. Install it
	// into the local cluster and wait until its webhook serves so the issuer
	// applies cleanly. Idempotent. (`ctx` here is the kube-context string.)
	if err := installCertManager(context.Background(), ctx); err != nil {
		return fmt.Errorf("install cert-manager: %w", err)
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
		// Retry: just after cert-manager's webhook deployment reports Available
		// there is a brief window before it serves admission, so the first
		// ClusterIssuer apply can fail transiently.
		if err := kubectlApplyRetry(ctx, step.manifest, 6); err != nil {
			fmt.Println("FAILED")
			return fmt.Errorf("%s: %w", step.name, err)
		}
		fmt.Println("OK")
	}
	return nil
}

// installCertManager installs cert-manager (CRDs + controller + webhook) and waits
// for it to be ready, so the devedge ClusterIssuer applies cleanly. Idempotent: a
// re-apply on an already-installed cluster is a no-op and the rollout waits return
// immediately. Only ever runs on local clusters (Bootstrap gates on
// ValidateLocalCluster first).
func installCertManager(ctx context.Context, kubeContext string) error {
	url := fmt.Sprintf("https://github.com/cert-manager/cert-manager/releases/download/%s/cert-manager.yaml", certManagerVersion)
	fmt.Printf("  install cert-manager %s... ", certManagerVersion)
	apply := exec.CommandContext(ctx, "kubectl", "--context", kubeContext, "apply", "-f", url)
	apply.Stdout = os.Stderr
	apply.Stderr = os.Stderr
	if err := apply.Run(); err != nil {
		fmt.Println("FAILED")
		return fmt.Errorf("apply cert-manager manifest: %w", err)
	}
	for _, dep := range []string{"cert-manager", "cert-manager-cainjector", "cert-manager-webhook"} {
		wait := exec.CommandContext(ctx, "kubectl", "--context", kubeContext, "-n", "cert-manager",
			"rollout", "status", "deployment/"+dep, "--timeout=120s")
		wait.Stdout = os.Stderr
		wait.Stderr = os.Stderr
		if err := wait.Run(); err != nil {
			fmt.Println("FAILED")
			return fmt.Errorf("cert-manager %s not ready: %w", dep, err)
		}
	}
	fmt.Println("OK")
	return nil
}

// kubectlApplyRetry applies a manifest, retrying transient failures with linear
// backoff (e.g. the cert-manager webhook warming up).
func kubectlApplyRetry(kubeContext, manifest string, attempts int) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = kubectlApply(kubeContext, manifest); err == nil {
			return nil
		}
		time.Sleep(time.Duration(i+1) * 2 * time.Second)
	}
	return err
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

	domain := ClusterDomain(opts.Name)
	fmt.Printf("\nCluster %q ready.\n", opts.Name)
	fmt.Printf("  Domain:  *.%s\n", domain)
	fmt.Printf("  Ingress: http://127.0.0.1:%s\n", port)
	fmt.Println()
	fmt.Println("Deploy your app and annotate Ingress with devedge.io/expose=true,")
	fmt.Printf("or attach manually: de cluster attach %s --host api\n", opts.Name)
	fmt.Printf("  -> api.%s\n", domain)
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
