package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/infobloxopen/devedge/internal/compose"
	"github.com/infobloxopen/devedge/internal/helm"
	"github.com/infobloxopen/devedge/pkg/config"
)

// envVarPrefix turns a composition name into the environment-variable-safe DSN
// prefix the generated host reads (upper-cased, non-alphanumerics to "_"),
// matching the compose generator's own default. Used only to describe the DSN env
// var in `de compose build` output.
func envVarPrefix(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 32
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			return r
		default:
			return '_'
		}
	}, name)
}

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
exposing the NewModule(db)/Models() seam a devedge-sdk scaffold generates).
'de compose build' generates a cmd/<name>/main.go that opens ONE shared database,
builds each member over it via NewModule(db), and runs them via servicekit.Run —
static composition, no Go plugins. The same module runs standalone or composed by
changing the host, not the module.`,
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
	var file, name, configPrefix, schema, localPath string
	cmd := &cobra.Command{
		Use:   "add MODULE[@VERSION]",
		Short: "Add a member module to the composition",
		Long: `Add a member module to the composition.

Published member (pin a version):
  de compose add github.com/acme/orders/module@v0.4.1

Local, not-yet-published member (a two-repo dev loop):
  de compose add github.com/acme/orders/module --path ../orders

--path points at the member's Go module ROOT (the dir with its go.mod). At
'de compose build' the generated go.mod gets a 'replace' to that checkout, so the
composition builds before the member is published, and the composed binary's
devedge-sdk pin is derived from the member's own go.mod.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			comp, err := loadCompositionForEdit(file)
			if err != nil {
				return err
			}
			ref := args[0]
			importPath, version, err := compose.ParseModuleRef(ref)
			if err != nil {
				return err
			}
			if localPath != "" && version != "" {
				return fmt.Errorf("pass either MODULE@VERSION or --path, not both (a local checkout is resolved by a replace, not a version)")
			}
			if name == "" {
				name = lastPathSegment(importPath)
				// devedge-sdk scaffolds expose their importable unit at <repo>/module
				// (package "module"), so the last path segment is "module" for EVERY
				// member and collides on the second add. Fall back to the module-root
				// segment (the repo name, e.g. "warehoused"). (DX run 27, finding 118)
				if name == "module" {
					if root := lastPathSegment(strings.TrimSuffix(importPath, "/module")); root != "" {
						name = root
					}
				}
			}
			for _, m := range comp.Spec.Modules {
				if m.Name == name {
					return fmt.Errorf("module name %q is already a member", name)
				}
			}
			// Store the import path (no @version) as the module ref; --path/version
			// are recorded on their own fields so the file stays a valid go-importable
			// path plus an explicit local/published resolution.
			entry := config.ModuleEntry{Name: name, Module: ref, ConfigPrefix: configPrefix, Path: localPath}
			if schema != "" {
				entry.Database = &config.ModuleDatabase{Schema: schema}
			}
			comp.Spec.Modules = append(comp.Spec.Modules, entry)
			if err := writeComposition(file, comp); err != nil {
				return err
			}
			how := ref
			if localPath != "" {
				how = importPath + " (--path " + localPath + ")"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s %s\n", colorSuccess.Sprint("added"), colorHost.Sprint(name), colorLabel.Sprint("->"), how)
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "composition.yaml", "composition config file")
	cmd.Flags().StringVar(&name, "name", "", "member name (defaults to the import path's last segment)")
	cmd.Flags().StringVar(&configPrefix, "config-prefix", "", "config namespace prefix (defaults to name)")
	cmd.Flags().StringVar(&schema, "schema", "", "DB schema for this module (defaults to name)")
	cmd.Flags().StringVar(&localPath, "path", "", "local checkout dir of an unpublished member (writes a go.mod replace at build)")
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
			base := outDir
			if base == "" {
				base = filepath.Dir(file)
			}
			gen, err := compose.Generate(comp, goRuntimeVersion(), mp, compose.GenerateOptions{
				CompositionDir: filepath.Dir(file),
				OutBaseDir:     base,
			})
			if err != nil {
				return err
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
			fmt.Fprintf(out, "%s %s (devedge-sdk %s)\n", colorSuccess.Sprint("generated composed host"), colorHost.Sprint(comp.Project()), gen.SDK)
			for fn := range files {
				fmt.Fprintf(out, "  %s %s\n", colorLabel.Sprint("wrote"), filepath.Join(dir, fn))
			}
			fmt.Fprintf(out, "next: %s\n", colorLabel.Sprintf("cd %s && go mod tidy && go build", dir))
			// Report the generated database posture (#64). A declared postgres engine
			// now generates a scheme-branched dialector + the postgres driver + PER-MODULE
			// schema-scoped migration, so schema-preferred isolation holds. Any other
			// non-sqlite engine is not yet generated and falls back to the SQLite dev path,
			// which we still surface loudly (the residual of DX run 27, finding 119 / #63).
			if db := comp.Spec.Database; db != nil && db.Engine != "" {
				dsnRef := db.DSNRef
				if dsnRef == "" {
					dsnRef = envVarPrefix(comp.Project()) + "_DSN"
				}
				switch {
				case strings.EqualFold(db.Engine, "postgres"):
					fmt.Fprintf(out, "%s database.engine=postgres: a postgres:// DSN in %s uses the Postgres driver, and each module migrates into its own schema (schema-preferred isolation). An empty DSN falls back to in-memory SQLite for dev.\n",
						colorLabel.Sprint("database:"), colorHost.Sprint(dsnRef))
				case !strings.EqualFold(db.Engine, "sqlite"):
					fmt.Fprintf(out, "%s composition declares database.engine=%q, which `de compose build` does not yet generate — the host falls back to the SQLite dev path, so the declared engine/isolation is inert. Use engine: postgres for a real multi-schema host.\n",
						colorError.Sprint("WARNING:"), db.Engine)
				}
			}
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
				fmt.Fprintf(out, "%s %s (not linked into de — this smoke did NOT build or boot it)\n",
					colorLabel.Sprint("not verified:"), u)
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
			}

			// Fail loud: an external (separately-repo'd) member is NOT linked into
			// `de`, so this in-process smoke cannot compile or boot it — reporting a
			// pass here would green-wash a composition whose real binary may not
			// build (Run 18 finding 081). The only real gate for external members is
			// building + booting the generated host, so send the developer there.
			if len(res.Unresolved) > 0 {
				return fmt.Errorf("%d member(s) could not be verified in-process; build + boot the composed host to verify them: `de compose build && (cd cmd/%s && go mod tidy && go build && go vet ./...)`", len(res.Unresolved), comp.Project())
			}
			if len(res.Resolved) == 0 {
				return fmt.Errorf("composition %q has no verifiable members", comp.Project())
			}
			fmt.Fprintf(out, "%s composition %q verified in-process\n", colorSuccess.Sprint("ok:"), comp.Project())
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

