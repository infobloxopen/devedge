# Feature Specification: Cluster topology — shared dev cluster, ephemeral CI clusters, co-existence-safe projects

**Feature Branch**: `004-cluster-topology`
**Created**: 2026-06-01
**Status**: Draft
**Input**: User description: "Give devedge a cluster topology model so projects run on the right Kubernetes cluster automatically and safely share it. On a developer machine, `de project up` resolves and ensures a single shared k3d dev cluster across all projects (created once if absent, targeted without the developer manually creating a cluster or switching kube context), and many projects coexist on it without collision. In CI, the same commands run against a dedicated, ephemeral cluster per run that is created and torn down automatically and isolated from other runs. Every project's cluster footprint (namespaces, resource names, hostnames) is unique, non-interfering, and self-cleaning so projects are co-existence-safe; a project can explicitly opt into a dedicated cluster when it cannot coexist. Builds on the dependency runtime from feature 003, making cluster selection an explicit devedge-managed model rather than relying on the ambient kube context. The same e2e/integration tests must run unchanged on both the dedicated CI cluster and the shared dev cluster."

## Clarifications

### Session 2026-06-01

- Q: How does devedge select shared-dev vs. ephemeral/CI mode? → A: Auto-detect a standard CI indicator (e.g. the `CI` environment variable) → use a dedicated, ephemeral per-run cluster; default to the shared dev cluster otherwise; an explicit flag/env-var override always wins.
- Q: On the shared dev cluster, how are stateful dependency engines (Postgres/Redis) instanced across services? → A: Default to feature 003's shared per-engine instance with per-service logical isolation (own database/namespace + credentials), relocated onto the resolved cluster; a service may **opt a dependency into a dedicated instance** (configurable) as a rare exception. Complete physical isolation is the dedicated-cluster opt-in.
- Q: How is co-existence safety enforced? → A: Stateful stores (Postgres/Redis) are isolated **per service** (the microservice / generated-Helm-chart boundary) by default; some resources (e.g. shared messaging topics in a SaaS) are **intentionally shared** across services by **explicit declaration** and are allowed, not rejected; unique naming prevents *accidental* collisions; when complete isolation is required the outlet is a separate (dedicated) cluster. devedge does not inspect/reject arbitrary user declarations.
- Q: How is the ephemeral CI cluster torn down so teardown is guaranteed even on failure? → A: A devedge **wrapper command** creates the cluster, runs the flow, and tears it down via defer/trap — automatic on success or failure — so the CI workflow never calls the cluster tool (k3d) directly.

## User Scenarios & Testing *(mandatory)*

