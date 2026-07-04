package config

import (
	"testing"

	"github.com/infobloxopen/devedge/pkg/types"
)

const validShellMethod1 = `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: notesapp
spec:
  host: notesapp.dev.test
  shellUpstream: http://127.0.0.1:4200
  cdn:
    host: cdn.dev.test
  api:
    method: 1
    prefix: /api
    upstream: http://127.0.0.1:8080
  ufes:
    - id: notes-ufe
      route: notes
      upstream: http://127.0.0.1:4201
    - id: tags-ufe
      route: tags
      upstream: http://127.0.0.1:4202
`

const validShellMethod2 = `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: notesapp
spec:
  host: notesapp.dev.test
  shellUpstream: http://127.0.0.1:4200
  cdn:
    host: cdn.dev.test
  api:
    method: 2
    services:
      - host: api.notesapp.dev.test
        upstream: http://127.0.0.1:8080
      - host: files.notesapp.dev.test
        upstream: http://127.0.0.1:8081
  ufes:
    - id: notes-ufe
      route: notes
      upstream: http://127.0.0.1:4201
`

func TestParseShell_Method1_Valid(t *testing.T) {
	s, err := ParseShell([]byte(validShellMethod1))
	if err != nil {
		t.Fatalf("ParseShell: %v", err)
	}
	if s.Project() != "notesapp" {
		t.Errorf("Project() = %q, want notesapp", s.Project())
	}
	if s.Spec.Host != "notesapp.dev.test" {
		t.Errorf("Host = %q, want notesapp.dev.test", s.Spec.Host)
	}
	if s.Spec.API.Method != 1 {
		t.Errorf("api.method = %d, want 1", s.Spec.API.Method)
	}
	if len(s.Spec.UFEs) != 2 {
		t.Fatalf("ufes = %d, want 2", len(s.Spec.UFEs))
	}
}

func TestParseShell_Method2_Valid(t *testing.T) {
	s, err := ParseShell([]byte(validShellMethod2))
	if err != nil {
		t.Fatalf("ParseShell: %v", err)
	}
	if s.Spec.API.Method != 2 {
		t.Errorf("api.method = %d, want 2", s.Spec.API.Method)
	}
	if len(s.Spec.API.Services) != 2 {
		t.Fatalf("api.services = %d, want 2", len(s.Spec.API.Services))
	}
}

func TestParseShell_DefaultAPIPrefix(t *testing.T) {
	input := []byte(`apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: notesapp
spec:
  host: notesapp.dev.test
  shellUpstream: http://127.0.0.1:4200
  cdn:
    host: cdn.dev.test
  api:
    method: 1
    upstream: http://127.0.0.1:8080
  ufes:
    - id: notes-ufe
      route: notes
      upstream: http://127.0.0.1:4201
`)
	s, err := ParseShell(input)
	if err != nil {
		t.Fatalf("ParseShell: %v", err)
	}
	if s.Spec.API.Prefix != "/api" {
		t.Errorf("default api.prefix = %q, want /api", s.Spec.API.Prefix)
	}
}

func TestParseShell_UnknownField(t *testing.T) {
	input := []byte(`apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: notesapp
spec:
  host: notesapp.dev.test
  shellUpstream: http://127.0.0.1:4200
  bogus: nope
  cdn:
    host: cdn.dev.test
  api:
    method: 1
    upstream: http://127.0.0.1:8080
  ufes:
    - id: notes-ufe
      route: notes
      upstream: http://127.0.0.1:4201
`)
	if _, err := ParseShell(input); err == nil {
		t.Fatal("expected error for unknown field 'bogus'")
	}
}

func TestParseShell_WrongKind(t *testing.T) {
	input := []byte(`apiVersion: devedge.infoblox.dev/v1alpha1
kind: Config
metadata:
  name: notesapp
spec:
  host: notesapp.dev.test
  shellUpstream: http://127.0.0.1:4200
  cdn:
    host: cdn.dev.test
  api:
    method: 1
    upstream: http://127.0.0.1:8080
  ufes:
    - id: notes-ufe
      route: notes
      upstream: http://127.0.0.1:4201
`)
	if _, err := ParseShell(input); err == nil {
		t.Fatal("expected error for wrong kind")
	}
}

