// Package platform defines the interface for OS-specific service management
// and provides detection of the current platform.
package platform

import "runtime"

// Adapter handles platform-specific operations: daemon installation,
// startup, shutdown, and health checks.
type Adapter interface {
	// Install sets up the daemon as a background service.
	Install() error

	// Uninstall removes the background service.
	Uninstall() error

	// Start starts the daemon service.
	Start() error

	// Stop stops the daemon service.
	Stop() error

	// IsRunning reports whether the daemon service is currently running.
	IsRunning() (bool, error)

	// Name returns a human-readable platform name.
	Name() string
}

// Detect returns the appropriate platform adapter for the current OS.
func Detect() Adapter {
	switch runtime.GOOS {
	case "darwin":
		return &DarwinAdapter{}
	case "linux":
		return &LinuxAdapter{}
	default:
		return &UnsupportedAdapter{OS: runtime.GOOS}
	}
}
