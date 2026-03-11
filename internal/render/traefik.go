// Package render generates Traefik dynamic configuration files from the
// route registry state.
package render

import (
	"fmt"
	"net/url"
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
// dynamic configuration. HTTP routes use Host-header matching; TCP routes
// use SNI matching with TLS termination.
func TraefikRoute(r types.Route) string {
	if r.EffectiveProtocol() == types.ProtocolTCP {
		return traefikTCPRoute(r)
	}
	return traefikHTTPRoute(r)
}

// traefikHTTPRoute generates config for an HTTP/HTTPS route.
func traefikHTTPRoute(r types.Route) string {
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

// traefikTCPRoute generates config for a TCP route with SNI-based TLS
// termination on the frontend and raw TCP forwarding to the backend.
func traefikTCPRoute(r types.Route) string {
	name := safeName(r.Host)
	addr := normalizeTCPAddress(r.Upstream)

	tlsBlock := "      tls: {}"
	if r.BackendTLS {
		tlsBlock = `      tls:
        passthrough: true`
	}

	return fmt.Sprintf(`tcp:
  routers:
    %s:
      rule: "HostSNI(%[3]s)"
      service: %[2]s-svc
      entryPoints:
        - websecure
%[5]s
  services:
    %[2]s-svc:
      loadBalancer:
        servers:
          - address: "%[4]s"
`, name, name, "`"+r.Host+"`", addr, tlsBlock)
}

// normalizeTCPAddress ensures a TCP upstream is in host:port format.
// Strips any scheme prefix (tcp://, etc.) since Traefik TCP services
// expect bare host:port.
func normalizeTCPAddress(upstream string) string {
	// If it looks like a URL with a scheme, parse and extract host.
	if strings.Contains(upstream, "://") {
		if u, err := url.Parse(upstream); err == nil {
			return u.Host
		}
	}
	return upstream
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
