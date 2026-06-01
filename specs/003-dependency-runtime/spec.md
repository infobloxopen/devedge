# Feature Specification: Dependency runtime for the Service kind

**Feature Branch**: `003-dependency-runtime`
**Created**: 2026-05-31
**Status**: Draft
**Input**: User description: "When a developer runs `de project up` on a Service project file that declares runtime dependencies (e.g. Postgres, Redis), devedge actually starts those declared dependencies, reports their readiness and how to reach them, and `de project down` stops and cleans them up. Builds on the dependency contract frozen in feature 002, which validated and reported dependencies but deliberately did not start them. Dependencies must be co-existence-safe in the shared dev environment."

## Clarifications

### Session 2026-05-31

- Q: How are dependency instances allocated when several services in the shared dev environment declare the same kind of dependency? → A: A single shared instance **per engine** (one Postgres, one Redis) for the dev environment; each service is isolated *within* it by its own database/namespace and unique credentials. (Accepted limitation: migrations that install engine-global extensions are not isolated.)
- Q: What happens to a service's dependency data across `de project down` then `de project up`? → A: It **persists** by default so migrations and seed data survive a restart; an **explicit** action wipes it.
- Q: How much of the deploy story is in this feature? → A: Dev runtime **plus generating a Helm chart** for the service + its declared dependencies (a portable deploy artifact the developer gets without authoring Kubernetes/Helm). Actually *executing* a real-cluster deployment, and the production "dedicated instance per service via the org DB abstraction" realization, are **out of scope** here — but the dependency declaration stays environment-agnostic so that path carries forward.
- Q: What form does the connection endpoint take that devedge reports and the service uses? → A: The connection is delivered to the service as a **DSN in an environment variable**, but the env var carries an **indirect "hotload" DSN** (the `infobloxopen/hotload` pattern) that references a **file**; devedge writes the **real DSN** (host, port, database/namespace, credentials) to that file. The app connects through the hotload driver, which reads the real DSN from the file and **reloads connections when it changes without a restart**. This same env-var-DSN + file shape is what the generated chart uses (file backed by a mounted secret), keeping dev and a real cluster uniform.

## User Scenarios & Testing *(mandatory)*

Feature 002 lets a developer *declare* a service's runtime dependencies and validates them, but
explicitly stops short of starting them — `de project up` only prints "starting dependencies is
not yet supported." This feature closes that gap: the declared dependencies become **real,
reachable backing services** when the developer runs project-up, isolated per service so many
developers and services share one dev environment safely, and the same declaration also yields a
**deployment artifact** the developer can later ship to a real cluster without ever writing
Kubernetes by hand. It deliberately stops at the dev runtime plus artifact generation; running a
production deployment is a later feature.

### User Story 1 - Start and reach declared dependencies (Priority: P1)

A developer whose `Service` file declares a Postgres database and a Redis cache runs
`de project up`. devedge starts (or attaches the service to) those dependencies, waits until they
are ready, and tells the developer exactly how to connect — an endpoint, a database/namespace, and
credentials scoped to this service. The developer points their service at the reported connection
and it works, with no manual database creation and no knowledge of the underlying cluster.

**Why this priority**: This is the whole point of the feature and the minimum that delivers value.
Without it the dependency declaration from feature 002 remains inert. With it, a developer can run
a service against real backing stores locally.

**Independent Test**: Author a `Service` file declaring one Postgres dependency, run project-up,
and connect to the reported endpoint with the reported credentials; confirm a table can be created
and queried. Run project-down and confirm the connection is released.

**Acceptance Scenarios**:

1. **Given** a valid `Service` file declaring one `postgres` dependency, **When** the developer runs project-up, **Then** the dependency is started/attached, project-up waits until it accepts connections, and the developer is shown a connection endpoint, database name, and credentials scoped to this service.
2. **Given** project-up has reported a dependency's connection info, **When** the developer connects using exactly that info, **Then** they can read and write data without having manually created a database or user.
3. **Given** a `Service` file declaring both a `postgres` and a `redis` dependency, **When** project-up runs, **Then** both are reported as ready with their own connection info, and a failure to start either is reported per-dependency rather than as one opaque error.
4. **Given** a running service, **When** the developer runs project-down, **Then** the service's dependency access and routes are released and co-located services' dependencies are unaffected.