func TestParseShell_MissingFields(t *testing.T) {
	cases := map[string]string{
		"missing host": `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: n
spec:
  shellUpstream: http://127.0.0.1:4200
  cdn:
    host: cdn.dev.test
  api:
    method: 1
    upstream: http://127.0.0.1:8080
  ufes:
    - id: u
      route: r
      upstream: http://127.0.0.1:4201
`,
		"missing shellUpstream": `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: n
spec:
  host: notesapp.dev.test
  cdn:
    host: cdn.dev.test
  api:
    method: 1
    upstream: http://127.0.0.1:8080
  ufes:
    - id: u
      route: r
      upstream: http://127.0.0.1:4201
`,
		"missing cdn.host": `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: n
spec:
  host: notesapp.dev.test
  shellUpstream: http://127.0.0.1:4200
  cdn: {}
  api:
    method: 1
    upstream: http://127.0.0.1:8080
  ufes:
    - id: u
      route: r
      upstream: http://127.0.0.1:4201
`,
		"empty ufes": `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: n
spec:
  host: notesapp.dev.test
  shellUpstream: http://127.0.0.1:4200
  cdn:
    host: cdn.dev.test
  api:
    method: 1
    upstream: http://127.0.0.1:8080
  ufes: []
`,
		"duplicate ufe id": `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: n
spec:
  host: notesapp.dev.test
  shellUpstream: http://127.0.0.1:4200
  cdn:
    host: cdn.dev.test
  api:
    method: 1
    upstream: http://127.0.0.1:8080
  ufes:
    - id: dup
      route: a
      upstream: http://127.0.0.1:4201
    - id: dup
      route: b
      upstream: http://127.0.0.1:4202
`,
		"duplicate ufe route": `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: n
spec:
  host: notesapp.dev.test
  shellUpstream: http://127.0.0.1:4200
  cdn:
    host: cdn.dev.test
  api:
    method: 1
    upstream: http://127.0.0.1:8080
  ufes:
    - id: a
      route: dup
      upstream: http://127.0.0.1:4201
    - id: b
      route: dup
      upstream: http://127.0.0.1:4202
`,
		"bad api.method": `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: n
spec:
  host: notesapp.dev.test
  shellUpstream: http://127.0.0.1:4200
  cdn:
    host: cdn.dev.test
  api:
    method: 3
    upstream: http://127.0.0.1:8080
  ufes:
    - id: u
      route: r
      upstream: http://127.0.0.1:4201
`,
		"method 1 missing upstream": `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: n
spec:
  host: notesapp.dev.test
  shellUpstream: http://127.0.0.1:4200
  cdn:
    host: cdn.dev.test
  api:
    method: 1
    prefix: /api
  ufes:
    - id: u
      route: r
      upstream: http://127.0.0.1:4201
`,
		"method 2 missing services": `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: n
spec:
  host: notesapp.dev.test
  shellUpstream: http://127.0.0.1:4200
  cdn:
    host: cdn.dev.test
  api:
    method: 2
  ufes:
    - id: u
      route: r
      upstream: http://127.0.0.1:4201
`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseShell([]byte(in)); err == nil {
				t.Fatalf("%s: expected error, got nil", name)
			}
		})
	}
}

func TestShell_ToRoutes_Method1(t *testing.T) {
	s, err := ParseShell([]byte(validShellMethod1))
	if err != nil {
		t.Fatalf("ParseShell: %v", err)
	}
	routes, err := s.ToRoutes()
	if err != nil {
		t.Fatalf("ToRoutes: %v", err)
	}
	// shell catch-all + /api strip route + 2 cdn routes.
	want := []types.Route{
		{Host: "notesapp.dev.test", Path: "", StripPrefix: false, Upstream: "http://127.0.0.1:4200", Project: "notesapp", Source: "project-file"},
		{Host: "notesapp.dev.test", Path: "/api", StripPrefix: true, Upstream: "http://127.0.0.1:8080", Project: "notesapp", Source: "project-file"},
		{Host: "cdn.dev.test", Path: "/notes", StripPrefix: true, Upstream: "http://127.0.0.1:4201", Project: "notesapp", Source: "project-file"},
		{Host: "cdn.dev.test", Path: "/tags", StripPrefix: true, Upstream: "http://127.0.0.1:4202", Project: "notesapp", Source: "project-file"},
	}
	assertRoutes(t, routes, want)
}

