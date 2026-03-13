package cluster

import "testing"

func TestClusterDomain(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"mydev", "mydev.test"},
		{"authz-e2e", "authz-e2e.test"},
		{"prod", "prod.test"},
	}
	for _, tt := range tests {
		got := ClusterDomain(tt.name)
		if got != tt.want {
			t.Errorf("ClusterDomain(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestFQDN(t *testing.T) {
	tests := []struct {
		host    string
		cluster string
		want    string
	}{
		// Bare hostname → expanded
		{"api", "mydev", "api.mydev.test"},
		{"web", "authz-e2e", "web.authz-e2e.test"},
		{"grafana", "monitoring", "grafana.monitoring.test"},

		// Already qualified → unchanged
		{"api.mydev.test", "mydev", "api.mydev.test"},
		{"api.custom.domain", "mydev", "api.custom.domain"},
		{"api.foo.dev.test", "mydev", "api.foo.dev.test"},
	}
	for _, tt := range tests {
		got := FQDN(tt.host, tt.cluster)
		if got != tt.want {
			t.Errorf("FQDN(%q, %q) = %q, want %q", tt.host, tt.cluster, got, tt.want)
		}
	}
}
