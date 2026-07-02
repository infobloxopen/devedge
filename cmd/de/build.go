package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/infobloxopen/devedge/internal/makefrag"
	"github.com/infobloxopen/devedge/internal/toolchain"
)

// This file makes `de` the hermetic build authority (WS-023). The build verbs
// —`de generate`, `de build`, `de test`, `de lint`, `de image`— operate on a
// service project directory (default: cwd, override with -C/--dir) and run the
// PINNED toolchain (see internal/toolchain) via `go run <tool>@<version>`, never
// off the host PATH. The build logic lives HERE, not in a Makefile; `de sync`
// then writes a thin Makefile shim that only delegates to these verbs.

// resolveProjectDir returns the service project directory: the -C/--dir flag if
// set, otherwise the current working directory. It mirrors how `de api publish`
// resolves its service dir.
func resolveProjectDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return wd, nil
}

// dirFlag registers the shared -C/--dir project-directory flag on cmd.
func dirFlag(cmd *cobra.Command, dir *string) {
	cmd.Flags().StringVarP(dir, "dir", "C", "", "service project directory (default: current directory)")
}

// runTool runs name+args in dir, streaming to cmd's out/err writers. extraEnv is
// appended to the current environment (later entries win). Empty env → inherit.
func runTool(cmd *cobra.Command, dir string, extraEnv []string, name string, args ...string) error {
	c := exec.CommandContext(cmd.Context(), name, args...)
	c.Dir = dir
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	if len(extraEnv) > 0 {
		c.Env = append(os.Environ(), extraEnv...)
	}
	return c.Run()
}

