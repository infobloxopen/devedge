---
description: Run the QA verification gate for the current change — build, lint, tests, e2e-if-relevant, and a scope check against the spec.
---

Invoke the `verify-change` skill and report results.

**Functional:** `make build`, `make lint`, then unit + integration tests (use the `run-tests`
skill). Run e2e (`go test ./test/e2e/...`) if the change touches routing, DNS, certs,
background processes, or dependency orchestration; otherwise state e2e was skipped and why.

**Scope:** diff the change against the active feature spec's acceptance criteria. Flag any
change that does not trace to a criterion or a task in `tasks.md` (speculative abstraction,
gold-plating, unused extension points). Functional failures or out-of-scope additions mean the
feature is NOT done.

If a Sonnet-implemented `[S]` task caused a failure, flag it for escalation to Opus per `CLAUDE.md`.
