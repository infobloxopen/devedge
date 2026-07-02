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
// `de api publish`. It puts the pinned buf plugins first on PATH, runs the
// pinned buf, then tidies the module.
func runGenerate(cmd *cobra.Command, dir string) error {
	if !hasBufConfig(dir) {
		return fmt.Errorf("no buf config (buf.gen.yaml) found in %s — is this a service project?", dir)
	}
	out := cmd.OutOrStdout()

	// 1. Install the pinned codegen plugins into a version-keyed cache bindir and
	//    put it first on PATH so buf's `local:` plugins resolve to the pinned
	//    versions, not whatever is on the host PATH.
	binDir, err := ensureBufPlugins(cmd)
	if err != nil {
		return err
	}
	env := []string{"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH")}

	// 2. Run the pinned buf.
	fmt.Fprintf(out, "generate: buf %s generate (pinned plugins from %s)\n", toolchain.BufVersion, binDir)
	if err := runTool(cmd, dir, env, "go", "run", toolchain.Ref(toolchain.BufModule, toolchain.BufVersion), "generate"); err != nil {
		return fmt.Errorf("buf generate: %w", err)
	}

	// 3. Tidy — generated imports may have changed the module graph.
	fmt.Fprintf(out, "generate: go mod tidy\n")
	if err := runTool(cmd, dir, nil, "go", "mod", "tidy"); err != nil {
		return fmt.Errorf("go mod tidy: %w", err)
	}
	fmt.Fprintf(out, "%s\n", colorSuccess.Sprint("generate ok"))
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

// ensureBufPlugins installs the pinned buf codegen plugins into a cache bindir
// keyed by their versions, so it installs once per version-set and is a no-op
// thereafter. It returns the bindir to prepend to PATH.
func ensureBufPlugins(cmd *cobra.Command) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	// Key the bindir by the exact plugin version-set so a version bump lands in a
	// fresh dir (no stale binaries).
	h := sha256.New()
	for _, p := range toolchain.BufPlugins {
		fmt.Fprintf(h, "%s@%s\n", p.Module, p.Version)
	}
	key := hex.EncodeToString(h.Sum(nil))[:12]
	binDir := filepath.Join(base, "devedge", "toolchain", key, "bin")

	var missing []toolchain.Plugin
	for _, p := range toolchain.BufPlugins {
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
