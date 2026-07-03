package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge/internal/tfscaffold"
)

// tfSDKModule is the open-core Terraform runtime module the scaffolded provider
// builds against. tfgen (the resource generator) lives under its cmd/.
const tfSDKModule = "github.com/infobloxopen/devedge-terraform-sdk"

// terraformCmd is `de terraform`, the noun for Terraform-provider scaffolding. It
// is the Terraform mirror of `de cli`: `de terraform new <name>` scaffolds a
// registry-publishable terraform-provider-<name> repo wired to the open-core
// devedge-terraform-sdk tfkit runtime, and `de terraform add` generates a
// resource from an API spec into that provider by driving tfgen.
func terraformCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "terraform",
		Aliases: []string{"tf"},
		Short:   "Scaffold and extend registry-publishable Terraform providers",
	}
	cmd.AddCommand(terraformNewCmd(), terraformAddCmd())
	return cmd
}

// terraformNewCmd is `de terraform new <name>` — the Terraform mirror of
// `de cli new`. Like that command it is self-contained: it renders an embedded
// template tree (as internal/scaffold does for services and internal/cliscaffold
// for CLIs). The generated provider owns a small tfkit seam and builds as-is;
// resources are added afterwards with `de terraform add`.
func terraformNewCmd() *cobra.Command {
	var dir, module, org, presetDir string

	cmd := &cobra.Command{
		Use:   "new NAME",
		Short: "Scaffold a new registry-publishable Terraform provider",
		Long: `Scaffold a new registry-publishable Terraform provider wired to the
open-core github.com/infobloxopen/devedge-terraform-sdk tfkit runtime.

The generated repo is a terraform-provider-<name> Go module shaped for the
Terraform Registry: a HashiCorp-style GoReleaser config (zip archives +
GPG-signed SHA256SUMS + the registry manifest), a terraform-registry-manifest.json,
and a tag-triggered release workflow. It owns a small hand-written seam
(internal/provider/provider.go composing tfkit) and builds as-is; add resources
afterwards with 'de terraform add'.

Apply an overlay on top of the base scaffold with:
  --preset-dir <path>  a preset directory holding a canonical preset.json
The public devedge repo ships no proprietary preset; a product-specific preset
(a concrete auth binding, branding) is applied with --preset-dir. A
missing/malformed preset.json fails with a clear error.

Examples:
  de terraform new toy
  de terraform new toy --module github.com/acme/terraform-provider-toy
  de terraform new toy --org acme --dir ./providers
  de terraform new toy --preset-dir ../devedge-terraform-sdk-internal/preset/acme`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			p := tfscaffold.Params{
				Name:      name,
				ParentDir: dir,
				Module:    module,
				Org:       org,
				PresetDir: presetDir,
			}
			if err := tfscaffold.Render(p); err != nil {
				return err
			}

			parent := dir
			if parent == "" {
				parent = "."
			}
			repoName := "terraform-provider-" + name
			root := filepath.Join(parent, repoName)
			resolvedOrg := org
			if resolvedOrg == "" {
				resolvedOrg = tfscaffold.DefaultOrg
			}
			mod := module
			if mod == "" {
				mod = "github.com/" + resolvedOrg + "/" + repoName
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "\n%s %s\n", colorSuccess.Sprint("scaffolded Terraform provider"), colorHost.Sprint(repoName))
			fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("module"), colorHost.Sprint(mod))
			fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("registry"), colorHost.Sprint("registry.terraform.io/"+resolvedOrg+"/"+name))
			if presetDir != "" {
				fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("preset-dir"), colorHost.Sprint(presetDir))
			}

			fmt.Fprintf(out, "\n%s\n", colorHeader.Sprint("Next steps:"))
			fmt.Fprintf(out, "  cd %s\n", root)
			fmt.Fprintf(out, "  de terraform add --input <spec> --resource <name>   %s\n", colorLabel.Sprint("# generate + register a resource"))
			fmt.Fprintf(out, "  go mod tidy && go build ./...\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "parent directory to create the provider in (defaults to .)")
	cmd.Flags().StringVar(&module, "module", "", "Go module path for the generated provider (defaults to github.com/<org>/terraform-provider-<name>)")
	cmd.Flags().StringVar(&org, "org", "", "Terraform Registry namespace / default module owner (defaults to "+tfscaffold.DefaultOrg+")")
	cmd.Flags().StringVar(&presetDir, "preset-dir", "", "path to a preset directory (with a canonical preset.json) to overlay on top of the base")
	return cmd
}

