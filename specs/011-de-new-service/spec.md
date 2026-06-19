# Feature Specification: `de new service` — one-command apx-native service scaffold

**Feature Branch**: `feat/011-de-new-service`
**Created**: 2026-06-19
**Status**: Draft
**Input**: WS-004 Phase 2 (development-hub) — deliver the long-promised `de init` from the product
vision as a thin driver over the devedge-sdk apx-native service scaffold.
**Initiative**: development-hub `specs/devedge-apx-scaffolding-proposal.md` §4.3 ("Where `de` fits").

## Background

`product_vision.md` headlines `de init foo` / `de project init` as the developer's first command, but
"project templates" was deferred to *Recommended v2 scope* and never built as an apx-native scaffold.
Meanwhile devedge-sdk now ships that scaffold as a CLI (`devedge-sdk new service`, released v0.12.0): it
declares a service's **public** API surface as an apx `app`-role module (authz-gated proto) and generates
the **private** implementation (models, repository, server) with `buf` + the SDK plugins. Onboarding a new
service by hand is ~10 error-prone steps (Appendix A of the proposal); the scaffold collapses them.

This feature delivers the devedge half of that promise: `de new service <name>` orchestrates the
devedge-sdk scaffold and then adds the **devedge-native value-add** — a `devedge.yaml` that routes the new
service's HTTP/JSON gateway through the local edge, so `de project up` immediately serves it over stable
HTTPS.

`de` is a **thin driver**, not a second scaffold engine. devedge-sdk's scaffold package is `internal/`, so
it cannot be imported; `de` shells out to the `devedge-sdk` binary on PATH and forwards the flags. The
heavy logic stays in devedge-sdk, versioned with the plugins it wires.

Note: this is distinct from feature 007 (`de project init`), an in-tree, embedded-template scaffold that
remains in place. The two coexist: 007 is self-contained; `de new service` drives the external apx-native
SDK scaffold.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Scaffold a service and route it through the edge (Priority: P1) 🎯 MVP

A developer runs `de new service orders --resource Order`. `de` confirms `devedge-sdk` is installed,
forwards the request to `devedge-sdk new service orders --resource Order`, and — once the scaffold
succeeds — writes a `devedge.yaml` in the new project routing `orders.dev.test` → `http://127.0.0.1:8080`
(the scaffold's HTTP gateway port). It then prints next steps (`cd`, `de project up`, the gateway URL).

**Why this priority**: This is the entire feature — the minimal-scaffold promise from the product vision.

**Independent Test**: With a stubbed exec runner (no apx/buf toolchain), assert `de new service` builds
the exact `devedge-sdk` invocation and emits a `devedge.yaml` the project-config parser accepts.

**Acceptance Scenarios**:

1. **Given** `devedge-sdk` is NOT on PATH, **When** the developer runs `de new service orders`, **Then**
   the command fails before scaffolding with an actionable message naming the install command
   (`go install github.com/infobloxopen/devedge-sdk/cmd/devedge-sdk@v0.12.0`).
2. **Given** `devedge-sdk` IS on PATH, **When** the developer runs
   `de new service orders --resource Order --backend gorm`, **Then** `de` invokes
   `devedge-sdk new service orders --resource Order --backend gorm` and, on success, writes
   `orders/devedge.yaml`.
3. **Given** a successful scaffold, **When** `de` emits the `devedge.yaml`, **Then** it is a valid
   `kind: Config` document (`apiVersion: devedge.infoblox.dev/v1alpha1`) with one route
   `host: orders.dev.test → upstream: http://127.0.0.1:8080`, accepted by the same loader `de project up`
   uses (`config.ParseResource`).
4. **Given** the scaffold step itself fails, **When** `de` returns the error, **Then** no `devedge.yaml`
   is written (no half-output) and the exit code is non-zero.
5. **Given** extra devedge-sdk flags after a `--` separator
   (`de new service orders -- --module github.com/acme/orders --force`), **When** `de` invokes the SDK,
   **Then** those flags are forwarded verbatim and in order.

## Requirements *(mandatory)*

- **FR-001** `de new service <name>` MUST require the `devedge-sdk` binary on PATH and, when absent, fail
  with an actionable message including the pinned install command (v0.12.0) — before any scaffolding.
- **FR-002** `de` MUST forward to `devedge-sdk new service <name>` with `--resource`, `--backend`, `--dir`
  passed through when set, plus any flags after a `--` separator forwarded verbatim. `de` MUST NOT
  reimplement scaffold logic.
- **FR-003** On scaffold success, `de` MUST emit a `devedge.yaml` in the project directory routing
  `<name>.dev.test` → `http://127.0.0.1:8080` (the scaffold gateway port), valid `kind: Config` against the
  existing project-config parser. `de` MUST NOT overwrite an existing `devedge.yaml`.
- **FR-004** On scaffold failure, `de` MUST surface the error and MUST NOT write a `devedge.yaml`.
- **FR-005** `de` MUST print next steps: `cd <dir>`, `de project up`, and the gateway URL
  (`https://<name>.dev.test/v1/...`).

### Failure modes

- `devedge-sdk` missing → actionable install message, non-zero exit, nothing written (FR-001).
- Scaffold (apx/buf) failure → error surfaced, no `devedge.yaml` (FR-004).
- Empty service name → clear error.
- Pre-existing `devedge.yaml` in the target dir → refuse to overwrite.

### Default gateway port — derivation

The devedge-sdk scaffold's generated `server/main.go` listens on HTTP `:8080` (gRPC `:9090`), fixed in its
template (not flag-configurable). `de` therefore routes the emitted host to `http://127.0.0.1:8080`. This
is pinned in `internal/sdkscaffold.GatewayPort`; if the scaffold's default ever changes, that constant
changes to match.

## Scope

**In scope**: the `de new service` thin driver (preflight, flag forwarding, devedge.yaml emission,
next-steps) + its unit/driver test.

**Out of scope** (scope-discipline): reimplementing any scaffold logic (apx/buf/proto/codegen — owned by
devedge-sdk and covered by its e2e tests); speculative subcommands beyond `new service`; configurable
gateway ports; modifying the existing 007 in-tree scaffold.

## Verification (after_implement gate)

- Functional: `make build`, `make lint` (go vet), `make test` (unit + e2e + integration) — all green.
  k3d e2e is **not** required: this change adds a CLI driver that shells out and writes a static YAML file;
  it touches no routing/DNS/cert/daemon runtime path (`.specify/extensions.yml` after_implement criteria).
  A real-toolchain e2e (apx + buf + devedge-sdk on PATH) was additionally exercised by hand and confirmed
  the scaffold ran and the routed `devedge.yaml` was emitted.
- Scope: the diff is the `cmd/de/new.go` command, the `internal/sdkscaffold` driver, and this spec — all
  trace to FR-001..005; no scaffold logic reimplemented.
