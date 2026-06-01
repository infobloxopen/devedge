# Implementation Plan: Dependency runtime for the Service kind

**Branch**: `003-dependency-runtime` | **Date**: 2026-06-01 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/003-dependency-runtime/spec.md`

## Summary

Make a `Service` file's declared dependencies (feature 002) **real and reachable**. On
`de project up`, devedge ensures a **single shared instance per engine** (one Postgres, one Redis)
runs in the dev k3d cluster, then provisions a **per-service database/namespace + unique
credentials** inside it, exposes that instance to the locally-running service over a **stable dev
hostname** on the engine's standard port, and writes the connection out as an
**`infobloxopen/hotload` DSN** (the service's env var is an indirect DSN; the **real DSN** is
written to a file the hotload driver reads and hot-reloads). Data persists across `down`/`up`; an
explicit destructive form drops only the requesting service's database. Separately, `de project
chart` emits a **Helm chart** for the service + its dependencies — the environment-agnostic deploy
artifact — without the developer authoring Kubernetes/Helm.

The design reuses devedge's existing seams rather than inventing parallel ones, with **one
deliberate addition**: all **Kubernetes objects are rendered through Helm charts** (embedded
in-repo charts applied via the `helm` CLI), not hand-assembled YAML — so the dev-runtime instances
and the FR-010 emitted artifact share a single rendering path instead of two. Everything else is
existing machinery: the **event-driven registry + idempotent reconciler** (as routes already use),
**stable hostnames → EdgeIP `127.0.0.2` → Traefik TCP routing** (already present, and devedge's own
edge config — not a cluster object — keeps its existing renderer), and **co-existence-safe** test
helpers. No `client-go` and no Helm **SDK** are introduced; `kubectl`/`k3d`/`helm` are invoked as
CLIs, consistent with current practice. devedge does **not** import `hotload` — it only emits the
hotload DSN string and the real-DSN file; the developer's service imports the driver.

Because the surface is large, the plan sequences delivery by spec priority so each slice is an
independently shippable MVP: **Slice A (P1 — US1+US2)** start + isolate + reach Postgres;
**Slice B (P1)** the same for Redis; **Slice C (P2 — US3)** persistence + explicit wipe;
**Slice D (P3 — US4)** chart emission.

## Technical Context

**Language/Version**: Go 1.25.5 (from `go.mod`)
**Primary Dependencies**: standard library + `os/exec` + `embed` (in-repo Helm charts embedded via `go:embed`); the **`helm` CLI** is invoked as a subprocess to render/install all Kubernetes objects (joining `kubectl`/`k3d` as required CLIs); `gopkg.in/yaml.v3` (already in use, for building chart values). **No new Go module dependency** — notably *not* `client-go`, *not* the Helm **SDK** (the `helm` binary is used, not the library), and *not* `infobloxopen/hotload` (that is the consuming service's dependency, not devedge's).
**Storage**: shared Postgres/Redis run in-cluster backed by a PersistentVolumeClaim (data durability across `down`/`up`); devedge persists desired dependency state in the daemon registry and writes real-DSN files under `~/.devedge/`. devedge itself adds no database.
**Testing**: `go test` — unit (manifest/chart/DSN rendering, desired-vs-observed reconciliation with a fake provisioner), integration (`test/integration`, isolated daemon + temp dirs, co-existence-safe), e2e/k3d (`test/e2e`: deploy shared Postgres, provision a DB, connect over the reported DSN) gated on Docker/k3d availability — reported **skipped**, never passed, when unavailable.
**Target Platform**: macOS + Linux (devedge's supported platforms); rendering/provisioning logic is platform-agnostic.
**Project Type**: CLI + library + local control-plane daemon (single Go module).
**Performance Goals**: project-up readiness wait is bounded (default 60s timeout per dependency, configurable); reconciliation is idempotent and re-entrant; a no-dependency `Service`/`Config` adds zero new work.
**Constraints**: co-existence-safe in the shared dev k3d cluster (no disturbance to a shared devedge daemon or other tenants); deterministic rendered manifests/charts (inspectable + diffable in tests); **zero behavior change** for `kind: Config` and for `Service` files that declare no dependencies; no new third-party Go dependency.
**Scale/Scope**: one shared instance per engine serving many services; ~4 new internal packages + CLI wiring + daemon endpoints; engines limited to `postgres` and `redis`.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design.*

| Principle | Assessment |
|-----------|------------|
| **I. Edge-First Developer Experience** | PASS — dependencies are reached by a **stable hostname on a standard port** (names over raw ports), the developer never touches the cluster or constructs a DSN by hand, and `up`/`down` are idempotent and safe to repeat. New setup complexity (starting backing stores) is justified by the core feature value and is opt-in (only engaged when a `Service` declares dependencies). |
| **II. Spec-Driven, Test-Driven Delivery** | PASS — proceeding through Spec Kit; `/speckit.tasks` will place test tasks before implementation tasks for each slice (render unit tests, reconcile tests with a fake provisioner, an integration test, and a k3d e2e for the critical connect path). |
| **III. End-to-End Confidence Over Mocked Comfort** | PASS (with note) — the critical promise (a declared dependency becomes reachable) is **integration behavior across boundaries**, so the plan mandates a **k3d e2e** that deploys the shared Postgres, provisions a service DB, and connects using the reported DSN. Where Docker/k3d is unavailable the e2e is **skipped with a stated reason**, never claimed passed. Tests are **co-existence-safe**: unique service/db names, self-cleanup, isolated daemon (see memory: `dev-k3d-shared-cluster-model`). |
| **IV. Portable Core, Explicit Platform Adapters** | PASS — the desired-state/reconcile logic and all rendering are **platform-agnostic Go**; the cluster touchpoints (apply manifests, exec into a pod to provision a DB) sit behind an explicit **`Provisioner` adapter** with a fake used in tests, mirroring the existing `cluster.Provider` seam. No core logic embeds k3d/kubectl assumptions. |
| **V. Safe Reconciliation and Observable Operations** | PASS — dependency desired state vs. observed state are distinct (declared dependencies vs. what's running/provisioned); reconciliation is idempotent (FR-008); failures are explicit, per-dependency, and **retryable without half-provisioned residue** (FR-009); every provision/teardown emits structured logs and the default `down` never destroys data (FR-005/FR-007). Safety over silent recovery: a readiness timeout fails loudly (FR-004). |

**Result: PASS — no violations.** The one item to keep honest is the k3d e2e (Principle III); it is a required task, skipped-with-reason only when the runtime is absent. Complexity Tracking intentionally empty.

**New external dependency — `helm` CLI (justified per Engineering Standards).** This feature adds
the `helm` binary as a required CLI alongside the existing `kubectl`/`k3d`. Justification: (a) the
feature's own deliverable is a Helm chart (FR-010), so Helm is intrinsic, not incidental; (b)
rendering all k8s objects through Helm gives one deterministic, inspectable rendering path for both
the dev runtime and the emitted artifact, versus two hand-rolled renderers; (c) it is the `helm`
**CLI as a subprocess** (the established devedge pattern), so **no new Go module** — notably not the
Helm SDK or `client-go`. Charts are embedded via `go:embed` so devedge stays self-contained (no
runtime dependency on an external chart repo). Absence of `helm` is detected and reported with an
actionable message (Principle IV: unsupported platform behavior fails clearly).

## Backward compatibility & external consumers

A known downstream consumer — **`Infoblox-CTO/platform.data.kit`** (the `dk` toolkit) — uses devedge
via the CLI/daemon only (not as a Go import) and runs its own k3d + Helm. Its **entire** devedge
surface is: (1) `de cluster watch <cluster>` (background, best-effort, skips if `de` is absent);
(2) the daemon HTTP API at `http://127.0.0.1:15353` — `PUT /v1/routes` and
`DELETE /v1/projects/{project}`; (3) a `kind: Config` `devedge.yaml` (routes only). It does **not**
use `de project up`, the devedge `Service` kind, or devedge-declared dependencies.

