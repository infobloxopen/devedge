package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge-sdk/apilayout"
	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/internal/ufescaffold"
	"github.com/infobloxopen/devedge/pkg/config"
)

const (
	// defaultShellFile is where `de ufe new` looks for (or creates) the shell
	// roster when --shell is omitted.
	defaultShellFile = "shell.yaml"
	// defaultUFEDevPort is the uFE dev-server port used for its shell-roster
	// upstream when --dev-port is omitted. It is the same source of truth the
	// scaffold uses for the generated angular.json serve port, so the roster
	// upstream and the real dev-server listener always agree.
	defaultUFEDevPort = ufescaffold.DefaultDevPort
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
	// shellHost overrides it (data, not core code); the public open core never
	// hardcodes a specific product host.
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
	cmd.AddCommand(ufeShellCmd())
	cmd.AddCommand(ufeOverrideCmd())
	return cmd
}

// ufeShellCmd is `de ufe shell` — it scaffolds a runnable single-spa SHELL
// (root-config) from a `kind: Shell` roster (shell.yaml), so a developer can
// render their uFE without copying the example shell. It reads the roster
// (default shell.yaml), renders the embedded shell template tree over it, and
// prints the next steps to install + serve the shell and route it through the
// edge. It is the host-side companion of `de ufe new` (which scaffolds a uFE
// and registers it into the roster).
func ufeShellCmd() *cobra.Command {
	var dir, shellFile, name, preset, presetDir string

	cmd := &cobra.Command{
		Use:   "shell",
		Short: "Scaffold a runnable single-spa shell from a shell.yaml roster",
		Long: `Scaffold a runnable single-spa SHELL (root-config) from a 'kind: Shell'
roster (shell.yaml, as written by 'de ufe new').

The generated shell is the host: it owns the session ONCE, registers every uFE
in the roster by HASH route, loads each uFE's bundle through the browser's native
importmap, and starts single-spa. It renders locally with a no-auth dev session
(flip environment.useDevSession to exercise real OIDC). This is what lets a
developer render their uFE without copying the example shell.

The shell serves on the port in the roster's shellUpstream, so the served port
matches the edge route 'de project up' creates to the shell host. Build + serve
use npx (esbuild + sirv-cli), so no destructive install of a global toolchain is
needed.

A preset is a downstream extension point: an overlay on top of the base shell
that rebinds things like the session provider, design system, and nav shell.
Apply one with either:
  --preset <name>      a built-in preset (the public CLI ships none)
  --preset-dir <path>  a preset directory holding a canonical preset.json
The public CLI ships no built-in preset — overlay your own with --preset-dir
<path>. An unknown built-in preset or a missing/malformed preset.json fails with
a clear error.

Examples:
  de ufe shell
  de ufe shell --shell notesapp-shell.yaml --name notesapp-shell
  de ufe shell --dir ./frontend
  de ufe shell --preset-dir ./my-shell-preset`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(shellFile)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("shell roster %q not found — run 'de ufe new' first to create a shell roster, or pass --shell", shellFile)
				}
				return fmt.Errorf("read shell roster %q: %w", shellFile, err)
			}
			roster, err := config.ParseShell(data)
			if err != nil {
				return err
			}
			if len(roster.Spec.UFEs) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "%s the roster lists no uFEs — the shell will render an empty menu; add one with 'de ufe new'.\n",
					colorWarning.Sprint("warning:"))
			}

			if name == "" {
				proj := roster.Project()
				if proj == "" {
					proj = "app"
				}
				name = proj + "-shell"
			}

			if err := ufescaffold.RenderShell(ufescaffold.ShellParams{
				ParentDir: dir,
				Name:      name,
				Roster:    roster,
				Preset:    preset,
				PresetDir: presetDir,
			}); err != nil {
				return err
			}

			parent := dir
			if parent == "" {
				parent = "."
			}
			root := filepath.Join(parent, name)
			port := ufescaffold.ShellServePort(roster)

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "\n%s %s\n", colorSuccess.Sprint("scaffolded shell"), colorHost.Sprint(name))
			fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("host"), colorHost.Sprint(roster.Spec.Host))
			fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("cdn"), colorHost.Sprint(roster.Spec.CDN.Host))
			if preset != "" {
				fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("preset"), colorHost.Sprint(preset))
			}
			if presetDir != "" {
				fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("preset-dir"), colorHost.Sprint(presetDir))
			}
			for _, u := range roster.Spec.UFEs {
				fmt.Fprintf(out, "%s %s %s %s\n",
					colorLabel.Sprint("uFE"),
					colorHost.Sprint(u.ID),
					colorLabel.Sprintf("#%s ->", u.Route),
					colorHost.Sprintf("https://%s/%s/main.js", roster.Spec.CDN.Host, u.Route))
			}

			fmt.Fprintf(out, "\n%s\n", colorHeader.Sprint("Next steps:"))
			fmt.Fprintf(out, "  cd %s\n", root)
			fmt.Fprintf(out, "  export GITHUB_TOKEN=...    %s\n", colorLabel.Sprint("# a GitHub PAT with read:packages (any account; see the uFE README)"))
			fmt.Fprintf(out, "  pnpm install              %s\n", colorLabel.Sprint("# resolves @infobloxopen/* from GitHub Packages"))
			fmt.Fprintf(out, "  pnpm start                %s\n", colorLabel.Sprintf("# builds + serves the shell on http://127.0.0.1:%d", port))
			fmt.Fprintf(out, "  de project up -f %s   %s\n", shellFile, colorLabel.Sprint("# route the shell + its uFEs through the edge"))
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "parent directory to create the shell in (defaults to .)")
	cmd.Flags().StringVar(&shellFile, "shell", defaultShellFile, "shell roster (kind: Shell) to scaffold the shell from")
	cmd.Flags().StringVar(&name, "name", "", "shell project dir name (defaults to <roster name>-shell)")
	cmd.Flags().StringVar(&preset, "preset", "", "built-in overlay preset to apply on top of the base shell (the public CLI ships none)")
	cmd.Flags().StringVar(&presetDir, "preset-dir", "", "path to a preset directory (with a canonical preset.json) to overlay a downstream preset on top of the base shell (the public CLI ships none)")
	cmd.MarkFlagsMutuallyExclusive("preset", "preset-dir")
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