// terraformAddCmd is `de terraform add` — generate a Terraform resource from an
// enriched OpenAPI v3 spec (via the devedge-terraform-sdk tfgen tool) into a
// provider created by `de terraform new`. tfgen writes the resource module and
// rewrites internal/provider/resources_gen.go to register every resource; it
// never touches the hand-written provider.go seam. It is the Terraform analog of
// `de cli add`: de drives the SDK's generator.
//
// It is idempotent: re-adding a resource regenerates it (and the registration)
// in place, byte-for-byte.
func terraformAddCmd() *cobra.Command {
	var dir, input, resource, providerName string

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Generate a Terraform resource and register it in the provider",
		Long: `Generate a Terraform resource from an enriched OpenAPI v3 spec and
register it in a provider created by 'de terraform new'.

It runs the devedge-terraform-sdk tfgen generator against the provider repo,
writing internal/provider/<resource>_resource*.go and rewriting
internal/provider/resources_gen.go so the provider serves the new resource. The
hand-written internal/provider/provider.go seam is never overwritten. Re-running
for the same resource regenerates it in place.

The provider type name is taken from --provider, else derived from the module's
terraform-provider-<name> suffix.

Examples:
  de terraform add --input widgets.openapi.yaml --resource widget
  de terraform add --input ../svc/openapi/svc.openapi.yaml --resource order --dir ./terraform-provider-toy`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if input == "" {
				return fmt.Errorf("--input is required (path to an enriched OpenAPI v3 spec)")
			}
			if resource == "" {
				return fmt.Errorf("--resource is required")
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
				return fmt.Errorf("read %s: %w (run this inside a 'de terraform new' provider repo, or pass --dir)", filepath.Join(targetDir, "go.mod"), err)
			}
			modPath := modulePathFromGoMod(gomod)
			if modPath == "" {
				return fmt.Errorf("no module directive in %s", filepath.Join(targetDir, "go.mod"))
			}
			if providerName == "" {
				providerName = providerNameFromModule(modPath)
			}
			if err := tfscaffold.ValidateName(providerName); err != nil {
				return fmt.Errorf("provider name %q (from --provider or the module path): %w — pass --provider", providerName, err)
			}

			// Pin tfgen to the devedge-terraform-sdk version the provider builds
			// against so the generated code matches the tfkit runtime. Running the
			// pinned tool via 'go run <pkg>@<ver>' also resolves it independently of
			// the provider's (possibly not-yet-tidied) go.sum.
			ver := tfSDKVersionFromGoMod(gomod)
			if ver == "" {
				ver = tfscaffold.DefaultVersions.SDK
			}

			specAbs, err := filepath.Abs(input)
			if err != nil {
				return err
			}
			if _, err := os.Stat(specAbs); err != nil {
				return fmt.Errorf("--input spec not readable: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "\n%s: tfgen --provider %s --resource %s --output %s\n",
				colorHeader.Sprint("generating resource"), colorHost.Sprint(providerName), colorHost.Sprint(resource), colorHost.Sprint(filepath.Join("internal", "provider")))
			if err := runExec(cmd, targetDir, "go", "run", tfSDKModule+"/cmd/tfgen@"+ver,
				"--input", specAbs,
				"--output", filepath.Join("internal", "provider"),
				"--module", modPath,
				"--provider", providerName,
				"--resource", resource,
			); err != nil {
				return fmt.Errorf("tfgen: %w", err)
			}

			fmt.Fprintf(out, "\n%s %s %s %s\n", colorSuccess.Sprint("registered"), colorHost.Sprint(resource), colorLabel.Sprint("in"), colorHost.Sprint(filepath.Join(targetDir, "internal", "provider", "resources_gen.go")))
			fmt.Fprintf(out, "\n%s\n", colorHeader.Sprint("Next steps:"))
			fmt.Fprintf(out, "  go mod tidy && go build ./...\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "the provider repo directory (defaults to .)")
	cmd.Flags().StringVar(&input, "input", "", "path to the enriched OpenAPI v3 spec (required)")
	cmd.Flags().StringVar(&resource, "resource", "", "resource (TF name) to add (required)")
	cmd.Flags().StringVar(&providerName, "provider", "", "provider type name (defaults to the module's terraform-provider-<name> suffix)")
	return cmd
}

// tfSDKVersionRe matches the devedge-terraform-sdk require version in a go.mod.
var tfSDKVersionRe = regexp.MustCompile(regexp.QuoteMeta(tfSDKModule) + `\s+(v\S+)`)

// tfSDKVersionFromGoMod extracts the devedge-terraform-sdk version the provider
// requires, or "" when absent.
func tfSDKVersionFromGoMod(gomod []byte) string {
	if m := tfSDKVersionRe.FindSubmatch(gomod); m != nil {
		return string(m[1])
	}
	return ""
}

// providerNameFromModule derives the Terraform provider type name from a
// provider module path: the last path element with any "terraform-provider-"
// prefix stripped (e.g. github.com/acme/terraform-provider-toy → "toy").
func providerNameFromModule(modPath string) string {
	base := modPath[strings.LastIndex(modPath, "/")+1:]
	return strings.TrimPrefix(base, "terraform-provider-")
}
