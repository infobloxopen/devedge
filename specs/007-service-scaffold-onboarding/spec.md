# Feature Specification: Service scaffold for the onboarding walk-through

**Feature Branch**: `007-service-scaffold-onboarding`
**Created**: 2026-06-10
**Status**: Draft
**Input**: User description: "service scaffold for the onboarding walk-through"

## Context

devedge's substrate is complete (features 002–006: `Service` kind, dependency runtime,
shared/ephemeral clusters, opt-in deploy, migrations + seed), and the service-framework layer's
authz axis exists (devedge-sdk: declared method rules, fail-closed interceptor + boot gate, the
canonical `infoblox.authz.v1` annotation released via apx). What's missing is the connective
tissue the platform vision calls **the product**: the onboarding walk-through — *a developer new
to the platform takes a service from nothing to deployed-on-dev in under one business day*.

This feature adds the scaffold step that makes the walk-through start: `de` generates a new,
buildable, authz-governed service project whose shape is exactly what the substrate already knows
how to run (`de project up` → deps + migrations; `--deploy` → in-cluster). The walk-through —
scaffold → declare resource + authz in proto → generate → `up` → CRUD over stable HTTPS →
`--deploy` — is this feature's acceptance test, exercised end-to-end. Deliberately rough: no
generated CRUD bodies, no AIP semantics beyond a plain REST mapping, no security-check tool;
those are later framework features, earned from what this walk-through teaches.

## Clarifications

### Session 2026-06-10

- Q: What is the scaffold command? → A: **`de project init <name>`** — the project lifecycle
  starts under the same `de project` family it continues in (`up`/`down`/`chart`/`ci`).
- Q: REST mapping technology for CRUD-over-HTTPS? → A: **grpc-gateway** — the vision's settled
  Tier-1 default (gRPC + grpc-gateway); the scaffold carries the gateway generator from day one
  so the walk-through proves the real target shape.
- Q: The example placeholder resource? → A: **Webhook endpoint** (URL, secret, event filters) —
  seeds toward the vision's flagship pentest service (Webhook Endpoint Registry) and surfaces the
  secret-field handling question early; still designed to be renamed.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Scaffold a new service project (Priority: P1) 🎯 MVP

A developer runs one `de` command with a service name and gets a new project directory that
compiles, passes its own tests, and is immediately understood by `de project up`: a Service
config declaring a dev hostname and a Postgres dependency, a proto defining one example resource
with canonical authz annotations on every RPC, generated Go bindings and authz rule tables, a
runnable server wired fail-closed (undeclared methods refuse to boot), an initial schema
migration, and a containerfile so `--deploy` works. The example resource is a placeholder the
developer renames — the scaffold is a starting shape, not a framework runtime.

**Why this priority**: This is the missing first step of the onboarding walk-through; everything
else in the walk-through already exists. Without scaffolding, "new service" means copying an
existing repo by hand — the exact failure mode the platform exists to remove.

**Independent Test**: Run the scaffold command with a fresh name in an empty directory; the
generated project builds, its tests pass, and its config validates — without starting devedge.

**Acceptance Scenarios**:

1. **Given** an empty directory and a service name, **When** the developer runs the scaffold
   command, **Then** a project is created containing: a devedge `Service` config (dev hostname,
   one Postgres dependency, route to the service), a proto with one example resource service
   whose every RPC carries a canonical authz annotation (`infoblox.authz.v1.rule`), generated Go
   code (messages, gRPC, REST gateway, authz rule table), a server entrypoint wired with the
   fail-closed authz interceptor + boot-time completeness gate, an initial migration, a
   containerfile, and a short AGENTS.md describing the project's shape for agentic tools.
2. **Given** a freshly scaffolded project, **When** the developer runs the project's build and
   tests, **Then** both succeed with no manual edits.
3. **Given** a scaffolded project, **When** the developer adds a new RPC to the proto *without*
   an authz annotation and regenerates, **Then** the service refuses to start, naming the
   undeclared method (fail-closed gate observable on day one).
4. **Given** a name that already exists in the target directory, **When** the developer runs the
   scaffold command, **Then** it refuses to overwrite and says why (no destructive defaults).

---

### User Story 2 - The walk-through: scaffold → up → CRUD over HTTPS (Priority: P1)

The developer continues: `de project up` brings up Postgres on the shared dev cluster, applies
the initial migration, and the scaffolded service starts locally (local-run default), reads its
DSN the devedge way (hotload file), and serves the example resource's CRUD over the project's
stable HTTPS dev hostname — gRPC and the REST mapping both work, and requests are
authz-governed (an allowed request succeeds; the dev authorizer's deny path returns
permission-denied).