func TestShell_ToRoutes_Method2(t *testing.T) {
	s, err := ParseShell([]byte(validShellMethod2))
	if err != nil {
		t.Fatalf("ParseShell: %v", err)
	}
	routes, err := s.ToRoutes()
	if err != nil {
		t.Fatalf("ToRoutes: %v", err)
	}
	// shell catch-all + 2 per-service host routes + 1 cdn route.
	want := []types.Route{
		{Host: "notesapp.dev.test", Path: "", StripPrefix: false, Upstream: "http://127.0.0.1:4200", Project: "notesapp", Source: "project-file"},
		{Host: "api.notesapp.dev.test", Path: "", StripPrefix: false, Upstream: "http://127.0.0.1:8080", Project: "notesapp", Source: "project-file"},
		{Host: "files.notesapp.dev.test", Path: "", StripPrefix: false, Upstream: "http://127.0.0.1:8081", Project: "notesapp", Source: "project-file"},
		{Host: "cdn.dev.test", Path: "/notes", StripPrefix: true, Upstream: "http://127.0.0.1:4201", Project: "notesapp", Source: "project-file"},
	}
	assertRoutes(t, routes, want)
}

func assertRoutes(t *testing.T, got, want []types.Route) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("route count = %d, want %d (got %+v)", len(got), len(want), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Host != w.Host || g.Path != w.Path || g.StripPrefix != w.StripPrefix ||
			g.Upstream != w.Upstream || g.Project != w.Project || g.Source != w.Source {
			t.Errorf("route[%d] = {Host:%q Path:%q Strip:%v Up:%q Proj:%q Src:%q}, want {Host:%q Path:%q Strip:%v Up:%q Proj:%q Src:%q}",
				i, g.Host, g.Path, g.StripPrefix, g.Upstream, g.Project, g.Source,
				w.Host, w.Path, w.StripPrefix, w.Upstream, w.Project, w.Source)
		}
	}
}

func TestShell_ImportMap(t *testing.T) {
	s, err := ParseShell([]byte(validShellMethod1))
	if err != nil {
		t.Fatalf("ParseShell: %v", err)
	}
	im := s.ImportMap()
	want := map[string]string{
		"notes-ufe": "https://cdn.dev.test/notes/",
		"tags-ufe":  "https://cdn.dev.test/tags/",
	}
	if len(im) != len(want) {
		t.Fatalf("ImportMap size = %d, want %d (%+v)", len(im), len(want), im)
	}
	for id, url := range want {
		if im[id] != url {
			t.Errorf("ImportMap[%q] = %q, want %q", id, im[id], url)
		}
	}
}

func TestShell_HashRoutes(t *testing.T) {
	s, err := ParseShell([]byte(validShellMethod1))
	if err != nil {
		t.Fatalf("ParseShell: %v", err)
	}
	hr := s.HashRoutes()
	want := []HashRoute{
		{ID: "notes-ufe", Route: "notes"},
		{ID: "tags-ufe", Route: "tags"},
	}
	if len(hr) != len(want) {
		t.Fatalf("HashRoutes = %d, want %d", len(hr), len(want))
	}
	for i := range want {
		if hr[i] != want[i] {
			t.Errorf("HashRoutes[%d] = %+v, want %+v", i, hr[i], want[i])
		}
	}
}

func TestParseResource_Shell_dispatch(t *testing.T) {
	res, err := ParseResource([]byte(validShellMethod1))
	if err != nil {
		t.Fatalf("ParseResource: %v", err)
	}
	sh, ok := res.(*Shell)
	if !ok {
		t.Fatalf("ParseResource returned %T, want *Shell", res)
	}
	if sh.Project() != "notesapp" {
		t.Errorf("Project() = %q, want notesapp", sh.Project())
	}
	// A Shell declares no dependencies/workload — it must NOT satisfy those SPIs,
	// so runResourceUp's dependency/deploy steps are clean no-ops for it.
	if _, ok := res.(DependencyDeclarer); ok {
		t.Error("Shell unexpectedly implements DependencyDeclarer")
	}
	if _, ok := res.(WorkloadDeclarer); ok {
		t.Error("Shell unexpectedly implements WorkloadDeclarer")
	}
}

