// Package integration — platform.data.kit regression surface guard.
//
// platform.data.kit (DK) depends on the following devedge route API surface and
// MUST NOT break across features:
//
//  1. PUT  /v1/routes            — register a route (client.Register)
//  2. GET  /v1/routes            — list active routes (client.List)
//  3. DELETE /v1/projects/{p}    — bulk-remove by project (client.DeregisterProject)
//  4. kind: Config project files — parse via config.ParseResource → ToRoutes
//
// If either test in this file fails, the change broke the DK integration contract
// and must not be merged until the surface is restored or DK is migrated first.
package integration

import (
	"context"
	"testing"

	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/pkg/config"
)

// TestDKRouteSurfaceUnchanged verifies that the three HTTP endpoints consumed
// directly by platform.data.kit still behave as expected:
//
//   - PUT  /v1/routes  (Register)
//   - GET  /v1/routes  (List)
//   - DELETE /v1/projects/{project}  (DeregisterProject)
func TestDKRouteSurfaceUnchanged(t *testing.T) {
	c := startRouteDaemon(t)
	ctx := context.Background()

	const (
		host     = "playground.cube.dev.test"
		upstream = "http://127.0.0.1:4000"
		project  = "cube"
		owner    = "project-file"
	)

	// 1. PUT /v1/routes — register exactly as DK does.
	if err := c.Register(ctx, daemon.RegisterRequest{
		Host:     host,
		Upstream: upstream,
		Project:  project,
		Owner:    owner,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// 2. GET /v1/routes — the route must appear in the list.
	routes, err := c.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, r := range routes {
		if r.Host == host {
			found = true
			if r.Upstream != upstream {
				t.Errorf("List: upstream = %q, want %q", r.Upstream, upstream)
			}
			if r.Project != project {
				t.Errorf("List: project = %q, want %q", r.Project, project)
			}
			break
		}
	}
	if !found {
		t.Fatalf("List: route %q not found in %d routes", host, len(routes))
	}

	// 3. DELETE /v1/projects/{project} — bulk-remove and confirm gone.
	removed, err := c.DeregisterProject(ctx, project)
	if err != nil {
		t.Fatalf("DeregisterProject: %v", err)
	}
	if removed < 1 {
		t.Errorf("DeregisterProject: removed = %d, want ≥1", removed)
	}

	after, err := c.List(ctx)
	if err != nil {
		t.Fatalf("List after deregister: %v", err)
	}
	for _, r := range after {
		if r.Host == host {
			t.Errorf("List: route %q still present after DeregisterProject", host)
		}
	}
}

// TestDKConfigKindStillParses verifies that a kind: Config project document
// (the format DK uses for its project files) still parses via
// config.ParseResource and produces the expected route via ToRoutes.
func TestDKConfigKindStillParses(t *testing.T) {
	const doc = `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Config
metadata:
  name: cube
spec:
  routes:
    - host: playground.cube.dev.test
      upstream: http://127.0.0.1:4000
`

	res, err := config.ParseResource([]byte(doc))
	if err != nil {
		t.Fatalf("ParseResource: %v", err)
	}

	if res.Project() != "cube" {
		t.Errorf("Project() = %q, want %q", res.Project(), "cube")
	}

	routes, err := res.ToRoutes()
	if err != nil {
		t.Fatalf("ToRoutes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("ToRoutes: got %d routes, want 1", len(routes))
	}

	r := routes[0]
	if r.Host != "playground.cube.dev.test" {
		t.Errorf("route.Host = %q, want %q", r.Host, "playground.cube.dev.test")
	}
	if r.Upstream != "http://127.0.0.1:4000" {
		t.Errorf("route.Upstream = %q, want %q", r.Upstream, "http://127.0.0.1:4000")
	}
	if r.Project != "cube" {
		t.Errorf("route.Project = %q, want %q", r.Project, "cube")
	}
}
