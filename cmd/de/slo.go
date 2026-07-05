package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/infobloxopen/devedge-sdk/slo"
)

// This file adds `de slo` (WS-025): the developer-facing verbs that turn a
// service's API contract into reliability artifacts. They orchestrate the SLO
// seam shipped in devedge-sdk's `slo` package — a pure-Go, type-safe library
// transform (OpenSLO IR + derivation + classifier + Prometheus/Grafana/Loki
// emitters), imported directly, no subprocess. The standalone `slogen` CLI in
// the SDK exposes the same transform; `de slo` is the in-CLI surface that adds
// project awareness (locating the OpenAPI and deriving the gRPC service FQN).
//
// The critical value `de slo generate` adds over calling the library directly is
// deriving the gRPC service fully-qualified name (proto package + service name)
// from the project's .proto files and passing it as the rpc.service label. The
// OpenAPI does NOT carry that FQN, so without it the derived SLIs would omit the
// rpc_service matcher and silently aggregate across services. When the FQN
// cannot be determined, generate fails loud rather than emitting un-scoped SLIs.

// sloCmd is `de slo`, the reliability (SLI/SLO) command group.
func sloCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "slo",
		Short: "Derive, lint, project, and calibrate SLI/SLOs (WS-025)",
		Long: `Turn a service's API contract into reliability artifacts (WS-025).

'de slo' orchestrates the devedge-sdk 'slo' seam:

  generate   Derive GOOD default OpenSLO SLOs from the enriched OpenAPI, scoped
             to the service's gRPC FQN (the rpc.service label).
  lint       Validate OpenSLO docs and run the fail-loud three-layer classifier
             (a Layer-0 signal declared as an SLI is rejected).
  render     Project a doc to Prometheus/Cortex rules, a Grafana dashboard, or
             Loki LogQL rules. --preset-dir consumes an internal emitter overlay.
  check      Query a Prometheus/Cortex API for each SLO's current SLI and
             error-budget consumption, to CALIBRATE the un-calibrated defaults.
  kpis       Print the Layer-0 API KPI reference (golden signals + RED + USE).

The scaffold already ships a GOOD default slo.yaml; regenerate after adding
custom methods, calibrate with 'de slo check', then render to deploy the
burn-rate rules. See the define-slo skill for authoring guidance.`,
	}
	cmd.AddCommand(sloGenerateCmd(), sloLintCmd(), sloRenderCmd(), sloCheckCmd(), sloKpisCmd())
	return cmd
}

