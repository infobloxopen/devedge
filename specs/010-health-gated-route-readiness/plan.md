# Implementation Plan: Health-Gated Route Readiness

**Branch**: `010-health-gated-route-readiness` | **Date**: 2026-06-11 | **Spec**: `spec.md`

## Summary

Routes that declare an optional `readiness` block in `devedge.yaml` are probed (HTTP GET)
before `de project up` calls `c.Register()`. The probe retries at a configured interval until
the upstream returns 2xx or the timeout elapses; on timeout it warns and registers anyway
(non-blocking fallback). Opt-in; routes without `readiness` behave exactly as before.

Three new/modified units: a new `internal/health` package (probe logic, independently
testable), a config extension (`ReadinessSpec` on `RouteEntry`, validation in
`ServiceConfig.Validate()`), and a small orchestration addition in `cmd/de/main.go` (~25 LOC).

## Technical Context

**Language/Version**: Go 1.25.5  
**Primary Dependencies**: stdlib only — `net/http`, `context`, `time`. No new module deps.  
**Storage**: N/A  
**Testing**: `httptest.Server` (unit), `make test-e2e` k3d (e2e regression gate, SC-002/SC-005)  
**Target Platform**: macOS + Linux (portable stdlib; Constitution IV compliant)  
**Project Type**: CLI + daemon  
**Performance Goals**: probe overhead is negligible for dev-time use; 500 ms default interval  
**Constraints**: must not break any of the 5 existing k3d e2e tests; no new module deps

## Constitution Check

| Principle | Status | Evidence |
|-----------|--------|---------|
| **I. Edge-First DX** | ✅ | Probe prevents 502 immediately after `de project up`; waiting message gives visibility |
| **II. Spec-Driven, Test-First** | ✅ | Tests written before implementation; spec defines acceptance criteria and failure modes |
| **III. E2E Confidence** | ✅ | SC-002/SC-005: all k3d e2e pass unchanged; new unit tests cover probe logic end-to-end |
| **IV. Portable Core, Explicit Adapters** | ✅ | `internal/health` is stdlib-only; no OS-specific paths; no platform adapter needed |
| **V. Safe Reconciliation + Observable Operations** | ✅ | Waiting message + warn-on-timeout make probe state fully visible; timeout never silently drops a route |

## Project Structure

### Documentation

```text
specs/010-health-gated-route-readiness/
├── spec.md     ✅ done
├── plan.md     ✅ this file
└── tasks.md    (next: /speckit.tasks)
```

### Source Code

```text
internal/health/
├── probe.go          NEW — HTTPProber struct + Probe()
└── probe_test.go     NEW — unit tests (httptest.Server)

pkg/config/
├── project.go        MODIFY — add ReadinessSpec struct; add Readiness field to RouteEntry
├── project_test.go   MODIFY — add RouteEntry YAML round-trip tests (readiness field)
├── service.go        MODIFY — add readiness validation to ServiceConfig.Validate()
└── service_test.go   MODIFY — table-driven tests for all Validate() error cases (SC-004)

cmd/de/main.go        MODIFY — add probe call between deploy and c.Register() loop (~25 LOC)
```

## Architecture Decisions

### 1. `internal/health.HTTPProber`

```go
type HTTPProber struct {
    TargetURL string        // pre-built: "http(s)://host:port/path"
    Timeout   time.Duration // default 30s when zero
    Interval  time.Duration // default 500ms when zero
    Client    *http.Client  // injected; nil → default client with no redirects
}

// Probe polls until 2xx or done.
// Returns (true, nil)  on 2xx.
// Returns (false, nil) on timeout (internal deadline exceeded; caller should warn+proceed).
// Returns (false, err) if the outer ctx was cancelled (propagate up).
func (p *HTTPProber) Probe(ctx context.Context) (bool, error)
```

- `Probe` creates `context.WithTimeout(ctx, effectiveTimeout)` internally.
- Polls on a `time.NewTicker(effectiveInterval)` inside a select loop.
- Non-2xx and connection errors are treated as "not ready" and retried silently.
- On outer `ctx.Done()` (Ctrl-C), returns `(false, ctx.Err())` immediately.
- Default `http.Client` has `Timeout: effectiveInterval * 2` (avoids one slow request
  blocking the entire next poll cycle) and disables redirects.
- For `https` targets: TLS config skips verification (`InsecureSkipVerify: true`) — local
  dev cert; this is explicit and scoped to the probe client only.

### 2. URL construction (in `cmd/de/main.go`)

```go
scheme := "http"
if r.Readiness != nil && r.BackendTLS {
    scheme = "https"
}
targetURL := scheme + "://" + r.Upstream + effectivePath(r.Readiness)
```

`effectivePath` returns `r.Readiness.Path` (always non-empty after Validate); no default
applied here because Validate already rejects empty paths.

### 3. Default durations (applied in `HTTPProber`, not in Validate)

`Validate()` rejects non-empty duration strings that are invalid or ≤ 0.
Empty `timeout`/`interval` strings are valid ("use default"); the prober applies:
- `timeout == ""` or 0 → 30 s
- `interval == ""` or 0 → 500 ms

This separation keeps Validate() as a pure schema check and the prober as the behavior owner.

### 4. Output format (consistent with `cmd/de/color.go` palette)

```
# Before probe (printed before first HTTP request)
waiting for <colorHost(host)> <colorLabel("(upstream/path)")>...

# On success
registered <colorHost(host)> <colorLabel("->")> upstream <colorLabel("(healthy)")>

# On timeout
<colorWarning("warning:")> readiness timeout after Xs (upstream/path) — registering route
registered <colorHost(host)> <colorLabel("->")> upstream
```

### 5. Probe placement in `projectUpCmd()`

Insert between the deploy block and the register loop:

```go
// existing deploy block ...

// Health gate: probe before registering (FR-003). Skipped with --deploy
// (helm --wait already gates pod readiness) and when no readiness block (FR-002).
for _, r := range routes {
    if r.Readiness == nil || deployFlag {
        continue
    }
    // ... construct prober, print waiting, call Probe(), handle result
}

// existing register loop (unchanged except for "(healthy)" suffix on success)
for _, r := range routes {
    // ...
}
```

Probe and registration are kept as two separate loops for clarity; the register loop remains
structurally unchanged. A route without a readiness block goes through the register loop
identically to today.

### 6. Tradeoffs

| Decision | Chosen | Rejected | Reason |
|----------|--------|----------|--------|
| Timeout behavior | Warn + register (exit 0) | Fail-fast (exit 1) | Dev may not have implemented /healthz; blocking the developer is worse than a brief 502 window |
| Config location | Per-route `readiness` | `spec.dev.readiness` | Per-route keeps target co-located; no upstream inference needed; works cleanly with multiple routes to different upstreams |
| Probe impl | `internal/health` package | Inline in `cmd/de/main.go` | Isolated, independently testable per FR-012; keeps cmd lean |
| Multiple routes | Sequential probing | Parallel | Simplicity; the common case is one route; parallel adds output interleaving complexity |
| Defaults | Applied in prober (empty → default) | Applied in Validate (populate fields) | Validate is a pure schema check; prober owns runtime behavior |
| TLS in probe | Skip-verify | Full chain | Local dev cert; a probe client with strict TLS would require trust-store setup (defeats the purpose) |
