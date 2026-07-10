package ufescaffold

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PresetManifest describes a preset overlay. It is the preset.json at the root
// of a preset directory. A preset adds or overrides files on top of the base
// scaffold; it never removes base files.
//
// Canonical schema (documented in presets/README.md so a downstream, out-of-tree
// preset can conform):
//
//	{ "name": "string", "description": "string",
//	  "files": [ { "path": "target/path/in/project", "template": "source/file/in/preset/dir" } ] }
//
// For each entry the source file at <preset-dir>/<template> is read, rendered
// through the SAME template data as the base scaffold (AppID/Name/etc.), and
// written to <project>/<path>, overriding any base file already at that path.
//
// The public devedge CLI ships NO built-in preset. A preset is a downstream
// extension point (a deploy chart, a design-system binding, an OIDC/session
// config, and so on) that a consumer supplies out-of-tree and applies with
// --preset-dir. This keeps the public repo a clean, coherent seam with no
// bundled preset content.
type PresetManifest struct {
	// Name is the preset identifier.
	Name string `json:"name"`
	// Description is a one-line human summary.
	Description string `json:"description"`
	// ShellHost, when non-empty, overrides the create-default shell host (the
	// public default is app.dev.test). It is DATA, not core code: the public
	// open core never hardcodes a specific host beyond app.dev.test; a
	// downstream preset may set this to its own host. It only affects a
	// shell created from scratch by `de ufe new` — an existing shell.yaml's
	// host is never rewritten.
	ShellHost string `json:"shellHost,omitempty"`
	// Files lists the overlay entries: each maps a source template within the
	// preset directory to a target path within the generated project.
	Files []PresetFile `json:"files"`
}

// PresetFile is one overlay entry: render Template (relative to the preset
// directory) and write it to Path (relative to the generated project root),
// overriding any base file at Path.
type PresetFile struct {
	// Path is the target path within the generated project.
	Path string `json:"path"`
	// Template is the source file within the preset directory. It is rendered
	// as a Go text/template against the base template data.
	Template string `json:"template"`
}

// presetsRoot is the embedded directory holding built-in presets. The public
// CLI intentionally ships none (only a README explaining the contract), so the
// only built-in presets available here are those an operator places under it.
// The directory always contains at least the contract README so the embed is
// non-empty. Downstream presets ship OUT-of-tree and are applied with
// --preset-dir.
const presetsRoot = "presets"

