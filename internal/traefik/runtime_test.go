package traefik

import (
	"log/slog"
	"testing"
)

func TestNewRuntime(t *testing.T) {
	r := NewRuntime("/tmp/traefik", slog.Default())
	if r == nil {
		t.Fatal("NewRuntime returned nil")
	}
	if r.IsRunning() {
		t.Error("should not be running initially")
	}
}

func TestFindBinary(t *testing.T) {
	// This test is informational — it passes whether or not traefik is installed.
	path, err := FindBinary()
	if err != nil {
		t.Skipf("traefik not found: %v", err)
	}
	if path == "" {
		t.Error("path should not be empty when found")
	}
	t.Logf("found traefik at %s", path)
}
