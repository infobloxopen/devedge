# Feature Specification: client-go port-forward migration

**Feature Branch**: `009-clientgo-portforward`
**Created**: 2026-06-10
**Status**: Draft

## Context

Friction #5 from the 007 onboarding walk-through (recorded in
`specs/008-daemon-toolchain-install-doctor/spec.md`): Rancher Desktop's `kubectl` is a
`kuberlr` shim that downloads ~57 MB on first use; devedge's port-forward establishment
timeout (~20 s) kills the download. The 008 feature added an actionable error and a
`de doctor --toolchain` pre-warm path as a workaround but explicitly deferred the durable
fix (Constitution IV — Portable Core, Explicit Platform Adapters).

**The durable fix:** replace the `kubectl port-forward` subprocess in
`internal/cluster/portforward.go` with `k8s.io/client-go`'s native portforwarder. No
subprocess means no shim, no PATH dependency, no 20 s timeout racing a download, and no
fragile stdout parsing. The port-forward becomes a direct SPDY stream to the API server —
same protocol `kubectl` uses, but in-process.

**Scope is tight.** The public interface of `PortForward` (`.LocalPort`, `.Alive()`,
`.Stop()`) and the signature of `StartPortForward` stay unchanged. The single caller
(`internal/depruntime/realprov.go:69`) needs no modification. Only
`internal/cluster/portforward.go` changes materially; `go.mod` gains `k8s.io/client-go`
and its indirect deps.

## Clarifications

- **Target resolution**: the only callers pass `statefulset/<release>` (e.g.
  `statefulset/devedge-postgres`). StatefulSets name their pods `<release>-0` for the
  first (and only) replica in devedge's single-instance model. This feature resolves
  `statefulset/<name>` → pod `<name>-0`. Other target formats are not needed and not
  supported; passing an unsupported format returns a clear error.
- **Kubeconfig loading**: use `client-go`'s `clientcmd.NewDefaultClientConfigLoadingRules()`
  (respects `$KUBECONFIG`, falls back to `~/.kube/config`) with
  `ConfigOverrides.CurrentContext` set when a non-empty `kubeContext` is supplied.
- **Ephemeral port**: pass `"0:<remotePort>"` to the portforwarder; read the OS-assigned
  local port back via `PortForwarder.GetPorts()[0].Local`.
- **`client-go` version**: v0.32.x (aligns with k3d's bundled k3s; compatible with the
  cluster API versions exercised by the e2e tests).