003 is therefore non-breaking **iff** it holds these invariants (each is a checkable constraint, and
a regression test task will assert them):

| Invariant | Why (DK dependency) |
|-----------|---------------------|
| Daemon route API is **additive only** — `PUT/GET/DELETE /v1/routes`, `/v1/routes/{host}`, `DELETE /v1/projects/{project}`, `GET /v1/status` keep path, **port `15353`**, request, and response shape unchanged; deps live under new `/v1/services/{svc}/dependencies`. | DK does `PUT /v1/routes` + `DELETE /v1/projects/{project}` at `:15353`. |
| Route **registry + reconciler + ingress-watcher / `de cluster watch`** semantics unchanged; the dependency reconciler runs **beside** them, never alters them. | DK relies on `de cluster watch` auto-registering `devedge.io/expose=true` Ingresses. |
| `kind: Config` and `Service`-without-dependencies behavior unchanged (already FR-002 / FR-013). | DK's `devedge.yaml` is `kind: Config`. |
| 003 cluster workloads are confined to a dedicated namespace (`devedge-deps`); 003 never mutates other namespaces, charts, or Helm releases. | DK installs its own charts (`dk-dashboard`, `cube`, …) in the same potentially-shared cluster. |

These extend the co-existence requirement (Principle III / `dev-k3d-shared-cluster-model`) to a
concrete named consumer. A `/speckit.tasks` regression task will exercise the route API + a
`kind: Config` up/down to prove no regression.

## Project Structure

### Documentation (this feature)

