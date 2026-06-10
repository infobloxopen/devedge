# Implementation Plan: Service scaffold for the onboarding walk-through

**Branch**: `007-service-scaffold-onboarding` | **Date**: 2026-06-10 | **Spec**: `specs/007-service-scaffold-onboarding/spec.md`
**Input**: Feature specification + clarifications (de project init / grpc-gateway / webhook-endpoint example)

## Summary

Add `de project init <name>`: generate a complete, buildable, authz-governed service project from
templates embedded in the `de` binary, shaped exactly for the existing substrate (`Service` kind →
`up` provisions Postgres + applies migrations (003/006), `--deploy` builds/imports the image and
runs the in-cluster schema job (005/006)). The generated service uses the public devedge-sdk
(fail-closed gRPC authz interceptor + boot gate, rules read by reflection from the canonical
`infoblox.authz.v1` annotations) and serves CRUD for a webhook-endpoint example resource over gRPC
+ grpc-gateway behind the project's dev hostname. The feature's gate is the automated onboarding
walk-through e2e: init → rename-resource script → regenerate → up → CRUD probe → deploy → down.

## Technical Context

**Language/Version**: Go (devedge module; generated project targets the same Go release)
**Primary Dependencies (devedge side)**: cobra (existing CLI), `embed` + `text/template` (new
scaffold package). No new daemon/runtime deps — the scaffold is CLI-side only.
**Primary Dependencies (generated project)**: `github.com/infobloxopen/devedge-sdk` (authz +
grpcauthz + authzpb), `github.com/infobloxopen/apis/proto/infoblox/authz` (canonical annotation,
v1.0.0-alpha.2+), grpc, grpc-gateway runtime, pgx (Postgres), `infobloxopen/migrate` fork (the 006
`migrate` subcommand contract). Generation toolchain: buf, protoc-gen-go, protoc-gen-go-grpc,
protoc-gen-grpc-gateway — **no** protoc-gen-devedge-authz (rules are extracted at runtime via
devedge-sdk `authzpb`, removing a toolchain dependency).
**Storage**: the generated project owns one Postgres table (webhook_endpoints) via 006 migrations.
**Testing**: devedge unit tests for the scaffold (name validation, refusal paths, template
rendering); a generated-project smoke (init → `go build` + `go test` in a temp dir) in
integration tests; the walk-through e2e against live k3d in `test/e2e/` (003–006 harness style).
**Target Platform**: macOS + Linux dev hosts (same as `de`).
**Project Type**: single project (devedge repo) + an embedded template tree.
**Performance Goals**: SC-001 — init+build+test of the generated project < 5 min on a dev machine.
**Constraints**: dk-compat surfaces untouched (no daemon API changes at all); `de project up`
/`down`/`chart` behavior unchanged for existing projects; generated project must build offline
after `go mod download` (protos vendored, no BSR network dependency).
**Scale/Scope**: one new CLI subcommand, one new internal package (`internal/scaffold`), an
embedded template tree (~15 files), one e2e, zero daemon changes.

## Constitution Check

- **I. Edge-First DX** — PASS: the scaffold's whole point; output works with stable FQDN + HTTPS
  via existing routing, no manual proxy config.
- **II. Spec-Driven, Test-Driven** — PASS: tasks order tests first (scaffold unit tests, generated
  smoke, walk-through e2e) and trace to FR-001..011.
- **III. End-to-End Confidence Over Mocked Comfort** — PASS: the gate is a live-k3d walk-through
  e2e (init → up → CRUD over HTTPS → deploy), like 003–006. Generated-project unit tests use a
  fake store (no DB) only for the *scaffold smoke*; the e2e exercises real Postgres.
- **IV. Portable Core, Explicit Platform Adapters** — PASS: templates are embedded (portable);
  no platform-specific paths beyond what `de` already handles.
- **V. Safe Reconciliation & Observable Ops** — PASS: no reconciler/daemon changes; init is
  strictly non-destructive (refuses non-empty targets; validates before writing).

No violations → Complexity Tracking not needed.

## Project Structure

### Documentation (this feature)

```
specs/007-service-scaffold-onboarding/
├── spec.md
├── plan.md              # this file
└── tasks.md
```

(The generated-project shape below doubles as the contract; the walk-through e2e is the
executable contract.)

### Source Code (devedge repository)