// sloGenerateCmd is `de slo generate`: derive default OpenSLO SLOs from the
// service's enriched OpenAPI, scoped to its gRPC service FQN.
func sloGenerateCmd() *cobra.Command {
	var dir, openapiPath, service, out string
	var noGenerate bool
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Derive default OpenSLO SLOs from the service's enriched OpenAPI",
		Long: `Derive GOOD default OpenSLO SLOs (availability + latency, read/write groups,
a 28d window, burn-rate alerts, a mandatory error-budget policy — all marked
un-calibrated) from the service's enriched OpenAPI, and write slo.yaml.

Run with no flags from a service project: the OpenAPI is located at
openapi/<svc>.openapi.yaml (produced by 'de generate') and the gRPC service FQN
is derived from the project's .proto files.

Right after a scaffold the intermediate OpenAPI is not on disk yet, so this runs
'de generate' first to produce it (pass --no-generate to skip that and fail loud
if it is missing). Give an explicit --openapi to derive from a spec elsewhere.

The FQN (proto package + service name, e.g. orders.v1.OrderService) becomes the
rpc.service label on every derived SLI. The OpenAPI does not carry it, so without
it the SLIs would aggregate across services. If the FQN cannot be determined,
this fails loud — pass --service to set it explicitly.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := resolveProjectDir(dir)
			if err != nil {
				return err
			}

			// #62: right after a scaffold the enriched OpenAPI is not on disk yet
			// (the scaffold ran the derivation but did not leave the intermediate).
			// When no --openapi was given and none exists but the project can produce
			// one (it has a buf config), run 'de generate' first so the fresh-scaffold
			// path succeeds instead of erroring. --no-generate opts out.
			if openapiPath == "" && !noGenerate {
				if matches, _ := filepath.Glob(filepath.Join(d, "openapi", "*.openapi.yaml")); len(matches) == 0 && hasBufConfig(d) {
					fmt.Fprintln(cmd.OutOrStdout(), "no openapi/*.openapi.yaml yet — running 'de generate' first")
					if gerr := runGenerate(cmd, d); gerr != nil {
						return fmt.Errorf("de generate (needed to produce the OpenAPI for SLO derivation): %w", gerr)
					}
				}
			}

			// Locate the enriched OpenAPI (standard path openapi/<svc>.openapi.yaml).
			oapi, err := resolveOpenAPIPath(d, openapiPath)
			if err != nil {
				return err
			}

			// Derive (or accept) the gRPC service FQN — the rpc.service label.
			fqn, err := resolveServiceFQN(d, service)
			if err != nil {
				return err
			}

			data, err := os.ReadFile(oapi)
			if err != nil {
				return fmt.Errorf("read openapi %s: %w\n"+
					"run 'de generate' (or 'make generate') first, or pass --openapi", oapi, err)
			}
			doc, err := slo.DefaultsFromOpenAPI(data, fqn, slo.DefaultDeriveOptions())
			if err != nil {
				return err
			}
			b, err := doc.Marshal()
			if err != nil {
				return err
			}
			return writeSLODoc(cmd.OutOrStdout(), resolveOutPath(d, out), b, doc, fqn)
		},
	}
	dirFlag(cmd, &dir)
	cmd.Flags().StringVar(&openapiPath, "openapi", "", "enriched OpenAPI YAML (default: the single openapi/*.openapi.yaml)")
	cmd.Flags().StringVar(&service, "service", "", "rpc.service label (proto FQN, e.g. orders.v1.OrderService); derived from protos when unset")
	cmd.Flags().StringVar(&out, "out", "slo.yaml", "output path (- for stdout)")
	cmd.Flags().BoolVar(&noGenerate, "no-generate", false, "do not run 'de generate' when the OpenAPI is missing; fail loud instead")
	return cmd
}

// writeSLODoc writes the marshalled OpenSLO doc to outPath ("-"/"" for stdout)
// and reports what was written, including the rpc.service FQN it was scoped to.
func writeSLODoc(w io.Writer, outPath string, b []byte, doc *slo.Document, fqn string) error {
	if outPath == "" || outPath == "-" {
		_, err := w.Write(b)
		return err
	}
	if err := os.WriteFile(outPath, b, 0o644); err != nil {
		return err
	}
	scope := fqn
	if scope == "" {
		scope = "(method-only; no rpc.service)"
	}
	fmt.Fprintf(w, "%s %s (%d SLOs, rpc.service=%s)\n", colorSuccess.Sprint("wrote"), outPath, len(doc.SLOs), scope)
	return nil
}

// sloLintCmd is `de slo lint`: validate OpenSLO docs and run the classifier.
func sloLintCmd() *cobra.Command {
	var format string
	var failOnWarn, strict bool
	cmd := &cobra.Command{
		Use:   "lint [files...]",
		Short: "Validate OpenSLO docs and run the fail-loud three-layer classifier",
		Long: `Validate one or more OpenSLO docs and run the WS-025 three-layer classifier.

The classifier REJECTS a category error (e.g. a Layer-0 saturation signal such
as cpu/memory/queue-depth declared as an SLI, or an SLO with no error-budget
policy) with an error-severity finding, and WARNS on an un-calibrated default or
a placeholder error-budget policy. Any error-severity finding exits non-zero;
warnings alone exit 0 so a fresh scaffold's slo.yaml lints green.

Pass --fail-on-warn (alias --strict) to exit non-zero on ANY finding, including
warnings — a production CI gate that refuses to promote un-calibrated SLOs or a
placeholder error-budget policy.

With no file argument it lints slo.yaml in the current directory.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			files := args
			if len(files) == 0 {
				files = []string{"slo.yaml"}
			}
			var all slo.Findings
			for _, f := range files {
				data, err := os.ReadFile(f)
				if err != nil {
					return fmt.Errorf("read %s: %w", f, err)
				}
				doc, err := slo.Parse(data)
				if err != nil {
					return fmt.Errorf("%s: %w", f, err)
				}
				all = append(all, slo.Lint(doc)...)
			}
			w := cmd.OutOrStdout()
			if format == "json" {
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				if err := enc.Encode(all); err != nil {
					return err
				}
			} else {
				printSLOFindings(w, all)
			}
			if all.HasError() {
				return fmt.Errorf("lint failed: %d error-severity finding(s)", sloCountErrors(all))
			}
			// --fail-on-warn / --strict: a CI gate fails on ANY finding, so
			// un-calibrated or placeholder-policy SLOs cannot be promoted.
			if (failOnWarn || strict) && len(all) > 0 {
				return fmt.Errorf("lint failed (--fail-on-warn): %d finding(s) including warnings; calibrate the SLOs (de slo check) and replace placeholder policies to pass", len(all))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "output format: text|json")
	cmd.Flags().BoolVar(&failOnWarn, "fail-on-warn", false, "exit non-zero on ANY finding, including warnings (a strict production CI gate)")
	cmd.Flags().BoolVar(&strict, "strict", false, "alias for --fail-on-warn")
	return cmd
}

