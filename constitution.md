<!--
Sync Impact Report
Version change: N/A → 1.0.0
Modified principles: Initial ratification
Added sections:
- Engineering Standards & Quality Gates
- Delivery Workflow & Governance
Removed sections:
- None
Templates requiring updates:
- ⚠ pending .specify/templates/plan-template.md
- ⚠ pending .specify/templates/spec-template.md
- ⚠ pending .specify/templates/tasks-template.md
- ⚠ pending .specify/templates/commands/*.md
Follow-up TODOs:
- None
-->

# Devedge Constitution

## Core Principles

### I. Edge-First Developer Experience

Every developer-facing capability MUST optimize for a predictable local edge experience built around stable FQDNs, trusted HTTPS, and minimal manual setup. A developer MUST be able to install Devedge, start it, register routes, and reach services through consistent hostnames without hand-editing proxy configuration.

Implications:

* Developer workflows MUST prefer names over raw ports for browser-facing services.
* The default experience MUST work on a single machine with no dependency on public DNS or external hosted control planes.
* Project setup and cleanup MUST be idempotent and safe to repeat.
* Any feature that increases setup complexity MUST justify the complexity with measurable developer value.

Rationale: Devedge exists to make local development environments feel like a coherent platform rather than a collection of ad hoc ports, scripts, and certificates.

### II. Spec-Driven, Test-Driven Delivery

All material changes MUST begin with a spec and MUST be implemented test-first. Work MUST flow through Spec Kit artifacts before code is merged: constitution, specification, plan, tasks, implementation, and analysis when applicable. Tests MUST be written or updated before the implementation that satisfies them.

Requirements:

* Each feature spec MUST define acceptance criteria, failure modes, and observable behavior.
* Each implementation plan MUST document architecture choices, tradeoffs, and constitutional checks.
* Tasks MUST map back to requirements and testing work; implementation tasks without corresponding test tasks are incomplete.
* Red-green-refactor is the default development loop for unit, integration, and end-to-end behavior.

Rationale: Devedge coordinates routing, certificates, DNS, and cluster integration. Test-first, spec-driven work reduces regressions in the parts that are hardest to reason about after the fact.

### III. End-to-End Confidence Over Mocked Comfort

Devedge MUST prove its value through realistic end-to-end testing, not only through isolated unit tests. The project MUST maintain automated end-to-end coverage using k3d to validate the core experience of route registration, HTTPS termination, host-based routing, reconfiguration, cleanup, and failure recovery.

Requirements:

* Critical user flows MUST have end-to-end tests that exercise the real edge stack where practical.
* k3d-based tests MUST cover route registration, lease expiry, deregistration, and forwarding into cluster-backed services.
* Mocked tests MAY be used for narrow failure injection and fast feedback, but they MUST not replace end-to-end verification for critical paths.
* A change that alters routing, DNS, certificate handling, background process behavior, or cluster integration MUST include end-to-end impact assessment.

Rationale: The product promise is integration behavior across boundaries. Confidence must come from exercising those boundaries directly.

### IV. Portable Core, Explicit Platform Adapters

Devedge MUST keep core logic portable and isolate platform-specific behavior behind explicit adapters. The registry, reconciliation engine, rendering logic, and policy enforcement MUST be platform-agnostic Go components. OS-specific installation, service management, privilege escalation, and DNS integration MUST be implemented as replaceable adapters with clear contracts.

Requirements:

* Core packages MUST not embed platform assumptions when those assumptions can be isolated.
* Platform adapters MUST expose health and capability checks through a uniform interface.
* Unsupported platform behavior MUST fail clearly with actionable diagnostics.
* New platform support MUST be added by extending adapter layers rather than forking core behavior.

Rationale: Devedge must work across macOS, Linux, and eventually Windows without fragmenting the architecture or the test strategy.

### V. Safe Reconciliation and Observable Operations

Devedge MUST behave like a small control plane: state changes MUST be explicit, reconciliation MUST be deterministic, and operations MUST be observable. Route registration, renewal, deregistration, certificate issuance, and config reloads MUST leave a trace that operators and developers can inspect.

Requirements:

* Desired state and observed state MUST be distinguishable in code and in operator-facing output.
* Reconfiguration MUST be atomic or roll back safely when atomicity is not possible.
* Every route mutation MUST emit structured logs and machine-readable status.
* The system MUST expose diagnostics that help explain why a route is active, stale, conflicting, or unhealthy.
* Safety takes precedence over silent recovery; when the system cannot guarantee correctness, it MUST surface a clear error.

Rationale: A local edge daemon can fail in confusing ways unless reconciliation and visibility are first-class concerns.

## Engineering Standards & Quality Gates

### Architecture & Implementation

* The primary implementation language MUST be Go, except where a small supporting component is materially better served by another language.
* The control plane MUST prefer simple, inspectable state transitions over clever implicit behavior.
* Public interfaces MUST be versioned deliberately and kept small.
* External dependencies MUST be justified by maintenance cost, portability, and operational value.
* Configuration files and generated artifacts MUST be deterministic so they can be inspected and compared in tests.

### Testing Pyramid

* Unit tests MUST cover pure logic, parsers, renderers, and reconciliation decisions.
* Integration tests MUST cover interactions with bundled runtimes, filesystem rendering, background process management, and adapter boundaries.
* End-to-end tests MUST run against k3d-based environments for the critical scenarios defined in feature specs.
* Flaky end-to-end tests are treated as defects. Tests that cannot pass reliably MUST be repaired, quarantined with a tracked issue, or removed.

### Performance & Reliability

* Startup, registration, reconciliation, and teardown paths MUST have measurable performance expectations in the relevant spec or plan.
* New features MUST define their operational impact on latency, memory, and process count when material.
* Background loops MUST use bounded retries, timeouts, and cancellation.
* Long-running components MUST be restart-safe and idempotent.

### Security & Trust

* Locally trusted certificate handling MUST minimize secret exposure and document trust implications.
* Privileged operations MUST be isolated and minimized.
* Features that affect traffic routing or certificate trust MUST document misuse and failure scenarios.
* Security-through-obscurity is not an acceptable control.

## Delivery Workflow & Governance

### Workflow

1. Amend the constitution only through explicit constitutional changes.
2. Create or update the feature spec before implementation begins.
3. Produce an implementation plan that includes a Constitution Check against all core principles.
4. Generate tasks that include testing work before coding work.
5. Implement with TDD and keep specs, plan, and tasks aligned.
6. Run constitution-aware analysis before merge when the change is material.

### Compliance

* Constitution conflicts are blockers. Specs, plans, tasks, and code MUST be corrected to comply; the constitution MUST not be silently bypassed.
* Any exception MUST be explicit, time-bound, and approved in the relevant spec or plan with rationale and follow-up work.
* Pull requests SHOULD cite the spec and summarize constitutional impact when the change affects routing, trust, platform adapters, or end-to-end behavior.

### Amendment Policy

* This constitution follows semantic versioning.
* MAJOR versions indicate incompatible governance changes or redefinition/removal of principles.
* MINOR versions add a principle, section, or materially expand guidance.
* PATCH versions clarify wording without changing project obligations.

### Review Expectations

* Reviews MUST verify that specs remain the source of truth for intent and that implementation details live in plans and code, not in product requirements.
* Reviews MUST verify test evidence appropriate to the level of change.
* Reviews MUST reject vague compliance claims that do not map to concrete code, tests, or observable behavior.

## Governance

This constitution is the binding quality contract for Devedge. It governs planning, implementation, review, and release decisions. When other project artifacts conflict with this constitution, this document takes precedence.

Amendments MUST be made intentionally, reviewed like code, and propagated to dependent Spec Kit artifacts. Every amendment MUST include a version decision, a rationale, and a sync impact review for templates and command prompts that rely on constitutional rules.

Version: 1.0.0 | Ratified: 2026-03-11 | Last Amended: 2026-03-11