```text
specs/003-dependency-runtime/
├── plan.md              # This file
├── research.md          # Phase 0 output — key decisions + alternatives
├── data-model.md        # Phase 1 output — entities + state
├── quickstart.md        # Phase 1 output — author a Service w/ deps and connect
├── contracts/           # Phase 1 output
│   └── dependency-runtime.md   # CLI commands, daemon API, DSN/file + chart contracts
└── tasks.md             # Phase 2 output (/speckit.tasks — NOT created here)
```

### Source Code (repository root)

```text
pkg/config/
└── service.go           # MODIFIED — small additions only: helpers on Dependency (e.g. default
                          #   port per engine, env-var name derivation). No schema change; the
                          #   002 Dependency contract is frozen.

internal/depruntime/      # NEW — portable core: dependency desired-state + reconciliation.
├── desired.go            #   Desired state from a Service's dependencies (pure).
├── reconcile.go          #   Idempotent reconcile: ensure shared instance, provision per-service
│                          #     db/creds, write DSN files, converge; diff desired vs observed.
├── provisioner.go        #   Provisioner adapter interface (EnsureInstance, EnsureDatabase,
│                          #     DropDatabase, Ready) + fake for tests. Real impl wraps kubectl.
└── *_test.go             #   Reconcile/desired unit tests against the fake provisioner.

internal/helm/             # NEW — Helm is the single renderer for all k8s objects.
├── charts/               #   In-repo Helm charts embedded via go:embed:
│   ├── postgres/         #     shared Postgres instance (StatefulSet + Service + PVC).
│   ├── redis/            #     shared Redis instance.
│   └── service/          #     a Service's Deployment/Service + abstract dependency claims
│                          #       (the FR-010/FR-011 emitted artifact template).
├── embed.go              #   //go:embed charts/* — exposes the chart FS.
├── helm.go               #   helm CLI adapter: Render (helm template), Install (helm upgrade
│                          #     --install), Uninstall, Lint — all via os/exec; values from yaml.v3.
└── helm_test.go          #   golden-render tests (deterministic `helm template` output).

internal/render/
└── traefik.go            # MODIFIED (small) — add a TCP entrypoint/router per dependency engine
                          #   in devedge's OWN edge config (not a cluster object → stays here).

internal/dsn/             # NEW — connection emission.
├── dsn.go                #   Build real DSN (postgres/redis) + the hotload indirect DSN; derive
│                          #     file paths and env-var names; atomic file write.
└── dsn_test.go

internal/cluster/
└── exec.go               # NEW (small) — kubectlExec helper (exec into the shared instance pod to
                          #   run psql/redis-cli for db/credential provisioning), beside the
                          #   existing kubectlApply pattern in bootstrap.go.

internal/daemon/
├── api.go                # MODIFIED — new endpoints: PUT/DELETE /v1/services/{name}/dependencies
│                          #   (desired state in), GET for status; reuses server wiring.
└── depstore.go           # NEW — registry of declared service dependencies (mirrors registry.go:
                          #   event-driven, drives the dependency reconciler).

internal/reconciler/
└── dependency.go         # NEW — subscribes to depstore events; calls depruntime.Reconcile;
                          #   runs alongside the existing route reconciler in the daemon.

internal/client/
└── client.go             # MODIFIED — client methods for the new daemon endpoints.

cmd/de/
├── main.go               # MODIFIED — `project up` provisions declared deps and prints the env var
│                          #   + DSN-file path per dependency (replaces 002's "not yet supported");
│                          #   `project down` releases them (default: keep data) with `--clean`.
└── chart.go              # NEW — `de project chart [-o dir]` emits the Helm chart (FR-010).

test/integration/
└── dependency_runtime_test.go  # NEW — daemon + fake provisioner: declare deps, reconcile,
                                 #   assert db/creds provisioned, DSN file + hotload env var,
                                 #   idempotent re-up, down (keep data) vs --clean (drop), isolation.

test/e2e/
└── dependency_postgres_test.go # NEW — k3d: deploy shared Postgres, provision a service DB,
                                 #   connect via the reported DSN, write+read; skipped-with-reason
                                 #   when Docker/k3d absent.
```

**Structure Decision**: Single Go module. The **portable core** (`internal/depruntime`,
`internal/dsn`) holds the decision logic and is unit-tested without a cluster via the `Provisioner`
fake. **All Kubernetes objects are rendered by Helm** through `internal/helm` (embedded charts +
the `helm` CLI), giving the dev-runtime instances and the FR-010 artifact one rendering path.
**Cluster-specific behavior** is isolated behind the `Provisioner` adapter (Helm install/uninstall
+ the `cluster/exec.go` SQL-provisioning helper), with a fake for tests. **Reconciliation lives in
the daemon** (a `dependency.go` reconciler beside the route reconciler) so dependencies are a
first-class, observable control-plane concern (Principle V) rather than one-shot CLI side effects.

