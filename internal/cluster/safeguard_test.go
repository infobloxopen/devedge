package cluster

import "testing"

func TestIsLocalContextName(t *testing.T) {
	local := []string{
		"k3d-mycluster",
		"k3d-foo",
		"kind-test",
		"minikube",
		"docker-desktop",
		"rancher-desktop",
		"orbstack",
		"colima",
	}
	for _, name := range local {
		if !isLocalContextName(name) {
			t.Errorf("%q should be detected as local", name)
		}
	}

	remote := []string{
		"arn:aws:eks:us-east-1:123456789:cluster/prod",
		"gke_myproject_us-central1_prod",
		"my-prod-cluster",
		"staging",
		"",
	}
	for _, name := range remote {
		if isLocalContextName(name) {
			t.Errorf("%q should NOT be detected as local", name)
		}
	}
}

func TestIsLoopbackServer(t *testing.T) {
	loopback := []string{
		"https://127.0.0.1:6443",
		"https://localhost:6443",
		"https://[::1]:6443",
		"https://0.0.0.0:6443",
	}
	for _, s := range loopback {
		if !isLoopbackServer(s) {
			t.Errorf("%q should be detected as loopback", s)
		}
	}

	remote := []string{
		"https://eks.us-east-1.amazonaws.com",
		"https://34.123.45.67:6443",
		"https://my-cluster.example.com:6443",
	}
	for _, s := range remote {
		if isLoopbackServer(s) {
			t.Errorf("%q should NOT be detected as loopback", s)
		}
	}
}