func TestShell_UpsertUFE(t *testing.T) {
	s, err := ParseShell([]byte(validShellMethod1))
	if err != nil {
		t.Fatalf("ParseShell: %v", err)
	}
	if len(s.Spec.UFEs) != 2 {
		t.Fatalf("fixture has %d ufes, want 2", len(s.Spec.UFEs))
	}

	// Append a brand-new uFE: reports updated=false, roster grows by one.
	if updated := s.UpsertUFE(ShellUFE{ID: "assets-ufe", Route: "assets", Upstream: "http://127.0.0.1:4203"}); updated {
		t.Error("UpsertUFE(new) reported updated=true, want false (appended)")
	}
	if len(s.Spec.UFEs) != 3 {
		t.Fatalf("after appending new uFE, roster = %d, want 3", len(s.Spec.UFEs))
	}

	// Upsert an EXISTING id: reports updated=true, roster does NOT grow, and the
	// entry's route + upstream are replaced in place (no duplicate).
	if updated := s.UpsertUFE(ShellUFE{ID: "notes-ufe", Route: "notes-v2", Upstream: "http://127.0.0.1:4210"}); !updated {
		t.Error("UpsertUFE(existing id) reported updated=false, want true (in-place update)")
	}
	if len(s.Spec.UFEs) != 3 {
		t.Fatalf("after updating existing uFE, roster = %d, want 3 (no duplicate)", len(s.Spec.UFEs))
	}
	var found *ShellUFE
	count := 0
	for i := range s.Spec.UFEs {
		if s.Spec.UFEs[i].ID == "notes-ufe" {
			found = &s.Spec.UFEs[i]
			count++
		}
	}
	if count != 1 {
		t.Fatalf("notes-ufe appears %d times, want exactly 1", count)
	}
	if found.Route != "notes-v2" || found.Upstream != "http://127.0.0.1:4210" {
		t.Errorf("notes-ufe not updated in place: %+v", *found)
	}

	// Still a valid shell after the upserts.
	if err := s.Validate(); err != nil {
		t.Errorf("shell invalid after upserts: %v", err)
	}
}

// --- WS-019: apilayout URL-layout seam (per-domain product-rest routing) ---

const validShellPerDomain = `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: notesapp
spec:
  host: app.dev.test
  shellUpstream: http://127.0.0.1:4200
  cdn:
    host: cdn.dev.test
  api:
    method: 1
    layout: product-rest
    services:
      - domain: notes
        upstream: http://127.0.0.1:8080
      - domain: tags
        upstream: http://127.0.0.1:8081
  ufes:
    - id: notes-ufe
      route: notes
      upstream: http://127.0.0.1:4201
`

// TestParseShell_LayoutDefault: an omitted spec.api.layout resolves to
// product-rest during Validate.
func TestParseShell_LayoutDefault(t *testing.T) {
	s, err := ParseShell([]byte(validShellMethod1))
	if err != nil {
		t.Fatalf("ParseShell: %v", err)
	}
	if s.Spec.API.Layout != "product-rest" {
		t.Errorf("default api.layout = %q, want product-rest", s.Spec.API.Layout)
	}
}

// TestParseShell_LayoutInvalid: a bad spec.api.layout is rejected by Validate.
func TestParseShell_LayoutInvalid(t *testing.T) {
	input := []byte(`apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: notesapp
spec:
  host: app.dev.test
  shellUpstream: http://127.0.0.1:4200
  cdn:
    host: cdn.dev.test
  api:
    method: 1
    layout: bogus-layout
    services:
      - domain: notes
        upstream: http://127.0.0.1:8080
  ufes:
    - id: notes-ufe
      route: notes
      upstream: http://127.0.0.1:4201
`)
	if _, err := ParseShell(input); err == nil {
		t.Fatal("expected error for invalid spec.api.layout")
	}
}

