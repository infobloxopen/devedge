# Feature Specification: Service kind in devedge configuration

**Feature Branch**: `002-service-config-kind`
**Created**: 2026-05-31
**Status**: Draft
**Input**: User description: "Add a Service kind to devedge project configuration so a developer can declare a service's dev hostname, runtime dependencies, and routes in one config file, validated with clear errors, using a kind-dispatch parser seam so future kinds are additive"

## Clarifications

### Session 2026-05-31

- Q: What is the scope of strict unknown-field validation? → A: `Service` only; `Config` keeps its current lenient parsing unchanged (preserves backward compatibility per FR-002).

## User Scenarios & Testing *(mandatory)*

Today a devedge project file describes only routes (`kind: Config`). A developer working on a
single service has more to say about it than "forward this hostname to that port" — the service
has a development hostname and it has runtime dependencies (a database, a cache). This feature
lets a developer describe a *service* as a first-class thing in one project file, and have
devedge validate that description before anything is acted on. It deliberately stops at
description and routing; actually starting the declared dependencies is a separate, later feature.

### User Story 1 - Route a declared service through devedge (Priority: P1)

A developer writes a project file that declares their service (its development hostname and
routes) using the new `Service` kind, runs the existing project-up flow, and reaches the service
over a stable HTTPS hostname — exactly the experience `kind: Config` gives today.

**Why this priority**: This is the minimum that delivers value on its own. Without it, the new
kind is inert. With it, a developer can adopt the `Service` kind for real and lose nothing
relative to `kind: Config`.

**Independent Test**: Author a `Service` project file with one route, run project-up, and confirm
the route is registered and reachable over HTTPS; run project-down and confirm it is removed.

**Acceptance Scenarios**:

1. **Given** a valid `Service` project file declaring a hostname and one HTTP route, **When** the developer runs project-up, **Then** the route is registered and reachable over HTTPS, identically to an equivalent `kind: Config` file.
2. **Given** an existing `kind: Config` project file, **When** the developer runs any project command, **Then** behavior is unchanged from before this feature (full backward compatibility).
3. **Given** a `Service` project file with no routes, **When** project-up runs, **Then** the developer is told no routes were declared rather than receiving a silent success or an error.

---

### User Story 2 - Declare and validate runtime dependencies (Priority: P2)

A developer lists the service's runtime dependencies (e.g. a Postgres database, a Redis cache)
in the same project file. devedge validates each declaration and reports what it found, so the
contract is exercised and authoring mistakes are caught now — even though devedge does not yet
start those dependencies.

**Why this priority**: It establishes and freezes the dependency contract that the later runtime
feature will rely on, and gives immediate authoring-time feedback. It is valuable without the
runtime, but the service can still be routed (P1) without it.

**Independent Test**: Author a `Service` file declaring two dependencies, run project-up, and
confirm devedge reports the declared dependencies and clearly states that starting them is not
yet supported; then introduce a malformed dependency and confirm a clear validation error.

**Acceptance Scenarios**:

1. **Given** a `Service` file declaring two well-formed dependencies, **When** project-up runs, **Then** devedge reports the count and names of declared dependencies and states that starting them is not yet supported.
2. **Given** a dependency missing a required attribute (name, engine, or port), **When** the file is loaded, **Then** loading fails with a message naming the offending dependency and the missing attribute.
3. **Given** two dependencies sharing the same name, **When** the file is loaded, **Then** loading fails with a message identifying the duplicate.
4. **Given** a dependency whose engine is not a recognized value, **When** the file is loaded, **Then** loading fails with a message listing the recognized engines.

---

### User Story 3 - Clear errors for unsupported or malformed configuration (Priority: P3)

A developer who mistypes the kind, omits a required top-level field, or uses an unsupported kind
gets an actionable error that tells them what is supported and what to fix — not a vague parse
failure.

**Why this priority**: Good failure messages are what make the new kind safe to adopt, but the
happy paths (P1/P2) deliver the core value first.

**Independent Test**: Load a file with an unknown kind and confirm the error names the
unsupported kind and lists the supported ones; repeat for missing apiVersion and missing name.

**Acceptance Scenarios**:

