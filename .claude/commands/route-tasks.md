---
description: Confirm complexity tags on tasks.md and route each task to the right model before implementing.
---

Read `tasks.md`. Ensure every task carries a complexity tag — `[S]` (simple/mechanical) or
`[C]` (complex). Add a tag to any task missing one, using these heuristics:

- **`[S]`** — single-file or mechanical change, add a flag/field, wire a test, follow an
  existing pattern.
- **`[C]`** — new subsystem or package, cross-package change, concurrency, a platform adapter,
  anything touching routing / DNS / certs / reconciliation, or anything with non-obvious
  design choices.

Then route implementation:

- **`[S]` tasks** → dispatch to Sonnet subagents (`Agent` tool, `model: sonnet`).
- **`[C]` tasks** → keep on Opus.

Report the tag distribution and which tasks will run on which model.