---

### User Story 2 - Safe co-existence in the shared dev environment (Priority: P1)

Two developers (or two services) are active in the same shared dev environment, and both declare a
dependency named `db` on engine `postgres`. Each service gets its own isolated database and
credentials inside the shared Postgres instance, so neither can see or corrupt the other's data,
and neither has to coordinate ports or names with the other.

**Why this priority**: The shared dev environment is the stated deployment model
(`dev-k3d-shared-cluster-model`). Isolation is not a nice-to-have here — without it, two services
with the conventional name `db` would collide, making the feature unusable in the environment it
targets. It is co-equal P1 with US1.

**Independent Test**: Bring up two services that each declare a `postgres` dependency named `db`,
write a distinctive row in each, and confirm from each service that it sees only its own row and
that both are simultaneously reachable.

**Acceptance Scenarios**:

1. **Given** two services each declaring a `postgres` dependency named `db`, **When** both run project-up, **Then** each receives a distinct database and distinct credentials and both are reachable at the same time.
2. **Given** the two co-located services above, **When** one writes data, **Then** the other cannot read or modify that data through its own credentials.
3. **Given** one service runs project-down (default, non-destructive), **When** it completes, **Then** the other service's dependency and data are unaffected.

---

### User Story 3 - Data persists across restarts, with an explicit wipe (Priority: P2)

A developer brings their service up, runs migrations and seeds data, then takes it down for the
day. The next morning they run project-up again and their schema and data are still there. When
they want a clean slate, they run an explicit destructive form of down that drops the service's
data.

**Why this priority**: It matches how developers actually work (schema/seed survive restarts) and
prevents the surprise of silent data loss, but the core reachable-dependency value (US1/US2) lands
without it.

**Independent Test**: Bring a service up, write data, run project-down, run project-up again, and
confirm the data is still present. Then run the explicit destructive down, run project-up, and
confirm the data is gone.

**Acceptance Scenarios**:

1. **Given** a service whose dependency holds written data, **When** the developer runs project-down then project-up, **Then** the previously written data is still present.
2. **Given** the same service, **When** the developer runs the explicit destructive form of down and then project-up, **Then** the dependency starts with no prior data.
3. **Given** a default (non-destructive) project-down, **When** it runs, **Then** the developer is not silently destroying data — the default preserves it.

---

### User Story 4 - Get a deployable artifact without writing Kubernetes (Priority: P3)

A developer who has been running their service locally wants to hand off something deployable. They
ask devedge to produce a deployment artifact for the service and its declared dependencies. devedge
generates a Helm chart that describes the service and its dependency needs, and the developer never
has to author Kubernetes or Helm YAML themselves.

**Why this priority**: It carries the dependency declaration forward toward real deployment and
delivers on "the developer shouldn't need to understand the cluster," but the local runtime
(US1–US3) is the immediate value; the artifact is the bridge to a later deployment feature.

**Independent Test**: For a `Service` file declaring a service with one Postgres dependency, run
the artifact-generation command and confirm a Helm chart is produced that passes `helm lint`,
references the declared dependency abstractly, and required no hand-written Kubernetes/Helm input.

**Acceptance Scenarios**:

1. **Given** a `Service` file declaring a service and one or more dependencies, **When** the developer generates the deployment artifact, **Then** a Helm chart is produced describing the service and each declared dependency, and it passes a standard chart lint.
2. **Given** the generated chart, **When** a developer reads it, **Then** dependencies are expressed abstractly (the same declaration yields a shared logical database in dev and would yield a dedicated instance in a real cluster) rather than hard-coding the dev environment's shared-instance realization.
3. **Given** a `Service` file with no dependencies, **When** the developer generates the artifact, **Then** a valid service-only chart is produced.

---

### Edge Cases

- **No dependencies declared**: project-up behaves exactly as in feature 002 for routing — no
  dependency runtime is engaged and routes are registered as before.
- **Shared instance not yet present**: on the first project-up that needs an engine, devedge brings
  up the shared instance for that engine, then provisions the service's database/credentials.
