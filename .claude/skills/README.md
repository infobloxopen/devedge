# Devedge Agent Skills

Reusable, low-token procedures for working on devedge. Invoke a skill instead of
rediscovering how to do a routine task (running tests, building, verifying). Skills exist to
cut tokens and keep mechanical steps consistent across features.

## Layout

Each skill is a directory containing a `SKILL.md`:

```
.claude/skills/<skill-name>/SKILL.md
```

`SKILL.md` is YAML frontmatter + a short Markdown body:

```markdown
---
name: <kebab-case-name>
description: One line — WHAT it does and WHEN to use it. The agent matches on this, so be
  specific about the triggers.
---

# Title

Commands and steps. Keep it lean and command-first. Wrap the Makefile and the `de` CLI
rather than duplicating logic. Link to the constitution for the "why".
```

## Current skills

| Skill | Use when |
|-------|----------|
| `new-service` | Bootstrapping a new service **as a consumer** of devedge-sdk from a tiny prompt ("build a `<X>` service with devedge") |
| `run-tests` | Running unit / integration / e2e tests |
| `build-run` | Compiling and manually smoke-testing |
| `verify-change` | The QA gate after implementing a feature |

`new-service` is consumer-facing (it drives `de new service` to scaffold a service
built *on* devedge); the others are maintainer-facing (working *on* the devedge
repo itself). See the ["Use with Claude Code"](../../docs/content/docs/getting-started/use-with-claude-code.md)
getting-started page for how a developer discovers and runs `new-service`.

## Adding a skill

Promote a procedure to a skill when it is **repeated across features** and **mechanical**
(the agent would otherwise re-derive the same commands). Keep skills:

- **Lean** — commands over prose. Verbose context files measurably hurt agent success and
  cost more tokens; say only what's needed and point elsewhere for the rest.
- **Single-purpose** — one job per skill; compose them (`verify-change` uses `run-tests`).
- **Tooling-backed** — wrap `make` targets and `de` subcommands. As the `de` CLI grows
  agent-friendly affordances (e.g. `de doctor --json`, `de project up --wait`), update the
  skills to use them so verification is one command, not a rediscovered sequence.
