# Tasks: health-gated route readiness

**Branch**: `010-health-gated-route-readiness`
**Spec**: `specs/010-health-gated-route-readiness/spec.md`
**Plan**: `specs/010-health-gated-route-readiness/plan.md`

## Phase 1: Foundation — add config types (no behavior change yet)

- [X] T001 [S] Add `ReadinessSpec` struct and `Readiness *ReadinessSpec` field to `RouteEntry`
  in `pkg/config/project.go` (FR-001). `ReadinessSpec` fields: `Path string yaml:"path"`,
  `Timeout string yaml:"timeout,omitempty"`, `Interval string yaml:"interval,omitempty"`.
  No validation yet. Verify `go build ./...` passes and `go test ./pkg/config/...` is still
  green (no existing test should break — strict decoder for `kind: Service` accepts the new
  field; lenient decoder for `kind: Config` silently ignores unknown fields).

---

## Phase 2: Tests (write before implementation — must be red first)

- [X] T002 [S] [US3] Write table-driven validation tests in `pkg/config/service_test.go`
  covering every error branch added in T005:
  - readiness block present, `path: ""` → error names `readiness.path`
  - `path: "noslash"` (no leading `/`) → error names `readiness.path`
  - `timeout: "notaduration"` → error names `readiness.timeout`
  - `timeout: "-5s"` (negative parsed duration) → error names `readiness.timeout`
  - `interval: "notaduration"` → error names `readiness.interval`
  - `timeout: "100ms"` + `interval: "1s"` (interval > timeout) → error names both fields
  - valid block (`path: "/healthz"`, `timeout: "30s"`, `interval: "500ms"`) → no error
  - valid block (`path: "/healthz"`, all defaults omitted) → no error
  - no readiness block → no error (backward compat, SC-002)
  These tests MUST fail before T005 is implemented.

