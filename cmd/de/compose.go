package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge/internal/compose"
	"github.com/infobloxopen/devedge/pkg/config"
)

// composeCmd is `de compose`, the WS-012 Phase 4 surface: turn a `kind:
// Composition` file into a STATIC composed-suite binary built on devedge-sdk's
// servicekit host. The generated cmd/<name>/main.go IMPORTS the member modules
// and calls servicekit.Run — no Go plugins (proposal §10-B).
func composeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compose",
		Short: "Compose service modules into one suite binary (WS-012)",
		Long: `Compose several service modules into ONE host process.

A 'kind: Composition' file lists member modules (each an importable package
exposing a zero-arg Module() constructor). 'de compose build' generates a
cmd/<name>/main.go that imports those modules and runs them via
servicekit.Run — static composition, no Go plugins. The same modules run
standalone or composed by changing the host, not the module.`,
	}
	cmd.AddCommand(
		composeInitCmd(),
		composeAddCmd(),
		composeRemoveCmd(),
		composeTidyCmd(),
		composeBuildCmd(),
		composeTestCmd(),
		composeUpCmd(),
		composeChartCmd(),
	)
	return cmd
}

// loadComposition loads a composition file and asserts its kind, returning an
// actionable error if the file is some other resource kind. It validates the file
// as a COMPLETE composition (≥1 module) — the form build/tidy/up require.
func loadComposition(file string) (*config.Composition, error) {
	res, err := config.LoadResource(file)
	if err != nil {
		return nil, err
	}
	comp, ok := res.(*config.Composition)
	if !ok {
		return nil, fmt.Errorf("%s is %q, not a Composition (use a 'kind: Composition' file)", file, kindOf(res))
	}
	return comp, nil
}

// loadCompositionForEdit loads a composition file tolerantly (an as-yet-empty
// member set is allowed) so the FIRST member can be added to a freshly-scaffolded
// file. Used by `de compose add`/`remove`.
func loadCompositionForEdit(file string) (*config.Composition, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read composition config: %w", err)
	}
	return config.ParseCompositionForEdit(data)
}

// kindOf reports a loaded resource's kind for error messages.
func kindOf(res config.Resource) string {
	switch res.(type) {
	case *config.ProjectConfig:
		return config.Kind
	case *config.ServiceConfig:
		return config.KindService
	case *config.Composition:
		return config.KindComposition
	default:
		return "unknown"
	}
}

// composeInitCmd scaffolds a `kind: Composition` file.
func composeInitCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "init NAME",
		Short: "Scaffold a kind: Composition file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if _, err := os.Stat(file); err == nil {
				return fmt.Errorf("%s already exists; not overwriting", file)
			}
			data := renderCompositionYAML(name)
			// A freshly-scaffolded composition has no members yet (added via
			// `de compose add`), so it does not yet satisfy the strict
			// at-least-one-module rule — write it as a starter, not a complete doc.
			if err := os.WriteFile(file, []byte(data), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s (%s)\n", colorSuccess.Sprint("scaffolded composition"), colorHost.Sprint(name), file)
			fmt.Fprintf(cmd.OutOrStdout(), "next: %s\n", colorLabel.Sprint("de compose add <module>@<version>"))
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "composition.yaml", "composition config file")
	return cmd
}

// composeAddCmd adds a member module to a composition.
func composeAddCmd() *cobra.Command {
	var file, name, configPrefix, schema string
	cmd := &cobra.Command{
		Use:   "add MODULE[@VERSION]",
		Short: "Add a member module to the composition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			comp, err := loadCompositionForEdit(file)
			if err != nil {
				return err
			}
			ref := args[0]
			path, _, err := compose.ParseModuleRef(ref)
			if err != nil {
				return err
			}
			if name == "" {
				name = lastPathSegment(path)
			}
			for _, m := range comp.Spec.Modules {
				if m.Name == name {
					return fmt.Errorf("module name %q is already a member", name)
				}
			}
			entry := config.ModuleEntry{Name: name, Module: ref, ConfigPrefix: configPrefix}
			if schema != "" {
				entry.Database = &config.ModuleDatabase{Schema: schema}
			}
			comp.Spec.Modules = append(comp.Spec.Modules, entry)
			if err := writeComposition(file, comp); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s %s\n", colorSuccess.Sprint("added"), colorHost.Sprint(name), colorLabel.Sprint("->"), ref)
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "composition.yaml", "composition config file")
	cmd.Flags().StringVar(&name, "name", "", "member name (defaults to the import path's last segment)")
	cmd.Flags().StringVar(&configPrefix, "config-prefix", "", "config namespace prefix (defaults to name)")
	cmd.Flags().StringVar(&schema, "schema", "", "DB schema for this module (defaults to name)")
	return cmd
}

