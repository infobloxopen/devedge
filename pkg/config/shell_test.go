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