// generateCmd is `de generate`: (re)generate code from protos with the pinned
// buf + codegen plugins, then tidy the module. This is the build authority for
// codegen — `de api publish` calls the same logic instead of `make generate`.
func generateCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate code from protos (pinned buf + plugins), then go mod tidy",
		Long: `Generate code from the service's protos using the PINNED buf CLI and codegen
plugins (see 'de version' toolchain pins), then run 'go mod tidy'.

Hermetic: buf and the protoc-gen-* plugins are pinned by the 'de' binary and run
via 'go run'/'go install' — not resolved off the host PATH — so the same 'de'
version always generates with the same tools. Requires a buf config
(buf.gen.yaml) in the project directory.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := resolveProjectDir(dir)
			if err != nil {
				return err
			}
			return runGenerate(cmd, d)
		},
	}
	dirFlag(cmd, &dir)
	return cmd
}

// runGenerate is the shared codegen routine used by `de generate` and
// `de api publish`. It reproduces the SDK scaffold's `make generate` flow
// hermetically, backend-aware from the service's buf.gen.yaml:
//
//  1. install the plugins buf.gen.yaml drives — public plugins pinned by `de`,
//     SDK plugins (protoc-gen-{devedge-authz,svc,ent|storage}) pinned to the
//     SERVICE's resolved devedge-sdk version — into a version-keyed cache bindir
//     put first on PATH;
//  2. lock proto deps if the tree never has (guarded buf dep update);
//  3. run the pinned buf;
//  4. convert the emitted OpenAPI v2 surface to v3 with the SDK's openapiv2to3;
//  5. run the entc second step (go get seed + go generate ./gen/ent) when the
//     backend is ent;
//  6. go mod tidy.
func runGenerate(cmd *cobra.Command, dir string) error {
	if !hasBufConfig(dir) {
		return fmt.Errorf("no buf config (buf.gen.yaml) found in %s — is this a service project?", dir)
	}
	out := cmd.OutOrStdout()

	// 1. Resolve which codegen plugins buf.gen.yaml drives and pin each one:
	//    public plugins by `de`, SDK plugins by the service's own SDK version.
	plan, err := planGenerate(cmd, dir)
	if err != nil {
		return err
	}
	binDir, err := ensurePlugins(cmd, plan.plugins)
	if err != nil {
		return err
	}
	env := []string{"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH")}
	bufRef := toolchain.Ref(toolchain.BufModule, toolchain.BufVersion)

	// 2. Lock the proto deps (google/api/*) if the tree never has — self-heals a
	//    tree scaffolded with --no-generate. A committed buf.lock is left as-is,
	//    so reproducibility is preserved.
	if !hasBufLock(dir) {
		fmt.Fprintf(out, "generate: buf dep update (no buf.lock yet)\n")
		if err := runTool(cmd, dir, env, "go", "run", bufRef, "dep", "update"); err != nil {
			return fmt.Errorf("buf dep update: %w", err)
		}
	}

	// 3. Run the pinned buf.
	fmt.Fprintf(out, "generate: buf %s generate (pinned plugins from %s)\n", toolchain.BufVersion, binDir)
	if err := runTool(cmd, dir, env, "go", "run", bufRef, "generate"); err != nil {
		return fmt.Errorf("buf generate: %w", err)
	}

	// 4. Convert the emitted OpenAPI v2 surface to v3, in place under openapi/.
	if plan.openapi {
		if err := runOpenAPIV2To3(cmd, dir, binDir); err != nil {
			return err
		}
	}

	// 5. ent is a TWO-STEP generate: protoc-gen-ent (run by buf) emitted the ent
	//    SCHEMAS + the repository adapter; entc turns the schemas into the ent
	//    CLIENT. `go get` seeds the entc tool + the SDK packages the schema/adapter
	//    import into go.sum WITHOUT building the module (a bare tidy here trips over
	//    the not-yet-generated gen/ent/* imports and prints an alarming remote-repo
	//    error), then `go generate` runs entc, then the clean tidy runs last.
	if plan.ent {
		fmt.Fprintf(out, "generate: seeding ent codegen toolchain (go.sum)\n")
		if err := runTool(cmd, dir, nil, "go", "get",
			"entgo.io/ent/cmd/ent",
			toolchain.SDKModule+"/persistence/entrepo",
			toolchain.SDKModule+"/middleware",
			toolchain.SDKModule+"/persistence/resourcename",
		); err != nil {
			return fmt.Errorf("seed ent codegen toolchain (go get): %w", err)
		}
		fmt.Fprintf(out, "generate: go generate ./gen/ent (entc client)\n")
		if err := runTool(cmd, dir, nil, "go", "generate", "./gen/ent"); err != nil {
			return fmt.Errorf("go generate ./gen/ent (entc): %w", err)
		}
	}

	// 6. Tidy — generated imports may have changed the module graph.
	fmt.Fprintf(out, "generate: go mod tidy\n")
	if err := runTool(cmd, dir, nil, "go", "mod", "tidy"); err != nil {
		return fmt.Errorf("go mod tidy: %w", err)
	}
	fmt.Fprintf(out, "%s\n", colorSuccess.Sprint("generate ok"))
	return nil
}

// genPlan is the codegen plan resolved from a service's buf.gen.yaml.
type genPlan struct {
	plugins []toolchain.Plugin // every tool to install (public + SDK plugins + openapiv2to3), deduped
	openapi bool               // buf emits an OpenAPI surface -> run openapiv2to3
	ent     bool               // backend is ent -> run the entc second step
}

// planGenerate reads dir's buf.gen.yaml, classifies each `local:` plugin, and
// resolves the full install plan. Public plugins are pinned by `de`; SDK plugins
// (and the openapiv2to3 tool) are pinned to the service's resolved devedge-sdk
// version — that pin is what keeps generated code matching the SDK the service
// compiles against. An unrecognized local plugin fails loudly: `de` cannot
// install it hermetically.
func planGenerate(cmd *cobra.Command, dir string) (genPlan, error) {
	locals, err := bufLocalPlugins(dir)
	if err != nil {
		return genPlan{}, err
	}
	var plan genPlan
	// First learn whether we need the service's SDK version at all: any SDK
	// plugin, or the openapiv2 surface (the SDK's openapiv2to3 converts it).
	needSDK := false
	for _, bin := range locals {
		switch {
		case toolchain.IsSDKPlugin(bin):
			needSDK = true
			if bin == "protoc-gen-ent" {
				plan.ent = true
			}
		case bin == "protoc-gen-openapiv2":
			plan.openapi = true
			needSDK = true
		}
	}
	var sdkVersion string
	if needSDK {
		if sdkVersion, err = resolveServiceSDKVersion(cmd, dir); err != nil {
			return genPlan{}, err
		}
	}

	seen := map[string]bool{}
	add := func(p toolchain.Plugin) {
		if !seen[p.Bin] {
			seen[p.Bin] = true
			plan.plugins = append(plan.plugins, p)
		}
	}
	for _, bin := range locals {
		if toolchain.IsSDKPlugin(bin) {
			add(toolchain.Plugin{Bin: bin, Module: toolchain.SDKCmd(bin), Version: sdkVersion})
			continue
		}
		p, ok := toolchain.PublicPlugin(bin)
		if !ok {
			return genPlan{}, fmt.Errorf("buf.gen.yaml references local plugin %q that `de generate` cannot install hermetically "+
				"(not a known public plugin and not a devedge-sdk plugin)", bin)
		}
		add(p)
	}
	// openapiv2 emits swagger.json (OpenAPI v2); the SDK's openapiv2to3 rewrites
	// it to v3. It is a post-step tool, not a buf plugin, but is installed into
	// the same version-keyed bindir.
	if plan.openapi {
		add(toolchain.Plugin{Bin: toolchain.OpenAPIV2To3Bin, Module: toolchain.SDKCmd(toolchain.OpenAPIV2To3Bin), Version: sdkVersion})
	}
	return plan, nil
}

// bufLocalPlugins returns the `local:` plugin bin names buf.gen.yaml drives, in
// file order (reading whichever of buf.gen.yaml / buf.gen.yml exists).
func bufLocalPlugins(dir string) ([]string, error) {
	path := bufConfigPath(dir)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg struct {
		Plugins []struct {
			Local yaml.Node `yaml:"local"`
		} `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var out []string
	for _, p := range cfg.Plugins {
		// `local:` is a scalar bin name in the scaffolds. buf also allows a
		// sequence (a custom command); `de` does not manage those (buf resolves
		// them itself off PATH), so non-scalar entries are skipped.
		if p.Local.Kind == yaml.ScalarNode && p.Local.Value != "" {
			out = append(out, p.Local.Value)
		}
	}
	return out, nil
}

