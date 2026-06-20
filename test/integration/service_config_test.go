package integration

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/client"
	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/pkg/config"
)

// startRouteDaemon brings up a daemon on a private socket + temp dirs and
// returns a connected client. Everything is scoped to t.TempDir(), so the test
// is co-existence-safe: it never touches a shared devedge daemon or shared k3d.
func startRouteDaemon(t *testing.T) *client.Client {
	t.Helper()

	tmpDir := t.TempDir()
	socketPath := shortSocketPath(t)
	configDir := filepath.Join(tmpDir, "dynamic")
	hostsFile := filepath.Join(tmpDir, "hosts")
	if err := os.WriteFile(hostsFile, []byte("127.0.0.1\tlocalhost\n"), 0644); err != nil {
		t.Fatalf("write hosts: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	srv := daemon.NewServer(
		daemon.WithSocketPath(socketPath),
		daemon.WithConfigDir(configDir),
		daemon.WithServerLogger(logger),
		daemon.WithHostsPath(hostsFile),
		daemon.WithTCPAddr("127.0.0.1:0"),
	)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-errCh
	})

	for i := 0; i < 50; i++ {
		if conn, err := net.Dial("unix", socketPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	return client.New(socketPath)
}

// registerResource loads a project file's bytes via config.ParseResource and
// registers its routes through the daemon, mirroring `de project up`.
func registerResource(t *testing.T, c *client.Client, doc string) string {
	t.Helper()
	res, err := config.ParseResource([]byte(doc))
	if err != nil {
		t.Fatalf("ParseResource: %v", err)
	}
	routes, err := res.ToRoutes()
	if err != nil {
		t.Fatalf("ToRoutes: %v", err)
	}
	for _, r := range routes {
		err := c.Register(context.Background(), daemon.RegisterRequest{
			Host: r.Host, Upstream: r.Upstream, Project: r.Project, Owner: "project-file",
		})
		if err != nil {
			t.Fatalf("register %s: %v", r.Host, err)
		}
	}
	return res.Project()
}

// TestServiceConfigRouting_parity asserts that a kind: Service file routes
// through the daemon identically to an equivalent kind: Config file, and that
// project-down removes the routes. Unique project names + hostnames keep it
// co-existence-safe on a shared cluster.
func TestServiceConfigRouting_parity(t *testing.T) {
	c := startRouteDaemon(t)
	ctx := context.Background()

	// Unique per-run identifiers so concurrent/shared runs never collide.
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	svcProject := "svc-" + suffix
	cfgProject := "cfg-" + suffix
	svcHost := svcProject + ".dev.test"
	cfgHost := cfgProject + ".dev.test"
	upstream := "http://127.0.0.1:18080"

	serviceDoc := fmt.Sprintf(`
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: %s
spec:
  dev:
    hostname: %s
  routes:
    - host: %s
      upstream: %s
`, svcProject, svcHost, svcHost, upstream)

	configDoc := fmt.Sprintf(`
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Config
metadata:
  name: %s
spec:
  routes:
    - host: %s
      upstream: %s
`, cfgProject, cfgHost, upstream)

	svcName := registerResource(t, c, serviceDoc)
	cfgName := registerResource(t, c, configDoc)
	t.Cleanup(func() {
		c.DeregisterProject(ctx, svcName)
		c.DeregisterProject(ctx, cfgName)
	})

	// Parity: both hosts resolve to the same upstream.
	svcRoute, err := c.Lookup(ctx, svcHost)
	if err != nil {
		t.Fatalf("Lookup service host: %v", err)
	}
	cfgRoute, err := c.Lookup(ctx, cfgHost)
	if err != nil {
		t.Fatalf("Lookup config host: %v", err)
	}
	if svcRoute.Upstream != cfgRoute.Upstream {
		t.Errorf("upstream mismatch: service %q vs config %q", svcRoute.Upstream, cfgRoute.Upstream)
	}
	if svcRoute.Project != svcName {
		t.Errorf("service route Project = %q, want %q", svcRoute.Project, svcName)
	}

	// project down removes the Service routes.
	n, err := c.DeregisterProject(ctx, svcName)
	if err != nil {
		t.Fatalf("DeregisterProject: %v", err)
	}
	if n != 1 {
		t.Errorf("removed = %d, want 1", n)
	}
	if _, err := c.Lookup(ctx, svcHost); err == nil {
		t.Errorf("expected service host to be gone after project down")
	}
}
