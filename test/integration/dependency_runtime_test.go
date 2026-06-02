package integration

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/client"
	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/internal/depruntime"
)

// recordingFake is a co-existence-safe, in-memory Provisioner: it never touches a
// real cluster, and records the bindings it provisions/drops so tests can assert
// per-service isolation.
type recordingFake struct {
	mu      sync.Mutex
	ensured []depruntime.Binding
	dropped []depruntime.Binding
}

func (f *recordingFake) EnsureInstance(_ context.Context, ref depruntime.InstanceRef) (depruntime.Instance, error) {
	port := 5432
	if ref.Engine == depruntime.EngineRedis {
		port = 6379
	}
	return depruntime.Instance{Engine: ref.Engine, Host: string(ref.Engine) + ".dev.test", Port: port}, nil
}
func (f *recordingFake) Ready(context.Context, depruntime.InstanceRef) error { return nil }
func (f *recordingFake) EnsureDatabase(_ context.Context, b depruntime.Binding) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensured = append(f.ensured, b)
	return nil
}
func (f *recordingFake) DropDatabase(_ context.Context, b depruntime.Binding) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropped = append(f.dropped, b)
	return nil
}

// startDepDaemon brings up a daemon with the fake provisioner and a temp DSN base
// dir, returning a client and the DSN base dir. Co-existence-safe (all temp).
func startDepDaemon(t *testing.T, prov depruntime.Provisioner) (*client.Client, string) {
	t.Helper()
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
	baseDir := filepath.Join(tmpDir, "home")
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	srv := daemon.NewServer(
		daemon.WithSocketPath(socketPath),
		daemon.WithConfigDir(filepath.Join(tmpDir, "dynamic")),
		daemon.WithServerLogger(logger),
		daemon.WithTCPAddr("127.0.0.1:0"),
		daemon.WithProvisioner(prov),
		daemon.WithDepBaseDir(baseDir),
	)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-errCh })

	for i := 0; i < 50; i++ {
		if conn, err := net.Dial("unix", socketPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return client.New(socketPath), baseDir
}

// T015: project-up drives a declared postgres dependency to Ready, writes the DSN
// file, reports the indirect env var, and is idempotent.
func TestDependencyUp_postgres(t *testing.T) {
	c, baseDir := startDepDaemon(t, &recordingFake{})
	ctx := context.Background()
	deps := []daemon.DependencyRequest{{Name: "db", Engine: "postgres", Port: 5432}}

	results, err := c.ApplyDependencies(ctx, "webhooks", daemon.ApplyRequest{Dependencies: deps})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(results) != 1 || !results[0].Ready() {
		t.Fatalf("want Ready, got %+v", results)
	}
	if results[0].EnvVarName != "DATABASE_URL" || !strings.HasPrefix(results[0].EnvVarValue, "fsnotify://postgres/") {
		t.Errorf("env var = %+v", results[0])
	}

	// The real DSN is in the file under the daemon's DSN base dir, not the env var.
	dsnPath := filepath.Join(baseDir, "services", "webhooks", "db.dsn")
	data, err := os.ReadFile(dsnPath)
	if err != nil {
		t.Fatalf("read dsn file %s: %v", dsnPath, err)
	}
	if !strings.HasPrefix(string(data), "postgres://") {
		t.Errorf("dsn file = %q", data)
	}

	// Idempotent re-apply.
	if _, err := c.ApplyDependencies(ctx, "webhooks", daemon.ApplyRequest{Dependencies: deps}); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
}

// T021: two services declaring the same dependency name get isolated bindings.
func TestDependencyIsolation(t *testing.T) {
	fake := &recordingFake{}
	c, _ := startDepDaemon(t, fake)
	ctx := context.Background()
	deps := []daemon.DependencyRequest{{Name: "db", Engine: "postgres", Port: 5432}}

	if _, err := c.ApplyDependencies(ctx, "svc-a", daemon.ApplyRequest{Dependencies: deps}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApplyDependencies(ctx, "svc-b", daemon.ApplyRequest{Dependencies: deps}); err != nil {
		t.Fatal(err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.ensured) != 2 {
		t.Fatalf("want 2 ensured bindings, got %d", len(fake.ensured))
	}
	a, b := fake.ensured[0], fake.ensured[1]
	if a.Database == b.Database || a.User == b.User {
		t.Errorf("co-located services not isolated: %+v vs %+v", a, b)
	}
}

// T032: two services declaring the same redis dependency get isolated bindings
// (distinct ACL user + key namespace).
func TestDependencyIsolation_redis(t *testing.T) {
	fake := &recordingFake{}
	c, _ := startDepDaemon(t, fake)
	ctx := context.Background()
	deps := []daemon.DependencyRequest{{Name: "cache", Engine: "redis", Port: 6379}}

	if _, err := c.ApplyDependencies(ctx, "svc-a", daemon.ApplyRequest{Dependencies: deps}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApplyDependencies(ctx, "svc-b", daemon.ApplyRequest{Dependencies: deps}); err != nil {
		t.Fatal(err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.ensured) != 2 {
		t.Fatalf("want 2 ensured bindings, got %d", len(fake.ensured))
	}
	a, b := fake.ensured[0], fake.ensured[1]
	if a.Engine != depruntime.EngineRedis {
		t.Fatalf("expected redis bindings, got %s", a.Engine)
	}
	if a.User == b.User || a.KeyNamespace == b.KeyNamespace || a.KeyNamespace == "" {
		t.Errorf("redis services not isolated: %+v vs %+v", a, b)
	}
}

// T025: default release keeps data (no drop); clean release drops only that
// service's binding and removes its DSN file.
func TestDependencyDown_keepVsClean(t *testing.T) {
	fake := &recordingFake{}
	c, baseDir := startDepDaemon(t, fake)
	ctx := context.Background()
	deps := []daemon.DependencyRequest{{Name: "db", Engine: "postgres", Port: 5432}}
	dsnPath := filepath.Join(baseDir, "services", "webhooks", "db.dsn")

	mustApply := func() {
		if _, err := c.ApplyDependencies(ctx, "webhooks", daemon.ApplyRequest{Dependencies: deps}); err != nil {
			t.Fatal(err)
		}
	}

	// Default down: DSN file removed, but no DropDatabase (data preserved).
	mustApply()
	if err := c.ReleaseDependencies(ctx, "webhooks", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dsnPath); !os.IsNotExist(err) {
		t.Errorf("default down should remove the DSN file")
	}
	fake.mu.Lock()
	if len(fake.dropped) != 0 {
		t.Errorf("default down must not drop data, dropped=%v", fake.dropped)
	}
	fake.mu.Unlock()

	// Clean down: DropDatabase called for this service's binding.
	mustApply()
	if err := c.ReleaseDependencies(ctx, "webhooks", true); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.dropped) != 1 || fake.dropped[0].Service != "webhooks" {
		t.Errorf("clean down should drop exactly this service's binding, got %+v", fake.dropped)
	}
}
