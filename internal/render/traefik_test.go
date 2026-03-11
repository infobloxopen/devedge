package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infobloxopen/devedge/pkg/types"
)

func TestTraefikRoute_HTTP(t *testing.T) {
	r := types.Route{
		Host:     "api.foo.dev.test",
		Upstream: "http://127.0.0.1:3000",
	}

	got := TraefikRoute(r)

	checks := []string{
		"http:",
		"api-foo-dev-test:",
		"Host(`api.foo.dev.test`)",
		`url: "http://127.0.0.1:3000"`,
		"entryPoints:",
		"websecure",
		"tls: {}",
	}
	for _, c := range checks {
		if !strings.Contains(got, c) {
			t.Errorf("output missing %q:\n%s", c, got)
		}
	}
	if strings.Contains(got, "tcp:") {
		t.Error("HTTP route should not contain tcp: section")
	}
}

func TestTraefikRoute_TCP(t *testing.T) {
	r := types.Route{
		Host:     "postgres.foo.dev.test",
		Upstream: "127.0.0.1:5432",
		Protocol: types.ProtocolTCP,
	}

	got := TraefikRoute(r)

	checks := []string{
		"tcp:",
		"postgres-foo-dev-test:",
		"HostSNI(`postgres.foo.dev.test`)",
		`address: "127.0.0.1:5432"`,
		"entryPoints:",
		"websecure",
		"tls: {}",
	}
	for _, c := range checks {
		if !strings.Contains(got, c) {
			t.Errorf("output missing %q:\n%s", c, got)
		}
	}
	if strings.Contains(got, "http:") {
		t.Error("TCP route should not contain http: section")
	}
	if strings.Contains(got, "passthrough") {
		t.Error("non-backendTLS route should not have passthrough")
	}
}

func TestTraefikRoute_TCP_backendTLS(t *testing.T) {
	r := types.Route{
		Host:       "secure-db.foo.dev.test",
		Upstream:   "127.0.0.1:5432",
		Protocol:   types.ProtocolTCP,
		BackendTLS: true,
	}

	got := TraefikRoute(r)

	if !strings.Contains(got, "passthrough: true") {
		t.Errorf("backendTLS route should have passthrough: true:\n%s", got)
	}
}

func TestTraefikRoute_TCP_with_scheme(t *testing.T) {
	r := types.Route{
		Host:     "redis.foo.dev.test",
		Upstream: "tcp://127.0.0.1:6379",
		Protocol: types.ProtocolTCP,
	}

	got := TraefikRoute(r)

	// Should strip the tcp:// scheme and use bare host:port.
	if !strings.Contains(got, `address: "127.0.0.1:6379"`) {
		t.Errorf("should normalize tcp:// to bare address:\n%s", got)
	}
	if strings.Contains(got, "tcp://") {
		t.Error("tcp:// scheme should be stripped from address")
	}
}

func TestNormalizeTCPAddress(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"127.0.0.1:5432", "127.0.0.1:5432"},
		{"tcp://127.0.0.1:5432", "127.0.0.1:5432"},
		{"tls://db.local:3306", "db.local:3306"},
		{"localhost:6379", "localhost:6379"},
	}
	for _, tt := range tests {
		got := normalizeTCPAddress(tt.input)
		if got != tt.want {
			t.Errorf("normalizeTCPAddress(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestWriteRouteFile_and_RemoveRouteFile(t *testing.T) {
	dir := t.TempDir()
	r := types.Route{Host: "web.foo.dev.test", Upstream: "http://127.0.0.1:8080"}

	if err := WriteRouteFile(dir, r); err != nil {
		t.Fatalf("WriteRouteFile: %v", err)
	}

	path := filepath.Join(dir, "web-foo-dev-test.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !strings.Contains(string(data), "web.foo.dev.test") {
		t.Error("file does not contain expected host")
	}

	if err := RemoveRouteFile(dir, "web.foo.dev.test"); err != nil {
		t.Fatalf("RemoveRouteFile: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should be removed")
	}
}

func TestSyncAll_removes_stale(t *testing.T) {
	dir := t.TempDir()

	// Pre-create a stale file.
	os.WriteFile(filepath.Join(dir, "old-route.yaml"), []byte("stale"), 0644)

	routes := []types.Route{
		{Host: "a.dev.test", Upstream: "http://127.0.0.1:1"},
	}

	if err := SyncAll(dir, routes); err != nil {
		t.Fatalf("SyncAll: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "a-dev-test.yaml")); err != nil {
		t.Error("expected a-dev-test.yaml to exist")
	}
	if _, err := os.Stat(filepath.Join(dir, "old-route.yaml")); !os.IsNotExist(err) {
		t.Error("stale file should be removed")
	}
}

func TestSyncAll_mixed_protocols(t *testing.T) {
	dir := t.TempDir()

	routes := []types.Route{
		{Host: "api.dev.test", Upstream: "http://127.0.0.1:3000"},
		{Host: "db.dev.test", Upstream: "127.0.0.1:5432", Protocol: types.ProtocolTCP},
	}

	if err := SyncAll(dir, routes); err != nil {
		t.Fatalf("SyncAll: %v", err)
	}

	// HTTP route.
	httpData, _ := os.ReadFile(filepath.Join(dir, "api-dev-test.yaml"))
	if !strings.Contains(string(httpData), "http:") {
		t.Error("HTTP route file should contain http: section")
	}

	// TCP route.
	tcpData, _ := os.ReadFile(filepath.Join(dir, "db-dev-test.yaml"))
	if !strings.Contains(string(tcpData), "tcp:") {
		t.Error("TCP route file should contain tcp: section")
	}
}
