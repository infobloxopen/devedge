// Package cliscaffold generates a new rebrandable devedge CLI-shell project from
// templates embedded in the de binary. It is the CLI mirror of
// internal/ufescaffold: where that renders an Angular + single-spa micro-frontend
// shell, this renders a small Go CLI shell wired to the open-core
// github.com/infobloxopen/devedge-cli-sdk `clikit` runtime, correct on first run.
//
// The render machinery is deliberately the same shape as internal/ufescaffold
// and internal/scaffold (embed.FS walk → path substitution → in-memory render →
// atomic write) so the scaffolds stay consistent. The generated shell owns
// session construction (a generic OIDC device-grant provider, or a --dev stub)
// and composes generated "domain command modules" — which `de cli add` produces
// with cligen and wires in via the regenerated domains_gen.go.
package cliscaffold

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

// presets holds the built-in preset overlays. The public devedge CLI ships NONE
// (only a contract README); it exists so the --preset-dir overlay seam has a
// documented home and mirrors internal/ufescaffold. Proprietary presets (e.g. a
// private Infoblox-CTO CLI preset) ship out-of-tree and are applied with
// --preset-dir.
//
//go:embed all:presets
var presets embed.FS

// Versions pins the generated CLI's dependency versions. SDK is the open-core
// github.com/infobloxopen/devedge-cli-sdk module the generated shell (and its
// generated domain modules) build against.
type Versions struct {
	// SDK is the version referenced for github.com/infobloxopen/devedge-cli-sdk
	// in the generated go.mod.
	SDK string
}

// DefaultVersions are the pinned versions baked into generated CLI projects.
// Bump deliberately and re-run the scaffold e2e when changing them.
var DefaultVersions = Versions{
	SDK: "v0.1.0",
}

// DefaultGoVersion is the go directive written into the generated go.mod. It
// matches the devedge-cli-sdk module's own go directive.
const DefaultGoVersion = "1.23"

// Params configures one CLI-shell scaffold render.
type Params struct {
	// Name is the CLI name: used as the project dir name, the binary name, the
	// default module path, and (unless AppName overrides it) the rebranded app
	// name. Must be a valid lowercase DNS label.
	Name string
	// ParentDir is the directory the project directory is created in. Empty
	// defaults to ".". The project root is ParentDir/Name.
	ParentDir string
	// Module is the Go module path for the generated project. Empty defaults to
	// Name (a bare-name module, valid for local development; pass a real path
	// such as github.com/<owner>/<name> for a published CLI).
	Module string
	// AppName is the rebranded application name baked into the shell (the clikit
	// App name, config dir, and env-var prefix). Empty defaults to Name.
	AppName string
	// PresetDir, when non-empty, is a filesystem path to a preset directory
	// containing a canonical preset.json (see PresetManifest). Its overlay is
	// applied on top of the base scaffold. This is how proprietary presets are
	// applied without any proprietary content living in the public repo.
	PresetDir string
}

// templateData is what the .tmpl files and path placeholders see.
type templateData struct {
	// Name is the raw CLI name (a DNS label); also the binary name.
	Name string
	// Module is the Go module path of the generated project.
	Module string
	// AppName is the rebranded application name baked into the shell.
	AppName string
	// GoVersion is the go directive for the generated go.mod.
	GoVersion string
	// TitleName is Name with a leading uppercase letter, for prose/labels.
	TitleName string
	// Versions pins the generated dependency versions.
	Versions Versions
}

// dnsLabel is the accepted CLI-name shape: starts with a lowercase letter,
// lowercase letters/digits/hyphens after, no trailing hyphen.
var dnsLabel = regexp.MustCompile(`^[a-z]([a-z0-9-]*[a-z0-9])?$`)

// ValidateName reports why name cannot be used (DNS-label + binary-name
// constraints), or nil.
func ValidateName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("CLI name is required")
	case len(name) > 63:
		return fmt.Errorf("CLI name %q is too long (%d chars; hostname labels allow at most 63)", name, len(name))
	case !dnsLabel.MatchString(name):
		return fmt.Errorf("CLI name %q must be a lowercase DNS label: lowercase letters, digits and hyphens, starting with a letter, not ending with a hyphen (it becomes the binary name and the default module path)", name)
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

// Render validates p, refuses to write into an existing non-empty project root,
// writes the full rendered base template tree to ParentDir/Name, then — if a
// preset was requested — applies the preset overlay. On any error after writing
// began, it removes what it created.
func Render(p Params) error {
	if err := ValidateName(p.Name); err != nil {
		return err
	}
	if p.ParentDir == "" {
		p.ParentDir = "."
	}
	if p.Module == "" {
		p.Module = p.Name
	}
	if p.AppName == "" {
		p.AppName = p.Name
	}

	root := filepath.Join(p.ParentDir, p.Name)
	preexisting, err := checkTarget(root)
	if err != nil {
		return err
	}

	data := templateData{
		Name:      p.Name,
		Module:    p.Module,
		AppName:   p.AppName,
		GoVersion: DefaultGoVersion,
		TitleName: titleCase(p.Name),
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
	if p.PresetDir != "" {
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
// .tmpl files against data, and returns the files in memory. Rendering fully in
// memory first means a template error never leaves a partial project.
func renderTree(fsys fs.FS, base string, data templateData) ([]outFile, error) {
	var out []outFile
	err := fs.WalkDir(fsys, base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(path, base+"/")
		rel = substitutePath(rel, data)

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

// checkTarget reports whether root already exists (it may, if empty) and errors
// when it exists non-empty — the scaffold never overwrites.
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

// substitutePath rewrites placeholder path segments: __name__ → the CLI name.
func substitutePath(rel string, d templateData) string {
	return strings.ReplaceAll(rel, "__name__", d.Name)
}