// availablePresets lists the built-in preset names (directories under
// presets/ that carry a preset.json).
func availablePresets() []string {
	var names []string
	entries, err := fs.ReadDir(presets, presetsRoot)
	if err != nil {
		return names
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := fs.Stat(presets, filepath.ToSlash(filepath.Join(presetsRoot, e.Name(), "preset.json"))); err == nil {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// checkPresetExists returns a clear error if the named built-in preset is not
// available. The public CLI ships none, so this points the caller at
// --preset-dir to overlay their own preset.
func checkPresetExists(name string) error {
	for _, p := range availablePresets() {
		if p == name {
			return nil
		}
	}
	return fmt.Errorf(
		"unknown built-in preset %q; the public CLI ships no built-in preset — "+
			"overlay your own with --preset-dir <path>",
		name,
	)
}

// validateManifest checks a decoded manifest against the canonical schema and
// returns a clear error naming the offending field when it is malformed.
func validateManifest(m PresetManifest, src string) error {
	if m.Name == "" {
		return fmt.Errorf("preset manifest %s: missing \"name\"", src)
	}
	if len(m.Files) == 0 {
		return fmt.Errorf("preset manifest %s: \"files\" is empty (a preset must overlay at least one file)", src)
	}
	for i, f := range m.Files {
		if f.Path == "" {
			return fmt.Errorf("preset manifest %s: files[%d] missing \"path\"", src, i)
		}
		if f.Template == "" {
			return fmt.Errorf("preset manifest %s: files[%d] missing \"template\"", src, i)
		}
		if filepath.IsAbs(f.Path) || strings.Contains(f.Path, "..") {
			return fmt.Errorf("preset manifest %s: files[%d] \"path\" %q must be relative and stay within the project", src, i, f.Path)
		}
		if filepath.IsAbs(f.Template) || strings.Contains(f.Template, "..") {
			return fmt.Errorf("preset manifest %s: files[%d] \"template\" %q must be relative and stay within the preset directory", src, i, f.Template)
		}
	}
	return nil
}

// loadPresetManifest reads and validates a built-in preset's preset.json.
func loadPresetManifest(name string) (PresetManifest, error) {
	var m PresetManifest
	path := filepath.ToSlash(filepath.Join(presetsRoot, name, "preset.json"))
	raw, err := fs.ReadFile(presets, path)
	if err != nil {
		return m, fmt.Errorf("reading preset manifest %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, fmt.Errorf("parsing preset manifest %s: %w", path, err)
	}
	if err := validateManifest(m, path); err != nil {
		return m, err
	}
	return m, nil
}

// applyPreset renders the named built-in preset's overlay against data and
// writes it into root, overriding any base file at the same path. The preset
// must already have been validated to exist (checkPresetExists). data is the
// template context the overlay is rendered against — a uFE templateData or a
// shellData — the same value the base tree was rendered with (see overlayPreset).
func applyPreset(root, name string, data any) error {
	m, err := loadPresetManifest(name)
	if err != nil {
		return err
	}
	base := filepath.ToSlash(filepath.Join(presetsRoot, name))
	src := func(rel string) ([]byte, error) {
		return fs.ReadFile(presets, filepath.ToSlash(filepath.Join(base, rel)))
	}
	return overlayPreset(root, m, base, src, data)
}

// PresetShellHost returns the shellHost declared by the preset directory's
// preset.json, or "" if presetDir is empty, the manifest is unreadable/invalid,
// or it declares no shellHost. It is a best-effort read used only to choose the
// create-default shell host; a genuinely malformed manifest is surfaced later
// when the overlay is applied (Render → applyPresetDir), so this deliberately
// swallows read/parse errors rather than failing the host lookup.
func PresetShellHost(presetDir string) string {
	if presetDir == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(presetDir, "preset.json"))
	if err != nil {
		return ""
	}
	var m PresetManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	return m.ShellHost
}

// applyPresetDir loads <presetDir>/preset.json and applies its overlay onto the
// generated project at root, reading source templates from the real filesystem
// under presetDir. A missing/malformed preset.json fails loud. data is the
// template context the overlay is rendered against — a uFE templateData (from
// Render) or a shellData (from RenderShell) — see overlayPreset.
func applyPresetDir(root, presetDir string, data any) error {
	manifestPath := filepath.Join(presetDir, "preset.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("reading preset manifest %s: %w", manifestPath, err)
	}
	var m PresetManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("parsing preset manifest %s: %w", manifestPath, err)
	}
	if err := validateManifest(m, manifestPath); err != nil {
		return err
	}
	src := func(rel string) ([]byte, error) {
		return os.ReadFile(filepath.Join(presetDir, filepath.FromSlash(rel)))
	}
	return overlayPreset(root, m, presetDir, src, data)
}

// overlayPreset renders each manifest entry (read via src, keyed by the
// manifest's Template path) against data and writes it to the entry's Path
// under root, overriding any base file. srcDesc names the preset source in
// errors. Rendering happens fully in memory before any write.
//
// data is the same template context the base tree was rendered against (a uFE
// templateData or a shellData). Path-placeholder substitution (__name__) applies
// only to the uFE tree, so it is guarded to templateData — mirroring renderTree;
// a shell overlay's paths are used verbatim.
func overlayPreset(root string, m PresetManifest, srcDesc string, src func(rel string) ([]byte, error), data any) error {
	var out []outFile
	for _, f := range m.Files {
		raw, err := src(f.Template)
		if err != nil {
			return fmt.Errorf("reading preset file %s (from %s): %w", f.Template, srcDesc, err)
		}
		dst := f.Path
		if td, ok := data.(templateData); ok {
			dst = substitutePath(f.Path, td)
		}
		t, err := parsePresetTemplate(dst, string(raw))
		if err != nil {
			return err
		}
		var b strings.Builder
		if err := t.Execute(&b, data); err != nil {
			return fmt.Errorf("rendering preset file %s (from %s): %w", f.Template, srcDesc, err)
		}
		out = append(out, outFile{rel: dst, body: []byte(b.String()), mode: 0o644})
	}

	// The base is already on disk; overlay writes override in place. No cleanup
	// of the base on a preset error (the base is valid on its own).
	dummyWrote := true
	return writeFiles(root, out, &dummyWrote, func() {})
}
