package ufescaffold

import (
	"embed"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/infobloxopen/devedge/pkg/config"
)

//go:embed all:shelltemplates
var shelltemplates embed.FS

// defaultShellPort is the port a generated shell serves on when the roster's
// shellUpstream carries no parseable port. It matches the standalone-shell port
// the WS-018 example uses.
const defaultShellPort = 9000

// ShellParams configures one shell scaffold render. The shell is a single-spa
// root-config generated from a kind: Shell roster: it registers every uFE in
// the roster by hash route and loads each uFE's bundle through the browser's
// native importmap. Name is the shell project dir (and package name); Roster is
// the parsed shell topology the templates are rendered over.
type ShellParams struct {
	// ParentDir is the directory the shell directory is created in. Empty
	// defaults to ".". The shell root is ParentDir/Name.
	ParentDir string
	// Name is the shell project dir name and package name (a DNS label).
	Name string
	// Roster is the parsed kind: Shell topology the shell is rendered from.
	Roster *config.Shell
	// Preset, when non-empty, names a BUILT-IN overlay applied on top of the
	// base shell after it is rendered. The public CLI ships no built-in preset;
	// an unknown preset is a clear error pointing at --preset-dir. Preset and
	// PresetDir are mutually exclusive.
	Preset string
	// PresetDir, when non-empty, is a filesystem path to a preset directory
	// holding a canonical preset.json (see PresetManifest). Its overlay is
	// applied on top of the base shell — this is the downstream extension point
	// for rebinding things like the session provider, design system, and nav
	// shell. The overlay is rendered against the shell's own template data
	// (shellData).
	PresetDir string
}

// shellData is what the shelltemplates/*.tmpl files see.
type shellData struct {
	// Name is the shell dir / package name.
	Name string
	// Host is the shell FQDN the browser loads the root-config from.
	Host string
	// CDNHost is the FQDN uFE bundles load from (https://<CDNHost>/<route>/…).
	CDNHost string
	// ShellPort is the port the shell serves on (parsed from the roster's
	// shellUpstream so the served port matches the edge route to Host).
	ShellPort int
	// Versions pins the generated SDK dependency versions.
	Versions Versions
	// UFEs is one view per roster uFE, in declaration order.
	UFEs []ShellUFEView
}

// ShellUFEView is the per-uFE data the shell templates render: the import-map
// specifier / single-spa app id (== the uFE's webpack UMD library name), its
// hash route, and the fully-qualified CDN bundle URL.
type ShellUFEView struct {
	// ID is the import-map specifier, the single-spa app name, and the uFE's
	// webpack UMD `library` global — the same string in all three roles.
	ID string
	// Route is the hash route the uFE mounts at (#<Route>) and its CDN path.
	Route string
	// BundleURL is where the uFE's bundle loads from: https://<cdn>/<route>/main.js.
	BundleURL string
	// Label is the human-readable side-nav label (title-cased ID).
	Label string
}

// ShellServePort returns the port the generated shell serves on: the port in
// the roster's shellUpstream (so the served port matches the edge route to the
// shell host), or defaultShellPort when that upstream has no parseable port.
func ShellServePort(roster *config.Shell) int {
	if roster == nil {
		return defaultShellPort
	}
	return shellPortFrom(roster.Spec.ShellUpstream)
}

// shellPortFrom parses the port out of a shellUpstream URL (e.g.
// http://127.0.0.1:4200 → 4200), returning defaultShellPort when it is empty or
// carries no valid port.
func shellPortFrom(upstream string) int {
	if upstream == "" {
		return defaultShellPort
	}
	u, err := url.Parse(upstream)
	if err != nil {
		return defaultShellPort
	}
	p := u.Port()
	if p == "" {
		return defaultShellPort
	}
	n, err := strconv.Atoi(p)
	if err != nil || n <= 0 {
		return defaultShellPort
	}
	return n
}

// shellTemplateData builds the template context from the render params.
func shellTemplateData(p ShellParams) shellData {
	r := p.Roster
	cdnHost := r.Spec.CDN.Host
	views := make([]ShellUFEView, 0, len(r.Spec.UFEs))
	for _, u := range r.Spec.UFEs {
		views = append(views, ShellUFEView{
			ID:        u.ID,
			Route:     u.Route,
			BundleURL: "https://" + cdnHost + "/" + u.Route + "/main.js",
			Label:     titleCase(u.ID),
		})
	}
	return shellData{
		Name:      p.Name,
		Host:      r.Spec.Host,
		CDNHost:   cdnHost,
		ShellPort: shellPortFrom(r.Spec.ShellUpstream),
		Versions:  DefaultVersions,
		UFEs:      views,
	}
}

// RenderShell renders a runnable single-spa shell (root-config) from a kind:
// Shell roster into ParentDir/Name. It mirrors Render: it refuses to write into
// an existing non-empty directory, renders the embedded shelltemplates tree
// fully in memory, then writes it atomically (removing a partial write on
// error). The generated shell registers every roster uFE by hash route and
// serves on the roster's shell port.
func RenderShell(p ShellParams) error {
	if p.Roster == nil {
		return fmt.Errorf("shell roster is required")
	}
	if err := ValidateName(p.Name); err != nil {
		return err
	}
	if p.ParentDir == "" {
		p.ParentDir = "."
	}

	if p.Preset != "" && p.PresetDir != "" {
		return fmt.Errorf("--preset and --preset-dir are mutually exclusive; pass one")
	}
	// A missing/unknown built-in preset must fail before we write anything.
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

	data := shellTemplateData(p)

	out, err := renderTree(shelltemplates, "shelltemplates", data)
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

	// Apply the preset overlay on top of the base shell (add/override files),
	// rendered against the shell's own template data. This mirrors Render so the
	// overlay is covered by the same atomic-write/cleanup: a malformed preset or
	// an overlay render error removes the partial shell.
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
