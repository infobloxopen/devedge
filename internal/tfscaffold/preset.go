package tfscaffold

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// PresetManifest describes a preset overlay. It is the preset.json at the root
// of a preset directory. A preset adds or overrides files on top of the base
// scaffold; it never removes base files.
//
// Canonical schema (documented in presets/README.md so external presets — e.g.
// a private Infoblox-CTO auth binding — can conform):
//
//	{ "name": "string", "description": "string",
//	  "files": [ { "path": "target/path/in/project", "template": "source/file/in/preset/dir" } ] }
//
// For each entry the source file at <preset-dir>/<template> is read, rendered
// through the SAME template data as the base scaffold (Name/Module/Org/etc.),
// and written to <project>/<path>, overriding any base file already at that
// path.
//
// The public devedge repo ships NO proprietary preset. A product-specific preset
// (concrete auth binding, branding, extra resources) is provided by a private
// repo and is applied with --preset-dir. This keeps zero proprietary content in
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
	// Template is the source file within the preset directory. It is rendered as
	// a Go text/template against the base template data.
	Template string `json:"template"`
}

// BuiltinPresets lists the names of built-in preset overlays embedded in the
// binary (directories under presets/ carrying a preset.json). The public
// devedge repo ships NONE (only a contract README under presets/), so this
// returns nil; it exists so the --preset-dir seam has a documented, testable
// home and mirrors internal/cliscaffold.
func BuiltinPresets() []string {
	entries, err := fs.ReadDir(presets, "presets")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := fs.Stat(presets, filepath.ToSlash(filepath.Join("presets", e.Name(), "preset.json"))); err == nil {
			names = append(names, e.Name())
		}
	}
	return names
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
// manifest's Template path) against data and writes it to the entry's Path under
// root, overriding any base file. srcDesc names the preset source in errors.
// Rendering happens fully in memory before any write.
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