// sloRenderCmd is `de slo render`: project an OpenSLO doc to a backend.
func sloRenderCmd() *cobra.Command {
	var target, in, out, presetDir string
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Project an OpenSLO doc to prometheus|grafana|loki artifacts",
		Long: `Project an OpenSLO doc to a monitoring backend and write the artifacts:

  --target prometheus   a Cortex-ruler PrometheusRule (SLI recording rules +
                        multi-window multi-burn-rate alerts)
  --target grafana      an SLO overview dashboard
  --target loki         LogQL recording rules for log-derived SLIs

--preset-dir <dir> renders from <dir>/<target>.tmpl instead of the built-in
open-core emitter, when that template exists. This is the seam the INTERNAL
Grafana-Operator overlay uses: point it at the overlay's preset directory
(e.g. a checkout of Infoblox-CTO/devedge-sdk-internal/slo/preset) to emit the
operator-flavored artifacts, e.g.

    de slo render --target grafana --preset-dir ../devedge-sdk-internal/slo/preset

With no --out the artifacts are written to stdout.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if target == "" {
				return fmt.Errorf("--target is required (prometheus|grafana|loki)")
			}
			data, err := os.ReadFile(in)
			if err != nil {
				return fmt.Errorf("read %s: %w", in, err)
			}
			doc, err := slo.Parse(data)
			if err != nil {
				return err
			}
			rendered, err := slo.Render(target, doc, slo.RenderOptions{PresetDir: presetDir})
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if out == "" || out == "-" {
				for _, r := range rendered {
					w.Write(r.Content)
				}
				return nil
			}
			if err := os.MkdirAll(out, 0o755); err != nil {
				return err
			}
			for _, r := range rendered {
				p := filepath.Join(out, r.Filename)
				if err := os.WriteFile(p, r.Content, 0o644); err != nil {
					return err
				}
				fmt.Fprintf(w, "%s %s\n", colorSuccess.Sprint("wrote"), p)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "prometheus|grafana|loki")
	cmd.Flags().StringVar(&in, "in", "slo.yaml", "input OpenSLO YAML")
	cmd.Flags().StringVar(&out, "out", "", "output directory (- or empty for stdout)")
	cmd.Flags().StringVar(&presetDir, "preset-dir", "", "directory of <target>.tmpl emitter overrides (internal overlay seam)")
	return cmd
}

// sloCheckCmd is `de slo check`: query Prometheus/Cortex for each SLO's current
// SLI and error-budget consumption, to calibrate the un-calibrated defaults.
func sloCheckCmd() *cobra.Command {
	var dir, promURL, in string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Query Prometheus/Cortex for each SLO's current SLI vs its target",
		Long: `Query a Prometheus/Cortex-compatible HTTP API (/api/v1/query) for each SLO's
CURRENT SLI ratio over its window and its error-budget consumption, so you can
CALIBRATE the un-calibrated default targets against a measured baseline.

The Prometheus/Cortex base URL is taken from --prometheus-url, else $PROMETHEUS_URL
or $CORTEX_URL, else spec.monitoring.prometheusUrl in devedge.yaml. With none set,
this prints how to point it at Cortex and exits non-zero.

This reads only; it changes nothing. Use the reported "current" ratio as the new
objective (a hair below it) and drop the devedge.io/uncalibrated marker.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := resolveProjectDir(dir)
			if err != nil {
				return err
			}
			base := resolvePrometheusURL(d, promURL)
			if base == "" {
				return fmt.Errorf("no Prometheus/Cortex URL configured.\n" +
					"point 'de slo check' at your metrics backend one of these ways:\n" +
					"  de slo check --prometheus-url http://localhost:9009/prometheus\n" +
					"  PROMETHEUS_URL=http://cortex.monitoring:9009/prometheus de slo check\n" +
					"  # or set spec.monitoring.prometheusUrl in devedge.yaml\n" +
					"Cortex serves the query API under its Prometheus path prefix " +
					"(e.g. /prometheus or /api/prom); use that base URL.")
			}
			inPath := in
			if !filepath.IsAbs(inPath) {
				inPath = filepath.Join(d, inPath)
			}
			data, err := os.ReadFile(inPath)
			if err != nil {
				return fmt.Errorf("read %s: %w", inPath, err)
			}
			doc, err := slo.Parse(data)
			if err != nil {
				return err
			}
			return runSLOCheck(cmd.Context(), cmd.OutOrStdout(), base, doc)
		},
	}
	dirFlag(cmd, &dir)
	cmd.Flags().StringVar(&promURL, "prometheus-url", "", "Prometheus/Cortex base URL (its /api/v1/query is queried)")
	cmd.Flags().StringVar(&in, "in", "slo.yaml", "input OpenSLO YAML")
	return cmd
}

