# Implementation Plan: Service kind in devedge configuration

**Branch**: `002-service-config-kind` | **Date**: 2026-05-31 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/002-service-config-kind/spec.md`

## Summary

Add a `Service` kind to the devedge project configuration alongside the existing `Config` kind,
under the same `devedge.infoblox.dev/v1alpha1` API version. A `Service` declares a development
hostname, runtime dependencies, and routes. The parser gains a small **kind-dispatch seam** (an
envelope read of `apiVersion`/`kind`, then a per-kind decoder) so that `Config` is untouched and
future kinds are additive. `Service` parsing is **strict** (unknown fields rejected) per the
clarification; `Config` stays **lenient** to preserve backward compatibility exactly. The
project-up flow consumes either kind through a common `Resource` interface: it registers routes
identically to today, and for a `Service` it additionally reports the declared dependencies and
states that starting them is not yet supported. Dependency *runtime* (start/stop/migrate/seed) is
out of scope for this feature.

## Technical Context

**Language/Version**: Go 1.25.5 (from `go.mod`)
**Primary Dependencies**: `gopkg.in/yaml.v3` (already in use); standard library
**Storage**: N/A (parses a local YAML file; no persistence added)
**Testing**: `go test` — unit (`pkg/config`), integration (`test/integration`), e2e/k3d (`test/e2e`)
**Target Platform**: macOS + Linux (devedge supported platforms); parsing is platform-agnostic
**Project Type**: CLI + library (single Go module)
**Performance Goals**: Parsing/validation is sub-millisecond on realistic files; no perf-sensitive path added
**Constraints**: Zero backward-compatibility regression for `kind: Config`; no new third-party dependency
**Scale/Scope**: One config package + the project-up/down CLI wiring; ~2 new files + tests

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design.*

| Principle | Assessment |
|-----------|------------|
| **I. Edge-First Developer Experience** | PASS — increases config expressiveness (a service in one file), preserves names-over-ports, no new setup steps. `Config` files keep working unchanged. |
| **II. Spec-Driven, Test-Driven Delivery** | PASS — proceeding through Spec Kit; tests authored before implementation (parser/validation unit tests, project-up integration test). |
| **III. End-to-End Confidence Over Mocked Comfort** | PASS (with note) — the change touches the project-up route-registration path. Plan includes an integration test (load `Service` → register routes via the daemon) and an e2e check that a `Service` file is reachable over HTTPS where Docker/k3d is available; if unavailable, e2e is reported as skipped, never as passed. **Cluster model:** CI runs on a dedicated ephemeral k3d (deterministic); developer machines share one k3d/devedge daemon across projects (dedicated only via explicit opt-in). Tests MUST therefore be co-existence-safe — unique project name + hostnames and self-cleanup — so they pass on both shared and dedicated infrastructure. (See memory: dev-k3d-shared-cluster-model.) |
| **IV. Portable Core, Explicit Platform Adapters** | PASS — all new logic lives in the platform-agnostic `pkg/config` core; no platform adapters touched. The kind-dispatch seam is pure core. |
| **V. Safe Reconciliation and Observable Operations** | PASS — validation failures are explicit and actionable (name the kind, field, dependency, supported set); project-up output makes declared dependencies and the "not yet started" state observable. No silent recovery. |

**Result: PASS — no violations.** Complexity Tracking section intentionally empty.

## Project Structure

### Documentation (this feature)

```text
specs/002-service-config-kind/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   └── service-config.md
└── tasks.md             # Phase 2 output (/speckit.tasks — NOT created here)
```

### Source Code (repository root)

```text
pkg/config/
├── project.go           # EXISTING — Config kind. Unchanged except: ParseProject reused as the
│                         #   Config decoder by the new dispatch. No behavior change.
├── resource.go          # NEW — kind-dispatch seam: typeMeta envelope, Resource interface,
│                         #   DependencyDeclarer, ParseResource/LoadResource, supported-kind registry.
├── service.go           # NEW — Service kind: ServiceConfig/ServiceSpec/ServiceDev/Dependency
│                         #   types, strict (KnownFields) parsing, validation, Project()/ToRoutes()/
│                         #   Dependencies().
├── project_test.go      # EXISTING — unchanged (guards Config back-compat).
├── resource_test.go     # NEW — dispatch + unsupported/missing-kind errors; Config routes via dispatch.
└── service_test.go      # NEW — Service parse/validate happy + every invalid-config case (FR-005/007).

cmd/de/
└── main.go              # MODIFIED — projectUpCmd & projectDownCmd use config.LoadResource instead
                          #   of LoadProject; project-up reports dependencies (FR-009) and empty-routes.

test/integration/
└── service_config_test.go  # NEW — load a Service file, register its routes through the daemon,
                             #   assert parity with an equivalent Config file.
```

**Structure Decision**: Single Go module (CLI + library). New code is confined to `pkg/config`
(the portable core) plus minimal wiring in `cmd/de/main.go`. `project.go` is reused, not rewritten.

## Architecture decisions

1. **Kind-dispatch seam (FR-001, FR-010).** A `typeMeta` envelope decodes `apiVersion`/`kind`
   only; `ParseResource` validates the API version, then switches on kind to a per-kind decoder
   and returns a `Resource`. Adding a future kind = add a `case` + a decoder file; no existing
   kind's handling changes. Supported kinds are listed from one place so error messages
   (FR-006) stay in sync.

2. **`Resource` interface as the CLI's view (FR-008).**
   `Resource { Project() string; ToRoutes() ([]types.Route, error) }`. Both `*ProjectConfig`
   (existing) and `*ServiceConfig` (new) implement it. The CLI registers `ToRoutes()` and reads
   `Project()` without knowing the concrete kind — so route registration is literally the same
   code path for both kinds.

3. **Dependency reporting via optional interface (FR-009).**
   `DependencyDeclarer { Dependencies() []Dependency }`. Only `*ServiceConfig` implements it. The
   CLI type-asserts; if present, it prints the count + names and the "starting dependencies is
   not yet supported" line. `Config` is unaffected.

4. **Strict for Service, lenient for Config (clarification A; FR-002, FR-007).** `Service`
   decoding uses `yaml.Decoder` with `KnownFields(true)` so unknown fields error. `Config`
   continues to use the existing lenient `yaml.Unmarshal` via the untouched `ParseProject`. This
   is the single most important back-compat guarantee in the plan.

5. **Validation lives in `ServiceConfig.Validate()` (FR-005).** Called by `parseService` after
   decode. Each failure names the specific problem (missing field + which dependency, duplicate
   name, unrecognized engine + the recognized set, out-of-range port, invalid hostname).

## Complexity Tracking

> No Constitution Check violations — section intentionally empty.
