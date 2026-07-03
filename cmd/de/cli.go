package main

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge/internal/cliscaffold"
)

// cliSDKModule is the open-core CLI runtime module the scaffolded shell (and its
// generated domain modules) build against. cligen lives under its cmd/.
const cliSDKModule = "github.com/infobloxopen/devedge-cli-sdk"

// cliCmd is `de cli`, the noun for CLI-shell scaffolding. It is the CLI mirror
// of `de ufe`: `de cli new <name>` scaffolds a rebrandable CLI shell wired to
// the open-core devedge-cli-sdk clikit runtime, and `de cli add` generates a
// domain command module from an API spec and wires it into that shell.
func cliCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cli",
		Short: "Scaffold and extend rebrandable devedge CLIs",
	}
	cmd.AddCommand(cliNewCmd(), cliAddCmd())
	return cmd
}

// cliNewCmd is `de cli new <name>` — the CLI mirror of `de ufe new`. Like that
// command it is self-contained: it renders an embedded template tree (exactly as
// internal/scaffold does for Go services and internal/ufescaffold for uFEs). The
// generated shell owns session construction and is correct on first run; domains
// are added afterwards with `de cli add`.
func cliNewCmd() *cobra.Command {
	var dir, module, presetDir string

	cmd := &cobra.Command{
		Use:   "new NAME",
		Short: "Scaffold a new rebrandable CLI shell",
		Long: `Scaffold a new rebrandable CLI shell wired to the open-core
github.com/infobloxopen/devedge-cli-sdk clikit runtime.

The generated shell is the CLI mirror of a devedge micro-frontend shell: it owns
session construction (binding the generic OIDC device-grant provider from the
active profile, or a --dev static stub) and composes generated "domain command
modules" that consume only the read-only clikit runtime. It builds as-is; add
domains afterwards with 'de cli add'.

Apply an overlay on top of the base scaffold with:
  --preset-dir <path>  a preset directory holding a canonical preset.json
The public CLI ships no proprietary preset; a product-specific preset (concrete
OIDC issuer/client, branding, extra commands) is applied with --preset-dir. A
missing/malformed preset.json fails with a clear error.

Examples:
  de cli new ib
  de cli new ib --module github.com/acme/ib
  de cli new ib --dir ./clis
  de cli new ib --preset-dir ../devedge-cli-sdk-internal/preset/infoblox-cli`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			p := cliscaffold.Params{
				Name:      name,
				ParentDir: dir,
				Module:    module,
				PresetDir: presetDir,
			}
			if err := cliscaffold.Render(p); err != nil {
				return err
			}

			parent := dir
			if parent == "" {
				parent = "."
			}
			root := filepath.Join(parent, name)
			mod := module
			if mod == "" {
				mod = name
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "\n%s %s\n", colorSuccess.Sprint("scaffolded CLI"), colorHost.Sprint(name))
			fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("module"), colorHost.Sprint(mod))
			if presetDir != "" {
				fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("preset-dir"), colorHost.Sprint(presetDir))
			}

			fmt.Fprintf(out, "\n%s\n", colorHeader.Sprint("Next steps:"))
			fmt.Fprintf(out, "  cd %s\n", root)
			fmt.Fprintf(out, "  de cli add --input <spec> --domain <name>   %s\n", colorLabel.Sprint("# generate + wire a domain command module"))
			fmt.Fprintf(out, "  go mod tidy && go build -o %s .\n", name)
			fmt.Fprintf(out, "  ./%s --help\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "parent directory to create the CLI in (defaults to .)")
	cmd.Flags().StringVar(&module, "module", "", "Go module path for the generated CLI (defaults to NAME)")
	cmd.Flags().StringVar(&presetDir, "preset-dir", "", "path to a preset directory (with a canonical preset.json) to overlay on top of the base")
	return cmd
}

