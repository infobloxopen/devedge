package cluster

import (
	"strings"
	"testing"
	"time"
)

// T002: target parser tests — must be red until parsePortForwardTarget is implemented.

func TestParsePortForwardTarget(t *testing.T) {
	tests := []struct {
		target  string
		wantPod string
		wantErr string // substring
	}{
		{"statefulset/devedge-postgres", "devedge-postgres-0", ""},
		{"statefulset/devedge-redis-myslug", "devedge-redis-myslug-0", ""},
		{"pod/foo", "", "unsupported"},
		{"deployment/bar", "", "unsupported"},
		{"", "", "unsupported"},
		{"justname", "", "unsupported"},
	}
	for _, tc := range tests {
		pod, err := parsePortForwardTarget(tc.target)
		if tc.wantErr != "" {
			if err == nil {
				t.Errorf("parsePortForwardTarget(%q): want error containing %q, got nil", tc.target, tc.wantErr)
				continue
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("parsePortForwardTarget(%q): error %q does not contain %q", tc.target, err.Error(), tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePortForwardTarget(%q): unexpected error: %v", tc.target, err)
			continue
		}
		if pod != tc.wantPod {
			t.Errorf("parsePortForwardTarget(%q): got pod %q, want %q", tc.target, pod, tc.wantPod)
		}
	}
}

// T003: PortForward lifecycle — no network required.

func TestPortForwardStopIdempotent(t *testing.T) {
	pf := &PortForward{
		stopCh: make(chan struct{}),
	}
	pf.Stop()
	pf.Stop() // must not panic
}

func TestPortForwardAliveAfterStop(t *testing.T) {
	pf := &PortForward{
		stopCh: make(chan struct{}),
	}
	if !pf.Alive() {
		t.Fatal("Alive() should be true before Stop()")
	}
	pf.markDone()
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !pf.Alive() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Alive() still true 100ms after markDone()")
}
