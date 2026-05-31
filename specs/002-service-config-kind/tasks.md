---
description: "Task list for Service kind in devedge configuration"
---

# Tasks: Service kind in devedge configuration

**Input**: Design documents from `/specs/002-service-config-kind/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/service-config.md

**Tests**: Included — Constitution II mandates test-first (red-green-refactor).

## Format: `[ID] [P?] [Complexity] [Story?] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Complexity]**: `[S]` simple/mechanical → Sonnet subagent; `[C]` complex → Opus. Per the
  agentic lifecycle in CLAUDE.md. An `[S]` task that fails the QA gate is re-tagged `[C]` and
  redone on Opus.
- **[Story]**: US1/US2/US3 for user-story phases only.
- Exact file paths are included in each description.

## Path Conventions

Single Go module. Config core in `pkg/config/`, CLI wiring in `cmd/de/`, integration tests in
`test/integration/`.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Establish the backward-compatibility baseline before any edits.

- [X] T001 [S] Confirm baseline is green on branch `002-service-config-kind`: run `make build` and `make test`; record that existing `pkg/config/project_test.go` (the `Config` back-compat oracle) passes unchanged.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The kind-dispatch seam and `Service` base types. **Blocks all user stories** — nothing can load a `Service` until this exists.

**⚠️ CRITICAL**: No user story work begins until this phase is complete.

### Tests (write first, must FAIL)

- [X] T002 [P] [C] Write failing tests in `pkg/config/resource_test.go`: `ParseResource` routes a `Config` document with parity to `ParseProject`; an unsupported kind errors and the message lists the supported kinds; missing `apiVersion`/`kind` each error naming the field.
- [X] T003 [P] [S] Write failing tests in `pkg/config/service_test.go`: a well-formed `Service` document decodes with `dev.hostname`, dependencies, and routes populated; an unknown field in a `Service` document is rejected (strict decode).

### Implementation

- [X] T004 [C] Implement the kind-dispatch seam in `pkg/config/resource.go`: `typeMeta` envelope (apiVersion/kind), `Resource` and `DependencyDeclarer` interfaces, `ParseResource`/`LoadResource`, a single supported-kind registry, and the unsupported/missing-kind errors. The `Config` case delegates to the existing `ParseProject` unchanged. (depends on T002)
- [X] T005 [S] Implement `ServiceConfig`/`ServiceSpec`/`ServiceDev`/`Dependency` types in `pkg/config/service.go` with strict decode (`yaml.NewDecoder` + `KnownFields(true)`), plus `Project()`, `ToRoutes()`, and `Dependencies()`. (depends on T003, T004)

**Checkpoint**: dispatch works; `Config` byte-for-byte unchanged; a `Service` document decodes.

---

## Phase 3: User Story 1 - Route a declared service (Priority: P1) 🎯 MVP

**Goal**: A `Service` file routes through devedge exactly like `kind: Config` does today.

**Independent Test**: Author a `Service` file with one route, run `de project up`, confirm the route registers and is reachable over HTTPS; `de project down` removes it.

### Tests (write first, must FAIL)

- [X] T006 [P] [S] [US1] Integration test in `test/integration/service_config_test.go`: load a `Service` file, register its routes through the daemon, and assert parity with an equivalent `Config` file; assert `project down` removes them. **Co-existence-safe:** use a unique project name + hostnames and clean up on teardown so the test passes against a shared devedge daemon / shared k3d (dev) and a dedicated one (CI).
- [X] T007 [P] [S] [US1] Unit test in `pkg/config/service_test.go`: `ServiceConfig.ToRoutes()` sets `Project`/`Source`/`TTL` like `ProjectConfig.ToRoutes()`; an empty `routes` list yields an empty slice (no error).

### Implementation

- [X] T008 [S] [US1] In `cmd/de/main.go` `projectUpCmd`, replace `config.LoadProject` + `cfg.ToRoutes()` with `config.LoadResource` + `res.ToRoutes()`; preserve identical `Config` behavior. (depends on T004, T005)
- [X] T009 [S] [US1] In `cmd/de/main.go` `projectDownCmd`, replace `config.LoadProject` with `config.LoadResource` and use `res.Project()`. (depends on T004)
- [X] T010 [S] [US1] In `projectUpCmd`, when the resource has no routes, tell the user no routes were declared (rather than a silent success). (depends on T008)

**Checkpoint**: User Story 1 fully functional — a `Service` file routes over HTTPS like `Config`.

---

## Phase 4: User Story 2 - Declare and validate runtime dependencies (Priority: P2)

