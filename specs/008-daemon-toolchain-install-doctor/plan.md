# Implementation Plan: Daemon toolchain setup & doctor hardening

**Branch**: `008-daemon-toolchain-install-doctor` | **Date**: 2026-06-10 | **Spec**: `specs/008-daemon-toolchain-install-doctor/spec.md`

## Summary

Five targeted fixes + three test harnesses. Every change is confined to existing packages:
`internal/platform/` (install + doctor), `cmd/de/` (preflight ordering), `internal/daemon/`
(toolchain API), `internal/client/` (toolchain client method). No new packages. No daemon
protocol changes that break existing clients.

## Technical Context

**Language/Version**: Go (devedge module, no new external dependencies)
**Affected packages**:
- `internal/platform/darwin.go` — `DarwinAdapter.Install()`: extend plist with PATH/HOME/KUBECONFIG
- `internal/platform/doctor.go` — `RunDoctor()`: add daemon toolchain check
- `internal/daemon/api.go` — new `GET /v1/doctor/toolchain` handler
- `internal/client/` — new `GetToolchain()` method
- `cmd/de/main.go` / `cmd/de/dependencies.go` — move `requireDependencyTools()` before cluster ensure
**No new external dependencies.**
**Testing**: unit tests in `internal/platform/` (plist golden, doctor toolchain with httptest);
absolute-path invariant in `pkg/config/`; launchd-equivalence integration test via a subprocess
harness in `internal/platform/`.
**Platform scope**: Darwin adapter changes for plist; Linux/unsupported adapters unaffected.

## Constitution Check

- **I. Edge-First DX** — PASS: early preflight and `de doctor` toolchain check directly improve
  the developer's experience of `de project up`; errors are actionable before wasted cluster time.
- **II. Spec-Driven, Test-Driven** — PASS: tasks order test writing before implementation for
  every change; plist golden test and doctor test validate new behavior.
- **III. End-to-End Confidence** — PASS: the launchd-equivalence harness exercises the daemon
  binary under a sanitized env; no mocking of tool resolution. Existing 003–006 e2es are
  unaffected.
- **IV. Portable Core, Explicit Platform Adapters** — PASS: plist changes are isolated to
  `DarwinAdapter`; the toolchain endpoint is daemon-core (platform-agnostic); no platform
  assumptions leak into core reconciliation.
- **V. Safe Reconciliation & Observable Ops** — PASS: pre-mutation preflight and `de doctor`
  toolchain expose cluster-toolchain health before side effects; errors name the searched PATH.

No violations. No complexity escalation needed.

## Implementation Groups

### Group A — `de install` writes PATH/HOME/KUBECONFIG into the daemon plist

**File**: `internal/platform/darwin.go`

