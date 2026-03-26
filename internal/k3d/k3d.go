// Package k3d provides helpers for discovering k3d clusters, their exposed
// ingress ports, and registering routes from cluster resources into devedge.
package k3d

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Cluster represents a k3d cluster with its exposed port mappings.
type Cluster struct {
	Name  string
	Ports []PortMapping
}

// PortMapping describes a host-to-container port mapping.
type PortMapping struct {
	HostPort      string
	ContainerPort string
	Protocol      string
}

// ListClusters returns all k3d clusters visible on the system.
func ListClusters() ([]Cluster, error) {
	if _, err := exec.LookPath("k3d"); err != nil {
		return nil, fmt.Errorf("k3d not found in PATH: %w", err)
	}

	out, err := exec.Command("k3d", "cluster", "list", "-o", "json").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("k3d cluster list: %w\noutput: %s", err, out)
	}

	var raw []struct {
		Name  string `json:"name"`
		Nodes []struct {
			Name       string `json:"name"`
			PortMappings map[string][]struct {
				HostPort string `json:"HostPort"`
				HostIP   string `json:"HostIp"`
			} `json:"portMappings"`
		} `json:"nodes"`
	}

	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse k3d output: %w", err)
	}

	var clusters []Cluster
	for _, c := range raw {
		cluster := Cluster{Name: c.Name}
		for _, node := range c.Nodes {
			for containerPort, mappings := range node.PortMappings {
				for _, m := range mappings {
					cluster.Ports = append(cluster.Ports, PortMapping{
						HostPort:      m.HostPort,
						ContainerPort: containerPort,
					})
				}
			}
		}
		clusters = append(clusters, cluster)
	}
	return clusters, nil
}

// FindIngressPort attempts to find the host port mapped to the cluster's
// ingress controller (typically container port 80/tcp on the load balancer
// node). It first checks the k3d JSON metadata, then falls back to
// `docker port` for ephemeral port allocations where HostPort is empty.
func FindIngressPort(clusterName string) (string, error) {
	clusters, err := ListClusters()
	if err != nil {
		return "", err
	}

	for _, c := range clusters {
		if c.Name != clusterName {
			continue
		}
		// Look for HTTP port mapping (80/tcp) with a known host port.
		for _, p := range c.Ports {
			if strings.HasPrefix(p.ContainerPort, "80/") && p.HostPort != "" {
				return p.HostPort, nil
			}
		}
		// If k3d metadata has port 80 but no host port (ephemeral),
		// ask docker for the actual mapped port.
		for _, p := range c.Ports {
			if strings.HasPrefix(p.ContainerPort, "80/") {
				port, err := dockerPort(clusterName, "80/tcp")
				if err == nil && port != "" {
					return port, nil
				}
			}
		}
		// Fallback: return any port mapping with a known host port.
		for _, p := range c.Ports {
			if p.HostPort != "" {
				return p.HostPort, nil
			}
		}
		return "", fmt.Errorf("cluster %q has no exposed ports", clusterName)
	}
	return "", fmt.Errorf("cluster %q not found", clusterName)
}

// dockerPort uses `docker port` to resolve the actual host port for a
// container port on the k3d load balancer.
func dockerPort(clusterName, containerPort string) (string, error) {
	container := "k3d-" + clusterName + "-serverlb"
	out, err := exec.Command("docker", "port", container, containerPort).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker port %s %s: %w", container, containerPort, err)
	}
	// Output format: "0.0.0.0:32768\n[::]:32768\n"
	// Take the first line and extract the port after the last colon.
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if idx := strings.LastIndex(line, ":"); idx >= 0 {
		return line[idx+1:], nil
	}
	return "", fmt.Errorf("unexpected docker port output: %s", line)
}
