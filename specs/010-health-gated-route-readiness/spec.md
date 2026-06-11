# Feature Specification: Health-Gated Route Readiness

**Feature Branch**: `010-health-gated-route-readiness`
**Created**: 2026-06-11
**Status**: Draft

## Context

When a developer runs `de project up` (local-run mode), routes are registered in the devedge
router immediately after dependencies are provisioned — regardless of whether the local service
is actually listening on its declared upstream. The result: Traefik starts forwarding traffic
to an upstream that may not respond for several seconds, producing 502 errors until the service
finishes starting.

This gap is most visible in the 007 onboarding walk-through, where the e2e script calls the
service immediately after `de project up` exits. In practice a developer hits the service URL
and sees a 502 until the service is ready.

**The fix:** a route declares an optional `readiness` block. When declared, `de project up`
probes the route's upstream health endpoint before calling `c.Register()` for that route.
Traffic is forwarded only once the service responds with 2xx. If the service does not become
ready within the configured timeout, devedge emits a warning and registers the route anyway —
preserving the current behavior as a safe, non-blocking fallback.

**Scope is tight and backward-compatible.** The probe is opt-in; absent `readiness` means the
current behavior unchanged. Local-run mode (`--deploy` absent) only. Deploy mode is explicitly
out of scope: `helm upgrade --install --wait` already gates on k8s pod readiness probes, making
an application-level pre-route probe redundant for that path. The ingress watcher
(`de cluster watch`) is also out of scope — it is asynchronous and not orchestrated by this
command. `kind: Config` documents are unaffected (`ParseService` strict-decoding already
rejects unknown fields; the new field is confined to `kind: Service`).

## Clarifications

- **Config location**: readiness config lives on each `RouteEntry` (per-route), not on
  `spec.dev`. This keeps the probe target co-located with the upstream it gates, handles
  multiple routes to different upstreams cleanly, and requires no target inference.
- **Probe target**: `http://<route.Upstream><path>` for non-TLS routes;
  `https://<route.Upstream><path>` (TLS skip-verify, local dev cert) for `backendTLS: true`.
- **Probe trigger**: local-run mode only (`--deploy` absent). With `--deploy`, the readiness
  block is parsed and stored but not consulted at registration time (deploy already gates via
  `helm install --wait`).
- **Timeout behavior**: timeout → warn (naming upstream + path) → register anyway → exit 0.
  Fail-fast on timeout is intentionally not the default; a developer may not have implemented
  `/healthz` yet and must not be blocked.
- **Watch mode (`-w`)**: probe fires exactly once per route at startup before the first
  `c.Register()`. Heartbeat renewals do not re-probe (the route is already live; liveness
  monitoring is a separate concern).
- **HTTP method**: GET only. HTTP 2xx = ready; anything else (connection refused, timeout,
  non-2xx status) = not-yet-ready, keep polling.
- **Defaults**: `path` defaults to `/healthz`; `timeout` defaults to `30s`; `interval`
  defaults to `500ms`. All three are overridable.
- **Multiple routes**: each route is probed independently against its own upstream; a route is
  registered as soon as its own probe passes, regardless of sibling routes.
- **Ctrl-C during probe**: context cancellation stops the probe immediately; no routes are
  registered; `de project up` exits cleanly (non-zero is fine on interrupt).
- **`kind: Config`**: not applicable. `RouteEntry` is shared but `ParseService` strict-decoding
  enforces the schema boundary; unknown fields in a `kind: Config` document are already
  rejected by the existing lenient parser path (the new field never surfaces there).

## User Scenarios & Testing

### User Story 1 — Readiness gate: route registered only when service is healthy (P1) 🎯 MVP

A developer adds `readiness.path: /healthz` to a route in their `devedge.yaml`. When they
run `de project up`, devedge prints a "waiting…" line and probes the upstream. Once the health
endpoint returns 200, the route is registered and a "registered (healthy)" confirmation is
printed. The developer can hit the service URL immediately after `de project up` exits without
seeing 502 errors.

**Acceptance Scenarios**:

