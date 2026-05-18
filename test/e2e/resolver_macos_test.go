//go:build darwin && e2e

package e2e

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/internal/dnsserver"
	"github.com/infobloxopen/devedge/pkg/types"
)

// TestResolverFrameworkPath drives the full macOS path: write
// /etc/resolver/<suffix>, start the daemon on the default DNS port,
// and resolve a synthetic hostname via the system resolver. It MUST
// return EdgeIP.
//
// Skipped unless DEVEDGE_E2E_MACOS=1 and effective uid is 0 because
// writing /etc/resolver/ requires root.
func TestResolverFrameworkPath(t *testing.T) {
	if os.Getenv("DEVEDGE_E2E_MACOS") != "1" {
		t.Skip("set DEVEDGE_E2E_MACOS=1 to run macOS resolver e2e")
	}
	if os.Geteuid() != 0 {
		t.Skip("test requires root to write /etc/resolver/")
	}

	suffix := fmt.Sprintf("devedge-e2e-%d.test", time.Now().UnixNano())
	dropIn := filepath.Join("/etc/resolver", suffix)

	// Bind the DNS server on the default port (15354). Anything else
	// won't match the /etc/resolver/<suffix> drop-in's port.
	dnsAddr := dnsserver.DefaultAddr

	// Write the resolver drop-in pointing at our daemon.
	content := "# Managed by devedge — do not edit\nnameserver 127.0.0.1\nport 15354\n"
	if err := os.WriteFile(dropIn, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", dropIn, err)
	}
	t.Cleanup(func() { _ = os.Remove(dropIn) })

	// Force mDNSResponder to re-read /etc/resolver/.
	// killall on macOS triggers launchd to relaunch the daemon.
	_ = runQuiet("killall", "-HUP", "mDNSResponder")

	tmpDir := t.TempDir()
	t.Setenv("DEVEDGE_HOME", tmpDir)

	src := dnsserver.NewStaticSuffixSource(suffix)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	srv := daemon.NewServer(
		daemon.WithSocketPath(filepath.Join(tmpDir, "test.sock")),
		daemon.WithConfigDir(filepath.Join(tmpDir, "dynamic")),
		daemon.WithServerLogger(logger),
		daemon.WithHostsPath(filepath.Join(tmpDir, "hosts")),
		daemon.WithTCPAddr("127.0.0.1:0"),
		daemon.WithDNSAddr(dnsAddr),
		daemon.WithDNSSuffixSource(src),
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	// Wait until the DNS endpoint is responsive.
	target := "devedge-healthcheck." + suffix
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for {
		if time.Now().After(deadline) {
			t.Fatalf("system resolver did not return EdgeIP within 10s: last err=%v", lastErr)
		}
		addrs, err := net.LookupHost(target)
		if err == nil {
			for _, a := range addrs {
				if strings.EqualFold(a, types.EdgeIP) {
					return
				}
			}
			lastErr = fmt.Errorf("got %v, want %s", addrs, types.EdgeIP)
		} else {
			lastErr = err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func runQuiet(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}
