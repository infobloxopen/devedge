// Package sdkscaffold drives `de new service` as a THIN DRIVER over the
// external devedge-sdk CLI, then adds devedge's native value: a devedge.yaml
// that routes the scaffolded service's HTTP/JSON gateway through the local
// edge so `de project up` serves it.
//
// Two scaffolds intentionally coexist in this repo:
//
//   - internal/scaffold (feature 007, `de project init`): an in-tree,
//     embedded-template scaffold owned entirely by devedge.
//   - this package (WS-004 Phase 2, `de new service`): a thin driver over the
//     apx-native devedge-sdk scaffold. devedge-sdk's scaffold package is
//     internal/, so it CANNOT be imported — we shell out to the binary and
//     forward the flags. The heavy logic (apx + buf wiring, proto, generated
//     models, server) lives in devedge-sdk, versioned with the plugins it
//     wires. See specs/011-de-new-service and the cross-repo proposal
//     (development-hub specs/devedge-apx-scaffolding-proposal.md §4.3).
package sdkscaffold

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/infobloxopen/devedge/pkg/config"
)

const (
	// SDKBinary is the devedge-sdk CLI we drive.
	SDKBinary = "devedge-sdk"

	// SDKInstallVersion is the released devedge-sdk version that ships the
	// `new service` CLI; surfaced in the not-installed error so the fix is a
	// copy-paste.
	SDKInstallVersion = "latest"

	// GatewayPort is the HTTP/JSON gateway port the devedge-sdk scaffold's
	// server listens on. The scaffold's main.go template hardcodes
	// HTTPPort=8080 (gRPC on 9090); the emitted route forwards here. If the
	// scaffold's default ever changes, change this constant to match.
	GatewayPort = "8080"

	// DevSuffix is devedge's canonical local hostname suffix.
	DevSuffix = "dev.test"
)

// Runner runs an external command. It is injectable so the driver can be tested
// without the apx/buf/devedge-sdk toolchain present.
type Runner interface {
	// Look reports whether the named binary is resolvable on PATH (like
	// exec.LookPath). It returns the resolved path or an error.
	Look(name string) (string, error)
	// Run executes name with args in dir, streaming output to stdout/stderr.
	// An empty dir means the current working directory.
	Run(ctx context.Context, dir, name string, args []string, stdout, stderr io.Writer) error
}

// execRunner is the real Runner backed by os/exec.
type execRunner struct{}

func (execRunner) Look(name string) (string, error) { return exec.LookPath(name) }

