package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge-sdk/apilayout"
	"github.com/infobloxopen/devedge/internal/ufescaffold"
	"github.com/infobloxopen/devedge/pkg/config"
)

const (
	// defaultShellFile is where `de ufe new` looks for (or creates) the shell
	// roster when --shell is omitted.
	defaultShellFile = "shell.yaml"
	// defaultUFEDevPort is the uFE dev-server port used for its shell-roster
	// upstream when --dev-port is omitted. It matches the scaffold's own base
	// dev-server port so the generated uFE is routable without extra flags.
	defaultUFEDevPort = 4201
	// defaultShellUpstream is the shell root-config dev server the create-default
	// shell points at (the standard Angular shell dev port).
	defaultShellUpstream = "http://127.0.0.1:4200"
	// defaultShellCDNHost is the simulated CDN host a create-default shell serves
	// uFE bundles from.
	defaultShellCDNHost = "cdn.dev.test"
	// defaultShellAPIUpstream is the backend a create-default (method-1) shell
	// fronts at /api.
	defaultShellAPIUpstream = "http://127.0.0.1:8080"
	// defaultShellHost is the host a create-default (from-scratch) shell is
	// served at when no preset overrides it. It is a single generic dev host
	// (NOT derived per-uFE) so multiple uFEs share one shell origin. A preset's
	// shellHost (e.g. the private infoblox-cto preset's csp.dev.test) overrides
	// it; the public open core never hardcodes a specific product host.
	defaultShellHost = "app.dev.test"
)

// ufeCmd is `de ufe`, the noun for micro-frontend scaffolding. It mirrors
// `de new` (service) but for the frontend: `de ufe new <name>` scaffolds an
// Angular-15 + single-spa micro-frontend wired to the open-core devedge-ufe
// SDK.
func ufeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ufe",
		Short: "Scaffold and manage devedge micro-frontends (uFEs)",
	}
	cmd.AddCommand(ufeNewCmd())
	return cmd
}