// resolveServiceSDKVersion resolves the devedge-sdk version the service in dir
// depends on, from its go.mod (via `go list -m`). This mirrors the scaffold
// Makefile's `SDK_VERSION := go list -m devedge-sdk`: go.mod is the single source
// of truth the SDK-versioned plugins pin to.
func resolveServiceSDKVersion(cmd *cobra.Command, dir string) (string, error) {
	c := exec.CommandContext(cmd.Context(), "go", "list", "-m", "-f", "{{.Version}}", toolchain.SDKModule)
	c.Dir = dir
	var stdout, stderr strings.Builder
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("resolve %s version from go.mod in %s: %w\n%s\n"+
			"(is this a devedge-sdk service? try `go mod download` first)",
			toolchain.SDKModule, dir, err, strings.TrimSpace(stderr.String()))
	}
	v := strings.TrimSpace(stdout.String())
	if v == "" {
		return "", fmt.Errorf("%s is not required by the module in %s — its go.mod must depend on devedge-sdk to generate", toolchain.SDKModule, dir)
	}
	return v, nil
}

// runOpenAPIV2To3 converts the OpenAPI v2 (swagger.json) files protoc-gen-openapiv2
// emitted under openapi/ into OpenAPI v3, in place, with the SDK's pinned
// openapiv2to3 (in binDir). Mirrors the scaffold Makefile step
// `openapiv2to3 openapi/<bin>.swagger.json openapi`.
func runOpenAPIV2To3(cmd *cobra.Command, dir, binDir string) error {
	swaggers, err := filepath.Glob(filepath.Join(dir, "openapi", "*.swagger.json"))
	if err != nil {
		return fmt.Errorf("scan openapi/: %w", err)
	}
	if len(swaggers) == 0 {
		return fmt.Errorf("openapiv2->v3: no openapi/*.swagger.json found after buf generate (expected an OpenAPI v2 surface)")
	}
	tool := filepath.Join(binDir, toolchain.OpenAPIV2To3Bin)
	out := cmd.OutOrStdout()
	for _, sw := range swaggers {
		rel, err := filepath.Rel(dir, sw)
		if err != nil {
			rel = sw
		}
		fmt.Fprintf(out, "generate: openapiv2to3 %s -> openapi/ (v3)\n", filepath.Base(sw))
		if err := runTool(cmd, dir, nil, tool, rel, "openapi"); err != nil {
			return fmt.Errorf("openapiv2to3 %s: %w", rel, err)
		}
	}
	return nil
}