1. **Given** a project file whose kind is neither `Config` nor `Service`, **When** it is loaded, **Then** the error names the unsupported kind and lists the supported kinds.
2. **Given** a project file missing `apiVersion`, `kind`, or `metadata.name`, **When** it is loaded, **Then** the error names the specific missing field.

---

### Edge Cases

- A `Service` file that declares dependencies but no routes is valid (dependencies-only is a
  legitimate intermediate state); project-up reports the dependencies and that no routes were declared.
- A dependency with a port outside the valid range (1–65535) is rejected with a clear message.
- A development hostname that is empty or not a valid hostname is rejected with a clear message.
- An empty file, or one that is not valid YAML, fails with a clear message rather than a panic.
- A file mixing recognized and unrecognized fields under a kind reports the unrecognized fields
  rather than silently ignoring them, so typos in field names are caught.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The project configuration MUST support a `Service` kind alongside the existing `Config` kind, under the same configuration API version.
- **FR-002**: Loading any project file MUST continue to support the existing `Config` kind with no change in observed behavior (backward compatibility).
- **FR-003**: A `Service` declaration MUST allow a developer to specify a development hostname, a list of runtime dependencies, and a list of routes.
- **FR-004**: Each dependency declaration MUST require a name, an engine, and a port, and MAY include a version.
- **FR-005**: The system MUST validate a loaded `Service` declaration and reject it with a specific, actionable message when: a required top-level field is missing; a dependency is missing a required attribute; two dependencies share a name; a dependency engine is not recognized; a dependency port is outside the valid range; or the development hostname is invalid.
- **FR-006**: When a project file uses an unsupported kind, the system MUST report the unsupported kind and list the supported kinds.
- **FR-007**: Unrecognized fields within a **`Service`** declaration MUST be reported rather than silently ignored, so field-name typos are caught at load time. The existing `Config` kind retains its current lenient parsing (unknown fields ignored) so that FR-002 backward compatibility is preserved exactly.
- **FR-008**: The project-up flow MUST register the routes declared by a `Service` file using the same registration behavior as `kind: Config`.
- **FR-009**: The project-up flow MUST report the dependencies declared by a `Service` file and MUST state that starting dependencies is not yet supported.
- **FR-010**: Adding the `Service` kind MUST be done in a way that lets further kinds be added later without reworking the dispatch of existing kinds. *(Internal extensibility requirement; verified by the structure permitting a new kind to be added without modifying existing kinds' handling.)*

### Key Entities *(include if feature involves data)*

- **Project file**: A single declarative configuration with an API version, a kind, metadata (including a name), and a kind-specific spec.
- **Service**: A kind of project file describing one service: its development hostname, its runtime dependencies, and its routes.
- **Dependency**: A runtime backing service the service needs (e.g. database, cache), described by name, engine, port, and optional version. Declared here; started by a later feature.
- **Route**: An existing concept — a hostname-to-upstream mapping registered with devedge.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A developer can convert an existing `Config` project file to a `Service` file and reach their service over HTTPS with no loss of routing behavior.
- **SC-002**: 100% of the invalid configurations enumerated in the requirements (missing field, duplicate dependency, unknown engine, out-of-range port, invalid hostname, unsupported kind, unknown field) are rejected at load time with a message that names the specific problem.
- **SC-003**: Every existing `Config` project file that loaded before this feature still loads with identical results (zero backward-compatibility regressions).
- **SC-004**: A developer reading project-up output for a `Service` file can tell which dependencies were declared and that starting them is not yet available, without consulting documentation.

## Assumptions

- **Scope of this feature is description plus routing, not dependency runtime.** Starting,
  stopping, health-checking, migrating, or seeding declared dependencies is explicitly out of
  scope and is the subject of a separate, later feature. This feature freezes the dependency
  contract and proves the authoring/validation experience.
- **Recognized dependency engines for this feature are `postgres` and `redis`.** These are the
  two common local backings; the recognized set can expand in later features. An unrecognized
  engine is a validation error (FR-005).
- **Dependencies are validated but not turned into routes** in this feature. Whether a
  dependency declaration should imply a route is a runtime concern deferred with the rest of
  dependency handling.
- **The development hostname follows the existing devedge hostname conventions** used by routes
  today; no new hostname scheme is introduced.
