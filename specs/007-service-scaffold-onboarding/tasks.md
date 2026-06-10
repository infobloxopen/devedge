# Tasks: Service scaffold for the onboarding walk-through

**Input**: spec.md + plan.md (this directory)
**Prerequisites**: devedge main @ `aad809e` (002–006 merged); devedge-sdk public; canonical authz
module `v1.0.0-alpha.2` released.

## Format: `[ID] [P?] [S|C] [Story] Description`

- **[P]**: parallelizable with neighbors.
- **[S] / [C]**: hub model-routing tag — `[S]` simple/mechanical → Sonnet subagent; `[C]` complex
  → Opus. An `[S]` task that fails QA is re-tagged `[C]` and redone (escalation noted).

## Phase 1: Setup

- [X] T001 [S] Record the pre-change baseline — `make build`, `go vet ./...`, `go test ./...` on
  branch `007-service-scaffold-onboarding`; capture results in the feature notes (verify/scope
  gate baseline).
- [X] T002 [C] Cross-repo prerequisite: tag **devedge-sdk `v0.1.0`** at current main (`75f9a01`)
  and verify `go list -m github.com/infobloxopen/devedge-sdk@v0.1.0` resolves via the proxy
  (FR-011). Record the version for the go.mod template.

## Phase 2: Foundational — `internal/scaffold` (blocking)

- [X] T003 [P] [S] US1 Unit tests first (`internal/scaffold/scaffold_test.go`): name validation
  (valid DNS-label/module/Helm-release names pass; uppercase, spaces, leading digit, >63 chars
  fail **before any file is written**); refuse non-empty target dir with a reason (FR-008);
  rendering writes the full tree with substitutions applied and no `.tmpl` suffixes left. Must
  fail first.
- [X] T004 [C] US1 Implement `internal/scaffold`: `embed.FS` template tree walker +
  `text/template` substitution (`Name`, `Module`, `GoVersion`, pinned dep versions struct),
  atomic behavior (validate everything → then write; on any write error, remove what was
  created), satisfying T003.
- [X] T005 [S] US1 Wire `de project init NAME [--dir DIR] [--module MODULE]` (`cmd/de/
  project_init.go`, registered in `projectCmd()` in `cmd/de/main.go`); help text matches the
  README style of `up`/`down`/`chart`.

## Phase 3: The template tree (US1 — the generated project)