```
cmd/de/
├── main.go                      # + projectInitCmd() wired into projectCmd()
└── project_init.go              # NEW: flag parsing, name validation, calls internal/scaffold

internal/scaffold/
├── scaffold.go                  # NEW: Render(name, module, dir) — validate, refuse-overwrite,
│                                #      walk embedded templates, substitute, write
├── scaffold_test.go             # NEW: validation/refusal/rendering unit tests
└── templates/                   # NEW: embedded (embed.FS), .tmpl suffix where substituted
    ├── devedge.yaml.tmpl        # kind: Service — hostname <name>.dev.test, workload.build
    │                            #   (context ., port 8080), dependencies: db postgres
    │                            #   (+ storage: migrations db/migrations), routes
    ├── go.mod.tmpl              # module {{.Module}} (default: the service name; --module to override)
    ├── proto/{{name}}/v1/webhook_endpoint.proto.tmpl
    │                            # WebhookEndpoint {url, secret, event_filters[]};
    │                            # Create/Get/List/Update/Delete RPCs; every RPC carries
    │                            #   (infoblox.authz.v1.rule) {verb, resource: "webhook-endpoint"};
    │                            #   google.api.http bindings for the gateway
    ├── proto/infoblox/authz/v1/authz.proto      # mirror, codegen input only (devedge-sdk pattern)
    ├── third_party/google/api/{annotations,http}.proto  # vendored for offline gateway codegen
    ├── buf.yaml.tmpl / buf.gen.yaml.tmpl        # go + go-grpc + grpc-gateway; module= mapping
    ├── cmd/{{name}}/main.go.tmpl                # subcommands: serve | migrate (006 C2 contract)
    │                            # serve: resolve fsnotify:// DSN file → real DSN (003 convention),
    │                            #   open pgx pool, grpc server + grpcauthz.UnaryServerInterceptor
    │                            #   (rules via authzpb.RulesFromGlobal, boot gate ON, DevAuthorizer),
    │                            #   grpc-gateway mux on :8080 (REST+gRPC one port)
    ├── internal/server/server.go.tmpl           # service impl over a small Store interface
    ├── internal/server/server_test.go.tmpl      # fake-store unit tests (no DB needed)
    ├── internal/store/postgres.go.tmpl          # pgx implementation
    ├── db/migrations/001_webhook_endpoints.{up,down}.sql
    ├── Dockerfile.tmpl          # multi-stage; ENTRYPOINT supports `serve` and `migrate` (006 C2)
    ├── AGENTS.md.tmpl           # curated: layout, rename flow, generate cmd, up/--deploy/down,
    │                            #   where authz declarations live (FR-009)
    ├── README.md.tmpl
    └── Makefile.tmpl            # generate / build / test / run targets

test/integration/
└── scaffold_smoke_test.go       # NEW: init into t.TempDir(); go build + go test the output
                                 #   (network-gated: needs module downloads; skip with -short)

test/e2e/
└── scaffold_onboarding_test.go  # NEW: the walk-through e2e (FR-010/SC-002):
                                 #   de project init → scripted rename (webhook_endpoint→note) →
                                 #   buf generate → up → HTTPS CRUD probe (gateway) + deny probe →
                                 #   boot-gate probe (drop one annotation → start fails, US1.3) →
                                 #   up --deploy → CRUD probe → down
```

## Key Design Decisions

1. **Templates embedded in `de`** (`embed.FS` + `text/template`): the scaffold version-locks with
   the substrate that consumes its output; offline-capable; no fetch step. Files needing
   substitution use `.tmpl`; static files copied verbatim. Substitutions: `{{.Name}}` (service +
   hostname + Helm slug), `{{.Module}}`, `{{.GoVersion}}`, pinned dependency versions.
2. **Reflection-based rule extraction** (devedge-sdk `authzpb.RulesFromGlobal`) instead of the
   `protoc-gen-devedge-authz` plugin: one less toolchain binary for the developer; the boot gate
   still enforces completeness. The plugin remains available for services that want generated
   tables — out of scope here.
3. **Module path default = service name** (e.g. `module webhooks`): valid Go, builds immediately
   with zero placeholders (FR-003); `--module github.com/acme/webhooks` for real repos. AGENTS.md
   documents the rename.
4. **Gateway and gRPC on one port** (mux on 8080): the Service config routes one upstream; keeps
   devedge.yaml minimal. REST is the probe surface in the e2e (CRUD over HTTPS through the dev
   hostname).
5. **`migrate` subcommand implements 006 C2 exactly** (`<image> migrate up`, DATABASE_URL from
   the dep Secret, `infobloxopen/migrate` fork with persisted-down-store): the scaffold's deploy
   path rides feature 006 unchanged. Template go.mod carries the same require/replace pair
   devedge itself uses (006 T002 finding: the fork keeps the upstream module path → replace).
6. **Vendored protos for codegen** (`google/api/*`, the authz-annotation mirror): `buf generate`
   works offline and without BSR auth; the *Go types* still come from the canonical released
   modules (FR-011) — the mirrors are codegen inputs only, never sources of Go code, exactly the
   devedge-sdk Phase-B pattern.
7. **Prerequisite (cross-repo, recorded as a task): tag `devedge-sdk v0.1.0`** so the generated
   go.mod pins released artifacts (FR-011) instead of a pseudo-version.

## Failure modes addressed

- Init into non-empty target → refuse with reason (FR-008); invalid name (module/hostname/Helm
  constraints) → reject before writing anything.
- Missing toolchain (buf/protoc plugins) → Makefile `generate` preflights and names exactly
  what's missing (edge case from spec); partial generation cannot make the tree unbuildable
  because the scaffold ships generated-code-free and the smoke test runs generate first.
- Canonical module unreachable → surfaces at `go mod download`/build with the standard Go error;
  AGENTS.md notes GOPRIVATE is *not* needed (public modules only).
- Hostname collision with an existing registered Service → existing `up` route-conflict behavior;
  e2e asserts regression suites stay green (SC-005).