// sloKpisCmd is `de slo kpis`: print the Layer-0 API KPI reference.
func sloKpisCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "kpis",
		Short: "Print the Layer-0 API KPI reference (golden signals + RED + USE)",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(cmd.OutOrStdout(), slo.KPIReferenceText())
			return nil
		},
	}
}

// --- OpenAPI + FQN resolution -------------------------------------------------

// resolveOpenAPIPath returns the enriched OpenAPI file to derive from. An
// explicit path is honored (resolved against dir when relative); otherwise the
// single openapi/*.openapi.yaml under dir is used, and ambiguity or absence
// fails loud.
func resolveOpenAPIPath(dir, explicit string) (string, error) {
	if explicit != "" {
		if filepath.IsAbs(explicit) {
			return explicit, nil
		}
		return filepath.Join(dir, explicit), nil
	}
	matches, err := filepath.Glob(filepath.Join(dir, "openapi", "*.openapi.yaml"))
	if err != nil {
		return "", err
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("no openapi/*.openapi.yaml in %s\n"+
			"run 'de generate' (or 'make generate') to produce the enriched OpenAPI first, "+
			"or pass --openapi <path>", dir)
	default:
		sort.Strings(matches)
		return "", fmt.Errorf("multiple OpenAPI specs in %s/openapi (%s); pass --openapi to pick one",
			dir, strings.Join(baseNames(matches), ", "))
	}
}