Through feature 003, `de project up` provisions a service's dependencies into **whatever kube
context happens to be current** (the daemon's provisioner is created with an empty context), in a
fixed namespace. Today a developer must manually `de cluster create`, bootstrap it, and point their
kube context at it before project-up does anything useful; nothing in devedge decides *which*
cluster a project belongs on or guarantees that two projects sharing a cluster will not collide.

This feature makes the cluster an explicit, devedge-managed part of the model rather than ambient
state. On a developer machine there is **one shared dev cluster** that devedge ensures exists and
targets by default, so many projects run side by side on it. In CI there is a **dedicated,
ephemeral cluster per run** that devedge creates and tears down. In both, every project's footprint
on the cluster is **uniquely named, non-interfering, and self-cleaning**, so the shared dev cluster
behaves like the real "many apps coexisting" environment the platform is meant to mirror. A project
that genuinely cannot coexist can **opt into its own dedicated cluster**. The same e2e/integration
tests run unchanged on the dedicated CI cluster and the shared dev cluster.

### User Story 1 - Project up resolves and ensures the shared dev cluster automatically (Priority: P1)

A developer on their laptop runs `de project up` for a project. Without having created a cluster,
chosen a name, or switched their kube context, devedge resolves the project to the **single shared
dev cluster**, ensures that cluster exists (creating and bootstrapping it once if it is absent),
targets it for the project's routes and dependencies, and brings the project up there. A second
project, brought up later, lands on the **same** shared cluster.

**Why this priority**: This is the default daily path and the core of the "shared dev cluster"
model. Without it, the developer hand-manages clusters and kube contexts and feature 003's
dependency runtime targets whatever context is ambient — the exact friction this feature removes.

**Independent Test**: On a machine with no devedge dev cluster present, run `de project up` for a
project that declares routes and one dependency; confirm the shared dev cluster is created once,
the project comes up on it, and the developer never named a cluster or changed kube context. Bring a
second project up and confirm it lands on the same cluster (no second cluster is created).

**Acceptance Scenarios**:

1. **Given** no devedge-managed shared dev cluster exists yet, **When** the developer runs `de project up`, **Then** devedge creates and bootstraps the shared dev cluster once, targets it, and the project's routes and dependencies come up on it — with no manual cluster creation or kube-context switching.
2. **Given** the shared dev cluster already exists, **When** the developer runs `de project up` for any project, **Then** devedge reuses that one cluster (it does not create another) and reports which cluster the project was placed on.
3. **Given** two projects brought up in sequence on a developer machine, **When** both are up, **Then** both occupy the same single shared dev cluster.
4. **Given** a project with no dependencies and only routes, **When** the developer runs `de project up`, **Then** cluster resolution still targets the shared dev cluster and routing behaves as before (this feature changes *which* cluster, not the routing contract).

---

### User Story 2 - Many projects coexist on one cluster without interfering (Priority: P1)

Two projects are up at the same time on the shared dev cluster, and both use conventional,
identical names internally. Each project's cluster footprint — its namespace, resource names, and
hostnames — is **unique to that project**, so neither can see, collide with, or clobber the other.
Bringing one project down removes **exactly** that project's footprint and leaves every other
project on the cluster untouched. Stateful stores (Postgres/Redis) are isolated **per service** (the
microservice / generated-Helm-chart boundary) by default, so neighbors never *accidentally* collide;
sharing a resource across services (e.g. a common messaging topic) is allowed only when a project
**explicitly declares** it, and when a project needs complete isolation the outlet is a dedicated
cluster (US4).

**Why this priority**: The shared dev cluster is only usable if coexistence is guaranteed; without
isolation, two projects using the conventional defaults collide and the shared model is unusable.
It is co-equal P1 with US1. This is the cluster-level analogue of the per-service dependency
isolation delivered in feature 003.

**Independent Test**: Bring up two projects that use identical internal names on the shared dev
cluster, write a distinguishing marker in each, and confirm from each that it sees only its own
footprint and both are simultaneously healthy. Bring one down and confirm the other is unaffected.

**Acceptance Scenarios**:

1. **Given** two projects with identical internal names brought up on the shared dev cluster, **When** both are up, **Then** each occupies a distinct, project-scoped namespace and uniquely named resources/hostnames, and both are reachable at the same time.
2. **Given** the two co-located projects, **When** one is changed or reconfigured, **Then** the other's resources, data, and routes are unaffected.
3. **Given** one project is brought down (default, non-destructive), **When** it completes, **Then** only that project's footprint is removed and every other project on the shared cluster remains up and unaffected.
4. **Given** two services that each declare a Postgres and a Redis dependency with conventional names, **When** both are up, **Then** each gets its own per-service-isolated store (its own database/namespace + credentials) by default so neither accidentally reads or collides with the other, and HTTP traffic is separated by host-based routing on the one shared ingress (no service claims a host port).
5. **Given** a project that explicitly declares a resource shared across its services (e.g. a common topic), **When** it is brought up, **Then** that resource is shared across the declaring services as intended while their stateful stores remain per-service isolated — devedge does not reject the declaration.

---

### User Story 3 - CI runs get a dedicated, ephemeral cluster automatically (Priority: P2)

A CI run executes the same project/e2e commands a developer runs. In CI, devedge resolves to a
**dedicated, ephemeral cluster created for that run**, isolated from every other run, and **torn
down automatically** when the run ends (including on failure). A **devedge wrapper command** creates
the cluster, runs the flow, and tears it down via defer/trap, so the CI workflow does not hand-write
`k3d cluster create`/`delete`; the ephemeral/CI mode is recognized automatically from a standard CI
indicator (with an explicit override available). The same e2e/integration tests pass unchanged on this dedicated
cluster and on the shared dev cluster.

**Why this priority**: Codifying CI's dedicated-ephemeral model standardizes today's hand-rolled
`k3d cluster create/delete` in test code and removes cross-run interference, but the developer-facing
shared model (US1/US2) is the immediate daily value and CI already functions via manual scripting,
so this lands second.

**Independent Test**: In an environment flagged as ephemeral/CI, run the project/e2e flow; confirm a
dedicated cluster is created for the run, the flow passes, and the cluster is deleted at the end
even when a test fails. Run the *same* test suite against the shared dev cluster and confirm it also
passes without modification.

**Acceptance Scenarios**:

1. **Given** an environment resolved as ephemeral/CI, **When** the project/e2e flow runs, **Then** devedge creates a dedicated cluster scoped to that run, runs the flow against it, and the workflow never calls the cluster tool directly.
2. **Given** an ephemeral/CI run that has created its cluster, **When** the run ends — whether it passed or failed — **Then** devedge tears the cluster down and leaves no leftover cluster, container, or kube-context entry.
3. **Given** two ephemeral/CI runs executing concurrently, **When** both create clusters, **Then** the clusters are distinctly named and isolated, and neither run observes or disturbs the other's cluster.
4. **Given** an e2e/integration test suite, **When** it is run against the dedicated CI cluster and then against the shared dev cluster, **Then** it passes in both without test changes, because tests use project-unique names/hostnames and clean up after themselves (never assuming an empty cluster).

---

### User Story 4 - A project can explicitly opt into a dedicated cluster (Priority: P3)

A project that cannot safely coexist — because it must mutate cluster-global state, install a
cluster-wide extension, or otherwise own the cluster — **declares in its configuration** that it
wants its own dedicated cluster. devedge then gives that project a dedicated cluster (lifecycle-
managed like any other devedge cluster) instead of placing it on the shared dev cluster. Projects
that do not opt in continue to default to the shared cluster.

**Why this priority**: This is the explicit escape hatch from the shared default named in the
shared-dev-cluster model — needed only by the minority of projects that genuinely cannot coexist,
so it follows the shared model and CI support.

**Independent Test**: Add the dedicated-cluster opt-in to a project's config, run `de project up`,
and confirm the project is placed on its own dedicated cluster (distinct from the shared dev
cluster), while a second project without the opt-in still lands on the shared cluster.

**Acceptance Scenarios**:

1. **Given** a project whose config declares it requires a dedicated cluster, **When** the developer runs `de project up`, **Then** devedge ensures and targets a dedicated cluster for that project rather than the shared dev cluster.
2. **Given** a project that does not declare the opt-in, **When** it is brought up, **Then** it defaults to the shared dev cluster (the opt-in is off by default).
3. **Given** a project on a dedicated cluster, **When** it is brought down with the destructive option, **Then** its dedicated cluster is removed without affecting the shared cluster or other projects.

---

### Edge Cases

- **Cluster tool unavailable**: if the underlying cluster tool (k3d) or container runtime is not
  installed/running when devedge needs to ensure a cluster, project-up fails with a clear,
  actionable message naming the missing prerequisite — not an opaque error or a hang.
- **Shared cluster present but not bootstrapped**: devedge detects the shared cluster exists but is
  missing devedge integration (CA, cert-manager issuer, dns webhook) and reconciles it (bootstraps
  it) rather than creating a duplicate or failing.
- **Concurrent first-time creation**: two `de project up` invocations race to create the shared dev
  cluster for the first time; exactly one cluster is created and both projects end up on it (no
  duplicate cluster, no corrupted half-created cluster).
- **Idempotent re-up**: running `de project up` again for an already-up project reconciles to the
  same cluster placement and footprint with no error and no duplication.
- **Bring-down isolation**: a default (non-destructive) project-down removes only that project's
  footprint; it never deletes the shared cluster or another project's namespace/resources.
- **Ambient kube context differs from the resolved cluster**: devedge targets the cluster it
  resolved for the project regardless of the user's current `kubectl` context, and does not silently
  mutate the user's global kube-context selection as a side effect.
- **Ephemeral teardown on failure**: an ephemeral/CI run that fails mid-flow still tears its cluster
  down; a crash that prevents teardown leaves a discoverable, uniquely named cluster that a
  subsequent run or an explicit cleanup can remove (no name collision with a healthy run).
- **Opt-in toggled after first up**: a project brought up on the shared cluster, then reconfigured
  to require a dedicated cluster (or vice-versa), is moved to the correct cluster on the next up
  with its previous footprint on the old cluster released, rather than being left running in two
  places.
- **Explicitly shared resource**: a project that declares a resource shared across its services
  (e.g. a common topic) gets that resource shared as intended — an allowed, declared exception —
  while the services' stateful stores stay per-service isolated; devedge does not reject the
  declaration. A project needing complete isolation uses the dedicated-cluster opt-in.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: devedge MUST resolve every project to a target cluster from an explicit topology
  model rather than relying on the ambient kube context: on a developer machine the default target
  is the single shared dev cluster; in the ephemeral/CI environment the target is a dedicated
  per-run cluster.
- **FR-002**: On a developer machine, devedge MUST maintain a **single shared dev cluster across all
  projects**, and `de project up` MUST ensure it exists — creating and bootstrapping it once if
  absent and reusing it otherwise — without the developer manually creating a cluster or switching
  kube context.
- **FR-003**: When the shared dev cluster already exists, devedge MUST reuse it (it MUST NOT create
  a second shared cluster) and MUST report which cluster a project was placed on.
- **FR-004**: Every project's footprint on a cluster MUST be uniquely named and isolated so
  co-located projects cannot read, collide with, or clobber one another, and a project MUST NOT
  assume an empty or exclusively owned cluster. The realized coexistence is per-service dependency
  isolation (own database/namespace + credentials, from feature 003) plus project/service-slug-unique
  names for any cluster resources this feature introduces (e.g. a dedicated instance's Helm release);
  devedge does NOT create a separate per-project namespace for an app workload it does not deploy.
- **FR-005**: A default (non-destructive) `de project down` MUST remove exactly the requesting
  project's footprint and MUST leave the shared cluster itself and every other project's
  namespace/resources/routes intact.
- **FR-006**: Co-existence safety MUST be provided by isolating each service's stateful stores
  (Postgres/Redis) per service (the microservice / generated-Helm-chart boundary) by default and by
  giving every project a uniquely named footprint, so neighbors never *accidentally* interfere.
  Sharing a resource across services (e.g. a common messaging topic) MUST be possible only as an
  **explicit, declared** choice — it is an allowed, expected exception, not something devedge
  rejects. When complete isolation is required, the supported outlet is a dedicated cluster (FR-010),
  not per-resource rejection. devedge MUST NOT inspect and reject arbitrary user declarations.
- **FR-007**: In the ephemeral/CI environment, devedge MUST create a dedicated cluster scoped to the
  run and MUST tear it down automatically when the run ends, including on failure, leaving no
  leftover cluster, container, or kube-context entry. Teardown MUST be guaranteed by a devedge
  **wrapper command** that creates the cluster, runs the flow, and removes it via defer/trap, so the
  CI workflow never invokes the cluster tool directly.
- **FR-008**: Concurrent ephemeral/CI runs MUST receive distinctly named, isolated clusters such
  that neither run observes or disturbs the other's cluster.
- **FR-009**: devedge MUST select the environment (shared-dev vs. ephemeral/CI) by auto-detecting a
  standard CI indicator (e.g. the `CI` environment variable) — present → ephemeral dedicated-per-run
  cluster; absent → the shared dev cluster — and an explicit flag/env-var override MUST always take
  precedence. The workflow MUST NOT need to invoke the cluster tool directly to get the correct
  topology.
- **FR-010**: A project MUST be able to **declare in its configuration** that it requires a
  dedicated cluster; when declared, devedge MUST ensure and target a dedicated cluster for that
  project instead of the shared dev cluster. The opt-in MUST default to off (shared).
- **FR-011**: Cluster resolution, ensuring, and targeting MUST be idempotent: re-running
  `de project up` for an already-up project MUST reconcile to the same cluster placement and
  footprint with no error and no duplicate cluster or resources.
- **FR-012**: When a required prerequisite for ensuring a cluster is missing (cluster tool or
  container runtime unavailable) or cluster creation/bootstrap fails, devedge MUST fail with a
  clear, actionable, retryable message and MUST NOT leave a half-created cluster that blocks a
  later retry.
- **FR-013**: devedge MUST target the resolved cluster for a project's routes and dependencies
  directly (the dependency runtime from feature 003 MUST provision into the resolved cluster, not
  the ambient kube context) and MUST NOT silently change the user's global kube-context selection as
  a side effect.
- **FR-014**: The same e2e/integration test suite MUST run unchanged on both the dedicated CI
  cluster and the shared dev cluster — tests rely on project-unique names/hostnames and self-cleanup
  and MUST NOT assume a clean or exclusively owned cluster.
- **FR-015**: When a project's dedicated-cluster opt-in changes between runs, devedge MUST move the
  project to the correct cluster on the next up and release its prior footprint on the old cluster,
  so the project is not left running on two clusters.
- **FR-016**: On any cluster, a service's stateful dependencies (Postgres/Redis) MUST default to
  feature 003's per-service-isolated realization — a per-service database/namespace + credentials
  within the shared per-engine instance — provisioned into the **resolved** cluster, and a service
  MUST be able to **opt a dependency into a dedicated instance** when per-service logical isolation
  is not enough. This feature MUST NOT change feature 003's default dependency contract; it only
  relocates provisioning onto the resolved cluster and adds the dedicated-instance opt-in.
- **FR-017**: A resource MAY be **shared across services** only when a project explicitly declares it
  shared; absent such a declaration, each service's resources MUST be private to that service.

### Key Entities *(include if data involved)*

- **Cluster topology**: The model that maps a project + its environment to a target cluster — shared
  dev cluster (developer-machine default), dedicated ephemeral cluster (CI/ephemeral), or dedicated
  cluster (explicit project opt-in).
- **Shared dev cluster**: The single devedge-managed cluster on a developer machine that all
  non-opted-in projects share; ensured once, bootstrapped with devedge integration, and persistent
  across projects and restarts.
- **Ephemeral cluster**: A dedicated cluster devedge creates for a single CI/ephemeral run and tears
  down when the run ends.
- **Project footprint**: A project's isolated presence on a cluster — its project-scoped namespace,
  uniquely named resources, and hostnames — created at up and removed at down; the unit isolation
  and cleanup operate on.
- **Environment signal**: The explicit, documented input that selects shared-dev vs. ephemeral/CI
  topology, defaulting to shared-dev.
- **Dedicated-cluster opt-in**: A project-configuration declaration that the project requires its own
  cluster instead of the shared dev cluster; off by default.
- **Dependency isolation policy**: The tiered isolation model — (1) default per-service isolation of
  stateful stores within the shared per-engine instance (003), (2) the rare per-dependency
  dedicated-instance opt-in, (3) the dedicated-cluster outlet for complete isolation.
- **Shared resource**: A resource a project explicitly declares shared across its services (e.g. a
  common messaging topic); absent the declaration, resources are private to their service.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A developer with no existing devedge cluster runs `de project up` and the project comes
  up on a shared dev cluster with zero manual cluster-creation or kube-context steps; a second
  project comes up on the **same** cluster (exactly one shared cluster exists).
- **SC-002**: Two projects using identical internal names run simultaneously on the shared dev
  cluster and, in 100% of cases, neither can observe or modify the other's footprint; bringing one
  down leaves the other healthy.
- **SC-003**: In an ephemeral/CI run, a dedicated cluster is created and, in 100% of runs (pass or
  fail), is torn down with no leftover cluster, container, or kube-context entry.
- **SC-004**: The same e2e/integration suite passes unchanged against both the dedicated CI cluster
  and the shared dev cluster (no test edits between the two runs).
- **SC-005**: A project that declares the dedicated-cluster opt-in is placed on its own cluster while
  a non-opted-in project run alongside it lands on the shared cluster — verified in the same session.
- **SC-006**: Running `de project up` twice in a row for the same project yields the same cluster
  placement and footprint with no error and no duplicate cluster or resources.
- **SC-007**: 100% of cluster-ensure failures (cluster tool/runtime missing, creation/bootstrap
  failure) produce an actionable, retryable message and leave no half-created cluster that blocks a
  retry.
- **SC-008**: With two co-located services using conventional names, each service's Postgres/Redis
  stores are isolated per service in 100% of cases (no accidental cross-service access); a resource is
  shared between services only when explicitly declared shared, and complete isolation is achievable
  by moving a project to a dedicated cluster.

## Assumptions

- **Shared cluster scope is per developer machine.** "Single shared dev cluster across all projects"
  means one devedge-managed cluster per host, shared by every non-opted-in project on that machine,
  matching the `dev-k3d-shared-cluster-model` decision. It persists across projects and restarts and
  is removed only by an explicit destructive cluster command, never automatically.
- **Provider is k3d.** The shared and ephemeral clusters use the existing k3d provider and the
  existing local-cluster safeguards/bootstrap (CA, cert-manager issuer, dns webhook). The provider
  interface stays the seam for future providers; this feature does not add a new provider.
- **Environment selection (resolved).** devedge auto-detects a standard CI indicator (e.g. the `CI`
  environment variable) to select the ephemeral/dedicated-per-run topology and otherwise defaults to
  the shared dev cluster; an explicit flag/env-var override always wins (FR-009). In CI, a devedge
  **wrapper command** owns the create→run→teardown lifecycle so teardown is guaranteed even on
  failure and the workflow never calls k3d directly (FR-007).
- **Builds directly on feature 003 (default contract unchanged).** The dependency runtime already
  installs a shared per-engine instance and per-service bindings (own database/namespace +
  credentials); this feature **relocates** that provisioning onto the *resolved* cluster and does not
  re-open 003's connection-delivery (hotload DSN + file) or persistence contracts. Stateful stores
  stay isolated **per service** by default; the only addition is the rare per-dependency
  dedicated-instance opt-in.
- **Project identity drives footprint naming.** The project name (already used for routes and
  dependency bindings) is the basis for the project-scoped namespace and unique resource/host names;
  uniqueness across projects on a shared cluster is derived from it.
- **Isolation is tiered (resolved).** (1) Default — each service's stateful stores (Postgres/Redis)
  are isolated per service (the microservice / generated-Helm-chart boundary) within the shared
  per-engine instance (003). (2) A service MAY opt a dependency into a **dedicated instance** for
  stronger isolation, as a rare exception. (3) **Complete** isolation uses a dedicated cluster (US4)
  — the explicit outlet for projects that cannot coexist. Resources are private to a service unless a
  project **explicitly declares** one shared (e.g. a common messaging topic in a SaaS).
- **Terminology.** A *microservice* corresponds to a devedge `Service` and its generated Helm chart
  (003); a *project* is what `de project up` acts on. "Per-service" isolation == per-microservice ==
  per-Helm-chart. Messaging/topics are illustrative of the SaaS target; only `postgres` and `redis`
  are in scope as runnable engines (carried from 003).
- **Cluster naming encodes scope.** The shared dev cluster has a single well-known devedge name;
  ephemeral clusters carry a per-run-unique name so concurrent runs and crash-leftovers never
  collide.
- **Tests already follow co-existence discipline.** Per the shared-cluster model, e2e/integration
  tests use unique project names + hostnames and self-clean; this feature relies on that discipline
  so the same suite runs on both topologies (FR-014).

## Out of Scope

- **Non-k3d providers** (kind, minikube, cloud). The provider interface remains the extension seam,
  but only k3d is implemented here.
- **Remote, shared-team, or production clusters.** The local-cluster safeguard still refuses
  non-loopback/non-local clusters; this feature is about local dev and CI topology only.
- **Multi-machine or networked sharing of the dev cluster.** "Shared" means shared across projects on
  one machine, not shared across developers or hosts.
- **Re-opening feature 003's dependency contracts** — engines, the default per-service isolation
  mechanics, connection delivery (hotload DSN + file), persistence/`--clean`, and chart generation
  are unchanged; this feature only relocates provisioning onto the resolved cluster and adds the rare
  per-dependency dedicated-instance opt-in.
- **Messaging/topic engines.** Shared topics illustrate the SaaS target and the explicit-sharing
  principle; no messaging engine is added here — runnable engines remain `postgres` and `redis`.
- **Cluster autoscaling, capacity management, eviction, or resource quotas** on the shared cluster
  beyond what coexistence (unique namespaces/names, no cluster-global mutation) requires.
- **Automatic deletion of the shared dev cluster.** It is removed only by an explicit destructive
  command; this feature does not garbage-collect it.
- **Migrating existing manually created clusters** into the managed shared/ephemeral model
  automatically; the model governs clusters devedge ensures going forward.
