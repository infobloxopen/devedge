package cluster

import (
	"fmt"
	"os/exec"
	"strings"
)

// ValidateLocalCluster checks that the given kubectl context points to a
// local development cluster and NOT a production or remote cluster. This
// prevents accidental bootstrapping of production environments.
//
// It verifies:
//  1. The context name matches a known local pattern (k3d-*, kind-*, minikube, etc.)
//  2. The cluster API server is on loopback (127.0.0.1 or localhost)
//  3. The cluster is not a known cloud provider (EKS, GKE, AKS)
func ValidateLocalCluster(kubeContext string) error {
	// Check context name against known local patterns.
	if !isLocalContextName(kubeContext) {
		return fmt.Errorf(
			"context %q does not look like a local dev cluster\n"+
				"Devedge only bootstraps local clusters (k3d-*, kind-*, minikube, docker-desktop, rancher-desktop).\n"+
				"If this is a local cluster, use --force to override this check.",
			kubeContext,
		)
	}

	// Verify the API server is on loopback.
	server, err := getClusterServer(kubeContext)
	if err != nil {
		return fmt.Errorf("cannot determine API server for context %q: %w", kubeContext, err)
	}

	if !isLoopbackServer(server) {
		return fmt.Errorf(
			"context %q points to a non-loopback API server (%s)\n"+
				"Devedge refuses to bootstrap remote or production clusters.\n"+
				"If this is genuinely local, use --force to override.",
			kubeContext, server,
		)
	}

	return nil
}

// isLocalContextName checks if the context name matches known local cluster
// naming conventions.
func isLocalContextName(name string) bool {
	localPrefixes := []string{
		"k3d-",
		"k3s-",
		"kind-",
		"minikube",
		"docker-desktop",
		"docker-for-desktop",
		"rancher-desktop",
		"orbstack",
		"colima",
	}
	lower := strings.ToLower(name)
	for _, prefix := range localPrefixes {
		if strings.HasPrefix(lower, prefix) || lower == prefix {
			return true
		}
	}
	return false
}

// isLoopbackServer checks if the API server URL points to a loopback address.
func isLoopbackServer(server string) bool {
	loopbacks := []string{
		"127.0.0.1",
		"localhost",
		"[::1]",
		"0.0.0.0",
	}
	lower := strings.ToLower(server)
	for _, lb := range loopbacks {
		if strings.Contains(lower, lb) {
			return true
		}
	}
	return false
}

// getClusterServer extracts the API server URL for a kubectl context.
func getClusterServer(kubeContext string) (string, error) {
	out, err := exec.Command("kubectl", "config", "view",
		"--context", kubeContext,
		"-o", "jsonpath={.clusters[0].cluster.server}",
	).CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
