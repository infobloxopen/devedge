---
description: "Task list for feature 001-fix-dns-udp-bind"
---

# Tasks: DNS Resolution Through macOS Per-Domain Resolver Framework

**Input**: Design documents from `/specs/001-fix-dns-udp-bind/`
**Prerequisites**: plan.md ✓, spec.md ✓, research.md ✓, data-model.md ✓,
contracts/ ✓, quickstart.md ✓

**Tests**: Test tasks are included because the project constitution (Principle II)
mandates test-first development for all material changes and requires that
implementation tasks have corresponding test tasks. Each test task lists the
file it lives in; each implementation task references the contract/data-model
section it satisfies.

**Organization**: Tasks are grouped by user story. Foundational types are
shared across all stories and live in Phase 2 so each story phase can be
implemented and validated independently.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: Which user story this task belongs to (US1, US2, US3)
- All paths are repo-relative

## Path Conventions

Single-project Go layout. `internal/` for packages, `cmd/` for binaries,
`pkg/` for shared types, `test/` for integration and e2e suites. Test files
live next to source files within `internal/` packages; cross-package tests
live in `test/integration/` and `test/e2e/`.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Bring in the new external dependency required by every later phase.

- [X] T001 Add `github.com/miekg/dns` dependency: edit `go.mod` to add the import, run `go mod tidy`, and commit the resulting `go.mod` and `go.sum` changes. Verify with `go build ./...` (no functional code yet, just dep wiring).

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Establish the platform-agnostic core types (`ConfiguredSuffix`,
`AuthoritativeSet`) and the platform adapter scaffold (`SuffixSource`
interface, `darwinSuffixSource`, `noopSuffixSource`) that every user story
relies on.

**⚠️ CRITICAL**: No user-story work can begin until this phase is complete —
the DNS handler (US1), doctor probe (US2), and wildcard behavior (US3) all
consume these types.

- [X] T002 [P] Write unit tests for `ConfiguredSuffix` validation and `AuthoritativeSet.Replace`/`Match`/`Snapshot` in `internal/dnsserver/suffixes_test.go`. Cover: name canonicalization (lowercase, trailing-dot strip), reject invalid DNS names, longest-suffix matching when two configured suffixes nest, trailing-dot query names, case-insensitive match, concurrent reads during `Replace`, atomic visibility (no partial state observed).
- [X] T003 [P] Write unit tests for `SuffixSource` polling helpers and the test-only `staticSuffixSource` in `internal/dnsserver/source_test.go`. Cover: `staticSuffixSource.List` returns defensive copy; mutating the returned slice does not mutate source; `Set` updates list visible to next `List` call.
- [X] T004 [P] Write unit tests for `darwinSuffixSource` in `internal/dnsserver/source_darwin_test.go` with build tag `//go:build darwin`. Cover: lists regular files in a tempdir; skips hidden files (`.DS_Store`), non-regular entries, and files whose names are not valid DNS labels; returns `([], nil)` when the directory does not exist; returns the error when the directory exists but is unreadable.
- [X] T005 [P] Implement `ConfiguredSuffix` type and `AuthoritativeSet` in `internal/dnsserver/suffixes.go` per `data-model.md` §AuthoritativeSet. Use `sync.RWMutex` over `map[string]struct{}`. `Match` MUST do longest-suffix matching. `Replace` MUST return `(added, removed []ConfiguredSuffix)` for the polling loop to log. All exported names are case-canonicalized on entry.
- [X] T006 Implement `SuffixSource` interface and `staticSuffixSource` in `internal/dnsserver/source.go` per `contracts/suffix-source.md`. Define the interface (`List(ctx) ([]ConfiguredSuffix, error)`, `Name() string`), the `staticSuffixSource` test helper with thread-safe `Set([]string)`, and constants for the polling interval (5 s) and per-call timeout (2 s). Depends on T005 (uses `ConfiguredSuffix`).
- [X] T007 [P] Implement `darwinSuffixSource` in `internal/dnsserver/source_darwin.go` with build tag `//go:build darwin`. Reads `/etc/resolver/`, returns one `ConfiguredSuffix` per qualifying file. Honors all skip rules from `contracts/suffix-source.md` §darwinSuffixSource. Depends on T005 and T006.
- [X] T008 [P] Implement `noopSuffixSource` in `internal/dnsserver/source_other.go` with build tag `//go:build !darwin`. Returns `([]ConfiguredSuffix{}, nil)` always; `Name()` returns `"noop"`. Depends on T006.