- **Idempotent re-up**: running project-up again on an already-up service reconciles to the desired
  state without error and without losing data.
- **Readiness timeout**: if a dependency does not become ready within a bounded time, project-up
  fails with a clear, retryable message naming the dependency, rather than hanging or reporting a
  false success.
- **Provisioning failure**: if creating the service's database/credentials fails, project-up fails
  with an actionable message and leaves nothing half-provisioned that would block a retry.
- **Engine recognized but not runnable**: an engine accepted by the config schema (`postgres`,
  `redis`) for which runtime support is unavailable fails with a message naming the engine.
- **Redis isolation limits**: per-service isolation in Redis uses a logical database index or an
  ACL user plus key namespace; if the engine's isolation capacity is exhausted, the failure is
  reported clearly rather than silently sharing a namespace.
- **Engine-global extensions**: a migration that installs an engine-wide extension affects the
  shared instance globally; this is a known, accepted limitation of the shared-instance dev model
  (see Assumptions), not a defect.
- **Destructive down on a shared instance**: the explicit wipe drops only the requesting service's
  database/namespace, never the shared instance or other services' data.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: When `de project up` runs on a `Service` file that declares dependencies, the system MUST start or attach each declared dependency so it is reachable — replacing feature 002's "starting dependencies is not yet supported" notice.
- **FR-002**: The system MUST isolate each service's dependency data from every other service's, using a per-service database/namespace and unique credentials within a shared per-engine instance, so co-located services in the shared dev environment cannot read or modify each other's data.
- **FR-003**: On a successful project-up, the system MUST make each dependency's connection available to the service as a **DSN delivered through an environment variable**, and MUST write the **real DSN** (endpoint, database/namespace, and the service's scoped credentials) to a **file** the developer does not have to construct by hand.
- **FR-003a**: The environment variable MUST carry an **indirect ("hotload") DSN** that references the real-DSN file (the `infobloxopen/hotload` pattern), so the application connects through the hotload driver and picks up changes to the file **without restarting**. The real DSN is read from the file, not inlined in the environment variable.
- **FR-003b**: When the real DSN file changes (e.g. credentials are re-provisioned), a service connected via the hotload DSN MUST be able to pick up the new connection without a restart. (This feature provides the indirection that enables this; it does not require devedge to actively rotate credentials.)
- **FR-004**: project-up MUST wait until each declared dependency is ready to accept connections before reporting success, and MUST fail with a clear message after a bounded time if a dependency does not become ready.
- **FR-005**: A service's dependency data MUST persist across a default `de project down` followed by `de project up`, so migrations and seed data are retained.
- **FR-006**: The system MUST provide an explicit destructive action (distinct from the default down) that wipes a service's dependency data, and the default down MUST NOT destroy data.
- **FR-007**: `de project down` MUST release the service's dependency access and routes without affecting co-located services' dependencies, and (by default) without destroying the service's data.
- **FR-008**: Starting dependencies MUST be idempotent — re-running project-up on an already-up service reconciles to the desired state with no error and no data loss.
- **FR-009**: When a dependency cannot be started (shared instance unavailable, readiness timeout, or provisioning failure), project-up MUST fail with an actionable per-dependency message and MUST NOT leave the service half-provisioned in a way that blocks a later retry.
- **FR-010**: The system MUST generate, on request, a deployment artifact (a Helm chart) describing the service and its declared dependencies, without requiring the developer to author Kubernetes or Helm by hand.
- **FR-011**: The generated artifact MUST express dependencies abstractly so the same declaration realizes a shared logical database in the dev environment and would realize a dedicated instance in a real cluster — i.e. the dependency declaration is environment-agnostic.
- **FR-012**: The set of runtime-supported engines MUST be `postgres` and `redis` (the recognized engines from feature 002); a recognized engine without runtime support MUST fail with a message naming the engine.
- **FR-013**: A `Service` file that declares no dependencies MUST retain feature 002's routing behavior unchanged (no dependency runtime engaged).

### Key Entities *(include if data involved)*