A preset is a downstream extension point: an overlay on top of the base scaffold
that rebinds things like the session provider, design system, and nav. Apply one
with either:
  --preset <name>      a built-in preset (the public CLI ships none)
  --preset-dir <path>  a preset directory holding a canonical preset.json
The public CLI ships no built-in preset — overlay your own with --preset-dir
<path>. An unknown built-in preset or a missing/malformed preset.json fails with
a clear error.

Examples:
  de ufe new discovery
  de ufe new widgets --dir ./frontends
  de ufe new tags --shell notesapp-shell.yaml --route tags --dev-port 4202
  de ufe new widgets --shell ""   # scaffold only, no roster wiring
  de ufe new widgets --preset-dir ./my-preset`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			p := ufescaffold.Params{
				Name:      name,
				ParentDir: dir,
				Preset:    preset,
				PresetDir: presetDir,
				DevPort:   devPort,
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
			// code).
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
			fmt.Fprintf(out, "  export GITHUB_TOKEN=...    %s\n", colorLabel.Sprint("# a GitHub PAT with read:packages (any account; see README)"))
			fmt.Fprintf(out, "  pnpm install              %s\n", colorLabel.Sprint("# resolves @infobloxopen/* from GitHub Packages"))
			fmt.Fprintf(out, "  pnpm start                %s\n", colorLabel.Sprintf("# ng serve on http://localhost:%d", devPort))
			fmt.Fprintf(out, "  pnpm run doctor           %s\n", colorLabel.Sprint("# loud dev-loop checklist (cert/CORS/manifest/nav)"))
			if shellFile != "" {
				fmt.Fprintf(out, "  de project up -f %s   %s\n", shellFile, colorLabel.Sprint("# route the shell + this uFE through the edge"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "parent directory to create the uFE in (defaults to .)")
	cmd.Flags().StringVar(&preset, "preset", "", "built-in overlay preset to apply on top of the base (the public CLI ships none)")
	cmd.Flags().StringVar(&presetDir, "preset-dir", "", "path to a preset directory (with a canonical preset.json) to overlay a downstream preset on top of the base (the public CLI ships none)")
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

// ufeOverrideParams captures the resolved inputs for `de ufe override` — the
// "integrated <env>" run mode. It is the single source the snippet builder reads
// so the printing is unit-testable without touching the daemon.
type ufeOverrideParams struct {
	name      string // the local uFE (edge path segment + default module specifier)
	env       string // the LIVE hosted shell URL to inject the override into
	module    string // the specifier the target shell knows the uFE by (defaults to name)
	namespace string // shell specifier namespace, e.g. @acme ("" = bare specifier)
	devPort   int    // the local uFE dev-server port to serve through the edge
	cdn       string // the edge CDN host that serves the local bundle over trusted TLS
	route     string // the edge path segment for the local uFE (defaults to name)
}

// overrideKey is the import-map-overrides specifier the localStorage key is
// scoped to: "<namespace>/<module>" when a namespace is set, else "<module>".
func (p ufeOverrideParams) overrideKey() string {
	if p.namespace != "" {
		return p.namespace + "/" + p.module
	}
	return p.module
}

// bundleURL is the trusted-TLS URL the edge serves the local uFE's single-spa
// main.js entry at, which the live shell cross-origin-fetches.
func (p ufeOverrideParams) bundleURL() string {
	return fmt.Sprintf("https://%s/%s/main.js", p.cdn, p.route)
}

// ufeOverrideCmd is `de ufe override NAME` — the "integrated <env>" run mode. It
// serves a developer's LOCAL uFE through the devedge edge and prints the exact
// browser import-map-overrides snippet to inject that local bundle into a LIVE
// hosted shell. The live-shell dev loop is pure browser-side
// import-map-overrides (no proxy): the shell cross-origin-fetches the local
// main.js. devedge already satisfies the three requirements — the edge serves the
// local uFE at https://<cdn>/<route>/main.js over a mkcert-trusted cert, and a
// `de ufe new` uFE sends Access-Control-Allow-Origin:* — so this command just
// wires the edge route and prints the ready-to-paste override.
func ufeOverrideCmd() *cobra.Command {
	var env, module, namespace, cdn, route string
	var devPort int
	var open, dryRun bool

	cmd := &cobra.Command{
		Use:   "override NAME",
		Short: "Serve a local uFE through the edge and print the import-map override to inject it into a live shell",
		Long: `Serve a developer's LOCAL uFE through the devedge edge and print the exact
