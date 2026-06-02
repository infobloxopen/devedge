# Feature Specification: Deploy the app workload onto the resolved cluster

**Feature Branch**: `005-app-workload-deploy`
**Created**: 2026-06-02
**Status**: Draft
**Input**: User description: "Deploy the application workload onto the resolved cluster so de project up can run the service in-cluster, not just locally"

## Context

Today devedge runs a service's **process locally** on the developer's machine; only its **dependencies**
(Postgres/Redis) run in the cluster (feature 003), reached over a hotload DSN + ephemeral port-forward.
Routes registered with the daemon point at the local process. Feature 004 made every project resolve to
a target cluster (shared dev / ephemeral CI / dedicated) and `de project chart` already renders a
deployable Helm chart for the service. The missing layer: **devedge can declare and run the service
workload itself inside the resolved cluster**, so a developer (or CI) gets the service running where its
dependencies already live — without hand-writing `kubectl`/`helm`. This closes the loop from "deps in
cluster, app on laptop" to "the whole service runs in the cluster on demand."

## Clarifications

### Session 2026-06-02

- **Image source (FR-011)**: reference by default, **build from the project when a build is declared**
  (both); built images are loaded into the resolved cluster (no external registry required).
- **Deploy mode (FR-010)**: in-cluster deploy is an **explicit opt-in that complements local-run**;
  local-run stays the default and the dev hostname resolves to exactly one running instance at a time.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Deploy the service into the resolved cluster (Priority: P1) 🎯 MVP

A developer with a containerized service declares it as a workload, then runs `de project up`. devedge
resolves + ensures the cluster (004), provisions the declared dependencies (003), and **deploys the
service workload into that cluster**, reporting when it is running — with no manual cluster, context, or
`kubectl`/`helm` steps.

**Why this priority**: This is the core value — running the service where its dependencies are. Without
it, nothing else in the feature matters.

**Independent Test**: On a clean machine, declare a workload + one dependency and run `de project up`;
the service pod reaches Ready in the resolved cluster, and `de project down` removes it. Delivers a
running in-cluster service end to end.

**Acceptance Scenarios**:

1. **Given** a project that declares a workload and a dependency, **When** `de project up` runs, **Then**
   the dependency is provisioned and the service workload is deployed to the resolved cluster and reaches
   a Ready/running state, reported to the user.
2. **Given** a deployed service, **When** `de project up` is re-run after a configuration/image change,
   **Then** the running workload is updated in place (rolled out) with no duplicate workload and no error.
3. **Given** a deployed service, **When** `de project down` runs, **Then** the service workload is removed
   from the cluster (its dependencies follow the existing `--clean` semantics from 003).

### User Story 2 - The deployed service reaches its dependencies and is addressable (Priority: P1)

The in-cluster service connects to its declared dependencies and is reachable over its declared dev
hostname, so a developer can exercise the running service the same way as the local-run model.

**Why this priority**: A deployed workload that can't reach its data or be called is not usable; this is
what makes the deploy meaningful rather than a bare pod.

**Independent Test**: Deploy a service with a Postgres dependency; from the deployed workload, connect to
the database over the injected connection info and read/write a row; resolve the service's dev hostname
and get a response from the in-cluster workload.

**Acceptance Scenarios**:

1. **Given** a deployed service with a declared dependency, **When** the workload starts, **Then** it
   receives its dependency connection info and can connect to the dependency in the same cluster.
2. **Given** a deployed service with a dev hostname, **When** a developer requests that hostname, **Then**
   the request is served by the in-cluster workload.

### User Story 3 - Many deployed services coexist safely (Priority: P2)