// TestShell_ToRoutes_PerDomain_ProductREST: method-1 per-domain routing emits one
// strip route per domain at /api/{domain} on the shell host — the product-rest
// public shape composed at the edge.
func TestShell_ToRoutes_PerDomain_ProductREST(t *testing.T) {
	s, err := ParseShell([]byte(validShellPerDomain))
	if err != nil {
		t.Fatalf("ParseShell: %v", err)
	}
	routes, err := s.ToRoutes()
	if err != nil {
		t.Fatalf("ToRoutes: %v", err)
	}
	// shell catch-all + 2 per-domain /api/{domain} strip routes + 1 cdn route.
	want := []types.Route{
		{Host: "app.dev.test", Path: "", StripPrefix: false, Upstream: "http://127.0.0.1:4200", Project: "notesapp", Source: "project-file"},
		{Host: "app.dev.test", Path: "/api/notes", StripPrefix: true, Upstream: "http://127.0.0.1:8080", Project: "notesapp", Source: "project-file"},
		{Host: "app.dev.test", Path: "/api/tags", StripPrefix: true, Upstream: "http://127.0.0.1:8081", Project: "notesapp", Source: "project-file"},
		{Host: "cdn.dev.test", Path: "/notes", StripPrefix: true, Upstream: "http://127.0.0.1:4201", Project: "notesapp", Source: "project-file"},
	}
	assertRoutes(t, routes, want)
}

// TestShell_ToRoutes_PerDomain_K8sAPIs: k8s-apis layout routes each domain
// (group) under the /apis prefix.
func TestShell_ToRoutes_PerDomain_K8sAPIs(t *testing.T) {
	input := []byte(`apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: platform
spec:
  host: app.dev.test
  shellUpstream: http://127.0.0.1:4200
  cdn:
    host: cdn.dev.test
  api:
    method: 1
    layout: k8s-apis
    services:
      - domain: ipam.infoblox.com
        upstream: http://127.0.0.1:8080
  ufes:
    - id: ipam-ufe
      route: ipam
      upstream: http://127.0.0.1:4201
`)
	s, err := ParseShell(input)
	if err != nil {
		t.Fatalf("ParseShell: %v", err)
	}
	routes, err := s.ToRoutes()
	if err != nil {
		t.Fatalf("ToRoutes: %v", err)
	}
	// The API route is under /apis (k8s-apis prefix), not /api.
	var apiRoute *types.Route
	for i := range routes {
		if routes[i].Host == "app.dev.test" && routes[i].StripPrefix {
			apiRoute = &routes[i]
			break
		}
	}
	if apiRoute == nil {
		t.Fatalf("no /apis strip route emitted: %+v", routes)
	}
	if apiRoute.Path != "/apis/ipam.infoblox.com" {
		t.Errorf("k8s-apis route path = %q, want /apis/ipam.infoblox.com", apiRoute.Path)
	}
}

// TestShell_ToRoutes_Method1_BackwardCompat: the shipped single prefix+upstream
// (no domain) method-1 shell still parses and routes at spec.api.prefix.
func TestShell_ToRoutes_Method1_BackwardCompat(t *testing.T) {
	s, err := ParseShell([]byte(validShellMethod1))
	if err != nil {
		t.Fatalf("ParseShell: %v", err)
	}
	routes, err := s.ToRoutes()
	if err != nil {
		t.Fatalf("ToRoutes: %v", err)
	}
	// Same as before WS-019: shell catch-all + /api strip route + 2 cdn routes.
	want := []types.Route{
		{Host: "notesapp.dev.test", Path: "", StripPrefix: false, Upstream: "http://127.0.0.1:4200", Project: "notesapp", Source: "project-file"},
		{Host: "notesapp.dev.test", Path: "/api", StripPrefix: true, Upstream: "http://127.0.0.1:8080", Project: "notesapp", Source: "project-file"},
		{Host: "cdn.dev.test", Path: "/notes", StripPrefix: true, Upstream: "http://127.0.0.1:4201", Project: "notesapp", Source: "project-file"},
		{Host: "cdn.dev.test", Path: "/tags", StripPrefix: true, Upstream: "http://127.0.0.1:4202", Project: "notesapp", Source: "project-file"},
	}
	assertRoutes(t, routes, want)
}

// TestShell_Method1_PerDomainMissingDomain: a method-1 per-domain backend
// missing its 'domain' is rejected.
func TestShell_Method1_PerDomainMissingDomain(t *testing.T) {
	input := []byte(`apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: notesapp
spec:
  host: app.dev.test
  shellUpstream: http://127.0.0.1:4200
  cdn:
    host: cdn.dev.test
  api:
    method: 1
    services:
      - upstream: http://127.0.0.1:8080
  ufes:
    - id: notes-ufe
      route: notes
      upstream: http://127.0.0.1:4201
`)
	if _, err := ParseShell(input); err == nil {
		t.Fatal("expected error for method-1 per-domain backend missing 'domain'")
	}
}

