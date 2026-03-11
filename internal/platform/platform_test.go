package platform

import (
	"runtime"
	"testing"
)

func TestDetect(t *testing.T) {
	adapter := Detect()
	if adapter == nil {
		t.Fatal("Detect returned nil")
	}

	name := adapter.Name()
	if name == "" {
		t.Error("Name() returned empty string")
	}

	switch runtime.GOOS {
	case "darwin":
		if _, ok := adapter.(*DarwinAdapter); !ok {
			t.Errorf("expected DarwinAdapter, got %T", adapter)
		}
	case "linux":
		if _, ok := adapter.(*LinuxAdapter); !ok {
			t.Errorf("expected LinuxAdapter, got %T", adapter)
		}
	default:
		if _, ok := adapter.(*UnsupportedAdapter); !ok {
			t.Errorf("expected UnsupportedAdapter, got %T", adapter)
		}
	}
}

func TestUnsupportedAdapter(t *testing.T) {
	a := &UnsupportedAdapter{OS: "plan9"}
	if err := a.Install(); err == nil {
		t.Error("expected error from Install")
	}
	if err := a.Start(); err == nil {
		t.Error("expected error from Start")
	}
	if err := a.Stop(); err == nil {
		t.Error("expected error from Stop")
	}
	if _, err := a.IsRunning(); err == nil {
		t.Error("expected error from IsRunning")
	}
}
