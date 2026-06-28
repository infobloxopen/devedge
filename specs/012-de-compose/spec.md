# Feature Specification: `kind: Composition` + `de compose` — compose service modules into one binary

**Feature Branch**: `feat/ws-012-de-compose`
**Created**: 2026-06-28
**Status**: Draft
**Input**: WS-012 "Composable Services" Phase 4 (development-hub `specs/composable-services-proposal.md` §6).
**Initiative**: development-hub `specs/composable-services-proposal.md` (P4 row §9; §6 the resource + CLI;
§5.2/§5.3 what `de compose build` generates; §10-B static composition, no Go plugins).

## Background

WS-012 splits "a service" into an importable **Module** (domain behavior) and an executable **host**
(process behavior). The SDK side shipped as **devedge-sdk v0.28.0**: `servicekit.Module`/`Descriptor`/
`App`, `servicekit.Run(HostConfig{...})` (the composed host), `servicekit.ValidateDescriptors`/
`CompatibleModules`, and the `servicekittest.AssertComposition`/`AssertCompatible` harnesses.

P4 surfaces the host through devedge's CLI: a `kind: Composition` project-file resource plus a `de compose`
cobra command group that scaffolds, validates, generates, tests, and boots a composed "suite" binary from a
set of member modules. Composition is **static** — `de compose build` generates a `cmd/<name>/main.go` that
*imports* the member modules and calls `servicekit.Run`. **No Go plugins** (proposal §10-B, non-negotiable).
Deploy rendering (`de compose chart`) is a later phase (P6); this feature stubs it.

devedge already dispatches resource kinds (`ParseResource` over `supportedKinds`, `pkg/config/resource.go`)
and wires cobra groups (`newCmd()`/`projectCmd()` in `rootCmd()`, `cmd/de/main.go`). P4 is additive: a new
kind decoder + a new command group + a main.go/lock generator. It reuses existing kind dispatch, cobra
wiring, and the project route/dependency sequencing.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Compose two modules into one binary (Priority: P1) 🎯 MVP

A platform developer writes a `composition.yaml` (`kind: Composition`) listing two member modules with their
`module` import path + `@version`, `configPrefix`, and `database.schema`. `de compose build` generates a
`cmd/<name>/main.go` that imports both modules and calls `servicekit.Run(HostConfig{Modules: ...})`, a
`go.mod` for the composed binary, and a `composition.lock` pinning each module's version (plus
codegen/proto/migration version provenance). The generated binary compiles into a single composed host.

**Why this priority**: This is the core P4 deliverable — surfacing the static composed host through the CLI.

**Independent Test**: A fixture `kind: Composition` with ≥2 real modules → `de compose build` → the generated
`cmd/<name>/main.go` + `go.mod` + `composition.lock` compile into one binary.

**Acceptance Scenarios**:

1. **Given** a `kind: Composition` with two member modules, **When** `de compose build` runs, **Then** it
   writes `cmd/<name>/main.go` (imports both module packages; calls `servicekit.Run` with both `Module()`s),
   a `go.mod`, and a `composition.lock` pinning the module versions.
2. **Given** the generated `cmd/<name>/`, **When** it is compiled (`go build`), **Then** it builds a single
   composed binary with no errors.
3. **Given** a `kind: Composition` document, **When** it is parsed via `ParseResource`, **Then** it satisfies
   the `Resource` interface (`Project()` + `ToRoutes()`) and aggregates its modules' routes + shared deps.

### User Story 2 — Detect a composition conflict before building (Priority: P1)

`de compose tidy` resolves the member modules, validates the descriptor union (duplicate route prefix /
gRPC service / permission name; incoherent event graph) via `servicekit.ValidateDescriptors`, and checks
version compatibility via `servicekittest.CompatibleModules`. A deliberately-broken fixture (duplicate route
prefix or incompatible version) is reported as a conflict, non-zero exit.

**Independent Test**: A broken fixture (duplicate route prefix) → `de compose tidy` reports the conflict.

**Acceptance Scenarios**:

1. **Given** two modules declaring the same HTTP route prefix, **When** `de compose tidy` runs, **Then** it
   reports the duplicate-prefix conflict and exits non-zero.
2. **Given** a module whose `Requires.SDK` exceeds the host SDK version, **When** `de compose tidy` runs,
   **Then** it reports the incompatibility.
3. **Given** a conflict-free composition, **When** `de compose tidy` runs, **Then** it reports OK, exit 0.

### User Story 3 — Smoke-test a composition (Priority: P2)

`de compose test` invokes the composition smoke harness (`servicekittest.AssertComposition`) against the
composition's modules: descriptor validity, host boot over the union, clean shutdown. When the modules have
migrations + a shared DB is configured, the real-DB path runs (Docker required); otherwise it runs
in-process.

**Independent Test**: `de compose test` against the fixture runs `AssertComposition` and passes.

### User Story 4 — Manage membership + scaffold + boot (Priority: P2)

