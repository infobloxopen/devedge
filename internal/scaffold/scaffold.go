// Package scaffold generates a new devedge service project from templates
// embedded in the de binary (feature 007).
package scaffold

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/infobloxopen/devedge-sdk/apilayout"
)

//go:embed all:templates
var templates embed.FS

// DefaultHost is the dev edge host baked into a generated project's
// devedge.yaml (and the README/AGENTS curl examples) when no Host is passed.
// It is a single generic dev host, NOT derived from the service name, so the
// public open core never hardcodes a product-specific host. The override knob
// is Params.Host (the `de project init --host` flag), which the Infoblox-CTO
// Go tooling sets to csp.dev.test.
const DefaultHost = "app.dev.test"

// Params configures one scaffold render.
type Params struct {
	// Name is the service name: used as the project dir name, the Helm
	// release slug, and the default Go module path. Must be a valid
	// lowercase DNS label (lowercase letters, digits, hyphens; starts with a
	// letter; no trailing hyphen; max 63 chars).
	Name string
	// Module is the Go module path for the generated project. Empty
	// defaults to Name.
	Module string
	// ParentDir is the directory the project directory is created in.
	// Empty defaults to ".". The project root is ParentDir/Name.
	ParentDir string
	// GoVersion is the go directive for the generated go.mod (e.g. "1.25").
	GoVersion string
	// Host is the dev edge host written into devedge.yaml (hostname + route
	// host) and the README/AGENTS curl examples. Empty defaults to
	// DefaultHost (app.dev.test).
	Host string
	// Layout names the URL layout strategy the emitted route composes at the
	// edge (WS-019). Empty defaults to apilayout.Default (product-rest);
	// validated via apilayout.Parse in Render.
	Layout string
	// Domain is the short product domain the service is routed under at the app
	// host: the edge route is <layout-prefix>/{domain} with strip-prefix, so the
	// public URL is product-rest and two services on the same host don't collide.
	// Empty defaults to Name.
	Domain string
}

// Versions pins the generated project's dependencies. The defaults are the
// released artifacts the scaffold is tested against (FR-011 — no local
// replaces, no internal-only repos).
type Versions struct {
	SDK         string
	AuthzModule string
	Gateway     string
	PGX         string
	GRPC        string
	Protobuf    string
}

// DefaultVersions are the pinned released versions baked into generated
// projects. Bump deliberately and re-run the scaffold smoke + walk-through
// e2e when changing any of them.
var DefaultVersions = Versions{
	SDK:         "v0.1.0",
	AuthzModule: "v1.0.0-alpha.2",
	Gateway:     "v2.27.4",
	PGX:         "v5.7.6",
	GRPC:        "v1.81.1",
	Protobuf:    "v1.36.11",
}

// templateData is what the .tmpl files and path placeholders see.
type templateData struct {
	Name      string
	Module    string
	GoVersion string
	// Host is the dev edge host used in devedge.yaml + the curl examples.
	Host string
	// ProtoPkg is Name with hyphens removed — a valid proto package /
	// Go package-name fragment (DNS labels allow '-', proto packages don't).
	ProtoPkg string
	// Layout is the resolved URL layout name (e.g. "product-rest").
	Layout string
	// Domain is the product domain the service is routed under at the edge.
	Domain string
	// APIPrefix is the layout's top-level path segment ("/api" for product-rest).
	APIPrefix string
	// APIPath is the edge route path the service is fronted at:
	// APIPrefix + "/" + Domain (e.g. "/api/webhooks"). The route strips this
	// prefix so the service still sees its own /v1/... paths.
	APIPath  string
	Versions Versions
}

// dnsLabel is the accepted service-name shape: starts with a lowercase
// letter, lowercase letters/digits/hyphens after, no trailing hyphen.
var dnsLabel = regexp.MustCompile(`^[a-z]([a-z0-9-]*[a-z0-9])?$`)

// ValidateName reports why name cannot be used (DNS-label + Go module +
// Helm release constraints), or nil.
func ValidateName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("service name is required")
	case len(name) > 63:
		return fmt.Errorf("service name %q is too long (%d chars; hostname labels allow at most 63)", name, len(name))
	case !dnsLabel.MatchString(name):
		return fmt.Errorf("service name %q must be a lowercase DNS label: lowercase letters, digits and hyphens, starting with a letter, not ending with a hyphen (it becomes the dev hostname, the Helm release name, and the default Go module path)", name)
	}
	return nil
}