// hasBufConfig reports whether dir contains a buf generate config.
func hasBufConfig(dir string) bool {
	for _, name := range []string{"buf.gen.yaml", "buf.gen.yml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// bufConfigPath returns the buf generate config in dir (preferring buf.gen.yaml).
func bufConfigPath(dir string) string {
	for _, name := range []string{"buf.gen.yaml", "buf.gen.yml"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(dir, "buf.gen.yaml")
}

// hasBufLock reports whether dir has a committed buf.lock (proto deps already locked).
func hasBufLock(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "buf.lock"))
	return err == nil
}

// ensurePlugins installs plugins into a cache bindir keyed by their exact
// bin+module+version set, so it installs once per set and is a no-op thereafter:
// a version bump — of a public plugin OR of the service's SDK — lands in a fresh
// dir, never a stale binary. It returns the bindir to prepend to PATH.
func ensurePlugins(cmd *cobra.Command, plugins []toolchain.Plugin) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	h := sha256.New()
	for _, p := range plugins {
		fmt.Fprintf(h, "%s\t%s@%s\n", p.Bin, p.Module, p.Version)
	}
	key := hex.EncodeToString(h.Sum(nil))[:12]
	binDir := filepath.Join(base, "devedge", "toolchain", key, "bin")

	var missing []toolchain.Plugin
	for _, p := range plugins {
		if _, err := os.Stat(filepath.Join(binDir, p.Bin)); err != nil {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return binDir, nil
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", fmt.Errorf("create plugin cache %s: %w", binDir, err)
	}
	out := cmd.OutOrStdout()
	for _, p := range missing {
		ref := toolchain.Ref(p.Module, p.Version)
		fmt.Fprintf(out, "generate: installing pinned plugin %s\n", ref)
		if err := runTool(cmd, "", []string{"GOBIN=" + binDir}, "go", "install", ref); err != nil {
			return "", fmt.Errorf("install %s: %w", ref, err)
		}
	}
	return binDir, nil
}

// buildCmd is `de build`: reproducible `go build -trimpath ./...`.
func buildCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Compile the service (go build -trimpath ./...)",
		Long: `Compile every package in the service with 'go build -trimpath ./...'.

'-trimpath' is applied so the build is reproducible — the same source yields a
byte-identical binary regardless of the checkout path.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := resolveProjectDir(dir)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "build: go build -trimpath ./... (%s)\n", d)
			if err := runTool(cmd, d, nil, "go", "build", "-trimpath", "./..."); err != nil {
				return fmt.Errorf("go build: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n", colorSuccess.Sprint("build ok"))
			return nil
		},
	}
	dirFlag(cmd, &dir)
	return cmd
}

// testCmd is `de test`: `go test ./...`.
func testCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Run the service test suite (go test ./...)",
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := resolveProjectDir(dir)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "test: go test ./... (%s)\n", d)
			if err := runTool(cmd, d, nil, "go", "test", "./..."); err != nil {
				return fmt.Errorf("go test: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n", colorSuccess.Sprint("test ok"))
			return nil
		},
	}
	dirFlag(cmd, &dir)
	return cmd
}

// golangciConfigNames are the config files that switch `de lint` from `go vet`
// to golangci-lint.
var golangciConfigNames = []string{
	".golangci.yml", ".golangci.yaml", ".golangci.toml", ".golangci.json",
}

// lintCmd is `de lint`: pinned golangci-lint when the project configures it,
// otherwise `go vet ./...`.
func lintCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Lint the service (pinned golangci-lint if configured, else go vet)",
		Long: `Lint the service. If the project has a golangci-lint config
(.golangci.yml/.yaml/.toml/.json) the PINNED golangci-lint is run via 'go run';
otherwise it falls back to 'go vet ./...'. The pinned linter means the same 'de'
version lints with the same rules on every host.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := resolveProjectDir(dir)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if configured := lintConfigured(d); configured {
				fmt.Fprintf(out, "lint: golangci-lint %s run ./... (%s)\n", toolchain.GolangciLintVersion, d)
				if err := runTool(cmd, d, nil, "go", "run", toolchain.Ref(toolchain.GolangciLintModule, toolchain.GolangciLintVersion), "run", "./..."); err != nil {
					return fmt.Errorf("golangci-lint: %w", err)
				}
			} else {
				fmt.Fprintf(out, "lint: go vet ./... (%s; no golangci-lint config found)\n", d)
				if err := runTool(cmd, d, nil, "go", "vet", "./..."); err != nil {
					return fmt.Errorf("go vet: %w", err)
				}
			}
			fmt.Fprintf(out, "%s\n", colorSuccess.Sprint("lint ok"))
			return nil
		},
	}
	dirFlag(cmd, &dir)
	return cmd
}