// resolveServiceFQN returns the gRPC service FQN to scope the SLIs to. An
// explicit --service wins. Otherwise it derives the FQN from the project's
// .proto files: exactly one service is used; zero or many fail loud so no
// un-service-scoped SLIs are ever emitted silently.
func resolveServiceFQN(dir, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	fqns, err := deriveServiceFQNs(dir)
	if err != nil {
		return "", err
	}
	switch len(fqns) {
	case 1:
		return fqns[0], nil
	case 0:
		return "", fmt.Errorf("could not determine the gRPC service FQN from the protos in %s\n"+
			"the OpenAPI does not carry the proto service name, so without it the derived SLIs\n"+
			"would omit the rpc.service matcher and aggregate across services.\n"+
			"pass --service <proto-package>.<ServiceName> (e.g. orders.v1.OrderService)", dir)
	default:
		return "", fmt.Errorf("found %d gRPC services in %s's protos: %s\n"+
			"pass --service <fqn> to scope the SLOs to one (derived SLIs must carry rpc.service)",
			len(fqns), dir, strings.Join(fqns, ", "))
	}
}

// deriveServiceFQNs scans the project's first-party .proto files and returns the
// fully-qualified gRPC service names (<proto package>.<ServiceName>) they
// declare, sorted and de-duplicated. Vendored/well-known infrastructure protos
// (google.*, grpc.*, buf.*, and third_party/ trees) are excluded; they define
// options, not the service under measurement.
func deriveServiceFQNs(dir string) ([]string, error) {
	seen := map[string]bool{}
	var fqns []string
	for _, pd := range protoSourceDirs(dir) {
		files, err := findProtoFiles(pd)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			pkg, services, err := parseProtoServices(f)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", f, err)
			}
			if pkg == "" || isInfraProtoPackage(pkg) {
				continue
			}
			for _, s := range services {
				fqn := pkg + "." + s
				if !seen[fqn] {
					seen[fqn] = true
					fqns = append(fqns, fqn)
				}
			}
		}
	}
	sort.Strings(fqns)
	return fqns, nil
}

// protoSourceDirs returns the directories to scan for the service's protos: the
// buf config's declared input/module directories when present, else the
// conventional proto/ and api/ dirs, else the project root.
func protoSourceDirs(dir string) []string {
	var dirs []string
	add := func(rel string) {
		p := filepath.Join(dir, rel)
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			dirs = append(dirs, p)
		}
	}
	for _, rel := range bufProtoDirs(dir) {
		add(rel)
	}
	if len(dirs) == 0 {
		add("proto")
		add("api")
	}
	if len(dirs) == 0 {
		dirs = append(dirs, dir)
	}
	return dirs
}

// bufProtoDirs reads the buf config (buf.gen.yaml then buf.yaml) and returns the
// proto source directories it declares (inputs[].directory and modules[].path).
func bufProtoDirs(dir string) []string {
	var out []string
	seen := map[string]bool{}
	addUnique := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}

	if b, err := os.ReadFile(bufConfigPath(dir)); err == nil {
		var gen struct {
			Inputs []struct {
				Directory string `yaml:"directory"`
			} `yaml:"inputs"`
		}
		if yaml.Unmarshal(b, &gen) == nil {
			for _, in := range gen.Inputs {
				addUnique(in.Directory)
			}
		}
	}
	for _, name := range []string{"buf.yaml", "buf.yml"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			var mod struct {
				Modules []struct {
					Path string `yaml:"path"`
				} `yaml:"modules"`
			}
			if yaml.Unmarshal(b, &mod) == nil {
				for _, m := range mod.Modules {
					addUnique(m.Path)
				}
			}
			break
		}
	}
	return out
}

// findProtoFiles walks root for *.proto files, skipping any path with a
// third_party segment (vendored imports are codegen inputs, not the service's
// own API).
func findProtoFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "third_party" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".proto") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

var (
	protoPackageRE = regexp.MustCompile(`(?m)^\s*package\s+([A-Za-z_][A-Za-z0-9_.]*)\s*;`)
	protoServiceRE = regexp.MustCompile(`\bservice\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{`)
)