func (execRunner) Run(ctx context.Context, dir, name string, args []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// DefaultRunner is the production Runner (real os/exec).
var DefaultRunner Runner = execRunner{}

// Options are the inputs to `de new service`.
type Options struct {
	// Name is the service name (e.g. "orders"). Required.
	Name string
	// Resource is the singular resource type (e.g. "Order"). Forwarded to
	// devedge-sdk, which defaults it from Name when empty.
	Resource string
	// Backend selects the persistence backend ("gorm" or "ent"). Forwarded to
	// devedge-sdk, which defaults to "gorm" when empty.
	Backend string
	// Dir is the target directory the project is generated into. Defaults to
	// Name (matching devedge-sdk's own default).
	Dir string
	// Passthrough carries extra flags forwarded verbatim to
	// `devedge-sdk new service` (e.g. --module, --org, --force, --no-generate).
	Passthrough []string
}

// TargetDir returns the directory the project is generated into.
func (o Options) TargetDir() string {
	if o.Dir != "" {
		return o.Dir
	}
	return o.Name
}

// SDKArgs builds the argument vector passed to the devedge-sdk binary. It is
// exported (and pure) so the driver test can assert the exact forwarding
// without running anything.
func (o Options) SDKArgs() []string {
	args := []string{"new", "service", o.Name}
	if o.Resource != "" {
		args = append(args, "--resource", o.Resource)
	}
	if o.Backend != "" {
		args = append(args, "--backend", o.Backend)
	}
	if o.Dir != "" {
		args = append(args, "--dir", o.Dir)
	}
	args = append(args, o.Passthrough...)
	return args
}

// GatewayHost returns the dev hostname routed to the service's HTTP gateway.
func (o Options) GatewayHost() string {
	return o.Name + "." + DevSuffix
}

// GatewayUpstream returns the loopback URL the gateway listens on.
func GatewayUpstream() string {
	return "http://127.0.0.1:" + GatewayPort
}

// Result reports what `de new service` produced for the caller to print.
type Result struct {
	Dir         string // project directory
	DevedgeYAML string // path to the emitted devedge.yaml
	GatewayHost string // host routed to the service
	Upstream    string // upstream URL the route points at
}

// Preflight verifies the devedge-sdk binary is on PATH, returning an actionable
// error if not.
func Preflight(r Runner) error {
	if r == nil {
		r = DefaultRunner
	}
	if _, err := r.Look(SDKBinary); err != nil {
		return fmt.Errorf(
			"%s not found on PATH — install it with:\n\n    go install github.com/infobloxopen/devedge-sdk/cmd/%s@%s\n",
			SDKBinary, SDKBinary, SDKInstallVersion)
	}
	return nil
}

// Run drives the full `de new service` flow: preflight, scaffold via
// devedge-sdk, then emit the devedge.yaml. Scaffold output is streamed to
// stdout/stderr.
func Run(ctx context.Context, r Runner, opts Options, stdout, stderr io.Writer) (Result, error) {
	if r == nil {
		r = DefaultRunner
	}
	if strings.TrimSpace(opts.Name) == "" {
		return Result{}, fmt.Errorf("service name is required")
	}

	if err := Preflight(r); err != nil {
		return Result{}, err
	}

	// Drive devedge-sdk to do the heavy scaffolding. Run from the current
	// working directory; devedge-sdk creates the target dir itself.
	if err := r.Run(ctx, "", SDKBinary, opts.SDKArgs(), stdout, stderr); err != nil {
		return Result{}, fmt.Errorf("%s new service: %w", SDKBinary, err)
	}

	// devedge-native value-add: route the new service's gateway through the
	// local edge so `de project up` in the scaffolded dir serves it.
	dir := opts.TargetDir()
	yamlPath := filepath.Join(dir, "devedge.yaml")
	if err := WriteDevedgeYAML(yamlPath, opts.Name, opts.GatewayHost(), GatewayUpstream()); err != nil {
		return Result{}, fmt.Errorf("emit devedge.yaml: %w", err)
	}

	return Result{
		Dir:         dir,
		DevedgeYAML: yamlPath,
		GatewayHost: opts.GatewayHost(),
		Upstream:    GatewayUpstream(),
	}, nil
}

// WriteDevedgeYAML emits a minimal, valid devedge project config that routes
// host -> upstream for the named project. It does not overwrite an existing
// file (the scaffold owns a fresh directory, but be safe).
func WriteDevedgeYAML(path, project, host, upstream string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists; not overwriting", path)
	}
	data := RenderDevedgeYAML(project, host, upstream)
	// Validate against the REAL loader `de project up` uses (ParseResource
	// dispatches kind: Config to ParseProject) before writing, so a bad
	// template can never produce a devedge.yaml that `de project up` rejects.
	if _, err := config.ParseResource([]byte(data)); err != nil {
		return fmt.Errorf("internal: rendered devedge.yaml is invalid: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(data), 0o644)
}

// RenderDevedgeYAML renders the devedge.yaml content. Kept pure (no I/O) so it
// is trivially testable. The shape matches pkg/config.ProjectConfig (kind:
// Config + spec.routes) and the product vision's Flow 2 example.
func RenderDevedgeYAML(project, host, upstream string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: %s\n", config.APIVersion)
	fmt.Fprintf(&b, "kind: %s\n", config.Kind)
	b.WriteString("metadata:\n")
	fmt.Fprintf(&b, "  name: %s\n", project)
	b.WriteString("spec:\n")
	b.WriteString("  defaults:\n")
	b.WriteString("    ttl: 30s\n")
	b.WriteString("    tls: true\n")
	b.WriteString("  routes:\n")
	fmt.Fprintf(&b, "    # HTTP/JSON gateway of the %s service (scaffolded by devedge-sdk;\n", project)
	fmt.Fprintf(&b, "    # its server listens on :%s). `de project up` serves it over the edge.\n", GatewayPort)
	fmt.Fprintf(&b, "    - host: %s\n", host)
	fmt.Fprintf(&b, "      upstream: %s\n", upstream)
	return b.String()
}
