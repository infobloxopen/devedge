# Tasks: Daemon toolchain setup & doctor hardening

**Feature**: `008-daemon-toolchain-install-doctor`
**Spec**: `spec.md` | **Plan**: `plan.md`

## Task List

### Group E1/E3 — Tests first (no daemon; run for red before implementation)

- [ ] T001 [T] [S] AC-007 Write **plist golden test** in `internal/platform/darwin_test.go`.
  Use `plistData` directly (or a `renderPlist(data)` helper extracted from Install); set
  HOME/KUBECONFIG from the test; assert the rendered XML contains keys PATH, HOME, KUBECONFIG.
  Must tolerate absent tools (empty ToolPATH). **Red before Group A.**

- [ ] T002 [T] [S] AC-009 **Absolute-path invariant tests** in `pkg/config/service_test.go`.
  Verify `Migrations(".")` and `Migrations("../relative")` both return absolute paths (extend
  the 577a649 tests if they don't already cover the `".."` case). Also confirm
  `Migrations("/abs/path")` is unchanged. **Red (or already-green for ".") before Group A.**

### Group A — `de install` writes PATH/HOME/KUBECONFIG

- [ ] T003 [S] AC-001 AC-002 AC-003 Extend `plistData` struct and `plistTmpl` in
  `internal/platform/darwin.go`: add `ToolPATH`, `Home`, `Kubeconfig` fields; update the
  template's `EnvironmentVariables` dict to emit PATH/HOME/KUBECONFIG entries.

- [ ] T004 [S] AC-001 Add `discoverToolEnv(home, kubeconfig string) (toolPath, warnings string)`
  in `internal/platform/darwin.go`. LookPath for helm, kubectl, k3d, mkcert; collect unique
  parent dirs; prepend to static fallback PATH `/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin`;
  build warnings string for missing tools.

- [ ] T005 [S] AC-001 AC-002 AC-003 Update `DarwinAdapter.Install()` to detect HOME (via
  `SUDO_USER`→`/Users/<user>` or `os.UserHomeDir()`), KUBECONFIG (env or `home+"/.kube/config"`),
  call `discoverToolEnv`, print warnings, and pass into the extended `plistData`. **Green T001.**

### Group B — Early preflight

- [ ] T006 [S] AC-004 In `cmd/de/main.go` (`projectUpCmd`): add `requireDependencyTools()` call
  immediately before `ensurer.EnsureCluster(...)` inside the `if dd ... && len(deps) > 0` block.
  In `cmd/de/dependencies.go`: remove the `requireDependencyTools()` call from
  `provisionDependencies`. This changes call order; no new tests needed beyond the existing
  preflight test exercising the path.

### Group C — Daemon toolchain endpoint

- [ ] T007 [T] [S] AC-005 AC-006 Write **doctor toolchain test** in
  `internal/platform/doctor_test.go`. Spin up an `httptest.Server` serving `GET
  /v1/doctor/toolchain` returning a canned JSON with helm found + kubectl not found. Add a
  `doctorToolchainBaseURL` test-override var. Call `checkDaemonToolchain` against it; assert
  two CheckResult entries (PASS helm, FAIL kubectl with "PATH=" in message). Also assert
  "skipped" result when the test server is not reachable. **Red before T008/T009.**

- [ ] T008 [S] AC-005 AC-006 Add to `internal/daemon/api.go`:
  - `ToolInfo` struct: `Name string`, `Found bool`, `Path string` (abs path or "").
  - `ToolchainResponse` struct: `Tools []ToolInfo`, `PathSearched string`.
  - `toolchainHandler`: runs `exec.LookPath` for helm, kubectl, k3d, mkcert; populates response
    with `os.Getenv("PATH")` as PathSearched; returns 200 JSON.
  - Register `GET /v1/doctor/toolchain` in `NewAPI`.

- [ ] T009 [S] AC-005 Add `GetToolchain() (*ToolchainResponse, error)` to
  `internal/client/client.go`. Issues `GET /v1/doctor/toolchain` on the Unix socket transport
  (reuse existing `doGet` / HTTP-over-Unix pattern in the client). Returns `nil, err` when
  daemon is not reachable.

### Group D — Doctor reports daemon toolchain

- [ ] T010 [S] AC-005 Add `checkDaemonToolchain(socketPath string) []CheckResult` in
  `internal/platform/doctor.go`. If socket not connectable → single skipped result. Otherwise
  call the daemon `GET /v1/doctor/toolchain` via the Unix-domain HTTP client (construct a
  `http.Client` with DialContext over `net.Dial("unix", socketPath)`, `GET
  http://daemon/v1/doctor/toolchain`). Decode `ToolchainResponse`; return one result per tool.
  Depends on T008 (daemon exports types).

- [ ] T011 [S] AC-005 Add `checkDaemonToolchain` call to `RunDoctor()` in
  `internal/platform/doctor.go`, appended after existing checks. Pass
  `daemon.DefaultSocketPath()`. **Green T007.**

### Group E4 — Launchd-equivalence harness

- [ ] T012 [C] AC-006 AC-008 Write **launchd-equivalence integration test** in
  `internal/platform/launchd_equiv_test.go` (build tag: `//go:build integration`).
  - Build `devedged` to a temp dir via `go build ./cmd/devedged/ -o <tmp>/devedged`.
  - Start it with `cmd.Env = []string{"PATH=/usr/bin:/bin", "DEVEDGE_HOME=<tmp>"}`.
  - Poll `GET /v1/doctor/toolchain` until daemon is up (≤ 5 s).
  - Assert helm/kubectl/k3d all `Found: false` and `PathSearched` equals `/usr/bin:/bin`.
  - Issue `PUT /v1/services/test/dependencies` (minimal body declaring one Postgres dep).
  - Assert response is 4xx or the JSON error body mentions the missing tools or missing kubeconfig
    (no cluster creation attempted — k3d cluster list remains empty or is never called).
  - `t.Cleanup`: kill daemon subprocess.
  Note: this test is skipped unless `TEST_INTEGRATION=1` is set (guarded by build tag + env
  check), consistent with the 003–006 e2e harness pattern.

### Commit + QA

- [ ] T013 [S] Commit spec/plan/tasks and implementation; run `make build && make lint` clean.

- [ ] T014 [S] Run full test suite: `make test` (unit + integration). Confirm all new and
  existing tests pass. Run `./bin/de doctor` against a live daemon to smoke-check the toolchain
  output. Confirm early-preflight fires before cluster time: attempt `de project up` with
  kubectl removed from PATH; verify error fires immediately with no cluster created.

## Dependencies

- T003 → T001 (test first)
- T004 → T003
- T005 → T004 → T001 (green)
- T008 → T007 (test first)
- T009 → T008
- T010 → T008 T009
- T011 → T010 → T007 (green)
- T012 → T005 T008 T011 (needs all fixes live in the binary)
- T013 → all implementation tasks
- T014 → T013

## Complexity Summary

| Tag | Count | Notes |
|-----|-------|-------|
| [S] | 12 | Mechanical; dispatch to Sonnet |
| [C] | 1 | T012 (subprocess harness, tricky lifecycle) — keep on Opus |
| [T] | 3 | T001, T002, T007 — test-first; mark [T] to run before their group |