**Checkpoint**: After this phase, `go test ./internal/dnsserver/...` (excluding
server/handler tests added later) passes. The DNS server itself does not yet
exist; only the suffix-set data structures and the platform suffix discovery
do.

---

## Phase 3: User Story 1 — Project hostnames resolve immediately after `de install` (Priority: P1) 🎯 MVP

**Goal**: Restore the broken macOS resolver framework path. After `de install`,
any hostname inside the configured suffix resolves to `pkg/types.EdgeIP`
(`127.0.0.2`) via the system resolver, over both UDP and TCP, on the first
attempt with no manual intervention (no cache flush, no daemon restart).

**Independent Test**: From a fresh macOS shell after `sudo de install` and
`sudo de start`: `host any.dev.test` returns `127.0.0.2`; `dig +short
@127.0.0.1 -p 15354 any.dev.test` returns `127.0.0.2`; `dig +tcp +short
@127.0.0.1 -p 15354 any.dev.test` returns `127.0.0.2`. All three succeed
without editing `/etc/hosts` and without restarting `mDNSResponder`.

### Tests for User Story 1 ⚠️ (write first, watch them fail)

- [X] T009 [P] [US1] Write DNS handler unit tests in `internal/dnsserver/handler_test.go` using `miekg/dns`'s in-process testing primitives. Cover the rows of `contracts/dns-protocol.md` §"Query handling rules": `TestHandler_AInSuffix_ReturnsEdgeIP` (A → 127.0.0.2 TTL 60), `TestHandler_AAAAInSuffix_EmptyAnswer` (NOERROR + zero answers), `TestHandler_OtherTypeInSuffix_EmptyAnswer` (MX/TXT/SRV all NOERROR + empty), `TestHandler_OutOfSuffix_Refused`, `TestHandler_EmptyAuthoritativeSet_AllRefused`, `TestHandler_TrailingDotAndCase` (case-insensitive, trailing-dot normalization), `TestHandler_MalformedQuery_FormErr`.
- [X] T010 [P] [US1] Write DNS server lifecycle tests in `internal/dnsserver/server_test.go`. Cover: `TestServer_Run_BindsBothTransports` (after `Run` starts, UDP and TCP listeners are both bound on the configured addr); `TestServer_Run_ShutsDownOnContextCancel` (both servers stop within 2 s of `ctx.Done()`); `TestServer_Run_RejectsNonLoopbackAddr` (binding to `0.0.0.0:…` returns error); `TestServer_BindFailure_ReturnsError` (port-busy scenario); `TestPollLoop_AppliesDiffs` (mutating a `staticSuffixSource` propagates to `AuthoritativeSet` within one tick); `TestPollLoop_RetainsPriorSetOnSourceError` (an erroring source does not clear known suffixes); `TestPollLoop_CancellationStopsLoop`.
- [X] T011 [P] [US1] Write integration test in `test/integration/dnsserver_test.go`. Boot a real `internal/daemon.Server` with `DEVEDGE_HOME` pointing at a tempdir, `WithDNSAddr` set to an ephemeral port via `127.0.0.1:0`, and an injected `staticSuffixSource` seeded with `["dev.test"]`. Then use a `miekg/dns` client to send queries: `TestDNSServer_UDP_RoundTrip`, `TestDNSServer_TCP_RoundTrip`, `TestDNSServer_UDP_TCP_Concurrent` (parallel queries on both transports return valid responses).
- [X] T012 [P] [US1] Write end-to-end test in `test/e2e/resolver_macos_test.go` with build tag `//go:build darwin && e2e`. Gate execution behind `DEVEDGE_E2E_MACOS=1` and `os.Geteuid() == 0`; skip otherwise. The test writes a `/etc/resolver/<test-suffix>` file pointing at a daemon-bound DNS endpoint, starts the daemon in-process, calls `net.LookupHost("devedge-healthcheck.<test-suffix>")`, and asserts the result contains `pkg/types.EdgeIP`. Cleans up `/etc/resolver/<test-suffix>` on exit. This satisfies FR-012.

