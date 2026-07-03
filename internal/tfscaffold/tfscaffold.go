// Package tfscaffold generates a new registry-publishable
// terraform-provider-<name> Go repo from templates embedded in the de binary. It
// is the Terraform mirror of internal/cliscaffold: where that renders a
// rebrandable Go CLI shell wired to devedge-cli-sdk clikit, this renders a
// terraform-plugin-framework provider wired to the open-core
// github.com/infobloxopen/devedge-terraform-sdk `tfkit` runtime, correct on
// first run.
//
// The render machinery is deliberately the same shape as internal/cliscaffold
// and internal/scaffold (embed.FS walk → path substitution → in-memory render →
// atomic write) so the scaffolds stay consistent. The generated provider owns a
// tiny hand-written seam (internal/provider/provider.go composing tfkit with the
// generated resource registration) and is extended with `de terraform add`,
// which runs the devedge-terraform-sdk tfgen generator to emit resource modules
// and (re)write internal/provider/resources_gen.go.
//
// Unlike the CLI scaffold, the emitted repo is shaped for the Terraform Registry:
// a HashiCorp-style GoReleaser config (zip archives + GPG-signed SHA256SUMS + the
// registry manifest), a terraform-registry-manifest.json, and a tag-triggered
// release workflow.
package tfscaffold

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

// presets holds the built-in preset overlays. The public devedge repo ships NONE
// (only a contract README); it exists so the --preset-dir overlay seam has a
// documented home and mirrors internal/cliscaffold. Proprietary presets (e.g. a
// private Infoblox-CTO auth binding) ship out-of-tree and are applied with
// --preset-dir.
//
//go:embed all:presets
var presets embed.FS

// Versions pins the generated provider's dependency versions. They match the
// devedge-terraform-sdk module's own go.mod so the generated provider builds
// against the same terraform-plugin-framework the tfkit runtime and tfgen output
// were built against.
type Versions struct {
	// SDK is the version referenced for github.com/infobloxopen/devedge-terraform-sdk.
	SDK string
	// Framework is the version referenced for github.com/hashicorp/terraform-plugin-framework.
	Framework string
	// Validators is the version referenced for
	// github.com/hashicorp/terraform-plugin-framework-validators.
	Validators string
}

// DefaultVersions are the pinned versions baked into generated provider
// projects. They mirror devedge-terraform-sdk's go.mod; bump deliberately and
// re-run the scaffold e2e when changing them.
var DefaultVersions = Versions{
	SDK:        "v0.1.0",
	Framework:  "v1.18.0",
	Validators: "v0.19.0",
}

// DefaultGoVersion is the go directive written into the generated go.mod. It
// matches the devedge-terraform-sdk module's own go directive (the tightest
// constraint on the generated provider).
const DefaultGoVersion = "1.25"

// DefaultOrg is the registry namespace (and default module owner) used when
// --org is not supplied. The generated provider's registry address is
// registry.terraform.io/<org>/<name>.
const DefaultOrg = "infobloxopen"

// Params configures one provider scaffold render.
type Params struct {
	// Name is the provider type name: used as the Terraform provider type
	// (registry.terraform.io/<org>/<name>), the derived repo/dir name
	// (terraform-provider-<name>), and the env-var prefix. Must be a valid
	// lowercase DNS label.
	Name string
	// ParentDir is the directory the project directory is created in. Empty
	// defaults to ".". The project root is ParentDir/terraform-provider-<name>.
	ParentDir string
	// Module is the Go module path for the generated provider. Empty defaults to
	// github.com/<org>/terraform-provider-<name>.
	Module string
	// Org is the Terraform Registry namespace (and default module owner) baked
	// into the provider address and default module path. Empty defaults to
	// DefaultOrg.
	Org string
	// PresetDir, when non-empty, is a filesystem path to a preset directory
	// containing a canonical preset.json (see PresetManifest). Its overlay is
	// applied on top of the base scaffold. This is how proprietary presets are
	// applied without any proprietary content living in the public repo.
	PresetDir string
}

// templateData is what the .tmpl files and path placeholders see.
type templateData struct {
	// Name is the raw provider name (a DNS label).
	Name string
	// RepoName is the repo/dir name, "terraform-provider-<name>".
	RepoName string
	// Module is the Go module path of the generated provider.
	Module string
	// Org is the registry namespace / default module owner.
	Org string
	// EnvPrefix is the uppercased provider name used for the endpoint/token
	// environment variables (e.g. "TOY" → TOY_ENDPOINT / TOY_TOKEN).
	EnvPrefix string
	// GoVersion is the go directive for the generated go.mod.
	GoVersion string
	// TitleName is Name with a leading uppercase letter, for prose/labels.
	TitleName string
	// Versions pins the generated dependency versions.
	Versions Versions
}

// dnsLabel is the accepted provider-name shape: starts with a lowercase letter,
// lowercase letters/digits/hyphens after, no trailing hyphen.
var dnsLabel = regexp.MustCompile(`^[a-z]([a-z0-9-]*[a-z0-9])?$`)

// ValidateName reports why name cannot be used as a Terraform provider type
// (DNS-label + registry-name constraints), or nil.
func ValidateName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("provider name is required")
	case len(name) > 63:
		return fmt.Errorf("provider name %q is too long (%d chars; hostname labels allow at most 63)", name, len(name))
	case !dnsLabel.MatchString(name):
		return fmt.Errorf("provider name %q must be a lowercase DNS label: lowercase letters, digits and hyphens, starting with a letter, not ending with a hyphen (it becomes the terraform-provider-<name> repo and the registry type name)", name)
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

// envPrefix maps a provider name to its endpoint/token environment-variable
// prefix, matching tfkit's derivation (lowercase→upper, non-alnum→underscore).
func envPrefix(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 32)
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// Render validates p, refuses to write into an existing non-empty project root,
// writes the full rendered base template tree to ParentDir/terraform-provider-<name>,
// then — if a preset was requested — applies the preset overlay. On any error
// after writing began, it removes what it created.
func Render(p Params) error {
	if err := ValidateName(p.Name); err != nil {
		return err
	}
	if p.ParentDir == "" {
		p.ParentDir = "."
	}
	if p.Org == "" {
		p.Org = DefaultOrg
	}
	repoName := "terraform-provider-" + p.Name
	if p.Module == "" {
		p.Module = "github.com/" + p.Org + "/" + repoName
	}

	root := filepath.Join(p.ParentDir, repoName)
	preexisting, err := checkTarget(root)
	if err != nil {
		return err
	}

	data := templateData{
		Name:      p.Name,
		RepoName:  repoName,
		Module:    p.Module,
		Org:       p.Org,
		EnvPrefix: envPrefix(p.Name),
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

// substitutePath rewrites placeholder path segments: __name__ → the provider name.
func substitutePath(rel string, d templateData) string {
	return strings.ReplaceAll(rel, "__name__", d.Name)
}
