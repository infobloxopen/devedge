// Package toolchain pins the external build tools `de` drives (buf, ko,
// golangci-lint, and the buf codegen plugins) to exact versions and runs them
// hermetically via `go run <module>@<version>`.
//
// # Why this exists (WS-023)
//
// The scaffolded service Makefile used to install these tools at `@latest`, so
// two services built a month apart — or one rebuilt today — could pull different
// buf/ko/golangci-lint versions off the host PATH. `de` is the build authority;
// hermeticity comes from pinning the toolchain HERE, baked into the `de` binary.
// The result: the same `de` version yields the same toolchain and the same
// output, independent of what happens to be on $PATH.
//
// # Why constants and not a go.mod `tool` directive
//
// This repo already pins provenance with version CONSTANTS (internal/compose's
// SDKVersion, the composition.lock toolchain pin). Constants keep that idiom and,
// critically, keep devedge's dependency GRAPH light: adding buf/ko/golangci-lint
// as `tool` directives would drag their (very large) module graphs into
// devedge's go.mod/go.sum and build graph. `go run <module>@<version>` pins the
// version just as tightly (Go records it in the build/module cache, checksummed)
// without polluting the graph, and — unlike a `tool` directive, which resolves
// against the CURRENT module — works when `de` is invoked from an arbitrary
// service directory (a different module). Bump these deliberately with the `de`
// release.
package toolchain

// Pinned tool versions — the single source of hermetic truth. Bump with the
// `de` release; the plugin versions are kept in lock-step with the scaffold's
// generated go.mod (internal/scaffold DefaultVersions) so generated code matches
// the modules a service compiles against.
const (
	// BufVersion pins the buf CLI (proto lint/generate driver).
	BufVersion = "v1.40.1"
	// KoVersion pins ko (the container image builder for `de image`).
	KoVersion = "v0.19.1"
	// GolangciLintVersion pins golangci-lint (the linter for `de lint`).
	GolangciLintVersion = "v2.9.0"

	// ProtocGenGoVersion pins protoc-gen-go (matches scaffold Protobuf).
	ProtocGenGoVersion = "v1.36.11"
	// ProtocGenGoGRPCVersion pins protoc-gen-go-grpc.
	ProtocGenGoGRPCVersion = "v1.5.1"
	// ProtocGenGRPCGatewayVersion pins protoc-gen-grpc-gateway (matches scaffold Gateway).
	ProtocGenGRPCGatewayVersion = "v2.27.4"
	// ProtocGenOpenAPIV2Version pins protoc-gen-openapiv2. It ships in the SAME
	// module as protoc-gen-grpc-gateway (grpc-gateway/v2), so its version is the
	// Gateway version by construction — they can never drift.
	ProtocGenOpenAPIV2Version = ProtocGenGRPCGatewayVersion
)

// Tool module paths. These are the `go run`/`go install` targets.
const (
	BufModule          = "github.com/bufbuild/buf/cmd/buf"
	KoModule           = "github.com/google/ko"
	GolangciLintModule = "github.com/golangci/golangci-lint/v2/cmd/golangci-lint"

	ProtocGenGoModule          = "google.golang.org/protobuf/cmd/protoc-gen-go"
	ProtocGenGoGRPCModule      = "google.golang.org/grpc/cmd/protoc-gen-go-grpc"
	ProtocGenGRPCGatewayModule = "github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway"
	ProtocGenOpenAPIV2Module   = "github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2"

	// SDKModule is the devedge-sdk module. Its codegen plugins and the
	// openapiv2->v3 converter live under SDKModule/cmd/<bin>. Unlike the public
	// plugins above (pinned by THIS binary), an SDK plugin is pinned to the
	// SERVICE's resolved devedge-sdk version (`go list -m` in the service dir):
	// that pin is what keeps generated code matching the SDK a service compiles
	// against. `de generate` resolves it per project.
	SDKModule = "github.com/infobloxopen/devedge-sdk"

	// OpenAPIV2To3Bin is the devedge-sdk tool that converts the OpenAPI v2
	// (swagger.json) protoc-gen-openapiv2 emits into OpenAPI v3, in place under
	// openapi/. It is not a buf `local:` plugin (buf never invokes it) — `de
	// generate` runs it as a post-step, pinned to the service's SDK version.
	OpenAPIV2To3Bin = "openapiv2to3"
)

// Plugin is a buf codegen plugin `de generate` makes available on PATH.
type Plugin struct {
	// Bin is the executable name buf looks up (the `local:` name in buf.gen.yaml).
	Bin string
	// Module is the `go install` target.
	Module string
	// Version is the pinned version.
	Version string
}

// BufPlugins are the PUBLIC codegen plugins `de generate` knows how to pin. A
// scaffold's buf.gen.yaml references them by bare name (`local: protoc-gen-go`,
// ...); `de generate` installs the ones a given buf.gen.yaml actually references,
// at these pinned versions, into a cache bindir it puts first on PATH — so buf
// resolves the pinned plugins rather than whatever is on the host PATH. The SDK
// scaffold additionally references SDK-provided plugins (see IsSDKPlugin), which
// are pinned to the service's SDK version, not to a version baked in here.
var BufPlugins = []Plugin{
	{Bin: "protoc-gen-go", Module: ProtocGenGoModule, Version: ProtocGenGoVersion},
	{Bin: "protoc-gen-go-grpc", Module: ProtocGenGoGRPCModule, Version: ProtocGenGoGRPCVersion},
	{Bin: "protoc-gen-grpc-gateway", Module: ProtocGenGRPCGatewayModule, Version: ProtocGenGRPCGatewayVersion},
	{Bin: "protoc-gen-openapiv2", Module: ProtocGenOpenAPIV2Module, Version: ProtocGenOpenAPIV2Version},
}

// PublicPlugin returns the pinned public plugin for a buf `local:` bin name, and
// whether `de` knows how to pin it.
func PublicPlugin(bin string) (Plugin, bool) {
	for _, p := range BufPlugins {
		if p.Bin == bin {
			return p, true
		}
	}
	return Plugin{}, false
}

// sdkPluginBins are the buf `local:` plugin bin names the SDK scaffold's
// buf.gen.yaml references that are PROVIDED BY the devedge-sdk module (installed
// at the service's SDK version). protoc-gen-storage is the gorm path;
// protoc-gen-ent is the ent path; a given buf.gen.yaml references one, not both.
var sdkPluginBins = map[string]bool{
	"protoc-gen-devedge-authz": true,
	"protoc-gen-svc":           true,
	"protoc-gen-storage":       true,
	"protoc-gen-ent":           true,
}

// IsSDKPlugin reports whether a buf `local:` plugin bin name is provided by the
// devedge-sdk module (so it is pinned to the service's SDK version, not by `de`).
func IsSDKPlugin(bin string) bool { return sdkPluginBins[bin] }

// SDKCmd returns the `go install` target for a devedge-sdk command (a codegen
// plugin or the openapiv2to3 tool) by its binary name: SDKModule/cmd/<bin>.
func SDKCmd(bin string) string { return SDKModule + "/cmd/" + bin }

// Ref returns the "module@version" form used by `go run` / `go install`.
func Ref(module, version string) string { return module + "@" + version }
