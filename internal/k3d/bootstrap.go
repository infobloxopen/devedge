package k3d

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/infobloxopen/devedge/internal/certs"
)

// BootstrapOptions configures what gets installed into a k3d cluster.
type BootstrapOptions struct {
	ClusterName string
	Context     string // kubectl context, defaults to "k3d-{ClusterName}"
	Namespace   string // namespace for the CA secret, defaults to "cert-manager"
}

func (o *BootstrapOptions) context() string {
	if o.Context != "" {
		return o.Context
	}
	return "k3d-" + o.ClusterName
}

func (o *BootstrapOptions) namespace() string {
	if o.Namespace != "" {
		return o.Namespace
	}
	return "cert-manager"
}

// InstallCA reads the mkcert CA and creates a Kubernetes TLS secret in the
// cluster that cert-manager can use as a CA issuer.
func InstallCA(opts BootstrapOptions) error {
	cert, key, err := certs.ReadCAFiles()
	if err != nil {
		return fmt.Errorf("read mkcert CA: %w", err)
	}

	ctx := opts.context()
	ns := opts.namespace()

	// Ensure namespace exists.
	kubectl(ctx, "create", "namespace", ns, "--dry-run=client", "-o", "yaml").
		pipe(kubectl(ctx, "apply", "-f", "-"))

	// Create TLS secret with the CA cert and key.
	manifest := fmt.Sprintf(`apiVersion: v1
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

	return applyManifest(ctx, manifest)
}

// InstallIssuer creates a cert-manager ClusterIssuer that references the
// devedge CA secret.
func InstallIssuer(opts BootstrapOptions) error {
	ns := opts.namespace()
	ctx := opts.context()

	manifest := fmt.Sprintf(`apiVersion: cert-manager.io/v1
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
`, ns)

	return applyManifest(ctx, manifest)
}

// InstallExternalDNSWebhook deploys the external-dns webhook provider that
// routes DNS upserts to the devedge daemon on the host.
func InstallExternalDNSWebhook(opts BootstrapOptions) error {
	ctx := opts.context()

	// The webhook runs as a deployment inside the cluster and calls back to
	// the devedge daemon on the host via host.k3d.internal.
	manifest := `apiVersion: v1
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
            - --webhook-provider-url=http://host.k3d.internal:15353
            - --txt-owner-id=devedge
            - --policy=upsert-only
          ports:
            - containerPort: 7979
`

	return applyManifest(ctx, manifest)
}

// Bootstrap performs the full cluster setup: CA, issuer, and external-dns webhook.
func Bootstrap(opts BootstrapOptions) error {
	steps := []struct {
		name string
		fn   func(BootstrapOptions) error
	}{
		{"install CA secret", InstallCA},
		{"install cert-manager issuer", InstallIssuer},
		{"install external-dns webhook", InstallExternalDNSWebhook},
	}

	for _, step := range steps {
		fmt.Printf("  %s... ", step.name)
		if err := step.fn(opts); err != nil {
			fmt.Println("FAILED")
			return fmt.Errorf("%s: %w", step.name, err)
		}
		fmt.Println("OK")
	}
	return nil
}

// applyManifest runs kubectl apply with the given manifest on stdin.
func applyManifest(context, manifest string) error {
	cmd := exec.Command("kubectl", "--context", context, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// kubectlCmd is a helper for building kubectl commands.
type kubectlCmd struct {
	args []string
	ctx  string
}

func kubectl(context string, args ...string) *kubectlCmd {
	return &kubectlCmd{ctx: context, args: args}
}

func (k *kubectlCmd) pipe(next *kubectlCmd) error {
	src := exec.Command("kubectl", append([]string{"--context", k.ctx}, k.args...)...)
	dst := exec.Command("kubectl", append([]string{"--context", next.ctx}, next.args...)...)

	var err error
	dst.Stdin, err = src.StdoutPipe()
	if err != nil {
		return err
	}
	dst.Stdout = os.Stderr
	dst.Stderr = os.Stderr

	if err := dst.Start(); err != nil {
		return err
	}
	if err := src.Run(); err != nil {
		return err
	}
	return dst.Wait()
}