- **Shared dependency instance**: A single backing service per engine (one Postgres, one Redis) that devedge maintains for the dev environment and that many services share.
- **Service dependency binding**: A service's isolated slice of a shared instance — its own database/namespace plus unique credentials — created at project-up and the unit the explicit wipe targets.
- **Connection info**: The service's connection to a dependency, delivered in two parts — a **real DSN** (endpoint, database/namespace, scoped credentials) written to a **file**, and a **hotload DSN** in an environment variable that references that file so the service connects through the hotload driver and hot-reloads on change.
- **Deployment artifact**: The generated, environment-agnostic Helm chart describing the service and its declared dependency needs.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A developer can declare a Postgres dependency, run project-up, and connect their service using only the connection info devedge reports — with zero manual database, user, or cluster steps.
- **SC-002**: Two services declaring identically named dependencies run simultaneously in the shared dev environment and, in 100% of cases, neither can read or modify the other's data.
- **SC-003**: After project-up → write data → project-down → project-up, the previously written data is present; after the explicit destructive down → project-up, it is absent.
- **SC-004**: A developer obtains a deployable Helm chart for the service and its dependencies from a single devedge command, the chart passes `helm lint`, and the developer wrote no Kubernetes or Helm YAML.
- **SC-005**: 100% of dependency start failures (instance unavailable, readiness timeout, provisioning failure, unsupported engine) yield an actionable message naming the dependency and cause, and leave the system retryable.
- **SC-006**: Running project-up twice in a row on the same service yields the same reachable state with no error and no data loss.
- **SC-007**: A service connecting via the hotload DSN environment variable reaches its dependency using only that variable (the real DSN lives in the referenced file, never hand-constructed), and a change to the real DSN file is picked up by the running service without a restart.

## Assumptions

- **Recognized engines are `postgres` and `redis`** (carried from feature 002). Expanding the set
  is a later concern; a recognized engine lacking runtime support is a clear failure (FR-012).
- **Dev realization vs. real-cluster realization.** In the dev environment a dependency is realized
  as a per-service database/namespace + credentials inside a shared per-engine instance. In a real
  cluster the same declaration would realize a dedicated instance (abstracted by the org DB layer).
  This feature implements the dev realization and *emits* the artifact for the real one; it does
  **not** execute a real-cluster deployment.
- **Engine-global state is not isolated.** Migrations that install engine-wide extensions affect
  the shared instance for all services; full extension-level isolation is out of scope and accepted
  as a known limitation of the shared-instance dev model.
- **Redis isolation** uses a logical database index or an ACL user plus key namespace, within the
  engine's capacity limits.
- **The dev environment is the shared k3d cluster** (`dev-k3d-shared-cluster-model`); dependency
  workloads and the shared instances must be co-existence-safe and must not disturb a shared
  devedge daemon or other tenants.
- **The developer's service runs its own migrations/seeding.** devedge provisions and exposes the
  dependency; it does not run the service's schema migrations or seed data.
- **Connection delivery uses the hotload DSN pattern** (`infobloxopen/hotload`): the service's
  environment variable holds an indirect DSN referencing a file, and devedge writes the real DSN
  (pointing at the shared instance, with the service's own database/namespace + credentials) to
  that file. The real DSN's host follows devedge's existing dev-addressing conventions. The same
  env-var-DSN + file shape carries into the emitted chart, where the file is backed by a mounted
  secret — so dev and a real cluster connect identically.

## Out of Scope

- Executing a real-cluster (production) deployment of the generated chart.
- The production "dedicated instance per service via the org DB abstraction" realization beyond
  expressing it abstractly in the emitted artifact.
- Running the service's database migrations or seeding its data.
- Backups, restores, and ongoing health monitoring beyond initial readiness.
- Engines other than `postgres` and `redis`.
- **App-owned ("bring-your-own") Helm charts.** This feature generates the service chart. Letting an
  app supply its *own* workload chart (devedge still provisions dependencies and wires the DSN via
  the documented secret/env-var convention), or manage its *own* dependency chart, is a deliberate
  future extension. It is left open by design — connection delivery is a convention, not chart-bound,
  and provisioning is independent of the app's workload chart — but no `spec.chart` field or
  user-chart installation is built here.
