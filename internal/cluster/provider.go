// Package cluster abstracts Kubernetes cluster lifecycle operations behind
// a provider interface. The default implementation targets k3d, but the
// interface is designed for future providers (kind, minikube, etc.).
package cluster

import "strings"

// Provider handles cluster lifecycle and discovery for a specific tool.
type Provider interface {
	// Name returns the provider identifier (e.g. "k3d", "kind").
	Name() string

	// Create creates a cluster with the given options.
	Create(opts CreateOptions) error

	// Delete removes a cluster and cleans up routes.
	Delete(name string) error

	// List returns known clusters.
	List() ([]ClusterInfo, error)

	// FindIngressPort discovers the host port mapped to the cluster ingress.
	FindIngressPort(name string) (string, error)

	// KubeContext returns the kubectl context name for a cluster.
	KubeContext(name string) string

	// HostGateway returns the hostname that pods use to reach the host
	// machine (e.g. "host.k3d.internal", "host.docker.internal").
	HostGateway() string
}

// CreateOptions configures cluster creation.
type CreateOptions struct {
	Name      string
	HostPort  string   // host port for ingress (default provider-specific)
	Agents    int      // worker nodes
	Image     string   // k8s/k3s image
	ExtraArgs []string // pass-through to the provider CLI
}

// ClusterDomain returns the DNS domain for a cluster: <name>.test
func ClusterDomain(name string) string {
	return name + ".test"
}

// FQDN returns the fully qualified hostname for an app in a cluster.
// If host already contains a dot, it's returned as-is (already qualified).
// Otherwise it's expanded to <host>.<cluster>.test.
func FQDN(host, clusterName string) string {
	if strings.Contains(host, ".") {
		return host
	}
	return host + "." + ClusterDomain(clusterName)
}

// ClusterInfo describes a discovered cluster.
type ClusterInfo struct {
	Name   string
	Domain string // e.g. "mydev.test"
	Ports  []PortMapping
}

// PortMapping describes a host-to-container port mapping.
type PortMapping struct {
	HostPort      string
	ContainerPort string
}