// composeRemoveCmd removes a member module from a composition by name.
func composeRemoveCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "remove NAME",
		Short: "Remove a member module from the composition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			comp, err := loadCompositionForEdit(file)
			if err != nil {
				return err
			}
			name := args[0]
			kept := comp.Spec.Modules[:0]
			removed := false
			for _, m := range comp.Spec.Modules {
				if m.Name == name {
					removed = true
					continue
				}
				kept = append(kept, m)
			}
			if !removed {
				return fmt.Errorf("no member named %q in %s", name, file)
			}
			comp.Spec.Modules = kept
			if err := writeComposition(file, comp); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", colorSuccess.Sprint("removed"), colorHost.Sprint(name))
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "composition.yaml", "composition config file")
	return cmd
}

// composeTidyCmd resolves + validates the composition's member modules.
func composeTidyCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "tidy",
		Short: "Validate member modules: descriptor conflicts + version compatibility",
		Long: `Resolve the composition's member modules and validate the descriptor union
(unique IDs; no duplicate gRPC service / HTTP route prefix / permission names; a
coherent event graph) plus version compatibility, reporting any conflict.

Modules linked into 'de' (and the test fixtures) are validated in-process;
external modules that 'de' does not link are reported as unresolved (they are
still buildable via 'de compose build', which compiles them statically).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			comp, err := loadComposition(file)
			if err != nil {
				return err
			}
			report, err := compose.Tidy(comp, nil, goRuntimeVersion())
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), report.Format(comp.Project()))
			if !report.OK() {
				return fmt.Errorf("composition %q has conflicts", comp.Project())
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "composition.yaml", "composition config file")
	return cmd
}

// composeBuildCmd generates the composed binary sources.
func composeBuildCmd() *cobra.Command {
	var file, modulePath, outDir string
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Generate cmd/<name>/main.go + go.mod + composition.lock",
		Long: `Generate the STATIC composed-binary sources from the composition:
a cmd/<name>/main.go that imports the member modules and calls servicekit.Run,
a go.mod for the composed binary, and a composition.lock pinning the members +
SDK + toolchain. No Go plugins — the modules are imported, not loaded.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			comp, err := loadComposition(file)
			if err != nil {
				return err
			}
			mp := modulePath
			if mp == "" {
				mp = "example.com/" + comp.Project() + "/cmd/" + comp.Project()
			}
			gen, err := compose.Generate(comp, goRuntimeVersion(), mp)
			if err != nil {
				return err
			}
			base := outDir
			if base == "" {
				base = filepath.Dir(file)
			}
			dir := filepath.Join(base, gen.Dir)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			files := map[string]string{
				"main.go":          gen.MainGo,
				"go.mod":           gen.GoMod,
				"composition.lock": gen.Lock,
			}
			for fn, content := range files {
				if err := os.WriteFile(filepath.Join(dir, fn), []byte(content), 0o644); err != nil {
					return fmt.Errorf("write %s: %w", fn, err)
				}
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s %s\n", colorSuccess.Sprint("generated composed host"), colorHost.Sprint(comp.Project()))
			for fn := range files {
				fmt.Fprintf(out, "  %s %s\n", colorLabel.Sprint("wrote"), filepath.Join(dir, fn))
			}
			fmt.Fprintf(out, "next: %s\n", colorLabel.Sprintf("cd %s && go mod tidy && go build", dir))
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "composition.yaml", "composition config file")
	cmd.Flags().StringVar(&modulePath, "module-path", "", "Go module path for the generated binary")
	cmd.Flags().StringVarP(&outDir, "out", "o", "", "base output directory (defaults to the composition file's dir)")
	return cmd
}

