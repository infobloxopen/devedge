// Package ufescaffold generates a new devedge micro-frontend (uFE) project
// from templates embedded in the de binary. It is the frontend mirror of
// internal/scaffold: an Angular-15 + single-spa uFE wired to the open-core
// @infobloxopen/devedge-ufe-* SDK, correct on first run.
//
// The render machinery is deliberately the same shape as internal/scaffold
// (embed.FS walk → path substitution → in-memory render → atomic write) so the
// two scaffolds stay consistent. Where scaffold renders a Go service, this
// renders a TypeScript uFE and — by construction — eliminates a set of known
// template-bootstrap bugs (silently-broken nav group, mismatched app route,
// dead single-spa architect targets, Angular-2-era deadweight deps, committed
// stale lockfile, and cert-trust dev-loop friction).
package ufescaffold

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

//go:embed all:templates
var templates embed.FS

//go:embed all:presets
var presets embed.FS

// Versions pins the generated uFE's dependency versions. The SDK versions are
// the open-core @infobloxopen/devedge-ufe-* packages; they are not yet
// published to a registry, so the generated README documents that install
// comes from npm once published (until then, pnpm link / file: the local
// packages — see Verify docs).
type Versions struct {
	// SDK is the version range referenced for every @infobloxopen/devedge-ufe-*
	// package in the generated package.json.
	SDK string
	// Angular is the range for the @angular/* framework packages (~15.2.x).
	Angular string
	// AngularMaterial is the range for @angular/material + @angular/cdk.
	AngularMaterial string
	// TypeScript is the range for the generated project's typescript devDep.
	TypeScript string
}

// DefaultVersions are the pinned versions baked into generated uFE projects.
// Bump deliberately and re-run the generated-app typecheck when changing them.
var DefaultVersions = Versions{
	SDK:             "^0.1.0",
	Angular:         "~15.2.0",
	AngularMaterial: "~15.2.0",
	TypeScript:      "~4.9.5",
}

// DefaultDevPort is the dev-server port a generated uFE listens on when a
// caller does not set Params.DevPort. It is the single source of truth shared
// by the scaffold (angular.json serve target) and the shell-roster upstream, so
// the generated uFE is routable without extra flags. It is NOT 4200 — that is
// the shell root-config's own port — so the uFE and its shell never collide.
const DefaultDevPort = 4201

// Params configures one uFE scaffold render.
type Params struct {
	// Name is the uFE name: used as the project dir name, the npm package
	// slug (csp-<name>-ufe), the single-spa app id, the dev route path, and
	// the nav-item label. Must be a valid lowercase DNS label.
	Name string
	// ParentDir is the directory the project directory is created in. Empty
	// defaults to ".". The project root is ParentDir/Name.
	ParentDir string
	// DevPort is the dev-server port written into the generated angular.json
	// serve target. It must match the shell-roster upstream port so the uFE is
	// routable. Zero selects DefaultDevPort.
	DevPort int
	// Preset, when non-empty, names a BUILT-IN overlay applied on top of the
	// base scaffold after the base is rendered. The public CLI ships no
	// built-in preset; an unknown preset is a clear error pointing at
	// --preset-dir.
	Preset string
	// PresetDir, when non-empty, is a filesystem path to a preset directory
	// containing a canonical preset.json (see PresetManifest). Its overlay is
	// applied on top of the base scaffold. This is the downstream extension
	// point a consumer supplies out-of-tree. Preset and PresetDir are mutually
	// exclusive.
	PresetDir string
}

// templateData is what the .tmpl files and path placeholders see.
type templateData struct {
	// Name is the raw uFE name (a DNS label).
	Name string
	// Package is the npm package name: csp-<name>-ufe.
	Package string
	// AppID is the single-spa application id (== Name).
	AppID string
	// TitleName is Name with a leading uppercase letter, for prose/labels.
	TitleName string
	// DevPort is the dev-server port the generated webpack config listens on.
	DevPort int
	// Versions pins the generated dependency versions.
	Versions Versions
}

// dnsLabel is the accepted uFE-name shape: starts with a lowercase letter,
// lowercase letters/digits/hyphens after, no trailing hyphen.
var dnsLabel = regexp.MustCompile(`^[a-z]([a-z0-9-]*[a-z0-9])?$`)