// lintConfigured reports whether dir has a golangci-lint config.
func lintConfigured(dir string) bool {
	for _, name := range golangciConfigNames {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// imageCmd is `de image`: build a distroless-static container image with pinned
// ko and a reproducible (-trimpath) Go build.
func imageCmd() *cobra.Command {
	var dir, repo, platform, baseImage string
	var push bool
	cmd := &cobra.Command{
		Use:   "image [-- KO_ARGS...]",
		Short: "Build a distroless-static OCI image (pinned ko, -trimpath)",
		Long: `Build a container image for the service with the PINNED ko:

  - distroless-static base image (nonroot),
  - reproducible Go build (GOFLAGS=-trimpath),
  - multi-arch (linux/amd64,linux/arm64 by default).

By default the image is built to the local Docker daemon (--repo ko.local,
--push=false). Set --repo to a registry and --push to publish.

The build target defaults to './...' (all main packages), which suits a registry
push. For a local single-image build, pass the service main explicitly after a
'--' separator, e.g.:

    de image --push=false -- ./cmd/myservice

Any tokens after '--' are forwarded verbatim to ko.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := resolveProjectDir(dir)
			if err != nil {
				return err
			}
			koArgs := []string{"run", toolchain.Ref(toolchain.KoModule, toolchain.KoVersion), "build",
				"--platform", platform,
				fmt.Sprintf("--push=%t", push),
			}
			// Everything after '--' is forwarded verbatim to ko; default target ./...
			passthrough := args
			if len(passthrough) == 0 {
				passthrough = []string{"./..."}
			}
			koArgs = append(koArgs, passthrough...)

			env := []string{
				"GOFLAGS=-trimpath",
				"KO_DOCKER_REPO=" + repo,
				"KO_DEFAULTBASEIMAGE=" + baseImage,
			}
			// A local (ko.local) build must reach the Docker daemon. Docker Desktop,
			// Rancher Desktop, colima, and plain Linux all put the socket in
			// different places, so if DOCKER_HOST is unset, autodetect it from the
			// active docker context (best-effort — matches the old `make image`).
			if os.Getenv("DOCKER_HOST") == "" {
				if h := dockerHostFromContext(cmd); h != "" {
					env = append(env, "DOCKER_HOST="+h)
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "image: ko %s build (base %s, -trimpath, repo %s, push=%t)\n",
				toolchain.KoVersion, baseImage, repo, push)
			if err := runTool(cmd, d, env, "go", koArgs...); err != nil {
				return fmt.Errorf("ko build: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n", colorSuccess.Sprint("image ok"))
			return nil
		},
	}
	dirFlag(cmd, &dir)
	cmd.Flags().StringVar(&repo, "repo", "ko.local", "image repository (KO_DOCKER_REPO)")
	cmd.Flags().BoolVar(&push, "push", false, "push the image (default: build to the local Docker daemon)")
	cmd.Flags().StringVar(&platform, "platform", "linux/amd64,linux/arm64", "target platforms")
	cmd.Flags().StringVar(&baseImage, "base", "gcr.io/distroless/static:nonroot", "base image (distroless-static by default)")
	return cmd
}

// dockerHostFromContext returns the active docker context's daemon endpoint, or
// "" if docker is absent or reports nothing. Used to point a local `de image`
// build at the right socket without the user setting DOCKER_HOST.
func dockerHostFromContext(cmd *cobra.Command) string {
	c := exec.CommandContext(cmd.Context(), "docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}")
	var stdout strings.Builder
	c.Stdout = &stdout
	if err := c.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(stdout.String())
}

// syncCmd is `de sync`: (re)write the managed Makefile fragment
// .devedge/make/devedge.mk. Idempotent — regenerating it never touches the
// hand-owned top-level Makefile.
func syncCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Write the managed Makefile shim (.devedge/make/devedge.mk)",
		Long: `Write .devedge/make/devedge.mk — the managed Makefile fragment whose targets
delegate to the 'de' build verbs (generate/build/test/lint/image/migrate-lint).

The fragment carries a "DO NOT EDIT" header and is regenerated idempotently. The
build logic lives in 'de', so the fragment's behavior cannot drift; only the set
of targets changes. Your top-level Makefile stays hand-owned and just reads it:

    -include .devedge/make/devedge.mk
    # project-specific targets below

'de doctor' flags a stale or hand-edited fragment.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := resolveProjectDir(dir)
			if err != nil {
				return err
			}
			path := makefrag.Path(d)
			want := makefrag.Content()

			// Report unchanged / updated / created honestly.
			state := "wrote"
			if existing, err := os.ReadFile(path); err == nil {
				if makefrag.IsCurrent(existing) {
					fmt.Fprintf(cmd.OutOrStdout(), "%s %s (up to date)\n", colorSuccess.Sprint("unchanged"), path)
					return nil
				}
				state = "updated"
			}

			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
			}
			if err := os.WriteFile(path, want, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", colorSuccess.Sprint(state), path)
			if !hasTopLevelInclude(d) {
				fmt.Fprintf(cmd.OutOrStdout(), "%s add %s to your Makefile:\n    -include %s\n",
					colorLabel.Sprint("hint:"), makefrag.RelPath, makefrag.RelPath)
			}
			return nil
		},
	}
	dirFlag(cmd, &dir)
	return cmd
}

// hasTopLevelInclude reports whether dir's Makefile already `-include`s the
// managed fragment (best-effort; used only to decide whether to print a hint).
func hasTopLevelInclude(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "Makefile"))
	if err != nil {
		return false
	}
	return strings.Contains(string(b), makefrag.RelPath)
}
