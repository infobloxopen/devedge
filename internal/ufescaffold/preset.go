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
// Canonical schema (documented in presets/README.md so external presets — e.g.
// the private Infoblox-CTO/devedge-ufe-sdk-internal repo — can conform):
//
//	{ "name": "string", "description": "string",
//	  "files": [ { "path": "target/path/in/project", "template": "source/file/in/preset/dir" } ] }
//
// For each entry the source file at <preset-dir>/<template> is read, rendered
// through the SAME template data as the base scaffold (AppID/Name/etc.), and
// written to <project>/<path>, overriding any base file already at that path.
//
// The public devedge CLI ships NO proprietary preset. The `infoblox-cto`
// preset (FeatureFlag-CRD chart, Infoblox design-system wiring, Okta OIDC
// config) is provided by the private Infoblox-CTO/devedge-ufe-sdk-internal
// repo and is applied with --preset-dir. This keeps zero Infoblox content in
// the public repo while leaving a clean, coherent seam.
type PresetManifest struct {
	// Name is the preset identifier.
	Name string `json:"name"`
	// Description is a one-line human summary.
	Description string `json:"description"`
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
// non-empty. Proprietary presets ship OUT-of-tree and are applied with
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
// --preset-dir for the proprietary infoblox-cto preset.
func checkPresetExists(name string) error {
	for _, p := range availablePresets() {
		if p == name {
			return nil
		}
	}
	return fmt.Errorf(
		"unknown built-in preset %q; the infoblox-cto preset is provided by the "+
			"private devedge-ufe-sdk-internal repo — apply it with "+
			"--preset-dir <path-to-that-repo>/preset/infoblox-cto",
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
// must already have been validated to exist (checkPresetExists).
func applyPreset(root, name string, data templateData) error {
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

// applyPresetDir loads <presetDir>/preset.json and applies its overlay onto the
// generated project at root, reading source templates from the real filesystem
// under presetDir. A missing/malformed preset.json fails loud.
func applyPresetDir(root, presetDir string, data templateData) error {
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
func overlayPreset(root string, m PresetManifest, srcDesc string, src func(rel string) ([]byte, error), data templateData) error {
	var out []outFile
	for _, f := range m.Files {
		raw, err := src(f.Template)
		if err != nil {
			return fmt.Errorf("reading preset file %s (from %s): %w", f.Template, srcDesc, err)
		}
		dst := substitutePath(f.Path, data)
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