**Why this priority**: Equal-P1 because the scaffold's only measure of correctness is that the
substrate accepts it without friction. A scaffold that needs hand-fixing before `up` fails the
mission.

**Independent Test**: From a scaffolded project: `de project up`, then exercise create/read over
the dev hostname with HTTPS; tear down with `de project down`.

**Acceptance Scenarios**:

1. **Given** a scaffolded project and a running devedge, **When** the developer runs
   `de project up` and starts the service, **Then** the service boots (gate satisfied), connects
   to its provisioned database through the emitted DSN, and serves requests on its dev hostname
   over trusted HTTPS.
2. **Given** the running scaffolded service, **When** the developer creates and reads an example
   resource via the REST mapping, **Then** the operations succeed and the data round-trips
   through Postgres (not memory).
3. **Given** the running scaffolded service, **When** a request arrives for a method the dev
   authorizer denies, **Then** the service returns permission-denied — enforcement is on by
   default, not opt-in.

---

### User Story 3 - The walk-through ends deployed (Priority: P2)

The developer finishes the day with `de project up --deploy`: the scaffolded service's image is
built and imported, migrations run in-cluster before serving (006 mechanics), and the same CRUD
works over the same dev hostname — now served by the in-cluster workload.

**Why this priority**: P2 only because local-run (US2) already proves the scaffold↔substrate
contract; deploy reuses 005/006 mechanics. It completes the walk-through's last step.

**Independent Test**: From the US2 state: `de project up --deploy`, re-run the CRUD probe,
`de project down`.

**Acceptance Scenarios**:

1. **Given** a scaffolded project, **When** the developer runs `de project up --deploy`, **Then**
   the image builds from the scaffolded containerfile, the schema job brings the database to the
   declared version, and the workload serves the same CRUD over the dev hostname.
2. **Given** the deployed scaffolded service, **When** the developer runs `de project down`,
   **Then** the workload is removed and the project can return to local-run.

---

### User Story 4 - The measure: under one business day, agent-legible (Priority: P3)

A developer (or an agent) who has never seen the scaffold completes the whole walk-through —
scaffold, rename the example resource to their own, declare authz, regenerate, `up`, CRUD,
`--deploy` — guided only by the scaffold's AGENTS.md and command help. The elapsed effort is
recorded.

**Why this priority**: This is the vision's acceptance metric ("< 1 business day, run early and
repeatedly — it is the product"). P3 because it is a measurement over US1–US3 rather than new
machinery; it must still be performed and recorded, not assumed.

**Independent Test**: A scripted end-to-end run of the full walk-through (rename included)
recorded as a repeatable e2e; the human/agent variant documented in the feature's notes with the
observed duration.

**Acceptance Scenarios**:

1. **Given** only the scaffold output and its AGENTS.md, **When** the walk-through is executed
   end-to-end (including renaming the example resource), **Then** every step succeeds without
   consulting devedge source code, and the run is captured as an automated e2e.

---

### Edge Cases

- Scaffold target inside an existing git repo vs a fresh directory — both must work; the scaffold
  never creates or rewrites git history on the developer's behalf beyond the new files.
- Service name that is not a valid Go module / hostname / Helm release name (uppercase, spaces,
  leading digits) — rejected early with a clear message, before any files are written.
- Developer's machine lacks a generation toolchain dependency (e.g. buf or protoc plugins) — the
  scaffold's generate step must say exactly what is missing and how to get it, not fail mid-way
  with a partial tree. Partial generation must not leave the project unbuildable.
- The canonical annotation module is unreachable (offline, no module proxy access) — scaffold
  still produces the tree; the failure surfaces at generate/build with an actionable message.
- Scaffolded project name collides with an existing Service hostname already registered in
  devedge — `up` surfaces the conflict the same way it does today for route collisions (no
  silent hijack).
- Re-running the scaffold command on an already-scaffolded project — refuses (US1.4); a future
  "upgrade scaffold" story is explicitly out of scope.
- The walk-through's deny-path check must not depend on a real OPA/PARGs pipeline — the dev
  authorizer ships as default; swapping in the internal OPA adapter is configuration, not code,
  and is out of scope here.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: devedge MUST provide `de project init <name>` (optional target directory) which, generates a complete new service project with no placeholder left
  unbuildable: devedge Service config, annotated proto, generation config, server entrypoint,
  initial migration, containerfile, AGENTS.md, and project README.
