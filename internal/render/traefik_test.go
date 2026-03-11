package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infobloxopen/devedge/pkg/types"
)

func TestTraefikRoute(t *testing.T) {
	r := types.Route{
		Host:     "api.foo.dev.test",
		Upstream: "http://127.0.0.1:3000",
	}

	got := TraefikRoute(r)

	checks := []string{
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

	// a.dev.test file should exist.
	if _, err := os.Stat(filepath.Join(dir, "a-dev-test.yaml")); err != nil {
		t.Error("expected a-dev-test.yaml to exist")
	}

	// Stale file should be removed.
	if _, err := os.Stat(filepath.Join(dir, "old-route.yaml")); !os.IsNotExist(err) {
		t.Error("stale file should be removed")
	}
}
