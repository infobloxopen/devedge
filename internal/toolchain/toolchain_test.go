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
		"protoc-gen-openapiv2":    ProtocGenOpenAPIV2Version,
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

// TestBufPluginsComplete asserts the public codegen plugins the scaffolds'
// buf.gen.yaml files reference are all present and pinned. openapiv2 ships in the
// same module as grpc-gateway; it is included because the SDK scaffold's
// buf.gen.yaml emits an OpenAPI surface with it.
func TestBufPluginsComplete(t *testing.T) {
	want := map[string]bool{
		"protoc-gen-go":           false,
		"protoc-gen-go-grpc":      false,
		"protoc-gen-grpc-gateway": false,
		"protoc-gen-openapiv2":    false,
	}
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

// TestSDKPluginClassification pins the gorm/ent backend split: storage is the
// gorm-path SDK plugin, ent the ent-path one, and public plugins are never
// classified as SDK plugins (they are pinned by `de`, not by the service's SDK).
func TestSDKPluginClassification(t *testing.T) {
	for _, bin := range []string{"protoc-gen-devedge-authz", "protoc-gen-svc", "protoc-gen-storage", "protoc-gen-ent"} {
		if !IsSDKPlugin(bin) {
			t.Errorf("%q should be an SDK plugin", bin)
		}
	}
	for _, bin := range []string{"protoc-gen-go", "protoc-gen-go-grpc", "protoc-gen-grpc-gateway", "protoc-gen-openapiv2"} {
		if IsSDKPlugin(bin) {
			t.Errorf("%q is public, must not be classified as an SDK plugin", bin)
		}
		if _, ok := PublicPlugin(bin); !ok {
			t.Errorf("%q must be a known public plugin", bin)
		}
	}
	if got := SDKCmd("protoc-gen-svc"); got != "github.com/infobloxopen/devedge-sdk/cmd/protoc-gen-svc" {
		t.Errorf("SDKCmd = %q", got)
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