// cliAddCmd is `de cli add` — generate a domain command module from an enriched
// OpenAPI v3 spec (via the devedge-cli-sdk cligen tool) into the shell's gen/,
// then regenerate the shell's domains_gen.go so the new domain is wired in. It is
// the CLI analog of `de api publish --client`: de drives the SDK's generator.
//
// It is idempotent: re-adding a domain regenerates it in place, and the wiring
// is rebuilt from the gen/ directory listing each run.
func cliAddCmd() *cobra.Command {
	var dir, input, domain, appName string

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Generate a domain command module and wire it into the CLI shell",
		Long: `Generate a domain command module from an enriched OpenAPI v3 spec and
wire it into a CLI shell created by 'de cli new'.

It runs the devedge-cli-sdk cligen generator into <dir>/gen/<domain>, then
regenerates <dir>/domains_gen.go so the shell registers the new domain. The
generated package builds in-module (no nested go.mod). Re-running for the same
domain regenerates it in place.

Examples:
  de cli add --input widgets.openapi.yaml --domain widgets
  de cli add --input ../svc/openapi/svc.openapi.yaml --domain orders --dir ./ib`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if input == "" {
				return fmt.Errorf("--input is required (path to an enriched OpenAPI v3 spec)")
			}
			if domain == "" {
				return fmt.Errorf("--domain is required")
			}
			if err := requireTools("go"); err != nil {
				return err
			}

			targetDir := dir
			if targetDir == "" {
				targetDir = "."
			}

			gomod, err := os.ReadFile(filepath.Join(targetDir, "go.mod"))
			if err != nil {
				return fmt.Errorf("read %s: %w (run this inside a 'de cli new' shell repo, or pass --dir)", filepath.Join(targetDir, "go.mod"), err)
			}
			modPath := modulePathFromGoMod(gomod)
			if modPath == "" {
				return fmt.Errorf("no module directive in %s", filepath.Join(targetDir, "go.mod"))
			}
			if appName == "" {
				appName = appNameFromShell(targetDir, modPath)
			}

			// Pin cligen to the SDK version the shell builds against so the
			// generated code matches the runtime. Running the pinned tool via
			// 'go run <pkg>@<ver>' also resolves it independently of the shell's
			// (possibly not-yet-tidied) go.sum.
			ver := cliSDKVersionFromGoMod(gomod)
			if ver == "" {
				ver = cliscaffold.DefaultVersions.SDK
			}

			specAbs, err := filepath.Abs(input)
			if err != nil {
				return err
			}
			if _, err := os.Stat(specAbs); err != nil {
				return fmt.Errorf("--input spec not readable: %w", err)
			}

			outRel := filepath.Join("gen", domain)
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "\n%s: cligen --domain %s --output %s\n", colorHeader.Sprint("generating domain"), colorHost.Sprint(domain), colorHost.Sprint(outRel))
			if err := runExec(cmd, targetDir, "go", "run", cliSDKModule+"/cmd/cligen@"+ver,
				"--input", specAbs,
				"--output", outRel,
				"--module", modPath,
				"--domain", domain,
				"--package", domain,
				"--app", appName,
			); err != nil {
				return fmt.Errorf("cligen: %w", err)
			}

			if err := regenerateDomainsFile(targetDir, modPath); err != nil {
				return err
			}

			fmt.Fprintf(out, "\n%s %s %s %s\n", colorSuccess.Sprint("wired"), colorHost.Sprint(domain), colorLabel.Sprint("into"), colorHost.Sprint(filepath.Join(targetDir, "domains_gen.go")))
			fmt.Fprintf(out, "\n%s\n", colorHeader.Sprint("Next steps:"))
			fmt.Fprintf(out, "  go mod tidy && go build ./...\n")
			fmt.Fprintf(out, "  ./%s %s --help\n", appName, domain)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "the CLI shell repo directory (defaults to .)")
	cmd.Flags().StringVar(&input, "input", "", "path to the enriched OpenAPI v3 spec (required)")
	cmd.Flags().StringVar(&domain, "domain", "", "domain command name to add (required)")
	cmd.Flags().StringVar(&appName, "app", "", "rebranded app name (defaults to the shell's appName, else the module basename)")
	return cmd
}