### Implementation for User Story 1

- [X] T013 [P] [US1] Implement the DNS handler in `internal/dnsserver/handler.go`. Signature: `func NewHandler(set *AuthoritativeSet, edgeIP net.IP, logger *slog.Logger) dns.Handler`. Implement the answer rules from `contracts/dns-protocol.md` §"Query handling rules" exactly: A → single RR with `edgeIP` and TTL 60; AAAA / other → NOERROR + empty answer; out-of-suffix → REFUSED. Log out-of-suffix queries at debug. T013 makes T009 pass.
- [X] T014 [US1] Implement `Server` in `internal/dnsserver/server.go`. Fields: `addr string`, `udpServer *dns.Server`, `tcpServer *dns.Server`, `set *AuthoritativeSet`, `source SuffixSource`, `logger *slog.Logger`. Methods: `New(opts ...Option) *Server`, `Run(ctx context.Context) error`. `Run` MUST: (1) validate `addr` resolves to loopback (reject otherwise), (2) call `source.List` synchronously with a 2 s timeout for initial population, (3) start UDP and TCP `miekg/dns` `*dns.Server` goroutines on the same addr, (4) start the polling goroutine at 5 s ticks, (5) on `ctx.Done()` call `Shutdown` on both servers with a 2 s budget. Emit structured `slog` events per research R7. Depends on T013 (handler) and Phase 2 (set + source).
- [X] T015 [P] [US1] Update `internal/dns/resolver_darwin.go` so the drop-in content writes `port 15354` instead of `port 15353`. Update the inline comment block. Update any test in the package that asserts on the literal `15353` (search for it) to expect `15354`. Independent of T013/T014 because it only touches the file-writing helper.
- [X] T016 [US1] Wire the DNS server into `internal/daemon/server.go`. Add `dnsAddr string` (default `127.0.0.1:15354`) and `dnsSource dnsserver.SuffixSource` fields to `Server`. Add `WithDNSAddr(addr string) ServerOption` and `WithDNSSuffixSource(s dnsserver.SuffixSource) ServerOption`. Default `dnsSource` to `dnsserver.NewPlatformSuffixSource()` (a helper exported from Phase 2 that returns `darwinSuffixSource` on macOS, `noopSuffixSource` elsewhere). In `Run()`, after the proxy goroutine is spawned, construct `dnsserver.New(...)` and launch its `Run(ctx)` in a goroutine alongside the others. On DNS bind failure, log the error structurally but do NOT abort the rest of `Run` (failing-open per research R7). Depends on T014.

**Checkpoint**: After this phase, run `go test ./...` and the quickstart §§1–4
manually on macOS. The DNS endpoint is reachable, the system resolver
returns `127.0.0.2` for in-suffix names, and US1 is independently
demonstrable.

---

## Phase 4: User Story 2 — `de doctor` accurately reports DNS resolution health (Priority: P2)

**Goal**: Replace the current single, ambiguous DNS check with two probes
that together distinguish "DNS endpoint not responding" from
"`/etc/resolver/` drop-in misconfigured" from "endpoint healthy and
resolution working."

**Independent Test**: With the daemon stopped (so the DNS endpoint is down),
`de doctor` reports a DNS failure whose message names the unresponsive
endpoint (matching `FR-007`'s "DNS endpoint not responding" wording, not
"expected if hosts not configured yet"). With the daemon running and
`/etc/resolver/dev.test` present, `de doctor` reports DNS success and the
success line indicates that a real resolution probe succeeded — not just
that a file exists.

