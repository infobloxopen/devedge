// Package helm renders and installs the Kubernetes objects devedge manages for
// dependency runtime. All k8s objects flow through Helm (embedded in-repo charts
// invoked via the `helm` CLI) rather than hand-assembled YAML, so the dev-runtime
// instances and the emitted deploy artifact share one rendering path.
//
// The `helm` binary is a subprocess dependency (alongside `kubectl`/`k3d`); no
// Helm Go SDK is imported.
package helm

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultNamespace is where devedge installs shared dependency instances. It is
// set at install time (not hardcoded in chart templates) and created on demand.
const DefaultNamespace = "devedge-deps"

// Helm is a thin adapter over the `helm` CLI scoped to a kube context.
type Helm struct {
	bin         string
	kubeContext string
}

// New returns a Helm adapter targeting the given kube context (e.g. "k3d-mydev").
// An empty context uses the caller's current kube context.
func New(kubeContext string) *Helm {
	return &Helm{bin: "helm", kubeContext: kubeContext}
}

// Available reports whether the `helm` CLI is on PATH, with an actionable error
// when it is not (Principle IV: unsupported environment fails clearly).
func Available() error {
	if _, err := exec.LookPath("helm"); err != nil {
		return fmt.Errorf("helm CLI not found on PATH: install Helm to manage dependency runtime (https://helm.sh)")
	}
	return nil
}

// ChartNames are the embedded charts devedge ships.
const (
	ChartPostgres = "postgres"
	ChartRedis    = "redis"
	ChartService  = "service"
)

// MaterializeChart writes the named embedded chart to a fresh temp directory and
// returns its path plus a cleanup func. Callers run `helm` against the returned
// directory (the CLI reads charts from disk).
func MaterializeChart(name string) (dir string, cleanup func(), err error) {
	root := filepath.Join("charts", name)
	if _, err := fs.Stat(chartsFS, root); err != nil {
		return "", nil, fmt.Errorf("unknown embedded chart %q", name)
	}
	tmp, err := os.MkdirTemp("", "devedge-chart-"+name+"-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { os.RemoveAll(tmp) }

	walkErr := fs.WalkDir(chartsFS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(tmp, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := chartsFS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if walkErr != nil {
		cleanup()
		return "", nil, walkErr
	}
	return tmp, cleanup, nil
}

// writeValues marshals values to a temp YAML file and returns its path + cleanup.
func writeValues(values map[string]any) (string, func(), error) {
	if len(values) == 0 {
		return "", func() {}, nil
	}
	f, err := os.CreateTemp("", "devedge-values-*.yaml")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.Remove(f.Name()) }
	enc := yaml.NewEncoder(f)
	if err := enc.Encode(values); err != nil {
		f.Close()
		cleanup()
		return "", nil, err
	}
	enc.Close()
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return f.Name(), cleanup, nil
}

func (h *Helm) baseArgs(namespace string) []string {
	var args []string
	if h.kubeContext != "" {
		args = append(args, "--kube-context", h.kubeContext)
	}
	if namespace != "" {
		args = append(args, "--namespace", namespace)
	}
	return args
}

func (h *Helm) run(ctx context.Context, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, h.bin, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("helm %v: %w: %s", args, err, stderr.String())
	}
	return stdout.String(), nil
}

// Render runs `helm template` for the named embedded chart and returns the
// rendered manifest. Deterministic for a given chart + values (golden-testable).
func (h *Helm) Render(ctx context.Context, chart, release, namespace string, values map[string]any) (string, error) {
	dir, cleanup, err := MaterializeChart(chart)
	if err != nil {
		return "", err
	}
	defer cleanup()
	return h.renderDir(ctx, dir, release, namespace, values)
}

// RenderDir runs `helm template` against a chart directory on disk.
func (h *Helm) renderDir(ctx context.Context, chartDir, release, namespace string, values map[string]any) (string, error) {
	valuesFile, vcleanup, err := writeValues(values)
	if err != nil {
		return "", err
	}
	defer vcleanup()

	args := append([]string{"template", release, chartDir}, h.baseArgs(namespace)...)
	if valuesFile != "" {
		args = append(args, "-f", valuesFile)
	}
	return h.run(ctx, args...)
}

// Install runs `helm upgrade --install` for the named embedded chart, creating
// the namespace and waiting for resources to be ready. Idempotent.
func (h *Helm) Install(ctx context.Context, chart, release, namespace string, values map[string]any) error {
	dir, cleanup, err := MaterializeChart(chart)
	if err != nil {
		return err
	}
	defer cleanup()

	valuesFile, vcleanup, err := writeValues(values)
	if err != nil {
		return err
	}
	defer vcleanup()

	args := append([]string{"upgrade", "--install", release, dir, "--create-namespace", "--wait"}, h.baseArgs(namespace)...)
	if valuesFile != "" {
		args = append(args, "-f", valuesFile)
	}
	_, err = h.run(ctx, args...)
	return err
}

// Uninstall removes a release. A missing release is not an error.
func (h *Helm) Uninstall(ctx context.Context, release, namespace string) error {
	args := append([]string{"uninstall", release, "--ignore-not-found"}, h.baseArgs(namespace)...)
	_, err := h.run(ctx, args...)
	return err
}

// Lint runs `helm lint` against a chart directory; a non-nil error means the
// chart failed linting.
func (h *Helm) Lint(ctx context.Context, chartDir string) error {
	_, err := h.run(ctx, "lint", chartDir)
	return err
}