// moduleDirectiveRe matches the module path in a go.mod.
var moduleDirectiveRe = regexp.MustCompile(`(?m)^\s*module\s+(\S+)`)

// modulePathFromGoMod extracts the module path declared in a go.mod.
func modulePathFromGoMod(gomod []byte) string {
	if m := moduleDirectiveRe.FindSubmatch(gomod); m != nil {
		return string(m[1])
	}
	return ""
}

// cliSDKVersionRe matches the devedge-cli-sdk require version in a go.mod.
var cliSDKVersionRe = regexp.MustCompile(regexp.QuoteMeta(cliSDKModule) + `\s+(v\S+)`)

// cliSDKVersionFromGoMod extracts the devedge-cli-sdk version the shell requires,
// or "" when absent.
func cliSDKVersionFromGoMod(gomod []byte) string {
	if m := cliSDKVersionRe.FindSubmatch(gomod); m != nil {
		return string(m[1])
	}
	return ""
}

// appNameConstRe matches the `const appName = "..."` marker the scaffolded shell
// carries.
var appNameConstRe = regexp.MustCompile(`const\s+appName\s*=\s*"([^"]+)"`)

// appNameFromShell resolves the shell's rebranded app name from its main.go
// marker, falling back to the last path element of the module path.
func appNameFromShell(dir, modPath string) string {
	if b, err := os.ReadFile(filepath.Join(dir, "main.go")); err == nil {
		if m := appNameConstRe.FindSubmatch(b); m != nil {
			return string(m[1])
		}
	}
	return modPath[strings.LastIndex(modPath, "/")+1:]
}

// identReplacer maps characters not allowed in a Go identifier to underscores.
var identReplacer = regexp.MustCompile(`[^A-Za-z0-9_]`)

// sanitizeIdent turns a domain (a DNS label, e.g. "my-widgets") into a valid Go
// import-alias identifier (e.g. "my_widgets").
func sanitizeIdent(domain string) string {
	return identReplacer.ReplaceAllString(domain, "_")
}

// regenerateDomainsFile rebuilds <dir>/domains_gen.go from the domain packages
// present under <dir>/gen: each direct subdirectory of gen/ is a generated
// domain command module. It emits an import (aliased) and a root.AddCommand call
// per domain, then gofmt-formats the result. With no domains it emits the empty
// (but compiling) wiring hook.
func regenerateDomainsFile(dir, modPath string) error {
	genDir := filepath.Join(dir, "gen")
	entries, err := os.ReadDir(genDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", genDir, err)
	}
	var domains []string
	for _, e := range entries {
		if e.IsDir() {
			domains = append(domains, e.Name())
		}
	}
	sort.Strings(domains)

	var b strings.Builder
	b.WriteString("// Code generated by \"de cli add\". DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// This file wires generated domain command modules into the shell. It is\n")
	b.WriteString("// regenerated by \"de cli add\" each time a domain is added under gen/. When no\n")
	b.WriteString("// domains have been added yet, registerDomains is a no-op.\n")
	b.WriteString("package main\n\n")
	b.WriteString("import (\n")
	b.WriteString("\t\"github.com/infobloxopen/devedge-cli-sdk/clikit\"\n")
	b.WriteString("\t\"github.com/spf13/cobra\"\n")
	for _, d := range domains {
		fmt.Fprintf(&b, "\t%s %q\n", sanitizeIdent(d), modPath+"/gen/"+d)
	}
	b.WriteString(")\n\n")
	b.WriteString("// registerDomains adds every generated domain command module to the shell root.\n")
	b.WriteString("func registerDomains(root *cobra.Command, rt clikit.Runtime) {\n")
	for _, d := range domains {
		fmt.Fprintf(&b, "\troot.AddCommand(%s.Command(rt))\n", sanitizeIdent(d))
	}
	b.WriteString("}\n")

	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return fmt.Errorf("format domains_gen.go: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "domains_gen.go"), formatted, 0o644)
}