func TestMarshalShell_RoundTrip(t *testing.T) {
	s, err := ParseShell([]byte(validShellMethod1))
	if err != nil {
		t.Fatalf("ParseShell: %v", err)
	}
	data, err := MarshalShell(s)
	if err != nil {
		t.Fatalf("MarshalShell: %v", err)
	}
	s2, err := ParseShell(data)
	if err != nil {
		t.Fatalf("ParseShell round-trip: %v", err)
	}
	if s2.Project() != s.Project() || s2.Spec.Host != s.Spec.Host || len(s2.Spec.UFEs) != len(s.Spec.UFEs) {
		t.Errorf("round-trip mismatch: %+v vs %+v", s2.Spec, s.Spec)
	}
}

// TestParseShell_NoTile_BackwardCompat proves a kind:Shell document with no tile
// metadata still parses (the field is absent/nil) and behaves identically — the
// tile addition is backward-compatible.
func TestParseShell_NoTile_BackwardCompat(t *testing.T) {
	s, err := ParseShell([]byte(validShellMethod1))
	if err != nil {
		t.Fatalf("ParseShell: %v", err)
	}
	if s.Spec.Tile != nil {
		t.Errorf("Spec.Tile = %+v, want nil for a shell that declares none", s.Spec.Tile)
	}
	routes, err := s.ToRoutes()
	if err != nil {
		t.Fatalf("ToRoutes: %v", err)
	}
	// The shell catch-all is the first route; with no shell tile it stays nil.
	if routes[0].Tile != nil {
		t.Errorf("catch-all route Tile = %+v, want nil", routes[0].Tile)
	}
}

// TestParseShell_Tile_RoundTrip proves a tile-annotated shell parses, exposes the
// tile, attaches it to the catch-all route in ToRoutes, and survives a marshal
// round-trip.
func TestParseShell_Tile_RoundTrip(t *testing.T) {
	input := `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Shell
metadata:
  name: notesapp
spec:
  host: notesapp.dev.test
  shellUpstream: http://127.0.0.1:4200
  cdn:
    host: cdn.dev.test
  api:
    method: 1
    upstream: http://127.0.0.1:8080
  tile:
    displayName: Notes
    description: Take notes
    iconURL: https://cdn.dev.test/icons/notes.svg
    launchURL: https://notesapp.dev.test/home
  ufes:
    - id: notes-ufe
      route: notes
      upstream: http://127.0.0.1:4201
`
	s, err := ParseShell([]byte(input))
	if err != nil {
		t.Fatalf("ParseShell: %v", err)
	}
	if s.Spec.Tile == nil || s.Spec.Tile.DisplayName != "Notes" {
		t.Fatalf("Spec.Tile = %+v, want DisplayName=Notes", s.Spec.Tile)
	}
	if s.Spec.Tile.LaunchURL != "https://notesapp.dev.test/home" {
		t.Errorf("Tile.LaunchURL = %q", s.Spec.Tile.LaunchURL)
	}

	routes, err := s.ToRoutes()
	if err != nil {
		t.Fatalf("ToRoutes: %v", err)
	}
	// The catch-all (path-less, shell host) route carries the tile.
	if routes[0].Path != "" || routes[0].Host != "notesapp.dev.test" {
		t.Fatalf("routes[0] = %+v, want the shell catch-all", routes[0])
	}
	if routes[0].Tile == nil || routes[0].Tile.DisplayName != "Notes" {
		t.Fatalf("catch-all route Tile = %+v, want the shell tile", routes[0].Tile)
	}

	data, err := MarshalShell(s)
	if err != nil {
		t.Fatalf("MarshalShell: %v", err)
	}
	s2, err := ParseShell(data)
	if err != nil {
		t.Fatalf("ParseShell round-trip: %v", err)
	}
	if s2.Spec.Tile == nil || *s2.Spec.Tile != *s.Spec.Tile {
		t.Errorf("round-trip tile mismatch: %+v vs %+v", s2.Spec.Tile, s.Spec.Tile)
	}
}