### Tests for User Story 2 ⚠️

- [X] T017 [P] [US2] Write tests for the endpoint-liveness probe in `internal/platform/doctor_test.go` (create file). Cover: `TestCheckDNSEndpoint_NoListener_ReportsFailureWithAddr` (when nothing is bound, the failure message contains the address `127.0.0.1:15354/udp`); `TestCheckDNSEndpoint_HealthyListener_ReportsSuccess` (using an in-process `miekg/dns` test server bound to an ephemeral port). The test wires the probe with an addr-override hook so it can target the ephemeral port instead of the production one.
- [X] T018 [P] [US2] Write tests for the system-resolver round-trip probe in `internal/platform/doctor_test.go`. Cover: `TestCheckDNSSystemResolver_ResolvesToEdgeIP_Passes` (probe-with-stubbed-resolver returns `127.0.0.2`); `TestCheckDNSSystemResolver_NoAddrs_Fails` (resolution fails entirely); `TestCheckDNSSystemResolver_NonLoopbackAddr_Fails` (resolves but to a non-loopback address). Use a `*net.Resolver` with a custom `Dial` to inject controlled responses without touching the host's `/etc/resolver/`.

### Implementation for User Story 2

- [X] T019 [US2] Implement `checkDNSEndpoint` in `internal/platform/doctor.go`. Opens a UDP connection to the configured DNS endpoint, sends a synthetic `A?` query for `devedge-healthcheck.dev.test`, expects a response within 250 ms. On failure, returns `CheckResult{Name: "DNS endpoint", Passed: false, Message: "not responding on <addr>/udp (devedged not running, port in use, or DNS server not started)"}`. On success, returns `Passed: true` with a `responsive` message. Make the target addr configurable via a package-level variable (testing seam) defaulting to `dnsserver.DefaultAddr`.
- [X] T020 [US2] Rewrite `checkDNS` in `internal/platform/doctor.go` to perform the system-resolver round-trip with explicit semantics per `research.md` R5. Replace the existing single `net.LookupHost` call. On failure, return a message that identifies whether the system resolver returned an error vs. resolved to a non-loopback address. On success, return `Passed: true` with `"resolves to <addr> via system resolver"`. Iterate over the configured suffixes if available (read from `dnsserver.NewPlatformSuffixSource()`), falling back to `dev.test` if the suffix list is empty.
- [X] T021 [US2] Update `RunDoctor` in `internal/platform/doctor.go` to call both new check functions in order: `checkDNSEndpoint`, then `checkDNS`, then the existing `checkResolverConfig`. Make sure the user sees three distinct DNS-related lines in `de doctor` output so the failure mode is obvious from a glance.

**Checkpoint**: Quickstart §6 (negative test: stop daemon, observe doctor
reports DNS endpoint as down with actionable message) passes. US2 is
independently demonstrable.

---

## Phase 5: User Story 3 — Wildcard hostnames within a configured suffix resolve (Priority: P3)

**Goal**: Lock in the property that any hostname inside a configured suffix —
including names never registered as routes — resolves to the local edge.
The implementation is already done in US1's handler (wildcard is the
semantic, not an exact-match table). This phase adds explicit test
coverage so a future change cannot silently regress to exact-match
behavior.

**Independent Test**: `host whatever-new.dev.test` returns `127.0.0.2`
without `de register whatever-new.dev.test ...` ever having been run.
`host devedge-totally-unregistered.dev.test` likewise. `host example.com`
does NOT return `127.0.0.2` (off-suffix → REFUSED).

### Tests for User Story 3 ⚠️

