package reconciler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/registry"
	"github.com/infobloxopen/devedge/pkg/types"
)

func TestSync(t *testing.T) {
	dir := t.TempDir()
	reg := registry.New()
	rec := New(dir, reg)

	reg.Register(types.Route{Host: "a.dev.test", Upstream: "http://127.0.0.1:1"})
	reg.Register(types.Route{Host: "b.dev.test", Upstream: "http://127.0.0.1:2"})

	if err := rec.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	for _, name := range []string{"a-dev-test.yaml", "b-dev-test.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s to exist", name)
		}
	}
}

func TestSync_with_hosts(t *testing.T) {
	dir := t.TempDir()
	hostsFile := filepath.Join(t.TempDir(), "hosts")
	os.WriteFile(hostsFile, []byte("127.0.0.1\tlocalhost\n"), 0644)

	reg := registry.New()
	rec := New(dir, reg, WithHostsPath(hostsFile))

	reg.Register(types.Route{Host: "api.foo.dev.test", Upstream: "http://127.0.0.1:3000"})

	if err := rec.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// Check Traefik config exists.
	if _, err := os.Stat(filepath.Join(dir, "api-foo-dev-test.yaml")); err != nil {
		t.Error("expected Traefik config file")
	}

	// Check hosts file was updated.
	data, _ := os.ReadFile(hostsFile)
	if !strings.Contains(string(data), "api.foo.dev.test") {
		t.Error("expected hosts file to contain hostname")
	}
	if !strings.Contains(string(data), "localhost") {
		t.Error("original hosts content should be preserved")
	}
}

func TestOnEvent_syncs(t *testing.T) {
	dir := t.TempDir()
	reg := registry.New()
	rec := New(dir, reg)

	reg.Register(types.Route{Host: "x.dev.test", Upstream: "http://127.0.0.1:1"})
	rec.OnEvent(registry.Event{
		Kind:  registry.EventRegistered,
		Route: types.Route{Host: "x.dev.test"},
	})

	if _, err := os.Stat(filepath.Join(dir, "x-dev-test.yaml")); err != nil {
		t.Error("expected config file after OnEvent")
	}
}

func TestRun_sweeps_expired(t *testing.T) {
	now := time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC)
	clk := func() time.Time { return now }

	dir := t.TempDir()
	reg := registry.New(registry.WithClock(clk))
	rec := New(dir, reg, WithSweepInterval(10*time.Millisecond))

	reg.Register(types.Route{Host: "a.dev.test", Upstream: "http://127.0.0.1:1", TTL: time.Second})

	// Write initial config.
	rec.Sync()

	// Advance time past TTL.
	now = now.Add(5 * time.Second)
	reg.SetClock(func() time.Time { return now })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	rec.Run(ctx)

	// After sweep, the expired route's config file should be removed.
	if _, err := os.Stat(filepath.Join(dir, "a-dev-test.yaml")); !os.IsNotExist(err) {
		t.Error("expected config file to be removed after sweep")
	}
}