// composeChartCmd renders deploy artifacts for the composition (WS-012 P6).
//
// It maps the Composition resource onto one or more Helm service-chart workloads
// via the three topologies defined in internal/compose.ComposeChart:
//
//   - single-binary (default): ONE Deployment + one Ingress per member route.
//   - multi-daemon:            one Deployment per member + member-owned routes.
//   - hybrid:                  composed binary unless a member declares
//     failurePolicy: dedicated-required (→ its own Deployment).
//
// The shared DB (spec.database) is provisioned ONCE, expressed as a DependencyClaim
// in each workload's chart. Module-namespace isolation (schema wiring) is reflected
// in the values as compositionSchemas; the servicekit host performs the actual
// runtime namespacing. Helm is used for rendering (same ChartService as
// `de project chart`) — no new chart engine.
func composeChartCmd() *cobra.Command {
	var file, out, modeFlag string
	cmd := &cobra.Command{
		Use:   "chart",
		Short: "Render Helm deploy artifacts for the composition (WS-012 P6)",
		Long: `Render Helm deploy artifacts for a 'kind: Composition' from a single
descriptor set, supporting three deployment topologies:

  single-binary  — ONE Deployment running the composed binary + one Ingress per
                   member route. (default; also set via spec.runtime.mode)
  multi-daemon   — one Deployment per member module with member-owned routes.
  hybrid         — composed binary for most members; members with
                   failurePolicy: dedicated-required get their own Deployment.

The shared database (spec.database) is provisioned ONCE and expressed as a
DependencyClaim in each workload's chart. Module-namespace isolation is reflected
in the values (compositionSchemas) — the servicekit runtime performs the actual
schema namespacing. Rendering reuses the 'service' embedded chart (same path as
'de project chart').`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireTools("helm"); err != nil {
				return err
			}
			comp, err := loadComposition(file)
			if err != nil {
				return err
			}

			mode := compose.TopologyMode(modeFlag)
			plan, err := compose.ComposeChart(comp, mode)
			if err != nil {
				return fmt.Errorf("compose chart: %w", err)
			}

			outBase := out
			if outBase == "" {
				outBase = "chart-" + comp.Project()
			}
			if err := os.MkdirAll(outBase, 0o755); err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "%s composition %s in %s mode\n",
				colorSuccess.Sprint("rendering"), colorHost.Sprint(comp.Project()), colorLabel.Sprint(string(plan.Mode)))

			h := helm.New("")
			for i := range plan.Workloads {
				wl := plan.Workloads[i]
				vals := wl.ToHelmValues()

				// Write the chart template files into a per-workload subdirectory.
				wlDir := filepath.Join(outBase, wl.Name)
				if err := helm.WriteChart(helm.ChartService, wlDir); err != nil {
					return fmt.Errorf("write chart for workload %s: %w", wl.Name, err)
				}
				// Write the composition-specific values.yaml alongside the chart.
				valData, err := yaml.Marshal(vals)
				if err != nil {
					return fmt.Errorf("marshal values for %s: %w", wl.Name, err)
				}
				if err := os.WriteFile(filepath.Join(wlDir, "values.yaml"), valData, 0o644); err != nil {
					return fmt.Errorf("write values for %s: %w", wl.Name, err)
				}
				// Lint: the same gate `de project chart` runs.
				if err := h.Lint(cmd.Context(), wlDir); err != nil {
					return fmt.Errorf("generated chart for %s failed helm lint: %w", wl.Name, err)
				}
				fmt.Fprintf(w, "  %s %s → %s (modules: %s)\n",
					colorSuccess.Sprint("wrote"), colorHost.Sprint(wl.Name), wlDir,
					colorLabel.Sprint(strings.Join(wl.Modules, ",")))
			}

			fmt.Fprintf(w, "%s helm lint passed; workloads: %d\n",
				colorSuccess.Sprint("ok"), len(plan.Workloads))
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "composition.yaml", "composition config file")
	cmd.Flags().StringVarP(&out, "out", "o", "", "output base directory (default: chart-<name>)")
	cmd.Flags().StringVar(&modeFlag, "mode", "", "topology override: single-binary | multi-daemon | hybrid (default: from spec.runtime.mode)")
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