`de compose init <name>` scaffolds a `kind: Composition` file. `de compose add <module>@<ver>` / `remove
<module>` edit membership. `de compose up [--deploy]` reuses devedge's project route/dependency sequencing
to provision shared deps, register the modules' aggregated routes, and (later) boot the host.

## Requirements *(mandatory)*

- **FR-001**: A `kind: Composition` resource decoder (`ParseComposition`) MUST strictly decode the YAML shape
  in proposal §6.1 (`spec.runtime`, `spec.database`, `spec.modules[]` with `name` / `module` / `configPrefix`
  / `database.schema` / `failurePolicy`), validate required fields, and be registered in `supportedKinds` +
  the `ParseResource` switch.
- **FR-002**: `Composition` MUST satisfy `Resource` (`Project()`, `ToRoutes()` aggregating member routes) and
  `DependencyDeclarer` (the shared DB as a dependency). It MUST NOT break existing `Config`/`Service` dispatch.
- **FR-003**: `de compose build` MUST generate `cmd/<name>/main.go` calling `servicekit.Run` with each member
  module's `Module()`, a `go.mod`, and a `composition.lock` pinning module + codegen/proto/migration versions.
  Composition MUST be static (generated Go importing the modules); **no Go plugins**.
- **FR-004**: `de compose tidy` MUST resolve member modules and validate the descriptor union
  (`servicekit.ValidateDescriptors`) and version compatibility (`servicekittest.CompatibleModules`),
  reporting the first conflict with a non-zero exit; report OK on a clean composition.
- **FR-005**: `de compose test` MUST run `servicekittest.AssertComposition` against the composition's modules;
  report whether the real-DB path ran or was skipped (and why).
- **FR-006**: `de compose init/add/remove` MUST scaffold + edit a `kind: Composition` file.
- **FR-007**: `de compose up [--deploy]` MUST reuse devedge's existing project up sequencing (cluster resolve,
  dependency provision, route register) over the composition's aggregated routes + shared deps.
- **FR-008**: `de compose chart` MUST be a clearly-marked "not yet implemented (P6)" stub — NO chart rendering.
- **FR-009**: Existing `de new` / `de project` behavior MUST be unchanged.

### Key Entities

- **Composition** (`kind: Composition`): metadata.name + spec.runtime (mode/grpc/http) + spec.database
  (engine/dsnRef/isolation) + spec.modules[] (name/module ref@version/configPrefix/database.schema/
  failurePolicy/routes). Satisfies `Resource` + `DependencyDeclarer`.
- **composition.lock**: pins each member module's resolved version + the codegen/proto/migration provenance
  + the SDK + Go toolchain versions, for reproducible builds (proposal §6.2, §11 risk "module/SDK skew").

## Success Criteria *(mandatory)*

- **SC-001**: A `kind: Composition` fixture with ≥2 real modules → `de compose build` → the generated
  `cmd/<name>/main.go` + `go.mod` + `composition.lock` compile into a single composed binary.
- **SC-002**: `de compose tidy` detects a conflict on a deliberately-broken fixture (duplicate route prefix
  or incompatible version) and exits non-zero.
- **SC-003**: `de compose test` runs `AssertComposition` against the fixture; the real-DB path's run/skip is
  reported honestly.
- **SC-004**: `make build` + `make lint` + `make test` (unit + integration) pass; existing `de new` /
  `de project` behavior unchanged.

## Tasks

| ID | Task | Tag |
|----|------|-----|
| T001 | Bump `require github.com/infobloxopen/devedge-sdk` to v0.28.0; `go mod tidy`. | [S] |
| T002 | Add `Composition` type + `ParseComposition` + `KindComposition`; wire into `supportedKinds` + `ParseResource`. Satisfy `Resource` + `DependencyDeclarer`. Unit tests. | [C] |
| T003 | `internal/compose` package: module-ref parsing (`path@version`), main.go template, go.mod template, `composition.lock` model + writer. Unit tests (pure render). | [C] |
| T004 | Real fixture modules (≥2) under `internal/compose/testdata` exposing `Module()` constructors; a build-acceptance test that runs `de compose build` and `go build`s the result. | [C] |
| T005 | `de compose` cobra group: `init`/`add`/`remove`/`tidy`/`build`/`test`/`up`/`chart` (chart = P6 stub). Wire into `rootCmd()`. | [C] |
| T006 | `de compose tidy` via `servicekit.ValidateDescriptors` + `servicekittest.CompatibleModules`; conflict-detection test on a broken fixture. | [C] |
| T007 | `de compose test` via `servicekittest.AssertComposition`; report real-DB run/skip. | [S] |
| T008 | `make build` + `make lint` + `make test`; verification gate (functional + scope). | [S] |

## Out of Scope

- Chart/deploy rendering (`de compose chart`) — P6.
- Go plugins — forbidden (proposal §10-B).
- Changes to the SDK `servicekit`/`servicekittest` surfaces — consumed as-is from v0.28.0.
- New runtime / a parallel completeness gate — the SDK host owns boot validation.