- [X] T003 [S] [US1/US2] Create `internal/health/probe_test.go` with four unit tests using
  `httptest.NewServer`:
  1. **Delayed 200**: server returns 503 for the first 3 calls then 200; prober with
     `Interval: 20ms`, `Timeout: 5s`; assert `Probe` returns `(true, nil)` and that at least
     4 HTTP requests were made (counter via `atomic.Int32`).
  2. **Timeout**: server always returns 404; prober with `Interval: 20ms`, `Timeout: 100ms`;
     assert `Probe` returns `(false, nil)` (timeout is not an error — SC-003).
  3. **Context cancel**: server never returns 2xx; cancel outer context after 50 ms; assert
     `Probe` returns `(false, non-nil-err)`.
  4. **Immediate 200**: server returns 200 on the very first request; assert `Probe` returns
     `(true, nil)` and exactly 1 HTTP call was made.
  These tests MUST fail (package doesn't exist yet) before T006 is implemented.

- [X] T004 [S] [US4] Add a watch-mode probe-count test in `internal/health/probe_test.go`:
  Construct an `HTTPProber` with a counter client that returns 200 immediately. Call `Probe`
  once. Assert call count == 1. (Watch-mode enforcement — that heartbeats don't re-probe — is
  structural in the cmd layer; this test confirms one `Probe` call = one health request cycle.)

---

## Phase 3: Implementation

- [X] T005 [S] [US3] Add readiness validation to `ServiceConfig.Validate()` in
  `pkg/config/service.go`. Iterate `c.Spec.Routes`; for each route `r` where
  `r.Readiness != nil`:
  - Reject if `r.Readiness.Path == ""` or does not start with `"/"`.
  - If `r.Readiness.Timeout != ""`: parse with `time.ParseDuration`; reject on error or ≤ 0.
  - If `r.Readiness.Interval != ""`: parse with `time.ParseDuration`; reject on error or ≤ 0.
  - If both non-empty and valid: reject if parsed `timeout <= interval`.
  Error messages MUST name the offending field as `spec.routes[N].readiness.<field>`.
  Run T002 tests — they must be green.

- [X] T006 [C] Implement `internal/health/probe.go`:
  ```
  package health

  type HTTPProber struct {
      TargetURL string
      Timeout   time.Duration  // 0 → default 30s
      Interval  time.Duration  // 0 → default 500ms
      Client    *http.Client   // nil → build default (no-redirect, per-req timeout)
  }

  func (p *HTTPProber) Probe(ctx context.Context) (ready bool, err error)
  ```
  Implementation:
  - `effectiveTimeout` = `p.Timeout` if > 0, else 30 s.
  - `effectiveInterval` = `p.Interval` if > 0, else 500 ms.
  - Build default client when `p.Client == nil`: `http.Client` with
    `Timeout: effectiveInterval * 2`, no redirect policy
    (`CheckRedirect: func(...) error { return http.ErrUseLastResponse }`),
    and a custom `Transport` with `TLSClientConfig: &tls.Config{InsecureSkipVerify: true}`
    (covers `https` probe targets; the TLS skip is intentional and scoped to this client only).
  - Create `inner, cancel := context.WithTimeout(ctx, effectiveTimeout)` + `defer cancel()`.
  - Ticker loop: `ticker := time.NewTicker(effectiveInterval)` + `defer ticker.Stop()`.
  - On each tick: send GET to `p.TargetURL`; if 2xx → return `(true, nil)`; otherwise
    close body and continue.
  - On `inner.Done()`: if `ctx.Err() != nil` → return `(false, ctx.Err())`; else return
    `(false, nil)` (internal timeout exhausted).
  - First tick fires immediately (call before entering the ticker select — or use a
    `time.After(0)` pre-probe) so there is no 500 ms delay on an already-healthy service.
  Run T003/T004 tests — they must be green.

- [X] T007 [S] [US1/US2/US4] Wire probe into `projectUpCmd()` in `cmd/de/main.go`. Insert a
  new probe loop immediately before the register loop (lines ~332-346). Structure:
  ```go
  // Track which routes passed the readiness check (for output annotation).
  healthyRoutes := make(map[string]bool, len(routes))

  for _, r := range routes {
      if r.Readiness == nil || deployFlag {
          continue  // FR-002 (no readiness block) + FR-008 (deploy mode)
      }
      scheme := "http"
      if r.BackendTLS {
          scheme = "https"
      }
      targetURL := scheme + "://" + r.Upstream + r.Readiness.Path
      fmt.Printf("%s %s %s\n",
          colorLabel.Sprint("waiting for"),
          colorHost.Sprint(r.Host),
          colorLabel.Sprintf("(%s)", targetURL),
      )
      prober := &health.HTTPProber{
          TargetURL: targetURL,
          // Timeout/Interval: leave zero → prober applies defaults (30s / 500ms)
          // Override only when explicitly declared in the config.
      }
      if r.Readiness.Timeout != "" {
          prober.Timeout, _ = time.ParseDuration(r.Readiness.Timeout)  // validated
      }
      if r.Readiness.Interval != "" {
          prober.Interval, _ = time.ParseDuration(r.Readiness.Interval)
      }
      ready, err := prober.Probe(context.Background())
      if err != nil {
          return fmt.Errorf("readiness probe for %s: %w", r.Host, err)  // Ctrl-C
      }
      if !ready {
          fmt.Printf("%s readiness timeout (%s) — registering route anyway\n",
              colorWarning.Sprint("warning:"), targetURL)
      }
      healthyRoutes[r.Host] = ready
  }
  ```
  In the register loop, annotate the output line:
  ```go
  suffix := ""
  if healthyRoutes[r.Host] {
      suffix = colorLabel.Sprint(" (healthy)")
  }
  fmt.Printf("registered %s %s %s%s\n",
      colorHost.Sprint(r.Host), colorLabel.Sprint("->"), r.Upstream, suffix)
  ```
  The heartbeat loop is unchanged — it never calls the probe (US4 / FR-009 satisfied
  structurally: the probe loop runs once before `c.Register()`; the heartbeat loop
  below only calls `c.Heartbeat()`).

---

## Phase 4: Verify

- [X] T008 [S] Run `go build ./...` and `go vet ./...` from the repo root; fix any issues.

- [X] T009 [S] Run all unit tests: `go test ./internal/health/... ./pkg/config/...`; T002,
  T003, T004 must be green (SC-001, SC-003, SC-004, SC-006).

- [X] T010 [S] Run `make test-e2e`; all 5 existing k3d e2e tests must pass without any test
  file modification (SC-002, SC-005). No `devedge.yaml` fixture in the e2e suite declares a
  `readiness` block, so the backward-compatible path (FR-002) is exercised implicitly.

---

## Phase 5: Commit

- [X] T011 [S] Commit all changes: spec + plan + tasks + `pkg/config/project.go` +
  `pkg/config/service.go` + test files + `internal/health/` + `cmd/de/main.go`.
  Message: `010: health-gated route readiness — probe upstream before route registration`.

---

## Dependencies & Execution Order

- T001 → T002, T003, T004 (tests reference new struct fields / new package)
- T002 (red) → T005 (make green)
- T003, T004 (red) → T006 (make green)
- T005 + T006 → T007 (wiring uses both validated config and prober)
- T007 → T008 → T009 → T010 → T011

## Complexity Tags

| Task | Tag | Reason |
|------|-----|--------|
| T001 | [S] | Mechanical struct addition; no logic |
| T002 | [S] | Table-driven unit tests; pure config validation paths |
| T003 | [S] | Unit tests with httptest.Server; counter via atomic; no new logic |
| T004 | [S] | Single-call probe count assertion |
| T005 | [S] | Validation logic: string parsing + comparisons; no new data structures |
| T006 | [C] | Goroutine lifecycle, context timeout, ticker loop, TLS config, HTTP client; multiple error paths |
| T007 | [S] | Mechanical wiring: URL construction, loop, Printf calls; no new algorithms |
| T008 | [S] | Mechanical: run commands, fix any vet issues |
| T009 | [S] | Mechanical: run tests, check pass/fail |
| T010 | [S] | Mechanical: run e2e, no code change required |
| T011 | [S] | Mechanical: git commit |