- [X] T022 [P] [US3] Add wildcard-specific cases to `internal/dnsserver/handler_test.go`: `TestHandler_NeverRegisteredName_ResolvesToEdgeIP` (a synthetic name nowhere in the registry but inside the configured suffix returns EdgeIP); `TestHandler_DeepSubdomain_Resolves` (a name like `a.b.c.d.e.dev.test` still returns EdgeIP); `TestHandler_HandlerDoesNotConsultRegistry` (construct a handler with an empty registry and a configured suffix; in-suffix queries still resolve, proving the handler is wildcard-by-design).
- [X] T023 [P] [US3] Add wildcard integration case `TestDNSServer_WildcardForUnregisteredName` to `test/integration/dnsserver_test.go`. Same harness as T011; query a synthetic never-registered name; assert wildcard answer.

**Checkpoint**: US3 tests pass. The handler's wildcard semantic is locked
in by tests so a future change to "only resolve registered names" would
fail CI.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Final consistency, full-suite regression check, and human
validation against the quickstart.

- [X] T024 [P] Run `go mod tidy` and verify `go.sum` is minimal — no unintended transitive deps beyond what `miekg/dns` requires (`golang.org/x/net`, `golang.org/x/sys`).
- [X] T025 Run `go vet ./...` and `go test ./...` (excluding the e2e build tag) and confirm zero failures and zero new warnings.
- [X] T026 [P] On a macOS workstation, execute every step of `specs/001-fix-dns-udp-bind/quickstart.md` end-to-end and check off each expected outcome. Record any drift between the doc and observed behavior; if any, file a follow-up. This is the human-eyes regression guard for the macOS resolver framework path.
- [X] T027 [P] Run the gated e2e test once with `DEVEDGE_E2E_MACOS=1 sudo go test -tags=e2e ./test/e2e/...` and confirm it passes. This satisfies FR-012's "automated end-to-end test" requirement under realistic conditions.
- [X] T028 Cross-reference the contract `contracts/dns-protocol.md` §"Test obligations" and `contracts/suffix-source.md` §"Test obligations" tables against the actual test names in the implementation. Add a one-line table at the top of each contract pointing to the file:line of each test, so a future reader can verify coverage by greppable identifier.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)** — no dependencies; start immediately. T001 must complete before any test code (which imports `miekg/dns`) compiles.
- **Foundational (Phase 2)** — depends on T001. BLOCKS all user-story phases.
- **User Story 1 (Phase 3, P1)** — depends on Phase 2.
- **User Story 2 (Phase 4, P2)** — depends on Phase 3 (the doctor probe needs the DNS server to probe; without T014 the endpoint-liveness probe has nothing to talk to in tests). Can interleave with US3 if staffed.
- **User Story 3 (Phase 5, P3)** — depends on Phase 3 (US3's implementation is US1's handler; this phase is test-only). Can interleave with US2.
- **Polish (Phase 6)** — depends on US1, US2, US3 being complete.

### User Story Dependencies

- **US1**: Foundational only.
- **US2**: US1 (the doctor probe targets the DNS server US1 implements).
- **US3**: US1 (US3's handler behavior is delivered by US1's handler; US3 is the test-only differentiation).

US2 and US3 are independent of each other and can run in parallel after US1
is complete.

### Within Each User Story

- Tests are written before implementation (constitution Principle II).
- Within a story, multiple test files are written in parallel (`[P]`).
- Within a story, implementation tasks that touch different files are
  parallel; tasks that touch the same file (e.g. T019/T020/T021 all in
  `doctor.go`) are sequential.

### Parallel Opportunities

- T002/T003/T004 (Phase 2 test files) are all `[P]` — different files.
- T005/T006 (Phase 2 impls) are mostly parallel; T007 and T008 both need T006.
- T009/T010/T011/T012 (Phase 3 test files) are all `[P]`.
- T013/T015 are `[P]` (different files); T014 depends on T013; T016 depends on T014.
- T017/T018 (Phase 4 tests) are `[P]`; T019/T020/T021 are sequential (same file).
- T022/T023 are `[P]` (different files).
- T024/T026/T027 (Phase 6) are `[P]` (independent activities); T025 must run after T024 (tidy can change build).

