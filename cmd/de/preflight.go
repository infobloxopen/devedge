package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// requireTools checks that every named CLI tool is available on PATH.
// If one or more tools are missing it returns a single actionable error
// that lists all missing names so the caller can fix them in one shot.
// Returns nil when all tools are found.
func requireTools(tools ...string) error {
	var missing []string
	for _, t := range tools {
		if _, err := exec.LookPath(t); err != nil {
			missing = append(missing, t)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("missing required tools: %s — install them to use dependency runtime",
		strings.Join(missing, ", "))
}

// requireDependencyTools is a convenience wrapper that checks for the tools
// required to manage project dependency runtimes (helm, kubectl, k3d).
// It is intended to be called by "de project up" when a Service declares
// dependencies; it is a no-op for kind: Config files or Services with no
// dependencies.
func requireDependencyTools() error {
	return requireTools("helm", "kubectl", "k3d")
}