## Architecture decisions

1. **Shared instance per engine, isolated by database + credentials (FR-002, clarification).** One
   Postgres and one Redis run in the dev cluster, each **deployed by Helm** from an embedded in-repo
   chart (`internal/helm/charts/{postgres,redis}`) via `helm upgrade --install` (idempotent by
   construction). Per-service isolation is a dedicated database + unique role/password (Postgres) or
   an ACL user + key namespace / logical DB index (Redis), created by `kubectl exec` into the
   instance pod (`psql` / `redis-cli` ship in those images — no new host dependency), idempotently
   (create-if-absent). The shared instance and its PVC are never dropped by a service's `down`.

2. **Connectivity: stable hostname → EdgeIP → Traefik TCP entrypoint per engine (FR-003).** Each
   engine gets a stable dev hostname (e.g. `postgres.dev.test`, `redis.dev.test`) resolving to the
   EdgeIP `127.0.0.2` via the existing DNS/`/etc/hosts` path, and a dedicated Traefik **TCP
   entrypoint** on the engine's standard port (`5432`/`6379`) with a catch-all `HostSNI("*")` router
   forwarding to the in-cluster Service. The declared `port` is honored. This reuses
   `internal/render` + the DNS seam and keeps "names over ports"; raw-TCP (non-SNI) routing is why a
   dedicated entrypoint per engine is used rather than SNI multiplexing (detailed in research.md;
   `localhost` port-forward considered and rejected there).

3. **Connection emission: hotload DSN env var + real-DSN file (FR-003/003a/003b).** devedge writes
   the **real DSN** (e.g. `postgres://<svc-user>:<pw>@postgres.dev.test:5432/<svc-db>`) atomically
   to `~/.devedge/services/<service>/<dep>.dsn`, and reports the env var as the **hotload indirect
   DSN** in hotload's actual scheme — strategy as URL scheme, real driver as host, file as path:
   `fsnotify://postgres/<abs-path-to>/<dep>.dsn`. The app connects through the hotload driver and
   reloads on file change. **The same pattern is used for every engine, including Redis**
   (`fsnotify://redis/<abs-path>` → real `redis://…` DSN in a file) even though hotload's stock
   driver backs only SQL — the indirect-DSN-env-var + real-DSN-file is the contract; a
   reload-capable client is the consuming app's concern (see research.md). devedge does not import
   hotload; the chart (Slice D) wires the same env var + a secret-mounted file.

4. **Daemon-owned reconciliation mirroring routes (Principle V; FR-008/FR-009).** `project up`
   sends declared dependencies to the daemon (`PUT /v1/services/{name}/dependencies`); a `depstore`
   holds desired state and emits events; a dependency reconciler converges (ensure instance →
   provision db/creds → write DSN → ready-check), idempotently and re-entrantly. `down` sends
   `DELETE` (default keeps data; `--clean` drops the service's database). Failures are per-dependency
   and leave nothing half-provisioned that blocks a retry.

5. **Helm is the one renderer; the emitted chart is the same chart (FR-010/FR-011).** All k8s
   objects come from the embedded charts in `internal/helm/charts` rendered by the `helm` CLI — no
   hand-assembled YAML strings. `de project chart` materializes the `service` chart (the developer's
   Deployment/Service + per-dependency **abstract claim**) to an output dir; the dev runtime renders
   the same templates. Dependencies are expressed **abstractly** (a `dependencies` values list +
   a templated claim) so the chart maps to the shared logical DB in dev and a dedicated instance via
   the org DB abstraction in a real cluster (FR-011). Charts are validated with `helm lint`, and
   `helm template` output is golden-tested for determinism. This feature **emits/installs** charts;
   it does not execute a real-cluster (prod) deploy.

6. **Readiness + timeouts (FR-004).** Each dependency reconcile waits for the instance to accept
   connections and for the service DB to exist, bounded by a configurable timeout, then fails
   loudly and retryably. Background loops use bounded retries/cancellation per the constitution.

7. **Seam preserved for future app-owned charts (deferred — see spec Out of Scope).** In the
   `service` chart, the **dependency-claim + DSN-secret templates are kept separable** from the
   service `Deployment` template (own files under `templates/`, parameterized only by the
   `dependencies` values + secret name). This is a structural choice, not extra build scope: it
   means a later "bring-your-own workload chart" feature can have devedge inject just the
   claim/secret piece (or pass a documented values contract) while the app owns its `Deployment` —
   without reworking 003. Connection delivery is already a convention (the `fsnotify://` env var +
   secret-mounted file), and provisioning is daemon-side and chart-independent, so neither assumes
   devedge renders the app's workload. We do **not** add a `spec.chart` field or user-chart install
   here.

## Complexity Tracking

> No Constitution Check violations — section intentionally empty.
