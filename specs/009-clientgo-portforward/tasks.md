# Tasks: client-go port-forward migration

**Branch**: `009-clientgo-portforward`
**Spec**: `specs/009-clientgo-portforward/spec.md`
**Plan**: `specs/009-clientgo-portforward/plan.md`

## Phase 1: Dependency

- [X] T001 [S] Add `k8s.io/client-go` to go.mod: run `go get k8s.io/client-go@latest` then
  `go mod tidy` from the repo root; verify `go build ./...` still passes.

---

## Phase 2: Tests (write before implementation — must be red first)

- [X] T002 [S] Write target-parser unit tests in `internal/cluster/portforward_test.go`:
  - `"statefulset/devedge-postgres"` → pod `"devedge-postgres-0"`, no error
  - `"statefulset/devedge-redis-myslug"` → pod `"devedge-redis-myslug-0"`, no error
  - `"pod/foo"` → error containing "unsupported"
  - `"deployment/bar"` → error containing "unsupported"
  - empty string → error
  (Call an exported or package-internal `parsePortForwardTarget(target) (podName, error)` —
  define the function signature in the test file first, before it exists, so the build is red.)

- [X] T003 [S] Write `PortForward` lifecycle unit tests in `internal/cluster/portforward_test.go`:
  - `Stop()` is idempotent (call twice, no panic)
  - `Alive()` returns `false` after `Stop()` + 100 ms
  These tests operate only on the `PortForward` struct (no network), so they can be written
  by constructing the struct directly and calling `markDone()` / `Stop()`.

---

## Phase 3: Implementation

- [X] T004 [C] Rewrite `internal/cluster/portforward.go`:
  1. Remove `bufio`, `bytes`, `os/exec`, `regexp`, `strconv` imports.
  2. Add imports: `k8s.io/client-go/tools/clientcmd`, `k8s.io/client-go/tools/portforward`,
     `k8s.io/client-go/transport/spdy`.
  3. Implement `parsePortForwardTarget(target string) (podName string, err error)`:
     accept only `"statefulset/<name>"` → return `"<name>-0"`; all other formats return error.
  4. Update `PortForward` struct: replace `cancel context.CancelFunc` with
     `stopCh chan struct{}` + `stopOnce sync.Once`.
  5. Update `Stop()` to `pf.stopOnce.Do(func() { close(pf.stopCh) })`.
  6. Implement `StartPortForward`:
     - Call `parsePortForwardTarget`; error out immediately on bad target.
     - Load REST config via `clientcmd.NewDefaultClientConfigLoadingRules()` +
       `ConfigOverrides{CurrentContext: kubeContext}` (skip override when `kubeContext == ""`).
     - Build SPDY dialer against the pod's portforward subresource URL:
       `<host>/api/v1/namespaces/<namespace>/pods/<pod>/portforward`.
     - Create `portforward.New(dialer, []string{fmt.Sprintf("0:%d", remotePort)}, stopCh, readyCh, ...)`
     - Launch `pf.ForwardPorts()` in a goroutine; call `markDone()` when it returns.
     - Wait on `readyCh` with 30 s timeout; on success read `GetPorts()[0].Local`.
     - On timeout or error: close `stopCh` and return an error naming the target.
  Confirm: `internal/cluster/portforward.go` no longer imports `os/exec` (SC-005).

---

## Phase 4: Verify

- [X] T005 [S] Run `go vet ./...` and `go build ./...` from the repo root; confirm clean (SC-003).
  Fix any vet warnings introduced by the new dep before proceeding.

- [X] T006 [S] Run unit tests: `go test ./internal/cluster/...` — T002/T003 tests must be green.

- [X] T007 [S] Run the full k3d e2e suite: `make test-e2e` (or equivalent). All tests that
  exercise `de project up` (dependency_postgres, dependency_redis, migrations_local,
  migrations_seed, scaffold_onboarding) must pass with no test file changes (SC-002).

---

## Phase 5: Commit

- [X] T008 [S] Commit spec + plan + tasks + implementation in a single feature commit.
  Message: `009: replace kubectl port-forward subprocess with client-go native portforwarder`.

---

## Dependencies & Execution Order

- T001 → T002, T003 (tests reference the package; dep must be resolvable)
- T002, T003 (red) → T004 (make green)
- T004 → T005 → T006 → T007 → T008

## Complexity Tags

| Task | Tag | Reason |
|------|-----|--------|
| T001 | [S] | Mechanical: `go get` + `go mod tidy` |
| T002 | [S] | Pure unit test, no network, parser logic is trivial |
| T003 | [S] | Struct-level test, no network |
| T004 | [C] | New API (SPDY/client-go), goroutine lifecycle, error contracts |
| T005 | [S] | Mechanical: run commands, read output |
| T006 | [S] | Mechanical: run tests, check pass/fail |
| T007 | [S] | Mechanical: run e2e suite, no code change required |
| T008 | [S] | Mechanical: git commit |