1. **Given** a route with `readiness.path: /healthz` and a service that starts returning 200
   within 5 s, **When** `de project up` is run, **Then** the route is registered only after
   the first 2xx response, and the output includes a "waiting…" line followed by a registered
   confirmation.
2. **Given** a route with `readiness.path: /healthz` and a service that is already listening
   and healthy, **When** `de project up` is run, **Then** the probe returns on the first
   request and the route is registered with no perceptible delay.
3. **Given** a route without a `readiness` block, **When** `de project up` is run, **Then**
   the route is registered immediately without any probing (backward-compatible behavior).

**Independent Test**: unit test with an `httptest.Server` that returns 503 for the first N
requests then 200; assert the route is not registered until after the 200 is observed.

---

### User Story 2 — Timeout: warning emitted and route registered anyway (P1)

A developer has `readiness.path: /healthz` but the service does not implement the endpoint
(returns 404) or does not start within the timeout. `de project up` must not hang indefinitely
or exit non-zero: it warns and registers the route so the developer is not blocked.

**Acceptance Scenarios**:

1. **Given** a route with `readiness.path: /healthz` and `readiness.timeout: 2s`, **When**
   the upstream never returns 2xx within 2 s, **Then** `de project up` prints a warning that
   names the upstream and path, registers the route, and exits 0.
2. **Given** a route with `readiness.path: /healthz` and the upstream consistently returning
   404, **When** `de project up` is run with a short timeout, **Then** it times out, emits
   the warning, and still registers the route.

**Independent Test**: unit test with a mock upstream that always returns 404; assert warning
output contains the upstream address and path; assert `c.Register()` is called exactly once.

---

### User Story 3 — Config validation: malformed readiness block rejected at parse time (P2)

A developer mistypes a duration (`notaduration`) or leaves `path` empty. `ParseService`
returns a clear, field-named error before any cluster or network work begins.

**Acceptance Scenarios**:

1. **Given** `readiness.timeout: notaduration` in `devedge.yaml`, **When** the file is loaded,
   **Then** a validation error names `spec.routes[N].readiness.timeout` and exits non-zero.
2. **Given** `readiness.path: ""` (empty string with the block present), **When** the file is
   loaded, **Then** a validation error names `spec.routes[N].readiness.path`.
3. **Given** `readiness.timeout: 100ms` and `readiness.interval: 1s` (interval > timeout),
   **When** the file is loaded, **Then** a validation error names both fields.

**Independent Test**: table-driven unit tests of `Validate()` covering each invalid case;
no HTTP or cluster calls involved.

---

### User Story 4 — Watch mode: probe fires once, heartbeats skip re-probe (P2)

A developer runs `de project up -w`. The readiness probe fires before the first `c.Register()`
call. Subsequent heartbeat renewals do not re-probe the health endpoint.

**Acceptance Scenarios**:

1. **Given** a route with readiness config and watch mode (`-w`), **When** `de project up -w`
   runs and a heartbeat fires, **Then** the heartbeat renews the lease without calling the
   health endpoint again.
2. **Given** three heartbeat cycles have fired, **When** the probe-call count is inspected,
   **Then** it equals exactly 1 per route.

**Independent Test**: unit/integration test that counts HTTP probe calls; assert exactly one
call per route regardless of heartbeat count.

---

### Edge Cases

- What if the upstream address is malformed (no host:port)? → HTTP client returns an error on
  the first poll; treated as not-ready; the probe retries until timeout then warns+registers.
- What if `readiness.timeout` is shorter than `readiness.interval`? → validation error at
  parse time: timeout MUST be > interval (FR-007).
- What if `backendTLS: true`? → probe uses HTTPS with TLS verification disabled (local dev
  certificate; the probe client does not rely on the OS trust store).
- What if `de project up` is interrupted (Ctrl-C) during a probe? → context cancellation
  stops the poll immediately; no route is registered; exits cleanly.
- What if `spec.routes` is empty? → no readiness block can be declared; no probe, no-op.
- What if `--deploy` is set and a readiness block is present? → block is parsed/validated but
  silently ignored at registration time (FR-008).

