package integration

import (
	"os"
	"path/filepath"
	"testing"
)

// shortSocketPath returns a unix-socket path short enough for macOS's ~104-char
// sun_path limit. t.TempDir() embeds the full (long) test name, which can
// overflow that limit and make the daemon's bind/dial fail with "invalid
// argument" (observed on macOS CI). The socket lives in its own short-named
// temp dir, removed when the test finishes.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "de")
	if err != nil {
		t.Fatalf("create socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "d.sock")
}
