package cluster

import "testing"

func TestHostGateway(t *testing.T) {
	tests := []struct {
		runtime Runtime
		want    string
	}{
		{RuntimeDockerDesktop, "host.docker.internal"},
		{RuntimeRancherDesktop, "host.docker.internal"},
		{RuntimeOrbstack, "host.internal"},
		{RuntimeGenericDocker, "host.docker.internal"},
		{RuntimeUnknown, "host.docker.internal"},
	}
	for _, tt := range tests {
		t.Run(string(tt.runtime), func(t *testing.T) {
			got := HostGateway(tt.runtime)
			if got != tt.want {
				t.Errorf("HostGateway(%s) = %q, want %q", tt.runtime, got, tt.want)
			}
		})
	}
}

func TestK3dHostGateway(t *testing.T) {
	if got := K3dHostGateway(); got != "host.k3d.internal" {
		t.Errorf("K3dHostGateway() = %q", got)
	}
}

func TestDetectRuntime(t *testing.T) {
	// Informational — passes regardless of environment.
	rt := DetectRuntime()
	t.Logf("detected runtime: %s", rt)
}