## Requirements

### Functional Requirements

- **FR-001**: `RouteEntry` in `pkg/config/project.go` MUST gain an optional
  `Readiness *ReadinessSpec \`yaml:"readiness,omitempty"\`` field. `ReadinessSpec` has
  `Path string`, `Timeout string`, and `Interval string` (all YAML string; durations
  validated at parse time).
- **FR-002**: When `readiness` is absent on a route, `de project up` MUST register that
  route immediately — current behavior unchanged.
- **FR-003**: When `readiness` is present on a route and `--deploy` is absent, `de project up`
  MUST print a waiting message and probe `<route.Upstream><readiness.path>` (HTTP GET) before
  calling `c.Register()` for that route.
- **FR-004**: The probe MUST retry at the configured interval (default 500 ms) until the
  upstream returns any 2xx status code or the timeout elapses.
- **FR-005**: On timeout, `de project up` MUST print a warning to stdout naming the upstream
  address and path, MUST then call `c.Register()` for the timed-out route, and MUST exit 0.
- **FR-006**: `Validate()` MUST reject a `readiness` block where `path` is empty, where
  `timeout` or `interval` is not a valid Go duration string (via `time.ParseDuration`), or
  where either is ≤ 0.
- **FR-007**: `Validate()` MUST reject a `readiness` block where `timeout` ≤ `interval`.
- **FR-008**: With `--deploy` set, `de project up` MUST NOT probe the readiness endpoint;
  the block is parsed and validated but skipped at registration time.
- **FR-009**: With watch mode (`-w`), the probe MUST fire exactly once per route before the
  first `c.Register()` call; heartbeat renewals MUST NOT re-probe.
- **FR-010**: For a route with `backendTLS: true`, the probe MUST use HTTPS with TLS
  verification disabled.
- **FR-011**: `de project up` MUST print a waiting message (naming upstream + path) as soon
  as probing begins — before the first HTTP request — so the developer is not left waiting at
  a silent terminal.
- **FR-012**: The probe logic MUST live in a new `internal/health` package, not inline in
  `cmd/de/main.go`, to keep the orchestration layer thin and the probe logic independently
  testable.

### Key Entities

- **`ReadinessSpec`** (new, `pkg/config/project.go`): `Path string`, `Timeout string`,
  `Interval string`. Parsed as part of `RouteEntry`; validated by `ServiceConfig.Validate()`.
- **`RouteEntry`** (existing, `pkg/config/project.go`): gains `Readiness *ReadinessSpec`.
- **`internal/health.HTTPProber`** (new): accepts `upstream string`, `path string`,
  `timeout time.Duration`, `interval time.Duration`; exports `Probe(ctx context.Context)
  (ready bool, err error)` — returns `true` on 2xx, `false`+nil on timeout (probe exhausted),
  `false`+err on a hard configuration error. Caller decides warn-vs-fail.

## Success Criteria

- **SC-001**: A route with `readiness.path: /healthz` is never registered before the first
  2xx from the health endpoint (unit test: mock server delays 200 by N polls; assert
  `c.Register()` is not called until then).
- **SC-002**: A route without `readiness` is registered on the first `de project up` with
  behavior identical to pre-010; all existing unit + integration + k3d e2e tests pass without
  modification (`make test-e2e` green).
- **SC-003**: `de project up` exits 0 when a readiness timeout elapses; warning output
  contains the upstream address and health path (unit test asserts stdout content).
- **SC-004**: `Validate()` returns a named error for each invalid case: empty path, bad
  timeout duration, bad interval duration, timeout ≤ interval, timeout ≤ 0, interval ≤ 0
  (table-driven unit tests, one assertion per case).
- **SC-005**: All existing k3d e2e tests pass (`make test-e2e`); the 007 scaffold onboarding
  e2e in particular MUST remain green (no readiness block in scaffolded `devedge.yaml` →
  unchanged behavior).
- **SC-006**: Probe fires exactly once per `de project up` invocation in watch mode regardless
  of heartbeat count (unit test counting probe calls per route).
