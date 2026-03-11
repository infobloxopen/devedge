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
		for _, pm := range c.Ports {
			if strings.HasPrefix(pm.ContainerPort, "80/") {
				return pm.HostPort, nil
			}
		}
		if len(c.Ports) > 0 {
			return c.Ports[0].HostPort, nil
		}
		return "", fmt.Errorf("cluster %q has no exposed ports", name)
	}
	return "", fmt.Errorf("cluster %q not found", name)
}
