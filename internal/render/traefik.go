// Package render generates Traefik dynamic configuration files from the
// route registry state.
package render

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/infobloxopen/devedge/pkg/types"
)

// safeName converts a hostname into a filesystem- and Traefik-safe identifier.
func safeName(host string) string {
	return strings.ReplaceAll(host, ".", "-")
}

// TraefikRoute generates the YAML content for a single route's Traefik
// dynamic configuration.
func TraefikRoute(r types.Route) string {
	name := safeName(r.Host)
	return fmt.Sprintf(`http:
  routers:
    %s:
      rule: "Host(%[3]s)"
      service: %[2]s-svc
      entryPoints:
        - websecure
      tls: {}
  services:
    %[2]s-svc:
      loadBalancer:
        servers:
          - url: "%[4]s"
`, name, name, "`"+r.Host+"`", r.Upstream)
}

// WriteRouteFile atomically writes a Traefik dynamic config file for a route
// into the given directory.
func WriteRouteFile(dir string, r types.Route) error {
	content := TraefikRoute(r)
	name := safeName(r.Host) + ".yaml"
	target := filepath.Join(dir, name)
	tmp := target + ".tmp"

	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// RemoveRouteFile removes the Traefik dynamic config file for a host.
func RemoveRouteFile(dir string, host string) error {
	name := safeName(host) + ".yaml"
	target := filepath.Join(dir, name)
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SyncAll writes config files for all provided routes and removes files for
// hosts no longer present.
func SyncAll(dir string, routes []types.Route) error {
	// Build set of expected files.
	want := make(map[string]bool, len(routes))
	for _, r := range routes {
		want[safeName(r.Host)+".yaml"] = true
		if err := WriteRouteFile(dir, r); err != nil {
			return err
		}
	}

	// Remove stale files.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dir: %w", err)
	}
	for _, e := range entries {
		if !want[e.Name()] && strings.HasSuffix(e.Name(), ".yaml") {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}