**Goal**: Dependencies are declared, validated, and reported by `project up` — but not started.

**Independent Test**: Author a `Service` with two dependencies, run `project up`, confirm it reports them and states starting is not yet supported; introduce a malformed dependency and confirm a clear validation error.

### Tests (write first, must FAIL)

- [X] T011 [P] [S] [US2] Unit tests in `pkg/config/service_test.go` for dependency validation: missing `name`/`engine`/`port` (message names the dependency + attribute); duplicate dependency name; unrecognized engine (message lists recognized engines); port outside 1–65535.
- [X] T012 [P] [S] [US2] Unit test for the dependency-reporting helper used by the CLI: given declared dependencies it produces the count, names, and the "starting dependencies is not yet supported" line; given none it produces nothing.

### Implementation

- [X] T013 [S] [US2] Implement `ServiceConfig.Validate()` dependency rules in `pkg/config/service.go` (required attrs, unique names, recognized engines `postgres`/`redis`, port range), invoked by the `Service` decoder; each failure names the specific problem. (depends on T005)
- [X] T014 [S] [US2] In `cmd/de/main.go` `projectUpCmd`, report declared dependencies via the helper from T012 when the resource implements `DependencyDeclarer`; no output when it declares none. (depends on T008, T012)

**Checkpoint**: dependencies validated and reported; `Config` files show no dependency output.

---

## Phase 5: User Story 3 - Clear errors for unsupported or malformed configuration (Priority: P3)

**Goal**: Mistyped kinds, missing fields, and bad hostnames produce actionable errors.

**Independent Test**: Load files with an unknown kind, a missing `apiVersion`, a missing name, and an invalid hostname; confirm each error names the specific problem.

### Tests (write first, must FAIL)

- [X] T015 [P] [S] [US3] Unit tests in `pkg/config/resource_test.go` and `pkg/config/service_test.go`: unsupported kind lists supported kinds; missing `apiVersion`/`kind`/`metadata.name` each name the field; empty/invalid `dev.hostname` rejected; unknown `Service` field rejected naming the field; invalid YAML and empty file fail without panic.

### Implementation

- [X] T016 [S] [US3] Implement `dev.hostname` validation and the remaining top-level field checks in `pkg/config/service.go`, and confirm the unsupported/missing-kind messages from T004 match the contract in `contracts/service-config.md`. (depends on T004, T013)

**Checkpoint**: every invalid configuration enumerated in the spec is rejected with a specific message (SC-002).

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T017 [P] [S] Document the `Service` kind in `README.md` (schema + a pointer to `specs/002-service-config-kind/contracts/service-config.md`).
- [X] T018 [P] [S] Add package/doc comments in `pkg/config/resource.go` describing the kind-dispatch extension point, so adding a future kind is obviously additive (FR-010).
- [X] T019 [S] Run the QA gate (`/verify-change`): `make build` + `make lint` + unit + integration; e2e (`go test ./test/e2e/...`) — CI provides a dedicated k3d so e2e runs there; on a dev machine it uses the shared k3d if present, else report skipped (never claim passed). Tests must be co-existence-safe (unique names, self-cleanup). Then the scope check against this spec's acceptance criteria.

---

## Dependencies & Execution Order

- **Setup (T001)** → no deps.
- **Foundational (T002–T005)** → after Setup; **blocks all stories**. T004 depends on T002; T005 on T003+T004.
- **US1 (T006–T010)** → after Foundational. T008 depends on T004+T005; T009 on T004; T010 on T008.
- **US2 (T011–T014)** → after Foundational; T013 on T005, T014 on T008+T012. Independently testable from US1.
- **US3 (T015–T016)** → after Foundational; T016 on T004+T013. Independently testable.
- **Polish (T017–T019)** → after the stories being delivered are complete. T019 (QA gate) runs last.

## Parallel Opportunities

- T002 ∥ T003 (different test files).
- Within each story, the `[P]` test tasks run together (T006∥T007; T011∥T012).
- T017 ∥ T018 (docs vs code comments, different files).

## Model routing summary (for `/speckit.implement`)

- **`[C]` (Opus):** T002, T004 — the kind-dispatch seam (design-bearing).
- **`[S]` (Sonnet subagents):** T001, T003, T005–T019 — mechanical, pattern-following work.
- Escalate any `[S]` task that fails the QA gate to `[C]`/Opus and note it.

## Implementation Strategy

- **MVP = Setup + Foundational + US1** (T001–T010): a `Service` file that routes over HTTPS.
  Stop and validate independently before US2/US3.
- US2 and US3 are additive increments, each independently testable, neither breaking US1.