---

## Parallel Example: User Story 1 (MVP)

```bash
# After Phase 2 is complete, launch all US1 test files in parallel:
Task: "Write DNS handler unit tests in internal/dnsserver/handler_test.go"
Task: "Write DNS server lifecycle tests in internal/dnsserver/server_test.go"
Task: "Write integration test in test/integration/dnsserver_test.go"
Task: "Write end-to-end test in test/e2e/resolver_macos_test.go (build-tagged)"

# Then implementation, with the parallelism the dependency graph allows:
Task: "Implement DNS handler in internal/dnsserver/handler.go"     # T013 [P]
Task: "Update resolver_darwin.go port to 15354"                    # T015 [P]
# Sequentially after T013:
Task: "Implement Server.Run in internal/dnsserver/server.go"       # T014
# Sequentially after T014:
Task: "Wire DNS server into internal/daemon/server.go"             # T016
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: T001 (add `miekg/dns`).
2. Complete Phase 2: T002–T008 (foundational types and platform adapters). Run `go test ./internal/dnsserver/...` and confirm passing.
3. Complete Phase 3: T009–T016 (US1 — DNS resolution works).
4. **STOP and VALIDATE**: Run the quickstart's §§1–4. The browser-level resolution path should work end-to-end. This is the MVP — the actual bug fix.
5. Optionally ship at this point if US2/US3 hardening can come later.

### Incremental Delivery

1. **Phase 1 + 2** → foundation in place. Nothing user-visible yet.
2. **Phase 3 (US1)** → MVP. Bug closed. Ship.
3. **Phase 4 (US2)** → doctor is honest about reality. Ship.
4. **Phase 5 (US3)** → wildcard regression guard. Ship.
5. **Phase 6** → polish. Run quickstart end-to-end on a clean macOS box.

### Parallel Team Strategy

For a small team:

1. One developer does Phase 1 + Phase 2 together (small, sequential file
   dependencies make parallelism low-value here).
2. Once Phase 2 is done, one developer takes US1 through to MVP while a
   second developer drafts the US2 doctor probe (waiting on US1's
   `dnsserver.DefaultAddr` and `Server` to land before tests pass).
3. US3 is the smallest phase — a single developer adds the wildcard tests
   after US1 lands.

---

## Notes

- `[P]` marks tasks in different files with no incomplete-task dependencies.
- All test tasks list the exact file path; all implementation tasks list
  the exact file path and reference the contract or data-model section
  they implement.
- Constitution Principle II: every implementation task in this list has a
  preceding test task that writes the test first.
- Constitution Principle III: T012 and T027 deliver the end-to-end coverage
  through the macOS resolver framework, which is the boundary this feature
  exists to fix. No k3d-based e2e is needed for this feature because the
  resolver framework — not the cluster path — is the failing boundary.
- The `de install` command's hardcoded `dns.InstallResolverConfig("dev.test")`
  call site is **not** changed in this feature. Lifting that to a
  per-project / configurable value is a separate feature.
- No `cmd/<binary>/` is added. The DNS server runs as goroutines inside the
  existing `cmd/devedged` process; from `lsof`'s perspective there is still
  one `devedged` PID, now with additional UDP + TCP listeners on `:15354`.

## Total task count

- Setup: 1
- Foundational: 7 (3 tests + 4 impls)
- US1: 8 (4 tests + 4 impls)
- US2: 5 (2 tests + 3 impls)
- US3: 2 (2 tests, no new impl)
- Polish: 5
- **Total: 28 tasks**

| Phase | Test tasks | Impl tasks | Total |
|-------|-----------|------------|-------|
| Setup | 0 | 1 | 1 |
| Foundational | 3 | 4 | 7 |
| US1 (P1, MVP) | 4 | 4 | 8 |
| US2 (P2) | 2 | 3 | 5 |
| US3 (P3) | 2 | 0 | 2 |
| Polish | — | — | 5 |
