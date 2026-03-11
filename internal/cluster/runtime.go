package cluster

import (
	"os"
	"os/exec"
	"strings"
)

// Runtime identifies the container runtime environment.
type Runtime string

const (
	RuntimeDockerDesktop  Runtime = "docker-desktop"
	RuntimeRancherDesktop Runtime = "rancher-desktop"
	RuntimeColima         Runtime = "colima"
	RuntimeOrbstack       Runtime = "orbstack"
	RuntimeGenericDocker  Runtime = "docker"
	RuntimeUnknown        Runtime = "unknown"
)

// DetectRuntime identifies the active container runtime by inspecting
// the Docker socket and daemon info.
func DetectRuntime() Runtime {
	// Check Docker socket path for hints.
	socketPath := os.Getenv("DOCKER_HOST")
	if socketPath == "" {
		socketPath = "/var/run/docker.sock"
	}

	// Check if Docker is reachable.
	out, err := exec.Command("docker", "info", "--format", "{{.Name}}").CombinedOutput()
	if err != nil {
		return RuntimeUnknown
	}
	name := strings.TrimSpace(strings.ToLower(string(out)))

	switch {
	case strings.Contains(name, "docker-desktop") || strings.Contains(socketPath, "docker.sock.raw"):
		return RuntimeDockerDesktop
	case strings.Contains(name, "rancher-desktop") || strings.Contains(socketPath, "rancher-desktop"):
		return RuntimeRancherDesktop
	case strings.Contains(name, "colima") || strings.Contains(socketPath, "colima"):
		return RuntimeColima
	case strings.Contains(name, "orbstack") || strings.Contains(socketPath, "orbstack"):
		return RuntimeOrbstack
	default:
		return RuntimeGenericDocker
	}
}

// HostGateway returns the hostname that containers/pods use to reach
// the host machine for the given runtime. This is used by in-cluster
// components (like the external-dns webhook) to call back to the
// devedge daemon on the host.
func HostGateway(rt Runtime) string {
	switch rt {
	case RuntimeDockerDesktop, RuntimeRancherDesktop:
		return "host.docker.internal"
	case RuntimeOrbstack:
		return "host.internal"
	default:
		// k3d provides host.k3d.internal regardless of the underlying
		// Docker runtime. For generic Docker, host.docker.internal is
		// the most common convention.
		return "host.docker.internal"
	}
}

// K3dHostGateway always returns "host.k3d.internal" since k3d injects
// this regardless of the underlying Docker runtime.
func K3dHostGateway() string {
	return "host.k3d.internal"
}