- **`Alive()` semantics**: the existing implementation marks `done = true` when the
  `cmd.Wait()` goroutine returns. With client-go, mark `done = true` when the forwarding
  goroutine exits (portforwarder's `ForwardPorts` returns).
- **No other kubectl subprocess removal in scope.** `KubectlExec`, `kubectlApplyStdin`,
  `kubectlDelete` remain subprocess-based; migrating them is future work.

## User Scenarios & Testing

### User Story 1 — Port-forward works on a machine with no kubectl in PATH (P1) 🎯 MVP

A developer has Rancher Desktop installed but `kubectl` has never been used as root (shim
not pre-warmed). They run `de project up`. The dependency port-forward establishes via
client-go directly — no subprocess, no shim invocation, no timeout race.

**Acceptance Scenarios**:

1. **Given** no `kubectl` binary is present on `PATH`, **When** `StartPortForward` is
   called with a valid kubeContext and a reachable pod, **Then** the forward establishes
   and `PortForward.LocalPort` is set to an ephemeral port > 0.
2. **Given** a live port-forward, **When** `pf.Alive()` is called, **Then** it returns
   `true`.
3. **Given** a live port-forward, **When** `pf.Stop()` is called, **Then** `pf.Alive()`
   returns `false` within 1 s.

**Independent Test**: unit test with a mock SPDY dialer (no cluster required) verifying
establishment and `LocalPort` assignment.

---

### User Story 2 — Existing `de project up` e2e flow unchanged (P1)

A developer runs the full k3d-backed e2e suite. All tests that call `de project up`
(dependency_postgres, dependency_redis, migrations_local, migrations_seed,
scaffold_onboarding) pass without modification to test code or service configuration.

**Acceptance Scenarios**:

1. **Given** a live k3d cluster with `devedge-postgres` StatefulSet running, **When**
   `StartPortForward("devedge", "devedge-deps", "statefulset/devedge-postgres", 5432)`
   is called, **Then** `PortForward.LocalPort` is a reachable localhost port and a
   Postgres TCP connection succeeds on it.
2. **Given** a live k3d cluster with `devedge-redis` StatefulSet running, **When**
   `StartPortForward` is called for Redis (port 6379), **Then** a Redis PING succeeds on
   the returned `LocalPort`.

**Independent Test**: the existing k3d e2e tests (`make test-e2e`) green with no test code
changes.

---

### User Story 3 — Unsupported target format produces a clear error (P2)

A future caller passes a target in a format the resolver does not handle.

**Acceptance Scenarios**:

1. **Given** a target like `pod/mypod` or `deployment/foo`, **When** `StartPortForward`
   is called, **Then** it returns an error containing the unsupported target string and the
   word "unsupported".

**Independent Test**: unit test with a bad target; no network call required.

---

### Edge Cases

- What happens if the kubeContext does not exist in the kubeconfig? → `clientcmd` returns
  an error at config-load time; `StartPortForward` propagates it before dialling.
- What happens if the pod is not yet Running? → client-go's portforwarder returns an error
  (`error upgrading connection: ...`); `StartPortForward` propagates it with the target
  name for context.
- What happens if the API server is unreachable? → dial error surfaced immediately, no
  hanging goroutine.
- What happens if `Stop()` is called twice? → idempotent; second call is a no-op
  (context already cancelled).

## Requirements

### Functional Requirements

- **FR-001**: `StartPortForward` MUST NOT spawn a `kubectl` subprocess.
- **FR-002**: `StartPortForward` MUST load kubeconfig using
  `clientcmd.NewDefaultClientConfigLoadingRules()` (honoring `$KUBECONFIG`) and MUST use
  the supplied `kubeContext` when non-empty.
- **FR-003**: The local port MUST be OS-assigned (ephemeral) and MUST be returned in
  `PortForward.LocalPort` before `StartPortForward` returns.
- **FR-004**: Target format `statefulset/<name>` MUST resolve to pod `<name>-0`; any other
  format MUST return an error immediately (no network call attempted).
- **FR-005**: `PortForward.Alive()` MUST return `false` within 1 s of `Stop()` being
  called or of the underlying connection being lost.
- **FR-006**: `go.mod` MUST add `k8s.io/client-go@v0.32.x` (and required indirect deps);
  `go build ./...` and `go vet ./...` MUST pass.
- **FR-007**: The forwarding goroutine MUST be fully cleaned up (no goroutine leak) when
  the `PortForward` is stopped.

### Key Entities

- **`PortForward`**: unchanged public surface — `LocalPort int`, `Alive() bool`,
  `Stop()`. Internal state adds a `stopCh chan struct{}` replacing `context.CancelFunc`.
- **`StartPortForward`**: unchanged signature —
  `(kubeContext, namespace, target string, remotePort int) (*PortForward, error)`.

## Success Criteria

- **SC-001**: `de project up` completes successfully on a machine where `kubectl` is absent
  from PATH (verified by unit test with mock dialer).
- **SC-002**: All k3d e2e tests green (`make test-e2e`); no test file modified.
- **SC-003**: `go build ./...` and `go vet ./...` pass with the added `client-go` dep.
- **SC-004**: No goroutine leak — `Stop()` + a 100 ms sleep leaves no blocked portforward
  goroutine (verified by `goleak` or a goroutine-count check in the unit test).
- **SC-005**: `internal/cluster/portforward.go` no longer imports `os/exec`.