// ValidateName reports why name cannot be used (DNS-label + npm slug + route
// constraints), or nil.
func ValidateName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("uFE name is required")
	case len(name) > 63:
		return fmt.Errorf("uFE name %q is too long (%d chars; hostname labels allow at most 63)", name, len(name))
	case !dnsLabel.MatchString(name):
		return fmt.Errorf("uFE name %q must be a lowercase DNS label: lowercase letters, digits and hyphens, starting with a letter, not ending with a hyphen (it becomes the npm package slug, the single-spa app id, and the dev route path)", name)
	}
	return nil
}

// titleCase uppercases the first byte of a lowercase DNS label for prose use.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Render validates p, refuses to write into an existing non-empty project
// root, writes the full rendered base template tree to ParentDir/Name, then —
// if a preset was requested — applies the preset overlay. On any error after
// writing began, it removes what it created.
func Render(p Params) error {
	if err := ValidateName(p.Name); err != nil {
		return err
	}
	if p.ParentDir == "" {
		p.ParentDir = "."
	}
	if p.DevPort == 0 {
		p.DevPort = DefaultDevPort
	}

	if p.Preset != "" && p.PresetDir != "" {
		return fmt.Errorf("--preset and --preset-dir are mutually exclusive; pass one")
	}
	// A missing/unknown preset must fail before we write anything.
	if p.Preset != "" {
		if err := checkPresetExists(p.Preset); err != nil {
			return err
		}
	}

	root := filepath.Join(p.ParentDir, p.Name)
	preexisting, err := checkTarget(root)
	if err != nil {
		return err
	}

	data := templateData{
		Name:      p.Name,
		Package:   "csp-" + p.Name + "-ufe",
		AppID:     p.Name,
		TitleName: titleCase(p.Name),
		DevPort:   p.DevPort,
		Versions:  DefaultVersions,
	}

	out, err := renderTree(templates, "templates", data)
	if err != nil {
		return err
	}

	wrote := false
	cleanup := func() {
		if wrote && !preexisting {
			_ = os.RemoveAll(root)
		}
	}
	if err := writeFiles(root, out, &wrote, cleanup); err != nil {
		return err
	}

	// Apply the preset overlay on top of the base (add/override files).
	switch {
	case p.Preset != "":
		if err := applyPreset(root, p.Preset, data); err != nil {
			cleanup()
			return err
		}
	case p.PresetDir != "":
		if err := applyPresetDir(root, p.PresetDir, data); err != nil {
			cleanup()
			return err
		}
	}
	return nil
}

// outFile is one rendered file: its path relative to the project root, its
// body, and its mode.
type outFile struct {
	rel  string
	body []byte
	mode fs.FileMode
}

// renderTree walks fsys under base, substitutes path placeholders, renders any
// .tmpl files against data, and returns the files in memory. Rendering fully
// in memory first means a template error never leaves a partial project. data
// is the template context (a uFE templateData or a shellData); path-placeholder
// substitution (__name__) applies only to the uFE tree, so it is guarded to the
// uFE data type — the shell tree has no path placeholders.
func renderTree(fsys fs.FS, base string, data any) ([]outFile, error) {
	var out []outFile
	err := fs.WalkDir(fsys, base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(path, base+"/")
		if td, ok := data.(templateData); ok {
			rel = substitutePath(rel, td)
		}

		raw, err := fs.ReadFile(fsys, path)
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
		return nil, err
	}
	return out, nil
}

// writeFiles writes the rendered files under root, creating parent dirs. It
// flips *wrote on the first write and calls cleanup on any error so a partial
// write is removed.
func writeFiles(root string, out []outFile, wrote *bool, cleanup func()) error {
	for _, f := range out {
		dest := filepath.Join(root, filepath.FromSlash(f.rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			cleanup()
			return fmt.Errorf("creating %s: %w", filepath.Dir(dest), err)
		}
		*wrote = true
		if err := os.WriteFile(dest, f.body, f.mode); err != nil {
			cleanup()
			return fmt.Errorf("writing %s: %w", dest, err)
		}
	}
	return nil
}

// checkTarget reports whether root already exists (it may, if empty) and
// errors when it exists non-empty — the scaffold never overwrites.
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

// substitutePath rewrites placeholder path segments: __name__ → the uFE name.
func substitutePath(rel string, d templateData) string {
	return strings.ReplaceAll(rel, "__name__", d.Name)
}