Extend `plistData` with `ToolPATH`, `Home`, `Kubeconfig` string fields. Update `plistTmpl` to
emit them as `EnvironmentVariables` entries alongside `DEVEDGE_HOME`. Add
`discoverToolEnv(home, kubeconfig string) (toolPath, warnings string)`:
- `exec.LookPath` for helm, kubectl, k3d, mkcert; collect unique parent dirs.
- Prepend the dirs to `/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin`.
- Return a space-separated warnings string for any tool not found (warn, don't fail).

`DarwinAdapter.Install()`:
1. Detect `home`: `os.UserHomeDir()` (the sudo-invoker's home via `SUDO_USER` if set).
2. Detect `kubeconfig`: `os.Getenv("KUBECONFIG")` → fall back to `home + "/.kube/config"`.
3. Call `discoverToolEnv(home, kubeconfig)`.
4. Print a warning per missing tool (non-blocking).
5. Populate the extended `plistData` and render.

### Group B — Early preflight: requireDependencyTools before cluster ensure

**Files**: `cmd/de/main.go`, `cmd/de/dependencies.go`

In `projectUpCmd`, the deps block currently calls `EnsureCluster` before
`provisionDependencies` (which calls `requireDependencyTools` inside). Move the check before
`EnsureCluster`:

```go
// Before EnsureCluster:
if err := requireDependencyTools(); err != nil {
    return err
}
ensurer := cluster.NewEnsurer(provider)
if err := ensurer.EnsureCluster(...); err != nil { ...}
```

Remove `requireDependencyTools()` from `provisionDependencies` (it becomes the caller's concern).

### Group C — Daemon exposes toolchain check endpoint

**Files**: `internal/daemon/api.go`, `internal/client/client.go`

Add to the daemon API:

```
GET /v1/doctor/toolchain
→ 200 {"tools": [{"name":"helm","found":true,"path":"/opt/homebrew/bin/helm"}, ...],
        "path_searched": "..."}
```

Handler runs `exec.LookPath` for helm, kubectl, k3d, mkcert; includes `os.Getenv("PATH")` in
the response as `path_searched`. Runs in the daemon's goroutine (uses daemon's real PATH/HOME).

Add `ToolchainResult` struct (in `daemon` package, exported so the client can decode it).
Add `GetToolchain() (*ToolchainResponse, error)` to `internal/client/Client`.

### Group D — `de doctor` reports daemon toolchain

**File**: `internal/platform/doctor.go`

Add `checkDaemonToolchain(daemonSocket string) []CheckResult`:
- Open the daemon socket (reuse the `daemon.DefaultSocketPath()` pattern from existing checks).
- If socket not connectable → return single result "daemon toolchain: skipped (daemon offline)".
- Otherwise call `GET /v1/doctor/toolchain` via `http.Client` over `unix://` transport.
- Decode response; return one `CheckResult` per tool: `Name: "daemon tool: helm"`, `Passed: found`,
  `Message: path or "not found in PATH=..."`.

Add the toolchain results to `RunDoctor()` after the existing checks.

### Group E — Tests

**E1 — Plist golden test** (`internal/platform/darwin_test.go`):
- Set `HOME=/tmp/testhome` and `KUBECONFIG=/tmp/testhome/.kube/config` env overrides.
- Stub a temp dir where the plist will be written (override `launchDaemonPath` for the test, or
  call the template render directly on a `plistData` struct).
- Assert plist XML contains `<key>PATH</key>`, `<key>HOME</key>`, `<key>KUBECONFIG</key>`.
- Assert PATH value includes dirs of any tools found in the test's PATH.
  *Note*: in CI, helm/kubectl may be absent — test must tolerate empty ToolPATH gracefully (falls
  back to the static default PATH, no panic).

**E2 — Doctor toolchain test** (`internal/platform/doctor_test.go`):
- Spin up an `httptest.Server` responding to `GET /v1/doctor/toolchain` with a canned JSON body
  (helm found, kubectl not found).
- Patch `doctorDNSAddr` pattern: add `doctorToolchainURL` var pointing to the test server.
- Call `checkDaemonToolchain` and assert one PASS result (helm) and one FAIL result (kubectl)
  with the path_searched in the message.

**E3 — Absolute-path invariant** (`pkg/config/service_test.go`):
- Confirm (already present from 577a649) that `Migrations(".")` returns an absolute path.
- Add: `Migrations("../relative")` also returns an absolute path.
- New: verify that `DependencyRequest.Migrations` and `.Seed` fields (constructed by
  `provisionDependencies`) are always absolute when fed relative inputs. Drive this through the
  struct-construction path in `cmd/de/dependencies.go` (unit test or inspection-level test).

**E4 — Launchd-equivalence harness** (`internal/platform/launchd_equiv_test.go` — `[C]`):
- Build devedged to a temp bin.
- Start it under `cmd.Env = []string{"PATH=/usr/bin:/bin", "DEVEDGE_HOME=<tmp>"}` (no HOME, no
  helm/kubectl/k3d in PATH).
- Hit the daemon's toolchain endpoint after it starts.
- Assert all three cluster tools are reported as "not found".
- Assert calling `PUT /v1/services/foo/dependencies` returns an actionable error (HTTP 4xx or
  5xx) that names the missing tools.
- No k3d cluster should have been created.
- Clean up daemon subprocess in `t.Cleanup`.

## File Map

| File | Change |
|------|--------|
| `internal/platform/darwin.go` | `plistData` + template + `discoverToolEnv` + Install patch |
| `internal/platform/darwin_test.go` | Plist golden test (E1) |
| `internal/platform/doctor.go` | `checkDaemonToolchain` + `RunDoctor` patch |
| `internal/platform/doctor_test.go` | Doctor toolchain test (E2) |
| `internal/platform/launchd_equiv_test.go` | Launchd-equivalence test (E4) |
| `internal/daemon/api.go` | ToolchainResult + handler + route registration |
| `internal/client/client.go` | `GetToolchain()` |
| `cmd/de/main.go` | Early preflight before EnsureCluster |
| `cmd/de/dependencies.go` | Remove `requireDependencyTools()` (caller's job now) |
| `pkg/config/service_test.go` | Absolute-path invariant additions (E3) |

## Ordering

Tasks must write/update tests before implementation (`[T]` tag). The natural order:
1. E1/E3 tests (pure unit, no daemon) — run immediately for red.
2. Group A (Install) — green E1.
3. Group B (preflight) — trivial move, unit-testable.
4. Group C (daemon endpoint) — E2 test first, then handler.
5. Group D (doctor) — depends on C.
6. E4 (launchd-equivalence) — needs daemon binary + Groups A/C done.
