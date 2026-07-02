package toolchain

import (
	"strings"
	"testing"

	"github.com/infobloxopen/devedge/internal/scaffold"
)

func TestRef(t *testing.T) {
	if got := Ref(BufModule, BufVersion); got != "github.com/bufbuild/buf/cmd/buf@"+BufVersion {
		t.Errorf("Ref = %q", got)
	}
}

// TestNoAtLatest guards the hermeticity invariant: every pinned tool version is
// an exact semver, never "latest" (WS-023 kills @latest tool installs).
func TestNoAtLatest(t *testing.T) {
	versions := map[string]string{
		"buf":                     BufVersion,
		"ko":                      KoVersion,
		"golangci-lint":           GolangciLintVersion,
		"protoc-gen-go":           ProtocGenGoVersion,
		"protoc-gen-go-grpc":      ProtocGenGoGRPCVersion,
		"protoc-gen-grpc-gateway": ProtocGenGRPCGatewayVersion,
	}
	for name, v := range versions {
		if v == "" || v == "latest" || strings.Contains(v, "latest") {
			t.Errorf("%s must be pinned to an exact version, got %q", name, v)
		}
		if !strings.HasPrefix(v, "v") {
			t.Errorf("%s version %q should be a v-prefixed semver", name, v)
		}
	}
}

// TestBufPluginsComplete asserts the three codegen plugins the scaffold's
// buf.gen.yaml references are all present and pinned.
func TestBufPluginsComplete(t *testing.T) {
	want := map[string]bool{"protoc-gen-go": false, "protoc-gen-go-grpc": false, "protoc-gen-grpc-gateway": false}
	for _, p := range BufPlugins {
		if _, ok := want[p.Bin]; !ok {
			t.Errorf("unexpected plugin %q", p.Bin)
		}
		want[p.Bin] = true
		if p.Module == "" || p.Version == "" {
			t.Errorf("plugin %q missing module/version", p.Bin)
		}
	}
	for bin, seen := range want {
		if !seen {
			t.Errorf("missing pinned plugin %q", bin)
		}
	}
}

// TestCodegenPluginsMatchScaffoldRuntime enforces the WS-023 lock-step invariant
// documented on this package: the codegen plugin versions `de` runs must equal
// the runtime module versions the scaffold bakes into a generated service's
// go.mod, so generated code always matches the modules it compiles against. A
// plugin and its runtime library share one release line:
//   - protoc-gen-go        <-> google.golang.org/protobuf   (Protobuf)
//   - protoc-gen-grpc-gateway <-> grpc-gateway/v2            (Gateway)
//
// (protoc-gen-go-grpc and google.golang.org/grpc are DIFFERENT modules on
// independent version lines, so they are intentionally not paired here.)
func TestCodegenPluginsMatchScaffoldRuntime(t *testing.T) {
	if ProtocGenGoVersion != scaffold.DefaultVersions.Protobuf {
		t.Errorf("protoc-gen-go %s must match scaffold Protobuf runtime %s (lock-step drift)",
			ProtocGenGoVersion, scaffold.DefaultVersions.Protobuf)
	}
	if ProtocGenGRPCGatewayVersion != scaffold.DefaultVersions.Gateway {
		t.Errorf("protoc-gen-grpc-gateway %s must match scaffold Gateway runtime %s (lock-step drift)",
			ProtocGenGRPCGatewayVersion, scaffold.DefaultVersions.Gateway)
	}
}
