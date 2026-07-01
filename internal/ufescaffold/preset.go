package ufescaffold

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// PresetManifest describes a preset overlay. It is the preset.json at the root
// of a preset directory. A preset adds or overrides files on top of the base
// scaffold; it never removes base files.
//
// The public devedge CLI ships NO proprietary preset. The `infoblox-cto`
// preset (FeatureFlag-CRD chart, Infoblox design-system wiring, Okta OIDC
// config) is provided by the private Infoblox-CTO/devedge-ufe-sdk-internal
// repo and is applied from a configured location (see PresetSource). This
// keeps zero Infoblox content in the public repo while leaving a clean seam.
type PresetManifest struct {
	// Name is the preset identifier (matches --preset).
	Name string `json:"name"`
	// Description is a one-line human summary.
	Description string `json:"description"`
	// Files lists the overlay files, relative to the preset directory, that
	// are rendered (as templates when suffixed .tmpl) and written into the
	// generated project — overriding any base file at the same path.
	Files []string `json:"files"`
}

// presetsRoot is the embedded directory holding built-in presets. The public
// CLI intentionally ships none (only a README explaining the contract), so
// the only presets available here are those an operator places under it. The
// directory always contains at least the contract README so the embed is
// non-empty.
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

// checkPresetExists returns a clear error if the named preset is not a
// built-in preset. It lists what is available (which, in the public CLI, is
// usually empty — with a pointer to where the infoblox-cto preset lives).
func checkPresetExists(name string) error {
	for _, p := range availablePresets() {
		if p == name {
			return nil
		}
	}
	avail := availablePresets()
	var have string
	if len(avail) == 0 {
		have = "the public devedge CLI ships no built-in presets"
	} else {
		have = "available presets: " + strings.Join(avail, ", ")
	}
	return fmt.Errorf(
		"unknown preset %q — %s. "+
			"The `infoblox-cto` preset (FeatureFlag-CRD chart + Infoblox design system + Okta OIDC) "+
			"is provided by the private Infoblox-CTO/devedge-ufe-sdk-internal repo; install it there to enable --preset infoblox-cto",
		name, have,
	)
}

// loadPresetManifest reads and validates a preset's preset.json.
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
	if m.Name == "" {
		return m, fmt.Errorf("preset manifest %s: missing \"name\"", path)
	}
	return m, nil
}

// applyPreset renders the named preset's overlay files against data and writes
// them into root, overriding any base file at the same path. The preset must
// already have been validated to exist (checkPresetExists).
func applyPreset(root, name string, data templateData) error {
	m, err := loadPresetManifest(name)
	if err != nil {
		return err
	}
	base := filepath.ToSlash(filepath.Join(presetsRoot, name))

	var out []outFile
	for _, rel := range m.Files {
		src := filepath.ToSlash(filepath.Join(base, rel))
		raw, err := fs.ReadFile(presets, src)
		if err != nil {
			return fmt.Errorf("reading preset file %s: %w", src, err)
		}
		dst := substitutePath(rel, data)
		body := raw
		if strings.HasSuffix(dst, ".tmpl") {
			dst = strings.TrimSuffix(dst, ".tmpl")
			t, err := parsePresetTemplate(dst, string(raw))
			if err != nil {
				return err
			}
			var b strings.Builder
			if err := t.Execute(&b, data); err != nil {
				return fmt.Errorf("rendering preset file %s: %w", src, err)
			}
			body = []byte(b.String())
		}
		out = append(out, outFile{rel: dst, body: body, mode: 0o644})
	}

	// The base is already on disk; overlay writes override in place. No
	// cleanup of the base on a preset error (the base is valid on its own).
	dummyWrote := true
	return writeFiles(root, out, &dummyWrote, func() {})
}