// parseProtoServices reads a .proto file and returns its package and the names
// of the services it declares. Comments and string literals are stripped first
// so the "service" keyword is not matched inside a comment or string.
func parseProtoServices(path string) (pkg string, services []string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	src := stripProtoNoise(string(raw))
	if m := protoPackageRE.FindStringSubmatch(src); m != nil {
		pkg = m[1]
	}
	for _, m := range protoServiceRE.FindAllStringSubmatch(src, -1) {
		services = append(services, m[1])
	}
	return pkg, services, nil
}

// stripProtoNoise removes //-line comments, /* */-block comments, and string
// literals from proto source, replacing them with spaces so token positions and
// line structure for the package/service regexes are preserved.
func stripProtoNoise(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	i, n := 0, len(src)
	for i < n {
		c := src[i]
		switch {
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			i += 2
			for i < n && !(i+1 < n && src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
			b.WriteByte(' ')
		case c == '"' || c == '\'':
			quote := c
			i++
			for i < n && src[i] != quote {
				if src[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				i++
			}
			i++ // consume the closing quote
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// isInfraProtoPackage reports whether a proto package is a well-known
// infrastructure/vendored package rather than the service's own API.
func isInfraProtoPackage(pkg string) bool {
	for _, prefix := range []string{"google.", "grpc.", "buf.", "gogoproto.", "validate.", "openapi."} {
		if strings.HasPrefix(pkg, prefix) {
			return true
		}
	}
	return pkg == "google" || pkg == "grpc"
}

// resolveOutPath resolves an --out value against dir: "-" and "" pass through
// (stdout), absolute paths are used as-is, relative paths join dir.
func resolveOutPath(dir, out string) string {
	if out == "" || out == "-" || filepath.IsAbs(out) {
		return out
	}
	return filepath.Join(dir, out)
}

// baseNames returns the filepath.Base of each path.
func baseNames(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}

// --- lint output --------------------------------------------------------------

func printSLOFindings(w io.Writer, fs slo.Findings) {
	if len(fs) == 0 {
		fmt.Fprintln(w, colorSuccess.Sprint("OK: no findings."))
		return
	}
	for _, f := range fs {
		sev := string(f.Severity)
		if f.Severity == slo.SeverityError {
			sev = colorError.Sprint(sev)
		} else {
			sev = colorWarning.Sprint(sev)
		}
		fmt.Fprintf(w, "%-14s [%s] %s: %s\n", sev, f.Kind, f.Object, f.Message)
	}
}

func sloCountErrors(fs slo.Findings) int {
	n := 0
	for _, f := range fs {
		if f.Severity == slo.SeverityError {
			n++
		}
	}
	return n
}

// --- check: query Prometheus/Cortex ------------------------------------------

// resolvePrometheusURL resolves the metrics backend base URL: the flag, then
// $PROMETHEUS_URL / $CORTEX_URL, then spec.monitoring.prometheusUrl in
// devedge.yaml. Returns "" when none is set.
func resolvePrometheusURL(dir, flag string) string {
	if flag != "" {
		return flag
	}
	for _, env := range []string{"PROMETHEUS_URL", "CORTEX_URL"} {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	return prometheusURLFromConfig(dir)
}

// prometheusURLFromConfig reads spec.monitoring.prometheusUrl from devedge.yaml.
func prometheusURLFromConfig(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "devedge.yaml"))
	if err != nil {
		return ""
	}
	var cfg struct {
		Spec struct {
			Monitoring struct {
				PrometheusURL string `yaml:"prometheusUrl"`
			} `yaml:"monitoring"`
		} `yaml:"spec"`
	}
	if yaml.Unmarshal(b, &cfg) != nil {
		return ""
	}
	return cfg.Spec.Monitoring.PrometheusURL
}

// runSLOCheck queries base for each SLO's current SLI ratio over its window and
// reports it against the target, with error-budget consumption.
func runSLOCheck(ctx context.Context, w io.Writer, base string, doc *slo.Document) error {
	if len(doc.SLOs) == 0 {
		fmt.Fprintln(w, colorWarning.Sprint("no SLOs in the document."))
		return nil
	}
	client := &http.Client{Timeout: 30 * time.Second}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, colorHeader.Sprint("SLO\tWINDOW\tTARGET\tCURRENT\tBUDGET USED\tSTATUS"))

	var breached bool
	for i := range doc.SLOs {
		s := &doc.SLOs[i]
		window := sloWindow(s)
		target := sloTarget(s)
		sli := findSLI(doc, s)

		row := func(cur, used, status string) {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Metadata.Name, window, pct(target), cur, used, status)
		}
		if sli == nil || sli.Spec.RatioMetric == nil {
			row("-", "-", colorWarning.Sprint("no ratio SLI"))
			continue
		}
		query, err := sliRatioQuery(sli, window)
		if err != nil {
			row("-", "-", colorWarning.Sprint("unqueryable: "+err.Error()))
			continue
		}
		val, ok, err := promInstantQuery(ctx, client, base, query)
		if err != nil {
			row("-", "-", colorError.Sprint("query error: "+err.Error()))
			continue
		}
		if !ok {
			row(colorWarning.Sprint("no data"), "-", colorWarning.Sprint("no series yet"))
			continue
		}
		used := "-"
		if budget := 1 - target; budget > 0 {
			used = pct((1 - val) / budget)
		}
		status := colorSuccess.Sprint("OK")
		if val < target {
			status = colorError.Sprint("BELOW TARGET")
			breached = true
		}
		row(pct(val), used, status)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(w, "\nCalibrate: set each objective from its measured CURRENT ratio (a hair below),")
	fmt.Fprintln(w, "then drop the devedge.io/uncalibrated marker and re-run 'de slo lint'.")
	if breached {
		fmt.Fprintln(w, colorWarning.Sprint("\nsome SLIs are below their (un-calibrated) target — expected before calibration."))
	}
	return nil
}

// findSLI resolves the SLI an SLO indicates (inline Indicator or IndicatorRef).
func findSLI(doc *slo.Document, s *slo.SLO) *slo.SLI {
	if s.Spec.Indicator != nil {
		return s.Spec.Indicator
	}
	for i := range doc.SLIs {
		if doc.SLIs[i].Metadata.Name == s.Spec.IndicatorRef {
			return &doc.SLIs[i]
		}
	}
	return nil
}

func sloWindow(s *slo.SLO) string {
	if len(s.Spec.TimeWindow) > 0 && s.Spec.TimeWindow[0].Duration != "" {
		return s.Spec.TimeWindow[0].Duration
	}
	return "28d"
}

func sloTarget(s *slo.SLO) float64 {
	if len(s.Spec.Objectives) > 0 {
		return s.Spec.Objectives[0].Target
	}
	return 0
}

// pct formats a ratio in [0,1] as a percentage with three decimals.
func pct(v float64) string {
	return strconv.FormatFloat(v*100, 'f', 3, 64) + "%"
}

// sliRatioQuery builds the PromQL good/total ratio for an SLI over window. It
// mirrors the devedge-sdk Prometheus emitter (whose builder is unexported)
// using the exported OTelRatioSource fields and MetricNaming, so `de slo check`
// queries the same series the rendered recording rules record.
func sliRatioQuery(sli *slo.SLI, window string) (string, error) {
	rm := sli.Spec.RatioMetric
	naming := namingFor(rm.Good.Spec)
	good, err := promSideExpr(naming, rm.Good, window)
	if err != nil {
		return "", fmt.Errorf("good side: %w", err)
	}
	total, err := promSideExpr(naming, rm.Total, window)
	if err != nil {
		return "", fmt.Errorf("total side: %w", err)
	}
	return good + " / " + total, nil
}

// namingFor selects the metric naming for a ratio source by transport (and the
// legacy-semconv signal), matching the SDK's default emission.
func namingFor(src slo.OTelRatioSource) slo.MetricNaming {
	switch {
	case src.Transport == slo.TransportHTTP:
		return slo.HTTPGatewayNaming()
	case src.Signal == "rpc.server.duration":
		return slo.LegacyGRPCNaming()
	default:
		return slo.DefaultGRPCNaming()
	}
}

// promSideExpr builds sum(rate(<series><selector>[window])) for one side of a
// ratio. A raw Query (Layer-2 journey SLI) wins, with the $window token
// substituted.
func promSideExpr(naming slo.MetricNaming, ms slo.MetricSource, window string) (string, error) {
	if q := strings.TrimSpace(ms.Query); q != "" {
		return "(" + strings.ReplaceAll(q, "$window", window) + ")", nil
	}
	src := ms.Spec
	if ms.Type != slo.MetricSourceTypeOTel || src.Signal == "" {
		return "", fmt.Errorf("metric source has neither a raw query nor an OTel signal")
	}
	base := strings.ReplaceAll(src.Signal, ".", "_")
	if naming.UnitSuffix != "" {
		base += "_" + naming.UnitSuffix
	}
	var series, le string
	if src.SLIType == slo.SLITypeLatency && src.LatencyThresholdSeconds > 0 {
		series = base + "_bucket"
		le = `le="` + strconv.FormatFloat(src.LatencyThresholdSeconds, 'g', -1, 64) + `"`
	} else {
		series = base + "_count"
	}
	sel := promSelector(
		eqMatcher(naming.ServiceLabel, src.Service),
		reMatcher(naming.MethodLabel, src.Methods),
		notReMatcher(naming.StatusLabel, src.ExcludeStatuses),
		le,
	)
	return fmt.Sprintf("sum(rate(%s%s[%s]))", series, sel, window), nil
}

func promSelector(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return "{" + strings.Join(kept, ", ") + "}"
}

func eqMatcher(label, value string) string {
	if label == "" || value == "" {
		return ""
	}
	return label + `="` + value + `"`
}

func reMatcher(label string, values []string) string {
	if label == "" || len(values) == 0 {
		return ""
	}
	return label + `=~"` + strings.Join(values, "|") + `"`
}

func notReMatcher(label string, values []string) string {
	if label == "" || len(values) == 0 {
		return ""
	}
	return label + `!~"` + strings.Join(values, "|") + `"`
}

// promInstantQuery runs an instant query against base's /api/v1/query and
// returns the first sample value. ok is false when the query returned no series.
func promInstantQuery(ctx context.Context, client *http.Client, base, query string) (val float64, ok bool, err error) {
	endpoint := strings.TrimRight(base, "/") + "/api/v1/query?query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, false, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, false, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var pr struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			ResultType string          `json:"resultType"`
			Result     json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &pr); err != nil {
		return 0, false, err
	}
	if pr.Status != "success" {
		return 0, false, fmt.Errorf("query failed: %s", pr.Error)
	}
	switch pr.Data.ResultType {
	case "scalar":
		var sample [2]json.RawMessage
		if err := json.Unmarshal(pr.Data.Result, &sample); err != nil {
			return 0, false, nil
		}
		return parsePromSample(sample[1])
	default: // vector (and matrix, defensively): take the first sample
		var vec []struct {
			Value [2]json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(pr.Data.Result, &vec); err != nil {
			return 0, false, err
		}
		if len(vec) == 0 {
			return 0, false, nil
		}
		return parsePromSample(vec[0].Value[1])
	}
}

// parsePromSample parses a Prometheus sample value (a quoted string like "0.99")
// into a float. A NaN/absent value is treated as "no data" (ok=false).
func parsePromSample(raw json.RawMessage) (float64, bool, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, false, err
	}
	if s == "" || s == "NaN" {
		return 0, false, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false, err
	}
	return v, true, nil
}
