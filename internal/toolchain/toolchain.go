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
)

// Tool module paths. These are the `go run`/`go install` targets.
const (
	BufModule          = "github.com/bufbuild/buf/cmd/buf"
	KoModule           = "github.com/google/ko"
	GolangciLintModule = "github.com/golangci/golangci-lint/v2/cmd/golangci-lint"

	ProtocGenGoModule          = "google.golang.org/protobuf/cmd/protoc-gen-go"
	ProtocGenGoGRPCModule      = "google.golang.org/grpc/cmd/protoc-gen-go-grpc"
	ProtocGenGRPCGatewayModule = "github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway"
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

// BufPlugins are the codegen plugins the scaffold's buf.gen.yaml references by
// bare name (`local: protoc-gen-go`, ...). `de generate` installs exactly these
// pinned versions into a cache bindir and puts that dir first on PATH, so buf
// resolves the pinned plugins rather than whatever is on the host PATH.
var BufPlugins = []Plugin{
	{Bin: "protoc-gen-go", Module: ProtocGenGoModule, Version: ProtocGenGoVersion},
	{Bin: "protoc-gen-go-grpc", Module: ProtocGenGoGRPCModule, Version: ProtocGenGoGRPCVersion},
	{Bin: "protoc-gen-grpc-gateway", Module: ProtocGenGRPCGatewayModule, Version: ProtocGenGRPCGatewayVersion},
}

// Ref returns the "module@version" form used by `go run` / `go install`.
func Ref(module, version string) string { return module + "@" + version }