- **FR-002**: Every RPC in the scaffolded proto MUST carry a canonical `infoblox.authz.v1.rule`
  annotation (or an explicit `public: true`), and the generated server MUST enforce them through
  the fail-closed interceptor with the boot-time completeness gate enabled — an undeclared
  method prevents startup.
- **FR-003**: The scaffolded project MUST build and pass its generated tests immediately after
  scaffolding, with the only external requirements being the documented toolchain (Go, buf,
  protoc plugins) — verified by the scaffold's own smoke test.
- **FR-004**: The scaffolded Service config MUST be accepted by the existing substrate unchanged:
  `de project up` provisions the declared Postgres dependency, applies the project's initial
  migration (006 mechanics), and emits the DSN the scaffolded server consumes (hotload file
  convention from 003).
- **FR-005**: The scaffolded service MUST serve the example resource's CRUD over gRPC and a REST
  mapping on the project's dev hostname via devedge HTTPS routing, persisting to the provisioned
  Postgres.
- **FR-006**: Authorization MUST be live in the scaffold's default run: allowed requests succeed,
  denied requests return permission-denied, using the SDK's development authorizer by default;
  the authorizer is swappable by configuration without changing generated code.
- **FR-007**: `de project up --deploy` MUST work for the scaffolded project as for any 005/006
  service: image build from the scaffolded containerfile, in-cluster schema job before serving,
  same hostname, `down` removes the workload.
- **FR-008**: The scaffold MUST refuse to write into a non-empty target that already contains a
  project (no overwrite), and MUST validate the service name against module/hostname/release
  constraints before writing anything.
- **FR-009**: The scaffold MUST emit an AGENTS.md (short and curated) that lets a developer or
  agent complete the walk-through without reading devedge source: project layout, the
  rename-the-resource flow, the generate command, `up`/`--deploy`/`down`, and where authz
  declarations live.
- **FR-010**: The full walk-through (scaffold → rename resource → regenerate → up → CRUD probe →
  deploy → down) MUST exist as an automated end-to-end test in devedge's e2e suite, runnable
  against a live k3d like the 003–006 e2es.
- **FR-011**: The scaffolded project MUST depend on the released, canonical artifacts only — the
  public devedge-sdk module and the canonical annotation module — not on local replaces or
  internal-only repos.

### Key Entities

- **Scaffold template**: the versioned-in-devedge source shape from which projects are generated
  (files + substitutions). Owned by devedge (Layer 1); contains no framework runtime logic
  beyond wiring to the SDK.
- **Example resource**: the webhook-endpoint placeholder (URL, secret, event filters — one
  message, CRUD RPCs, one table) whose
  purpose is to be renamed; its only contract is that every RPC is authz-annotated and its
  storage round-trips through the provisioned database.
- **Walk-through e2e**: the automated end-to-end run of the onboarding path; the feature's gate
  and the platform's recurring acceptance probe from here on.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: From an empty directory, scaffold + build + test of the generated project completes
  in under 5 minutes on a developer machine with the documented toolchain.
- **SC-002**: The walk-through e2e (FR-010) passes against a live cluster: scaffold through
  deployed CRUD with zero manual file edits other than the scripted resource rename.
- **SC-003**: The boot-gate demonstration (US1.3) is observable: removing one annotation makes
  startup fail with the offending method named.
- **SC-004**: A first human (or agent) run of the walk-through, guided only by AGENTS.md,
  completes in under one business day, recorded with the observed duration and friction notes —
  the vision's onboarding metric, measured rather than asserted.
- **SC-005**: No regression in the existing e2e suites (002–006) and the `dk` regression contract
  (Part 0.5 surfaces unchanged).

## Assumptions

- The canonical annotation module (`github.com/infobloxopen/apis/proto/infoblox/authz`,
  v1.0.0-alpha.2+) and the public devedge-sdk module are reachable from the developer's module
  proxy/network; offline scaffolding is supported but generate/build then requires connectivity.
- The walk-through's authorization story uses the SDK's development authorizer; integration with
  the internal OPA/PARGs pipeline (devedge-sdk-internal) is deliberately out of scope for the
  rough walk-through and continues in WS-002.
- REST mapping means a plain gRPC-gateway-style mapping good enough for CRUD over HTTPS; AIP
  semantics (field masks, filtering, pagination, ETag/412) are a later framework feature.
- One example dependency (Postgres) is the right scaffold default; additional engines (Redis,
  etc.) remain declarable by editing the Service config as in 003.
- The scaffold emits a single-module Go project; multi-service monorepos are out of scope.
