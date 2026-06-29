package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

// apiCmd is `de api`, the namespace for API lifecycle operations.
func apiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api",
		Short: "API lifecycle operations (publish, ...)",
	}
	cmd.AddCommand(apiPublishCmd())
	return cmd
}

// apiPublishCmd is `de api publish` — a thin wrapper that:
//
//  1. (Re)generates the OpenAPI v3 spec via `make generate` in the service dir.
//  2. Arranges the flat `openapi/<svc>.openapi.yaml` into the apx layout
//     `openapi/<domain>/<svc>/<line>/<svc>.openapi.yaml`.
//  3. Shells out to `apx release prepare` with the right arguments, then (if
//     --submit is set) `apx release submit`.
//
// By default (no --submit) the command prints the two follow-on apx commands so
// the developer can inspect, commit, and run them manually. With --submit it runs
// both automatically.
func apiPublishCmd() *cobra.Command {
	var (
		domain        string
		apiID         string
		version       string
		lifecycle     string
		canonicalRepo string
		serviceDir    string
		submit        bool
		skipGenerate  bool
	)

	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish a service's OpenAPI v3 spec to the apx catalog",
		Long: `Publish a service's public API as an OpenAPI v3 spec to the apx catalog.

Steps performed:
  1. Run 'make generate' in the service directory to produce a fresh
     openapi/<svc>.openapi.yaml (skip with --skip-generate).
  2. Arrange the spec into the apx directory layout:
       openapi/<domain>/<svc>/<line>/<svc>.openapi.yaml
     where <svc> is the last segment of --api-id.
  3. Run 'apx release prepare openapi/<domain>/<svc>/<line> --version <v> ...'
     and, if --submit is set, also 'apx release submit'.

Without --submit the next-step apx commands are printed for you to run manually
after reviewing the spec and the PR that prepare opens on the canonical repo.

Requires apx on PATH. Install via:
  go install github.com/infobloxopen/apx@latest

Examples:
  # Prepare only (default) — prints the two follow-on commands:
  de api publish \
    --domain platform.data \
    --api-id openapi/platform.data/orders/v1 \
    --version v0.1.0 \
    --lifecycle beta \
    --canonical-repo github.com/infobloxopen/apis

  # Prepare + submit in one shot:
  de api publish \
    --domain platform.data \
    --api-id openapi/platform.data/orders/v1 \
    --version v0.1.0 \
    --lifecycle stable \
    --canonical-repo github.com/infobloxopen/apis \
    --submit`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireTools("apx"); err != nil {
				return fmt.Errorf("%w\n\nInstall apx: go install github.com/infobloxopen/apx@latest", err)
			}

			// Resolve the service directory (default: cwd).
			dir := serviceDir
			if dir == "" {
				var err error
				dir, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("get working directory: %w", err)
				}
			}

			// Derive the service name and apx API path from --api-id.
			// --api-id must be of the form openapi/<domain>/<svc>/<line>.
			apiPath, err := parseAPIID(apiID)
			if err != nil {
				return err
			}

			// 1. (Re)generate the spec.
			if !skipGenerate {
				fmt.Fprintf(cmd.OutOrStdout(), "generating spec: make generate in %s\n", dir)
				if err := runMake(cmd, dir, "generate"); err != nil {
					return fmt.Errorf("make generate: %w\n\nHint: skip with --skip-generate if the spec is already current", err)
				}
			}

			// 2. Arrange the flat spec into the apx layout.
			srcSpec := filepath.Join(dir, "openapi", apiPath.svc+".openapi.yaml")
			destDir := filepath.Join(dir, "openapi", apiPath.domain, apiPath.svc, apiPath.line)
			destSpec := filepath.Join(destDir, apiPath.svc+".openapi.yaml")

			if err := arrangeSpec(srcSpec, destDir, destSpec); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "arranged:  %s → %s\n", srcSpec, destSpec)

			// 3. apx release prepare.
			apxAPIArg := filepath.Join("openapi", apiPath.domain, apiPath.svc, apiPath.line)
			prepareArgs := []string{
				"release", "prepare", apxAPIArg,
				"--version", version,
				"--lifecycle", lifecycle,
				"--canonical-repo", canonicalRepo,
			}
			fmt.Fprintf(cmd.OutOrStdout(), "running: apx %v\n", prepareArgs)
			if err := runExec(cmd, dir, "apx", prepareArgs...); err != nil {
				return fmt.Errorf("apx release prepare: %w", err)
			}

			if submit {
				// apx release submit (opens the PR on the canonical repo).
				submitArgs := []string{"release", "submit"}
				fmt.Fprintf(cmd.OutOrStdout(), "running: apx %v\n", submitArgs)
				if err := runExec(cmd, dir, "apx", submitArgs...); err != nil {
					return fmt.Errorf("apx release submit: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "\nspec published; finalize after the PR is merged:\n")
				fmt.Fprintf(cmd.OutOrStdout(), "  apx release finalize --api %s --version %s\n", apxAPIArg, version)
			} else {
				// Print the follow-on commands for the developer.
				fmt.Fprintf(cmd.OutOrStdout(), "\nprepare complete — next:\n")
				fmt.Fprintf(cmd.OutOrStdout(), "  apx release submit\n")
				fmt.Fprintf(cmd.OutOrStdout(), "  # (after the PR is merged on the canonical repo:)\n")
				fmt.Fprintf(cmd.OutOrStdout(), "  apx release finalize --api %s --version %s\n", apxAPIArg, version)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&domain, "domain", "", "API domain segment, e.g. platform.data (required if not embedded in --api-id)")
	cmd.Flags().StringVar(&apiID, "api-id", "", "Full apx API ID, e.g. openapi/platform.data/orders/v1 (required)")
	cmd.Flags().StringVar(&version, "version", "", "Semantic version to publish, e.g. v0.1.0 (required)")
	cmd.Flags().StringVar(&lifecycle, "lifecycle", "beta", "API lifecycle: beta or stable")
	cmd.Flags().StringVar(&canonicalRepo, "canonical-repo", "", "Canonical APIs repo, e.g. github.com/infobloxopen/apis (required)")
	cmd.Flags().StringVar(&serviceDir, "service-dir", "", "Service root directory (default: current working directory)")
	cmd.Flags().BoolVar(&submit, "submit", false, "Also run 'apx release submit' after prepare (opens PR)")
	cmd.Flags().BoolVar(&skipGenerate, "skip-generate", false, "Skip 'make generate'; use the existing openapi/<svc>.openapi.yaml")

	_ = cmd.MarkFlagRequired("api-id")
	_ = cmd.MarkFlagRequired("version")
	_ = cmd.MarkFlagRequired("canonical-repo")

	return cmd
}

// apiIDParts holds the parsed segments of an apx API ID.
type apiIDParts struct {
	domain string
	svc    string
	line   string
}

// parseAPIID parses an apx API ID of the form openapi/<domain>/<svc>/<line>.
// It returns a structured breakdown used for both path arrangement and CLI args.
func parseAPIID(id string) (apiIDParts, error) {
	// Strip a leading "openapi/" prefix if present so the user can pass either
	// "openapi/platform.data/orders/v1" or "platform.data/orders/v1".
	trimmed := id
	if len(trimmed) > 8 && trimmed[:8] == "openapi/" {
		trimmed = trimmed[8:]
	}

	// Split into exactly three segments: domain / svc / line.
	parts := splitN(trimmed, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return apiIDParts{}, fmt.Errorf(
			"--api-id must be openapi/<domain>/<svc>/<line>, e.g. openapi/platform.data/orders/v1; got %q", id)
	}
	return apiIDParts{domain: parts[0], svc: parts[1], line: parts[2]}, nil
}

// splitN splits s by sep into at most n parts (simple version without importing strings).
func splitN(s, sep string, n int) []string {
	if n <= 0 {
		return nil
	}
	var parts []string
	for len(s) > 0 && len(parts) < n-1 {
		i := indexOf(s, sep)
		if i < 0 {
			break
		}
		parts = append(parts, s[:i])
		s = s[i+len(sep):]
	}
	parts = append(parts, s)
	return parts
}

// indexOf returns the index of sep in s, or -1 if not found.
func indexOf(s, sep string) int {
	for i := 0; i <= len(s)-len(sep); i++ {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}

// arrangeSpec copies srcSpec into destDir as destSpec, creating the directory
// tree if necessary. It does NOT delete the original flat file — both paths
// exist after this call, which is intentional: the flat file is the generated
// artifact; the arranged copy is what apx consumes.
func arrangeSpec(srcSpec, destDir, destSpec string) error {
	if _, err := os.Stat(srcSpec); os.IsNotExist(err) {
		return fmt.Errorf("spec not found at %s — run 'make generate' first (or omit --skip-generate)", srcSpec)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create apx layout dir %s: %w", destDir, err)
	}
	data, err := os.ReadFile(srcSpec)
	if err != nil {
		return fmt.Errorf("read spec %s: %w", srcSpec, err)
	}
	if err := os.WriteFile(destSpec, data, 0o644); err != nil {
		return fmt.Errorf("write arranged spec %s: %w", destSpec, err)
	}
	return nil
}

// runMake shells out to `make <target>` in dir, streaming stdout/stderr to the
// command's output writers.
func runMake(cmd *cobra.Command, dir, target string) error {
	return runExec(cmd, dir, "make", target)
}

// runExec runs name with args in dir, streaming to cmd's out/err writers.
func runExec(cmd *cobra.Command, dir, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	return c.Run()
}