browser import-map override to inject it into a LIVE hosted shell (e.g. a CSP
env) — the "integrated <env>" run mode.

The live-shell dev loop is pure browser-side import-map-overrides (no proxy): you
open the live shell and point one module specifier at your local bundle, and the
shell cross-origin-fetches it. This command wires that up: it registers an edge
route so your running dev server is served at https://<cdn>/<route>/main.js over
TLS the browser trusts, then prints the override snippet to paste into the live
env's DevTools console.

The local bundle must be the single-spa main.js entry, send
Access-Control-Allow-Origin: *, and be reachable over trusted TLS. A 'de ufe new'
uFE served through the edge satisfies all three (mkcert CA trusted after
'de install'; ACAO:* + allowedHosts:'all' in the scaffold).

NAME is the local uFE (its edge path segment and default module specifier). Use
--module when the target shell knows the uFE by a different specifier, and
--namespace for a namespaced shell (e.g. @acme — the key becomes
import-map-override:@acme/<module>). The override uses the standard
import-map-overrides localStorage key, so any shell that supports it (its UI or a
helper global) picks it up. The uFE dev server must be running (pnpm start).
--dry-run prints the snippet without registering the edge route (no daemon needed).

Examples:
  de ufe override notes --env https://your-shell.example.com
  de ufe override notes --env https://your-shell.example.com --namespace @acme --dev-port 4210 --open
  de ufe override discovery --env https://shell.dev.test --module @acme/discovery --route disco`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if module == "" {
				module = name
			}
			if route == "" {
				route = name
			}
			p := ufeOverrideParams{
				name:      name,
				env:       env,
				module:    module,
				namespace: namespace,
				devPort:   devPort,
				cdn:       cdn,
				route:     route,
			}

			out := cmd.OutOrStdout()
			// Wire the local dev server into the edge so it is served at the trusted
			// TLS <cdn>/<route> the browser can cross-origin fetch. --dry-run skips
			// this (and the daemon) and prints the snippet only.
			if !dryRun {
				if err := registerOverrideRoute(context.Background(), p); err != nil {
					return fmt.Errorf("wire the local uFE into the edge: %w\nstart the devedge daemon: sudo de start", err)
				}
				fmt.Fprintf(out, "%s %s %s %s\n",
					colorSuccess.Sprint("routed"),
					colorHost.Sprintf("https://%s/%s/", p.cdn, p.route),
					colorLabel.Sprint("->"),
					colorHost.Sprintf("http://127.0.0.1:%d", p.devPort))
			}

			printUFEOverride(out, p)

			if open {
				fmt.Fprintf(out, "\n%s %s\n", colorLabel.Sprint("opening"), colorHost.Sprint(p.env))
				return openBrowser(p.env)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&env, "env", "", "live hosted shell URL to inject the override into (REQUIRED), e.g. https://your-shell.example.com")
	cmd.Flags().StringVar(&module, "module", "", "module specifier the target shell knows the uFE by (defaults to NAME)")
	cmd.Flags().StringVar(&namespace, "namespace", "", "specifier namespace of the target shell, e.g. @acme (empty = bare specifier)")
	cmd.Flags().IntVar(&devPort, "dev-port", ufescaffold.DefaultDevPort, "local uFE dev-server port to serve through the edge")
	cmd.Flags().StringVar(&cdn, "cdn", defaultShellCDNHost, "edge CDN host that serves the local bundle over trusted TLS")
	cmd.Flags().StringVar(&route, "route", "", "edge path segment for the local uFE (defaults to NAME)")
	cmd.Flags().BoolVar(&open, "open", false, "open the live shell in a browser after wiring the override")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the override snippet without registering the edge route (no daemon needed)")
	_ = cmd.MarkFlagRequired("env")
	return cmd
}