// ufeNewCmd is `de ufe new <name>` — the frontend mirror of `de new service`.
// Unlike `de new service` (a thin driver over the devedge-sdk binary), this is
// self-contained: it renders an embedded template tree, exactly like
// internal/scaffold does for Go services. The generated uFE is correct on
// first run — the nav group validates, the route matches, and the known
// template-bootstrap traps are eliminated by construction.
func ufeNewCmd() *cobra.Command {
	var dir, preset, presetDir string
	var shellFile, route string
	var devPort int

	cmd := &cobra.Command{
		Use:   "new NAME",
		Short: "Scaffold a new Angular + single-spa micro-frontend",
		Long: `Scaffold a new Angular-15 + single-spa micro-frontend wired to the
open-core @infobloxopen/devedge-ufe-* SDK.

The generated uFE is correct on first run: its default nav group validates
against a dev GroupRegistry (so it renders, not silently drops), its app route
matches the manifest, the session is provided into Angular DI, and HTTP calls
carry the Bearer token. It ships no Angular-2-era deadweight and no committed
lockfile.

Roster wiring (WS-018): after scaffolding, the new uFE is also registered into a
'kind: Shell' roster so a shell picks it up — the same one-line addition as
'de compose add'. The entry is {id: <name>, route: <route>, upstream:
http://127.0.0.1:<dev-port>}, upserted by id (an existing same-id entry is
updated in place, never duplicated). If --shell names a file that does not exist,
a sensible default shell is created containing just this uFE. Pass --shell "" to
skip roster wiring entirely (scaffold only).

Apply an overlay on top of the base scaffold with either:
  --preset <name>      a built-in preset (the public CLI ships none)
  --preset-dir <path>  a preset directory holding a canonical preset.json
The public CLI ships no proprietary preset; the 'infoblox-cto' preset is
provided by the private Infoblox-CTO/devedge-ufe-sdk-internal repo — apply it
with --preset-dir <repo>/preset/infoblox-cto. An unknown built-in preset or a
missing/malformed preset.json fails with a clear error.

Examples:
  de ufe new discovery
  de ufe new widgets --dir ./frontends
  de ufe new tags --shell notesapp-shell.yaml --route tags --dev-port 4202
  de ufe new widgets --shell ""   # scaffold only, no roster wiring
  de ufe new widgets --preset-dir ../devedge-ufe-sdk-internal/preset/infoblox-cto`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			p := ufescaffold.Params{
				Name:      name,
				ParentDir: dir,
				Preset:    preset,
				PresetDir: presetDir,
			}
			if err := ufescaffold.Render(p); err != nil {
				return err
			}

			parent := dir
			if parent == "" {
				parent = "."
			}
			root := filepath.Join(parent, name)

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "\n%s %s\n", colorSuccess.Sprint("scaffolded uFE"), colorHost.Sprint(name))
			fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("package"), colorHost.Sprintf("csp-%s-ufe", name))
			if preset != "" {
				fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("preset"), colorHost.Sprint(preset))
			}
			if presetDir != "" {
				fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("preset-dir"), colorHost.Sprint(presetDir))
			}

			// Roster wiring (WS-018 Phase C): register the uFE into a shell so the
			// shell picks it up. --shell "" opts out (scaffold only).
			//
			// The create-default shell host is app.dev.test unless the applied
			// preset overrides it via its manifest's shellHost (data, not core
			// code — e.g. the private infoblox-cto preset sets csp.dev.test).
			if shellFile != "" {
				shellHost := defaultShellHost
				if h := ufescaffold.PresetShellHost(presetDir); h != "" {
					shellHost = h
				}
				if err := wireUFEIntoShell(out, shellFile, name, route, devPort, shellHost); err != nil {
					return err
				}
			}

			fmt.Fprintf(out, "\n%s\n", colorHeader.Sprint("Next steps:"))
			fmt.Fprintf(out, "  cd %s\n", root)
			fmt.Fprintf(out, "  pnpm install              %s\n", colorLabel.Sprint("# link local SDK packages until published (see README)"))
			fmt.Fprintf(out, "  pnpm start                %s\n", colorLabel.Sprint("# ng serve on https://localhost:4200"))
			fmt.Fprintf(out, "  pnpm run doctor           %s\n", colorLabel.Sprint("# loud dev-loop checklist (cert/CORS/manifest/nav)"))
			if shellFile != "" {
				fmt.Fprintf(out, "  de project up -f %s   %s\n", shellFile, colorLabel.Sprint("# route the shell + this uFE through the edge"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "parent directory to create the uFE in (defaults to .)")
	cmd.Flags().StringVar(&preset, "preset", "", "built-in overlay preset to apply on top of the base (the public CLI ships none)")
	cmd.Flags().StringVar(&presetDir, "preset-dir", "", "path to a preset directory (with a canonical preset.json) to overlay on top of the base — e.g. the private infoblox-cto preset")
	cmd.Flags().StringVar(&shellFile, "shell", defaultShellFile, `shell config to register the uFE into (created if absent); pass "" to skip roster wiring`)
	cmd.Flags().StringVar(&route, "route", "", "hash route the uFE mounts at + CDN path segment (defaults to NAME)")
	cmd.Flags().IntVar(&devPort, "dev-port", defaultUFEDevPort, "uFE dev-server port for its shell-roster upstream")
	cmd.MarkFlagsMutuallyExclusive("preset", "preset-dir")
	return cmd
}

// wireUFEIntoShell upserts the uFE into the shell roster at shellFile, creating a
// sensible default shell if the file does not yet exist, then reports the
// import-map entry the shell will get and the hash route the uFE mounts at. It is
// idempotent — re-running with the same id updates that entry, never duplicating.
func wireUFEIntoShell(out io.Writer, shellFile, name, route string, devPort int, shellHost string) error {
	if route == "" {
		route = name
	}
	ufe := config.ShellUFE{
		ID:       name,
		Route:    route,
		Upstream: fmt.Sprintf("http://127.0.0.1:%d", devPort),
	}

	shell, created, err := loadOrInitShell(shellFile, shellHost, ufe)
	if err != nil {
		return err
	}

	var verb string
	if created {
		// The default shell was built AROUND this uFE, so it is already present.
		verb = "created shell"
	} else if updated := shell.UpsertUFE(ufe); updated {
		verb = "updated uFE in"
	} else {
		verb = "added uFE to"
	}

	// Re-validate + marshal through the strict schema so the file stays a valid
	// kind: Shell document.
	if err := shell.Validate(); err != nil {
		return fmt.Errorf("shell %s would be invalid after wiring uFE %q: %w", shellFile, name, err)
	}
	data, err := config.MarshalShell(shell)
	if err != nil {
		return err
	}
	if err := os.WriteFile(shellFile, data, 0o644); err != nil {
		return fmt.Errorf("write shell config: %w", err)
	}

	fmt.Fprintf(out, "%s %s (%s)\n", colorSuccess.Sprint(verb), colorHost.Sprint(shellFile), colorHost.Sprint(shell.Project()))
	fmt.Fprintf(out, "%s %s %s %s\n", colorLabel.Sprint("import map"), colorHost.Sprint(name), colorLabel.Sprint("->"), colorHost.Sprint(shell.ImportMap()[name]))
	fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("mounts at"), colorHost.Sprintf("#%s", route))
	return nil
}

// loadOrInitShell reads shellFile as a kind: Shell, upserting ufe into it. If the
// file does not exist it builds a sensible default shell around this one uFE
// (reporting created=true), hosted at host. Any other read/parse error
// propagates.
func loadOrInitShell(shellFile, host string, ufe config.ShellUFE) (shell *config.Shell, created bool, err error) {
	data, rerr := os.ReadFile(shellFile)
	switch {
	case rerr == nil:
		shell, err = config.ParseShell(data)
		if err != nil {
			return nil, false, err
		}
		return shell, false, nil
	case os.IsNotExist(rerr):
		return defaultShell(host, ufe), true, nil
	default:
		return nil, false, fmt.Errorf("read shell config: %w", rerr)
	}
}

// defaultShell builds a create-default kind: Shell around a single uFE. The
// shell is served at host (app.dev.test by default; a preset may override it)
// and named "app" — a generic shell identity, NOT derived per-uFE, so multiple
// uFEs registered later share one shell origin. It uses the standard dev ports:
// the shell root-config on :4200, a simulated CDN, and a same-origin (method 1)
// /api fronting the backend on :8080, with the product-rest URL layout (the
// default; per-domain backends can be added later under spec.api.services).
func defaultShell(host string, ufe config.ShellUFE) *config.Shell {
	return &config.Shell{
		APIVersion: config.APIVersion,
		Kind:       config.KindShell,
		Metadata:   config.ObjectMeta{Name: "app"},
		Spec: config.ShellSpec{
			Host:          host,
			ShellUpstream: defaultShellUpstream,
			CDN:           config.ShellCDN{Host: defaultShellCDNHost},
			API: config.ShellAPI{
				Method:   1,
				Layout:   string(apilayout.Default),
				Prefix:   "/api",
				Upstream: defaultShellAPIUpstream,
			},
			UFEs: []config.ShellUFE{ufe},
		},
	}
}