// composeTestCmd runs the composition smoke harness against the member modules.
// It generates a throwaway test that calls servicekittest.AssertComposition and
// runs it in the generated cmd/<name> module (so the members are linked).
func composeTestCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Smoke-test the composition (servicekittest.AssertComposition)",
		Long: `Run the composition smoke test: AssertComposition validates the descriptor
union, boots the composed host over the union (the server's fail-closed
completeness gate), and shuts down cleanly. With no shared DB + migrations
configured it runs entirely in-process (no Docker); the real-DB path runs only
when the modules declare migrations and a shared database is configured.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			comp, err := loadComposition(file)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("smoke-testing composition"), colorHost.Sprint(comp.Project()))

			// Boot the resolvable (linked) members in-process — the same boot gate
			// servicekittest.AssertComposition runs (descriptor union + the server's
			// fail-closed completeness gate, no listener).
			res, err := compose.Smoke(comp, nil)
			if err != nil {
				return err
			}
			for _, u := range res.Unresolved {
				fmt.Fprintf(out, "%s %s (not linked into de; smoke it from cmd/%s via `de compose build`)\n",
					colorLabel.Sprint("skipped:"), u, comp.Project())
			}
			if len(res.Resolved) > 0 {
				fmt.Fprintf(out, "%s booted %d module(s) over the union completeness gate\n",
					colorSuccess.Sprint("ok:"), len(res.Resolved))
			}

			// Real-DB path reporting (honest): it runs only when a shared DB +
			// module migrations are present, from the generated cmd/<name> smoke
			// test that supplies a MigrationRunner + Database (gated on Docker).
			if comp.Spec.Database != nil {
				fmt.Fprintf(out, "%s shared database declared (%s); the REAL-DB migration path runs from the generated cmd/%s smoke test (Docker required) — NOT from this in-process smoke\n",
					colorLabel.Sprint("note:"), comp.Spec.Database.Engine, comp.Project())
			} else {
				fmt.Fprintf(out, "%s no shared database declared; the real-DB path is N/A (in-process smoke only, no Docker)\n", colorLabel.Sprint("note:"))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "composition.yaml", "composition config file")
	return cmd
}

// composeUpCmd provisions shared deps + registers the composition's aggregated
// routes, reusing the project-up sequencing.
func composeUpCmd() *cobra.Command {
	var file string
	var deployFlag bool
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Provision shared deps + register the composition's routes",
		Long: `Provision the composition's shared dependencies (its shared database) and
register the aggregated member routes through the edge — reusing the same
cluster-resolve + dependency-provision + route-register sequencing as
'de project up'. The composed host binary itself is built with
'de compose build' + 'go build'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			comp, err := loadComposition(file)
			if err != nil {
				return err
			}
			// Reuse the exact project-up sequencing over the Composition resource:
			// it satisfies Resource (routes) + DependencyDeclarer (shared DB), so the
			// shared up path provisions deps + registers routes uniformly.
			return runResourceUp(cmd, comp, file, "", deployFlag, false)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "composition.yaml", "composition config file")
	cmd.Flags().BoolVar(&deployFlag, "deploy", false, "also deploy the composed workload (opt-in)")
	return cmd
}

// composeChartCmd is the P6 stub: deploy rendering is a later phase.
func composeChartCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "chart",
		Short: "Render deploy artifacts for the composition (not yet implemented — P6)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("de compose chart is not yet implemented (WS-012 P6 — deploy rendering)")
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "composition.yaml", "composition config file")
	return cmd
}

// writeComposition marshals a composition back to its file, re-validating it
// edit-tolerantly (a `remove` may legitimately leave zero members).
func writeComposition(file string, comp *config.Composition) error {
	data, err := config.MarshalComposition(comp)
	if err != nil {
		return err
	}
	if _, err := config.ParseCompositionForEdit(data); err != nil {
		return fmt.Errorf("internal: rewritten composition is invalid: %w", err)
	}
	return os.WriteFile(file, data, 0o644)
}

// lastPathSegment returns the final element of a slash-separated import path.
func lastPathSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// goRuntimeVersion returns the Go toolchain version (e.g. "1.26.0") of the `de`
// binary, used as the generated go.mod's `go` line + the lock's toolchain pin.
func goRuntimeVersion() string {
	v := strings.TrimPrefix(runtime.Version(), "go")
	// runtime.Version() can be e.g. "go1.26.0" or "devel ..."; for a non-numeric
	// devel build fall back to a sane minimum so the generated go.mod is valid.
	if v == "" || (v[0] < '0' || v[0] > '9') {
		return "1.26.0"
	}
	return v
}

// renderCompositionYAML renders a starter `kind: Composition` document for
// `de compose init`. It is pure (no I/O). The shape matches config.Composition
// and the proposal §6.1 example; module entries are added by `de compose add`.
func renderCompositionYAML(name string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: %s\n", config.APIVersion)
	fmt.Fprintf(&b, "kind: %s\n", config.KindComposition)
	b.WriteString("metadata:\n")
	fmt.Fprintf(&b, "  name: %s\n", name)
	b.WriteString("spec:\n")
	b.WriteString("  runtime:\n")
	b.WriteString("    mode: single-binary\n")
	b.WriteString("    grpc: \":9090\"\n")
	b.WriteString("    http: \":8080\"\n")
	b.WriteString("  # database: { engine: postgres, dsnRef: DATABASE_URL, isolation: schema-preferred }\n")
	b.WriteString("  modules:\n")
	b.WriteString("    # add members with `de compose add <module>@<version>`, e.g.:\n")
	b.WriteString("    # - { name: orders, module: github.com/acme/orders/module@v0.4.1, configPrefix: orders, database: { schema: orders } }\n")
	b.WriteString("    []\n")
	return b.String()
}
