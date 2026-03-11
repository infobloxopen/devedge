package integration

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/client"
	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/pkg/types"
)

func TestServerIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
	configDir := filepath.Join(tmpDir, "dynamic")
	hostsFile := filepath.Join(tmpDir, "hosts")
	os.WriteFile(hostsFile, []byte("127.0.0.1\tlocalhost\n"), 0644)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	srv := daemon.NewServer(
		daemon.WithSocketPath(socketPath),
		daemon.WithConfigDir(configDir),
		daemon.WithServerLogger(logger),
		daemon.WithHostsPath(hostsFile),
		daemon.WithTCPAddr("127.0.0.1:0"), // random port for testing
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(ctx)
	}()

	// Wait for socket to be ready.
	for i := 0; i < 50; i++ {
		if conn, err := net.Dial("unix", socketPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	c := client.New(socketPath)

	// Status.
	status, err := c.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status["status"] != "running" {
		t.Errorf("status = %v", status["status"])
	}

	// Register.
	err = c.Register(ctx, daemon.RegisterRequest{
		Host: "api.foo.dev.test", Upstream: "http://127.0.0.1:3000",
		Project: "foo", Owner: "test",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// List.
	routes, err := c.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Host != "api.foo.dev.test" {
		t.Errorf("host = %q", routes[0].Host)
	}

	// Lookup.
	route, err := c.Lookup(ctx, "api.foo.dev.test")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if route.Upstream != "http://127.0.0.1:3000" {
		t.Errorf("upstream = %q", route.Upstream)
	}

	// Verify Traefik config was written.
	configFile := filepath.Join(configDir, "api-foo-dev-test.yaml")
	if _, err := os.Stat(configFile); err != nil {
		t.Error("expected Traefik config file to exist")
	}

	// Verify hosts file was updated.
	hostsData, _ := os.ReadFile(hostsFile)
	if !strings.Contains(string(hostsData), "api.foo.dev.test") {
		t.Error("expected hosts file to contain registered hostname")
	}

	// Conflict: different owner.
	err = c.Register(ctx, daemon.RegisterRequest{
		Host: "api.foo.dev.test", Upstream: "http://127.0.0.1:9999",
		Owner: "attacker",
	})
	if err == nil {
		t.Error("expected conflict error")
	}

	// Deregister.
	err = c.Deregister(ctx, "api.foo.dev.test")
	if err != nil {
		t.Fatalf("Deregister: %v", err)
	}

	routes, _ = c.List(ctx)
	if len(routes) != 0 {
		t.Errorf("expected 0 routes after deregister, got %d", len(routes))
	}

	// Register two routes for a project, then deregister project.
	c.Register(ctx, daemon.RegisterRequest{Host: "a.dev.test", Upstream: "http://127.0.0.1:1", Project: "bar"})
	c.Register(ctx, daemon.RegisterRequest{Host: "b.dev.test", Upstream: "http://127.0.0.1:2", Project: "bar"})

	n, err := c.DeregisterProject(ctx, "bar")
	if err != nil {
		t.Fatalf("DeregisterProject: %v", err)
	}
	if n != 2 {
		t.Errorf("removed = %d, want 2", n)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Errorf("server error: %v", err)
	}
}

// TestServerIntegration_raw exercises the API with raw HTTP to verify
// the JSON wire format matches what consumers expect.
func TestServerIntegration_raw(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
	configDir := filepath.Join(tmpDir, "dynamic")

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	hostsFile2 := filepath.Join(tmpDir, "hosts2")
	os.WriteFile(hostsFile2, []byte("127.0.0.1\tlocalhost\n"), 0644)

	srv := daemon.NewServer(
		daemon.WithSocketPath(socketPath),
		daemon.WithConfigDir(configDir),
		daemon.WithServerLogger(logger),
		daemon.WithTCPAddr("127.0.0.1:0"),
		daemon.WithHostsPath(hostsFile2),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.Run(ctx)

	for i := 0; i < 50; i++ {
		if conn, err := net.Dial("unix", socketPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}

	// PUT register.
	body := strings.NewReader(`{"host":"x.dev.test","upstream":"http://127.0.0.1:1"}`)
	req, _ := http.NewRequest("PUT", "http://devedge/v1/routes", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("PUT status = %d", resp.StatusCode)
	}

	var route types.Route
	json.NewDecoder(resp.Body).Decode(&route)
	resp.Body.Close()

	if route.Host != "x.dev.test" {
		t.Errorf("host = %q", route.Host)
	}
	if route.Source != "api" {
		t.Errorf("source = %q, want api", route.Source)
	}

	cancel()
}
