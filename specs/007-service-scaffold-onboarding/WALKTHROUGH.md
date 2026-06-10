# Onboarding walk-through — measurement record (SC-004)

## Scripted run (the automated e2e, 2026-06-10)

`TestScaffoldOnboarding_e2e` (live k3d, fresh ephemeral cluster every run):

| Leg | What happens | Result |
|---|---|---|
| Scaffold + rename | `de project init notesvc` → scripted resource rename (webhook endpoint → note) → `make generate` → build → tests | ✅ |
| Local-run | provision shared Postgres + isolated DB, apply migration v1 (006 seam), serve with fail-closed authz | ✅ |
| CRUD over HTTP | create + get through the REST gateway; row verified in Postgres via psql; deny probe (non-granted subject → 403) | ✅ |
| Deploy | image built from the scaffolded Dockerfile, schema hook (`migrate up`, no-op: already current), workload Ready, in-cluster probe sees the locally-created row, `down` removes it | ✅ |

**Wall time: 75.9s** with a warm Docker layer cache (~6.5 min cold — the multi-stage
image build downloading Go modules dominates). Scaffold→tested project alone: **~25s**
(SC-001 budget: 5 min).

## Agent-guided run (this feature's development, 2026-06-10)

The walk-through was executed interactively by a coding agent using only the scaffold
output, its AGENTS.md, and command help — total elapsed within one working session,
**well under one business day** (SC-004 for the agent case). A first run by a *human*
developer new to the platform remains to be scheduled; record its duration here.

## Friction found (and fixed in the templates during this feature)

1. **Rename gotcha:** a greedy resource rename also rewrites the generated gateway's
   `…HandlerFromEndpoint` suffix (it means "from a gRPC address", not the resource).
   → AGENTS.md now calls this out; the e2e's scripted rename guards the token.
2. **TLS default:** the migrate engine's pgx5 driver needs `sslmode=disable` against
   the no-TLS dev Postgres; the scaffolded `migrate` now defaults it (mirroring
   devedge's own applier).
3. **One binding, one password:** `depruntime.NewBinding` mints a fresh password per
   call — the deploy Secret and the database role must come from the *same* binding
   (the daemon's flow guarantees this; the e2e originally minted two).
4. **Boot-gate ordering:** the gate must run before any I/O so a missing annotation is
   diagnosable without a database; the template now gates first.

These are exactly the papercuts the vision's "run it early — it is the product"
directive was meant to surface before the first real user hits them.

## How to run the human measurement yourself (SC-004)

Note the clock time when you start and when the deployed CRUD probe succeeds; record both
below under "Human run". Use only the generated project's AGENTS.md/README + `de --help`
from step 4 onward — that's the metric.

**0. Prerequisites (5 min, not counted):**
- Docker running; `k3d`, `kubectl`, `helm`, `psql` on PATH.
- Codegen toolchain: `buf`, `protoc-gen-go`, `protoc-gen-go-grpc`, `protoc-gen-grpc-gateway`
  (`make generate` names anything missing, with the install command).
- Build the new CLI (your installed `de` predates this feature; the running daemon is fine):
  `cd ~/go/src/github.com/infobloxopen/devedge && make build` → use `./bin/de`.
- `./bin/de doctor` to confirm the edge is healthy (`de start` if the daemon isn't running).

**1. Scaffold** — `cd $(mktemp -d) && ~/go/src/github.com/infobloxopen/devedge/bin/de project init myhooks`
Expect: a `myhooks/` directory and a printed next-steps block. (`'Bad Name'` is rejected;
re-running on the same dir refuses — both worth 10 seconds to see.)

**2. Generate + verify it builds clean** — `cd myhooks && make generate && make test`
Expect: codegen into `internal/gen/`, then all tests pass with zero edits.

**3. See the fail-closed gate once** (optional but recommended, 2 min): delete the
`option (infoblox.authz.v1.rule) = …` line from one RPC in
`proto/myhooks/v1/webhook_endpoint.proto`, run `make generate && make run` — the service
refuses to start and names the method. Restore the line, regenerate.

**4. Bring up the substrate** — `~/go/src/.../bin/de project up`
Expect: cluster line (shared `devedge` cluster), Postgres provisioned, migration `v1`
applied, a printed `DATABASE_URL=fsnotify://postgres/...` line, route registered.

**5. Run it** — `export DATABASE_URL=…` (the printed value), then `make run`.
Expect: `myhooks: serving gRPC on 127.0.0.1:9090, HTTP on :8080`.

**6. CRUD over the dev hostname (separate terminal):**
```bash
curl -sk https://myhooks.dev.test/v1/webhook-endpoints \
  -X POST -d '{"url":"https://example.test/h","secret":"s1","event_filters":["created"]}'
curl -sk https://myhooks.dev.test/v1/webhook-endpoints          # list shows it
# deny probe: a non-granted subject is rejected (403)
curl -sk -o /dev/null -w '%{http_code}\n' \
  -H 'Grpc-Metadata-x-dev-subject: intruder' https://myhooks.dev.test/v1/webhook-endpoints
```

**7. Deploy it** — stop `make run`, then `…/bin/de project up --deploy`
Expect: image build from the scaffolded Dockerfile (first build downloads modules — the slow
step), schema hook runs `migrate up` (no-op: already current), workload Ready; re-run the
step-6 curls — same data, now served in-cluster.

**8. Down** — `…/bin/de project down` (add `--clean` to also drop the data).

**Record:** start time, end-of-step-7 time, and any step where you needed something not in
AGENTS.md/README — that's a friction bug; file it against the scaffold templates.

### Human run
- _(date, who, duration, friction notes — fill in)_

### Human run
- **2026-06-10, dgarcia — friction #1 (step 4):** `de project up` failed with
  `exec: "helm": executable file not found in $PATH` — but only *after* creating the
  cluster and installing cert-manager (~40s). Cause: helm/kubectl ship via Rancher
  Desktop (`~/.rd/bin`), absent from that terminal's PATH. Fixes to file: (a) preflight
  helm/kubectl/k3d *before* cluster creation, with remediation in the message;
  (b) `de doctor` should check the cluster toolchain. Workaround:
  `export PATH="$HOME/.rd/bin:$PATH"`.
