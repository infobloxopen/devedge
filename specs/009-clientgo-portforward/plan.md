# Implementation Plan: client-go port-forward migration

**Branch**: `009-clientgo-portforward` | **Date**: 2026-06-10 | **Spec**: `specs/009-clientgo-portforward/spec.md`

## Summary

Single-file rewrite: `internal/cluster/portforward.go` drops the `kubectl` subprocess and
replaces it with `k8s.io/client-go`'s native SPDY portforwarder. The public API
(`PortForward.LocalPort`, `Alive()`, `Stop()`, `StartPortForward(...)`) is unchanged — the
single caller (`internal/depruntime/realprov.go`) needs no modification. `go.mod` gains
`k8s.io/client-go` and its required indirect deps.

## Technical Context

**Language/Version**: Go (devedge module, go 1.25.5)
**New direct dependency**: `k8s.io/client-go` (latest stable ≥ v0.32; tracks k8s API version
used by k3d's bundled k3s)
**New indirect dependencies**: `k8s.io/apimachinery`, `k8s.io/api`, `k8s.io/client-go/rest`,
`k8s.io/client-go/tools/clientcmd`, `k8s.io/client-go/transport/spdy`
**Affected packages**: `internal/cluster/portforward.go` (full rewrite); `go.mod` + `go.sum`
**Unchanged**: `internal/depruntime/realprov.go`; all test files; all other cluster/*.go files
**Testing**: unit tests in `internal/cluster/portforward_test.go` (target parser + fake-server
lifecycle); existing k3d e2e suite as regression check

## Constitution Check

- **I. Edge-First DX** — PASS: removing the subprocess eliminates the kuberlr download race
  (friction #5); `de project up` now works on machines where `kubectl` is absent or a
  slow-initializing shim, with no developer intervention.
- **II. Spec-Driven, Test-Driven** — PASS: target parser tests are written before the parser
  implementation; fake-server lifecycle test written before the portforward logic; tasks order
  accordingly.
- **III. End-to-End Confidence** — PASS: all existing k3d e2e tests that exercise
  `de project up` remain the regression suite; no e2e test file is modified. The portforward
  path is the same critical boundary — the existing e2e tests provide the required coverage.
- **IV. Portable Core, Explicit Platform Adapters** — PASS: this change is the Constitution
  IV concern: replacing a platform subprocess with a portable Go library. No OS-specific
  code is introduced; `k8s.io/client-go` is a pure Go library that works identically on macOS,
  Linux, and Windows.
- **V. Safe Reconciliation & Observable Ops** — PASS: the new implementation surfaces errors
  immediately (dial failure, context-not-found) with the same `StartPortForward` error return
  contract; the goroutine lifecycle is explicit (`stopCh chan struct{}`, `sync.Once` for
  idempotent Stop).

No violations. No complexity escalation needed.

## Implementation Design

### `StartPortForward` internals (new)

```
1. Parse target: "statefulset/<name>" → pod "<name>-0"
   Any other format → return error immediately (no network call).

2. Load kubeconfig:
   clientcmd.NewDefaultClientConfigLoadingRules()       // respects $KUBECONFIG
   + ConfigOverrides{CurrentContext: kubeContext}       // only when kubeContext != ""
   → rest.Config

3. Build SPDY dialer:
   url = restConfig.Host + "/api/v1/namespaces/<namespace>/pods/<pod>/portforward"
   roundTripper, upgrader, err = spdy.RoundTripperFor(restConfig)
   dialer = spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper},
                           http.MethodPost, parsedURL)

4. Create portforwarder:
   stopCh  = make(chan struct{})   // closed by Stop()
   readyCh = make(chan struct{}, 1)
   pf, err = portforward.New(dialer, []string{fmt.Sprintf("0:%d", remotePort)},
                             stopCh, readyCh, io.Discard, io.Discard)

5. Start in goroutine:
   go func() { _ = pf.ForwardPorts(); once.Do(func() { pf.markDone() }) }()

6. Wait (with 30 s timeout) on readyCh → then GetPorts()[0].Local → PortForward.LocalPort.
   Timeout or ForwardPorts error → cancel (close stopCh) + return error.
```

### `PortForward` struct (new)

```go
type PortForward struct {
    LocalPort int
    stopCh    chan struct{}
    stopOnce  sync.Once
    mu        sync.Mutex
    done      bool
}

func (pf *PortForward) Stop()        { pf.stopOnce.Do(func() { close(pf.stopCh) }) }
func (pf *PortForward) Alive() bool  { pf.mu.Lock(); defer pf.mu.Unlock(); return !pf.done }
func (pf *PortForward) markDone()    { pf.mu.Lock(); pf.done = true; pf.mu.Unlock() }
```

### Timeout change

The old 20 s timeout raced the kuberlr download. The new implementation has no download
dependency; 30 s is generous for a TCP connection + SPDY handshake to a local cluster.

## File Map

| File | Change |
|------|--------|
| `internal/cluster/portforward.go` | Full rewrite — drop `os/exec`, add client-go SPDY |
| `internal/cluster/portforward_test.go` | New — target parser + lifecycle unit tests |
| `go.mod` | Add `k8s.io/client-go` direct dep |
| `go.sum` | Updated by `go mod tidy` |

**No other files change.**

## Ordering

1. Add dependency (`go get k8s.io/client-go`) — unblocks everything.
2. Write unit tests (portforward_test.go) — red.
3. Implement portforward.go — green.
4. Run `go vet ./... && go build ./...` — SC-003.
5. Run `make test-e2e` — SC-002 regression.