// Render validates p, refuses to write into an existing non-empty project
// root, then writes the full rendered template tree to ParentDir/Name.
// On any error after writing began, it removes what it created.
func Render(p Params) error {
	if err := ValidateName(p.Name); err != nil {
		return err
	}
	if p.Module == "" {
		p.Module = p.Name
	}
	if p.ParentDir == "" {
		p.ParentDir = "."
	}
	if p.GoVersion == "" {
		p.GoVersion = "1.25"
	}
	if p.Host == "" {
		p.Host = DefaultHost
	}
	layout, err := apilayout.Parse(p.Layout)
	if err != nil {
		return fmt.Errorf("invalid --api-layout: %w", err)
	}
	if p.Domain == "" {
		p.Domain = p.Name
	}

	root := filepath.Join(p.ParentDir, p.Name)
	preexisting, err := checkTarget(root)
	if err != nil {
		return err
	}

	apiPrefix := layout.Prefix()
	data := templateData{
		Name:      p.Name,
		Module:    p.Module,
		GoVersion: p.GoVersion,
		Host:      p.Host,
		ProtoPkg:  strings.ReplaceAll(p.Name, "-", ""),
		Layout:    string(layout),
		Domain:    p.Domain,
		APIPrefix: apiPrefix,
		APIPath:   apiPrefix + "/" + p.Domain,
		Versions:  DefaultVersions,
	}

	// Render the whole tree in memory first, then write: a template error
	// must not leave a partial project behind.
	type outFile struct {
		rel  string
		body []byte
		mode fs.FileMode
	}
	var out []outFile

	err = fs.WalkDir(templates, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(path, "templates/")
		rel = substitutePath(rel, data)

		raw, err := templates.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading embedded %s: %w", path, err)
		}

		body := raw
		if strings.HasSuffix(rel, ".tmpl") {
			rel = strings.TrimSuffix(rel, ".tmpl")
			t, err := template.New(rel).Option("missingkey=error").Parse(string(raw))
			if err != nil {
				return fmt.Errorf("parsing template %s: %w", path, err)
			}
			var b strings.Builder
			if err := t.Execute(&b, data); err != nil {
				return fmt.Errorf("rendering template %s: %w", path, err)
			}
			body = []byte(b.String())
		}

		mode := fs.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		out = append(out, outFile{rel: rel, body: body, mode: mode})
		return nil
	})
	if err != nil {
		return err
	}

	wrote := false
	cleanup := func() {
		if wrote && !preexisting {
			_ = os.RemoveAll(root)
		}
	}
	for _, f := range out {
		dest := filepath.Join(root, filepath.FromSlash(f.rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			cleanup()
			return fmt.Errorf("creating %s: %w", filepath.Dir(dest), err)
		}
		wrote = true
		if err := os.WriteFile(dest, f.body, f.mode); err != nil {
			cleanup()
			return fmt.Errorf("writing %s: %w", dest, err)
		}
	}
	return nil
}

// checkTarget reports whether root already exists (it may, if empty) and
// errors when it exists non-empty — the scaffold never overwrites (FR-008).
func checkTarget(root string) (preexisting bool, err error) {
	info, statErr := os.Stat(root)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return false, nil
		}
		return false, fmt.Errorf("checking target %s: %w", root, statErr)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("target %s already exists and is not a directory", root)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		return true, fmt.Errorf("reading target %s: %w", root, readErr)
	}
	if len(entries) > 0 {
		return true, fmt.Errorf("target %s already exists and is not empty — refusing to overwrite (move it aside or pick another name)", root)
	}
	return true, nil
}

// substitutePath rewrites placeholder path segments: __name__ → the service
// name, __protopkg__ → the proto-package form of the name.
func substitutePath(rel string, d templateData) string {
	rel = strings.ReplaceAll(rel, "__name__", d.Name)
	rel = strings.ReplaceAll(rel, "__protopkg__", d.ProtoPkg)
	return rel
}
