---
name: verify-change
description: The QA gate run after implementing a feature. Use to confirm a change is both functionally correct AND within scope before marking work complete. Runs build, lint, tests, e2e when relevant, and a scope check against the spec. Backs the /verify-change command and the Spec Kit after_implement hook.
---

# Verify a change (QA gate)

Run after `/speckit.implement`, before declaring a feature done. Two parts; **both must pass**.

## 1. Functional

- `make build` — must compile clean.
- `make lint` — `go vet` clean.
- Unit + integration green (use the `run-tests` skill).
- **E2E (k3d):** REQUIRED when the change touches routing, DNS, certificates, background
  process behavior, or dependency orchestration (Constitution III):
  `go test ./test/e2e/...`. If Docker/k3d is unavailable, state that e2e was skipped and
  why — do not claim it passed.

## 2. Scope (anti over-build)

Diff the change against the feature spec's acceptance criteria:

- `git diff --stat <base>...HEAD` to see the surface area.
- For each changed file, ask: does this trace to an acceptance criterion or a task in
  `tasks.md`?
- **Flag anything that does not:** speculative abstraction, unused extension points,
  gold-plating, flags/config nobody asked for. These FAIL the gate even if tests pass.

## Result

- **PASS:** functional green + zero out-of-scope changes. Report what ran and what was skipped.
- **FAIL:** list the failures and out-of-scope additions. If a Sonnet-implemented `[S]` task
  caused the failure, flag it for escalation to Opus per `CLAUDE.md`.
