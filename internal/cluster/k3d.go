package cluster

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// K3dProvider implements Provider for k3d clusters.
type K3dProvider struct{}

func (p *K3dProvider) Name() string { return "k3d" }

func (p *K3dProvider) KubeContext(name string) string {
	return "k3d-" + name
}

func (p *K3dProvider) HostGateway() string {
	return K3dHostGateway()
}

func (p *K3dProvider) Create(opts CreateOptions) error {
	port := opts.HostPort
	if port == "" {
		port = "8081"
	}

	args := []string{
		"cluster", "create", opts.Name,
		"-p", port + ":80@loadbalancer",
	}
	if opts.Agents > 0 {
		args = append(args, "--agents", fmt.Sprintf("%d", opts.Agents))
	}
	if opts.Image != "" {
		args = append(args, "--image", opts.Image)
	}
	args = append(args, opts.ExtraArgs...)

	cmd := exec.Command("k3d", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (p *K3dProvider) Delete(name string) error {
	cmd := exec.Command("k3d", "cluster", "delete", name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (p *K3dProvider) List() ([]ClusterInfo, error) {
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
			PortMappings map[string][]struct {
				HostPort string `json:"HostPort"`
			} `json:"portMappings"`
		} `json:"nodes"`
	}

	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse k3d output: %w", err)
	}

	var clusters []ClusterInfo
	for _, c := range raw {
		info := ClusterInfo{Name: c.Name}
		for _, node := range c.Nodes {
			for containerPort, mappings := range node.PortMappings {
				for _, m := range mappings {
					info.Ports = append(info.Ports, PortMapping{
						HostPort:      m.HostPort,
						ContainerPort: containerPort,
					})
				}
			}
		}
		clusters = append(clusters, info)
	}
	return clusters, nil
}

func (p *K3dProvider) FindIngressPort(name string) (string, error) {
	clusters, err := p.List()
	if err != nil {
		return "", err
	}

	for _, c := range clusters {
		if c.Name != name {
			continue
		}
		// Look for HTTP port mapping (80/tcp) with a known host port.
		for _, pm := range c.Ports {
			if strings.HasPrefix(pm.ContainerPort, "80/") && pm.HostPort != "" {
				return pm.HostPort, nil
			}
		}
		// If k3d metadata has port 80 but host port is empty (ephemeral),
		// ask docker for the actual mapped port.
		for _, pm := range c.Ports {
			if strings.HasPrefix(pm.ContainerPort, "80/") {
				port, err := dockerPortLookup(name, "80/tcp")
				if err == nil && port != "" {
					return port, nil
				}
			}
		}
		// Fallback: return any port mapping with a known host port.
		for _, pm := range c.Ports {
			if pm.HostPort != "" {
				return pm.HostPort, nil
			}
		}
		return "", fmt.Errorf("cluster %q has no exposed ports", name)
	}
	return "", fmt.Errorf("cluster %q not found", name)
}

// dockerPortLookup uses `docker port` to resolve the actual host port for a
// container port on the k3d load balancer.
func dockerPortLookup(clusterName, containerPort string) (string, error) {
	container := "k3d-" + clusterName + "-serverlb"
	out, err := exec.Command("docker", "port", container, containerPort).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker port %s %s: %w", container, containerPort, err)
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if idx := strings.LastIndex(line, ":"); idx >= 0 {
		return line[idx+1:], nil
	}
	return "", fmt.Errorf("unexpected docker port output: %s", line)
}
