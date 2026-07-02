package toolchain

import (
	"strings"
	"testing"
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
