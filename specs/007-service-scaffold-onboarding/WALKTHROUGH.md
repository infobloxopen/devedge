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