- [X] T006 [C] US1 Author the generated project's **proto + codegen** templates:
  `proto/{{name}}/v1/webhook_endpoint.proto.tmpl` (WebhookEndpoint{url, secret, event_filters};
  Create/Get/List/Update/Delete each carrying `(infoblox.authz.v1.rule)` with verbs
  create/get/list/update/delete, resource `webhook-endpoint`; `google.api.http` bindings),
  the canonical-annotation mirror (byte-identical to released `authz.proto`, header marking it
  codegen-input-only, go_package canonical), vendored `google/api/{annotations,http}.proto`,
  `buf.yaml.tmpl` + `buf.gen.yaml.tmpl` (go, go-grpc, grpc-gateway; module mapping; gateway
  generates only for the project's own proto).
- [X] T007 [C] US1+US2 Author the **runtime** templates: `cmd/{{name}}/main.go.tmpl` with `serve`
  (resolve `fsnotify://<engine>/<path>` indirection → read real DSN file (003 convention) → pgx
  pool; gRPC server with `grpcauthz.UnaryServerInterceptor` — rules via
  `authzpb.RulesFromGlobal`, boot-time completeness gate ON, `DevAuthorizer` default with
  documented env switch; grpc-gateway + gRPC muxed on :8080) and `migrate` implementing the 006
  C2 contract (`migrate up`, `DATABASE_URL`, `infobloxopen/migrate` fork with persisted-down
  config — same require/replace pair as devedge's own go.mod); `internal/server/server.go.tmpl`
  (impl over a `Store` interface); `internal/store/postgres.go.tmpl`;
  `db/migrations/001_webhook_endpoints.{up,down}.sql`.
- [X] T008 [P] [S] US1 Author the **packaging/docs** templates: `devedge.yaml.tmpl` (Service kind:
  hostname `{{.Name}}.dev.test`, workload.build context `.` port 8080, dependency `db` postgres
  with `storage.migrations: db/migrations`, route host→`http://127.0.0.1:8080`),
  `Dockerfile.tmpl` (multi-stage; entrypoint dispatches serve/migrate), `Makefile.tmpl`
  (generate preflights buf/protoc plugins and names anything missing; build; test; run),
  `go.mod.tmpl` (devedge-sdk `v0.1.0`, canonical authz module `v1.0.0-alpha.2`, gateway/pgx/
  migrate pins), `README.md.tmpl`.
- [X] T009 [P] [S] US1+US4 Author `internal/server/server_test.go.tmpl` (fake-store unit tests:
  CRUD round-trip via the interface, no DB — keeps FR-003's "tests pass immediately" true) and
  `AGENTS.md.tmpl` (FR-009: layout, the rename-the-resource flow, generate, up/--deploy/down,
  where authz declarations live; short and curated).

## Phase 4: Verification harnesses

- [X] T010 [C] US1 Generated-project smoke (`test/integration/scaffold_smoke_test.go`): init into
  `t.TempDir()` → `make generate` → `go build ./...` → `go test ./...` inside the generated
  project; assert zero manual edits needed (FR-003, SC-001). Skipped with `-short` (needs
  network for module downloads + buf plugins on PATH).
- [X] T011 [C] US2+US3+US4 The **walk-through e2e** (`test/e2e/scaffold_onboarding_test.go`,
  003–006 live-k3d harness style): init → scripted rename webhook_endpoint→note (sed-level, the
  US4 rename flow) → regenerate → `de project up` (deps + migrations) → start `serve` → HTTPS
  CRUD probe via the dev hostname (create + get round-trip through Postgres) + deny probe
  (DevAuthorizer deny path → permission-denied) → boot-gate probe (remove one annotation,
  regenerate, `serve` must refuse naming the method — SC-003) → `de project up --deploy` →
  CRUD probe again → `de project down`. This is FR-010/SC-002, the feature gate.
- [X] T012 [S] US4 Record the onboarding measurement (SC-004): run the walk-through guided only
  by the generated AGENTS.md; capture duration + friction notes in
  `specs/007-service-scaffold-onboarding/WALKTHROUGH.md`.

## Phase 5: QA & Documentation

- [X] T013 [S] Regression: full existing suites green (`go test ./...`, integration, 003–006
  e2es as available locally) + `dk` regression contract untouched (SC-005). `/verify-change`
  functional + scope gates.
- [X] T014 [S] Docs: README section for `de project init` (mirroring the `Service` kind section
  style), CHANGELOG entry, CLAUDE.md note if conventions changed.


## Execution notes (running)

- **T001 baseline (2026-06-10, branch HEAD `6665fb9`):** `make build` OK (3 binaries), `go vet ./...` clean, `go test ./...` 22 packages ok.
- **T002:** devedge-sdk tagged `v0.1.0` at `75f9a01`; resolves via proxy. go.mod template pins SDK v0.1.0 + authz module v1.0.0-alpha.2.
- **T009 (escalation note, minor):** agent's fakeStore returned a gRPC status for missing ids instead of the `server.ErrNotFound` sentinel (contract landed after dispatch) — 2 generated-project tests failed; fixed in the template ([S] held, one-line fix).
- **Layout deviation from plan:** vendored protos live under `third_party/proto/` as a second buf module (proven devedge-sdk Phase-B pattern) rather than plan's flat `third_party/google/api`; gateway+gRPC muxed via separate ports (gateway :8080 routed by devedge, gRPC 127.0.0.1:9090) rather than single-port cmux — simpler, same observable contract.
- **T010 (2026-06-10):** automated smoke green in 6.6s — init → make generate → build → tests, plus boot-gate positive (gate passes → fails only on missing DATABASE_URL) and negative (annotation removed → "refusing to start … DeleteWebhookEndpoint"). SC-001 ✓ SC-003 ✓.
- **T011 (2026-06-10): walk-through e2e GREEN in 75.9s** (warm image cache; ~6.5min cold). Iterations that
  hardened the templates en route: rename gotcha (gateway `HandlerFromEndpoint` suffix — guarded + documented),
  readiness-poll test bug, `sslmode=disable` default in the scaffolded migrate (pgx5 vs no-TLS dev postgres),
  one-binding-one-password (deploy Secret must share the reconcile binding). Hook logs captured via a sidecar
  watcher — pod logs beat inference.
- **T012:** WALKTHROUGH.md records the scripted timings + agent-run SC-004 evidence + the friction log; first
  human run still to be scheduled.
- **T013 (2026-06-10):** vet clean; `go test ./...` all green (incl. scaffold suite + smoke); dk regression
  contract explicitly green (`TestDKRouteSurfaceUnchanged`, `TestDKConfigKindStillParses`). Diff touches only
  cmd/de (new command + one AddCommand line), internal/scaffold (new), tests, docs — no daemon/reconciler/chart/
  dk surfaces (scope gate ✓).
- **T014:** README `de project init` section + CHANGELOG entry.

## Dependencies & Order

- T001, T002 first (T002 blocks T008's go.mod pin).
- T003 → T004 → T005 (tests first; CLI last).
- T006–T009 after T004 (templates land in the embedded tree); T006/T007 are the complex pair,
  T008/T009 parallel-safe.
- T010 needs T005–T009. T011 needs T010 green. T012 needs T011. T013/T014 close.

## Notes

- No daemon, reconciler, chart, or dk-surface changes anywhere — scaffold is CLI + templates +
  tests only. Anything drifting into those areas fails the scope gate.
- The walk-through e2e doubles as the platform's recurring onboarding probe; keep it runnable in
  isolation (`go test ./test/e2e/ -run ScaffoldOnboarding`).