Multiple deployed services on the shared dev cluster run side by side without interfering, each isolated
to its own footprint (building on 004's co-existence guarantees).

**Why this priority**: The shared dev cluster hosts many projects; a deploy that clobbered another
project would break the shared-cluster model. Important, but only after a single service deploys well.

**Independent Test**: Deploy two services with the same internal names to the shared cluster; both run,
neither's workload or routing collides, and `down` on one leaves the other running.

**Acceptance Scenarios**:

1. **Given** two projects deployed to the shared cluster, **When** both are up, **Then** each runs in its
   own isolated workload footprint and neither disrupts the other.
2. **Given** two co-located deployed services, **When** one is taken `down`, **Then** the other remains
   running and addressable.

### Edge Cases

- **Image unavailable / pull failure**: deploy fails with an actionable message and leaves no
  half-deployed workload that blocks a retry.
- **Dependency not ready**: the workload deploy waits for (or surfaces) dependency readiness rather than
  crash-looping silently.
- **No dependencies**: a workload with no declared dependencies still deploys and runs.
- **Re-deploy / drift**: re-running `up` converges the running workload to the declared state idempotently.
- **Relationship to local-run**: a developer must not end up with the service running *both* locally and
  in-cluster for the same routes without a clear, single source of truth (see FR-010).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: A service MUST be able to declare a deployable **workload** in its configuration (the port
  it listens on, plus either a pre-built image reference or a build declaration — see FR-011), additive
  to the existing `kind: Service` schema.
- **FR-002**: `de project up` MUST deploy the declared workload onto the **resolved** cluster (004) after
  ensuring the cluster and provisioning declared dependencies (003), with no manual `kubectl`/`helm`.
- **FR-003**: The deployed workload MUST receive its declared dependencies' connection information so it
  can reach them within the same cluster, reusing 003's per-service isolation (no new credential model).
- **FR-004**: The deployed workload MUST be reachable over the service's declared dev hostname.
- **FR-005**: Deploy MUST be idempotent: re-running `up` converges the running workload to the declared
  state (image/config/replicas) without creating a duplicate workload.
- **FR-006**: `de project down` MUST remove only the requesting service's workload from the cluster; it
  MUST NOT remove the shared cluster or another project's workload. Dependency data follows existing
  `--clean` semantics.
- **FR-007**: When deploy cannot proceed (image pull failure, cluster/deploy error), devedge MUST exit
  non-zero with an actionable, retryable message and leave no half-deployed workload.
- **FR-008**: Multiple services deployed to the shared cluster MUST be isolated so that deploying,
  updating, or removing one never disrupts another (co-existence, building on 004).
- **FR-009**: devedge MUST report where and how the service is running (which cluster, workload status)
  so placement and health are observable.
- **FR-010**: In-cluster deploy MUST be an explicit **opt-in** (flag and/or config) that **complements**
  the default local-run inner loop — local-run remains the default. A given service's dev hostname MUST
  resolve to exactly one running instance at a time (local **or** in-cluster, never both simultaneously).
- **FR-011**: devedge MUST deploy a **pre-built image reference** declared by the service by default,
  **and** MUST build the image from the project when a build is declared (e.g. a Dockerfile). When devedge
  builds the image, it MUST make that image available to the resolved cluster (e.g. load it into k3d) so
  the workload can run it without an external registry.

### Key Entities

- **Workload**: the runnable form of a service in a cluster — its container image, listening port, and
  runtime parameters (e.g. replicas, resource hints). Owned by the service, deployed into the resolved
  cluster, isolated per service.
- **Deployed service footprint**: a service's in-cluster presence — its workload plus the routing that
  makes it addressable — distinct from (and additive to) its dependency footprint from 003.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A developer with a containerized service and no manual cluster setup runs one command
  (`de project up`) and the service is running in the resolved cluster, reachable by its dev hostname,
  with its dependencies connected — in 100% of clean-start runs.
- **SC-002**: Re-running `de project up` after a change updates the running service with zero duplicate
  workloads and no manual intervention.
- **SC-003**: Two services deployed to the shared dev cluster run simultaneously without interfering;
  taking one down leaves the other running and addressable in 100% of cases.
- **SC-004**: 100% of deploy failures (image unavailable, cluster/deploy error) produce an actionable
  message and leave no half-deployed workload that blocks a retry.
- **SC-005**: `de project down` removes the service's workload from the cluster and never affects the
  shared cluster or another project.

## Assumptions

- The resolved-cluster model (004) and dependency runtime (003) are prerequisites and unchanged; this
  feature adds the workload layer on top of them and reuses `de project chart`'s rendered service chart
  as the deploy artifact where practical.
- Routing reuses the existing devedge route/ingress model rather than introducing a new addressing scheme.
- Co-existence and per-service isolation follow 004's conventions (project/service-slug naming).
- For the build path (FR-011), a container build tool (e.g. Docker) is available; built images are loaded
  directly into the resolved cluster, so no external container registry is required.
- Local-run remains the default inner loop; in-cluster deploy is opt-in (FR-010), so this feature does
  not change the default `de project up` behavior for services that don't opt in.
