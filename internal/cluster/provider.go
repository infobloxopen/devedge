// Package cluster abstracts Kubernetes cluster lifecycle operations behind
// a provider interface. The default implementation targets k3d, but the
// interface is designed for future providers (kind, minikube, etc.).
package cluster

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

// ClusterInfo describes a discovered cluster.
type ClusterInfo struct {
	Name  string
	Ports []PortMapping
}

// PortMapping describes a host-to-container port mapping.
type PortMapping struct {
	HostPort      string
	ContainerPort string
}
