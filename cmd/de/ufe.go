package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge/internal/ufescaffold"
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
			fmt.Fprintf(out, "\n%s\n", colorHeader.Sprint("Next steps:"))
			fmt.Fprintf(out, "  cd %s\n", root)
			fmt.Fprintf(out, "  pnpm install              %s\n", colorLabel.Sprint("# link local SDK packages until published (see README)"))
			fmt.Fprintf(out, "  pnpm start                %s\n", colorLabel.Sprint("# ng serve on https://localhost:4200"))
			fmt.Fprintf(out, "  pnpm run doctor           %s\n", colorLabel.Sprint("# loud dev-loop checklist (cert/CORS/manifest/nav)"))
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "parent directory to create the uFE in (defaults to .)")
	cmd.Flags().StringVar(&preset, "preset", "", "built-in overlay preset to apply on top of the base (the public CLI ships none)")
	cmd.Flags().StringVar(&presetDir, "preset-dir", "", "path to a preset directory (with a canonical preset.json) to overlay on top of the base — e.g. the private infoblox-cto preset")
	cmd.MarkFlagsMutuallyExclusive("preset", "preset-dir")
	return cmd
}