// registerOverrideRoute wires the local uFE dev server into the edge so the live
// shell can cross-origin fetch its bundle over trusted TLS. It is the exact route
// `de register <cdn> http://127.0.0.1:<dev-port> --path /<route> --strip-prefix`
// creates, kept as a separate helper so the snippet printing stays daemon-free
// and unit-testable.
func registerOverrideRoute(ctx context.Context, p ufeOverrideParams) error {
	return newClient().Register(ctx, daemon.RegisterRequest{
		Host:        p.cdn,
		Upstream:    fmt.Sprintf("http://127.0.0.1:%d", p.devPort),
		Protocol:    "http",
		Path:        "/" + p.route,
		StripPrefix: true,
	})
}

// printUFEOverride writes the "integrated mode" block: the target env, the
// cross-origin bundle URL the live shell fetches, and the exact
// import-map-overrides snippets (set + clear) to paste into the live env's
// DevTools console. The JS command lines are printed plain (no color) so they
// copy-paste cleanly. This is intentionally separate from route registration so
// it can be exercised without a running daemon.
func printUFEOverride(out io.Writer, p ufeOverrideParams) {
	key := p.overrideKey()
	bundle := p.bundleURL()
	storageKey := "import-map-override:" + key

	fmt.Fprintf(out, "\n%s %s\n", colorSuccess.Sprint("integrated mode"), colorHost.Sprint(p.name))
	fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("target env"), colorHost.Sprint(p.env))
	fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("specifier "), colorHost.Sprint(key))
	fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("bundle    "), colorHost.Sprint(bundle))

	fmt.Fprintf(out, "\n%s\n", colorHeader.Sprint("Set the override — paste into the live shell's DevTools console:"))
	fmt.Fprintf(out, "  localStorage.setItem(%q, %q); location.reload()\n", storageKey, bundle)

	fmt.Fprintf(out, "\n%s\n", colorHeader.Sprint("Clear the override:"))
	fmt.Fprintf(out, "  localStorage.removeItem(%q); location.reload()\n", storageKey)

	fmt.Fprintf(out, "\n%s\n", colorHeader.Sprint("Notes:"))
	fmt.Fprintf(out, "  %s the uFE dev server must be running (pnpm start) and serving main.js\n", colorLabel.Sprint("-"))
	fmt.Fprintf(out, "  %s the bundle sends Access-Control-Allow-Origin: * (de ufe new does this)\n", colorLabel.Sprint("-"))
	fmt.Fprintf(out, "  %s the browser must trust the devedge mkcert CA (it does after 'de install')\n", colorLabel.Sprint("-"))
	fmt.Fprintf(out, "  %s this is the standard import-map-overrides key; a shell's override UI/helper writes the same one\n", colorLabel.Sprint("-"))
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
