package sdkscaffold

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/infobloxopen/devedge/pkg/config"
)

// fakeRunner records the devedge-sdk invocation and lets a test control whether
// the binary "exists" on PATH and whether the scaffold "succeeds". It does NOT
// run anything — the real scaffolding is covered by devedge-sdk's own e2e
// tests; here we only assert the driver's behavior.
type fakeRunner struct {
	present  bool  // is devedge-sdk on PATH?
	runErr   error // error returned from Run (simulate a scaffold failure)
	gotDir   string
	gotName  string
	gotArgs  []string
	runCalls int
}

func (f *fakeRunner) Look(name string) (string, error) {
	if name != SDKBinary {
		return "", errors.New("unexpected lookup: " + name)
	}
	if !f.present {
		return "", errors.New("not found")
	}
	return "/fake/bin/" + name, nil
}

func (f *fakeRunner) Run(_ context.Context, dir, name string, args []string, _, _ io.Writer) error {
	f.runCalls++
	f.gotDir, f.gotName, f.gotArgs = dir, name, args
	return f.runErr
}

func TestPreflight_missingBinary(t *testing.T) {
	err := Preflight(&fakeRunner{present: false})
	if err == nil {
		t.Fatal("expected error when devedge-sdk is absent")
	}
	// The message must be actionable: name the install command + the pinned version.
	for _, want := range []string{
		"go install github.com/infobloxopen/devedge-sdk/cmd/devedge-sdk@" + SDKInstallVersion,
		SDKBinary,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("preflight error missing %q; got:\n%s", want, err.Error())
		}
	}
}

func TestPreflight_present(t *testing.T) {
	if err := Preflight(&fakeRunner{present: true}); err != nil {
		t.Fatalf("preflight should pass when binary present: %v", err)
	}
}

func TestSDKArgs_forwarding(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		want []string
	}{
		{
			name: "name only (sdk defaults resource+backend)",
			opts: Options{Name: "orders"},
			want: []string{"new", "service", "orders"},
		},
		{
			name: "resource and backend",
			opts: Options{Name: "notes", Resource: "Note", Backend: "ent"},
			want: []string{"new", "service", "notes", "--resource", "Note", "--backend", "ent"},
		},
		{
			name: "dir and passthrough preserved in order",
			opts: Options{
				Name: "orders", Resource: "Order", Backend: "gorm", Dir: "services/orders",
				Passthrough: []string{"--module", "github.com/acme/orders", "--force"},
			},
			want: []string{
				"new", "service", "orders",
				"--resource", "Order", "--backend", "gorm", "--dir", "services/orders",
				"--module", "github.com/acme/orders", "--force",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.opts.SDKArgs(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("SDKArgs() = %v\nwant %v", got, tc.want)
			}
		})
	}
}

// TestRun_invokesSDKAndEmitsYAML is the core driver test: with a stubbed
// runner, Run must (1) build the correct devedge-sdk invocation and (2) emit a
// devedge.yaml that the real project-config parser accepts.
func TestRun_invokesSDKAndEmitsYAML(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "orders")

	fr := &fakeRunner{present: true}
	opts := Options{
		Name:        "orders",
		Resource:    "Order",
		Backend:     "gorm",
		Dir:         dir,
		Passthrough: []string{"--org", "acme"},
	}

	res, err := Run(context.Background(), fr, opts, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 1. The scaffold binary was invoked exactly once with the forwarded args.
	if fr.runCalls != 1 {
		t.Fatalf("Run called devedge-sdk %d times, want 1", fr.runCalls)
	}
	if fr.gotName != SDKBinary {
		t.Errorf("invoked %q, want %q", fr.gotName, SDKBinary)
	}
	wantArgs := []string{"new", "service", "orders", "--resource", "Order", "--backend", "gorm", "--dir", dir, "--org", "acme"}
	if !reflect.DeepEqual(fr.gotArgs, wantArgs) {
		t.Errorf("devedge-sdk args = %v\nwant %v", fr.gotArgs, wantArgs)
	}

	// 2. A devedge.yaml was emitted in the project dir.
	if res.DevedgeYAML != filepath.Join(dir, "devedge.yaml") {
		t.Errorf("DevedgeYAML = %q", res.DevedgeYAML)
	}
	data, err := os.ReadFile(res.DevedgeYAML)
	if err != nil {
		t.Fatalf("read emitted devedge.yaml: %v", err)
	}

	// 3. It parses via the REAL loader `de project up` uses, as a kind: Config
	//    with the gateway route.
	parsed, err := config.ParseResource(data)
	if err != nil {
		t.Fatalf("emitted devedge.yaml rejected by parser: %v\n---\n%s", err, data)
	}
	if parsed.Project() != "orders" {
		t.Errorf("project = %q, want orders", parsed.Project())
	}
	routes, err := parsed.ToRoutes()
	if err != nil {
		t.Fatalf("ToRoutes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("got %d routes, want 1", len(routes))
	}
	if routes[0].Host != "orders.dev.test" {
		t.Errorf("route host = %q, want orders.dev.test", routes[0].Host)
	}
	if routes[0].Upstream != "http://127.0.0.1:"+GatewayPort {
		t.Errorf("route upstream = %q, want http://127.0.0.1:%s", routes[0].Upstream, GatewayPort)
	}
}

func TestRun_missingBinary_doesNotScaffold(t *testing.T) {
	fr := &fakeRunner{present: false}
	_, err := Run(context.Background(), fr, Options{Name: "orders"}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected preflight error")
	}
	if fr.runCalls != 0 {
		t.Errorf("devedge-sdk must not run when preflight fails; got %d calls", fr.runCalls)
	}
}

func TestRun_scaffoldFailure_noYAML(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "orders")
	fr := &fakeRunner{present: true, runErr: errors.New("buf generate failed")}

	_, err := Run(context.Background(), fr, Options{Name: "orders", Dir: dir}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected error when scaffold fails")
	}
	// No devedge.yaml should be written if the scaffold itself failed.
	if _, statErr := os.Stat(filepath.Join(dir, "devedge.yaml")); !os.IsNotExist(statErr) {
		t.Errorf("devedge.yaml should not exist after a scaffold failure")
	}
}

func TestRun_emptyName(t *testing.T) {
	_, err := Run(context.Background(), &fakeRunner{present: true}, Options{Name: "  "}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected error for empty service name")
	}
}

func TestWriteDevedgeYAML_refusesOverwrite(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "devedge.yaml")
	if err := os.WriteFile(path, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteDevedgeYAML(path, "orders", "orders.dev.test", GatewayUpstream()); err == nil {
		t.Fatal("expected refusal to overwrite an existing devedge.yaml")
	}
}

func TestRenderDevedgeYAML_isValid(t *testing.T) {
	data := RenderDevedgeYAML("notes", "notes.dev.test", GatewayUpstream())
	if _, err := config.ParseResource([]byte(data)); err != nil {
		t.Fatalf("rendered devedge.yaml invalid: %v\n---\n%s", err, data)
	}
}
