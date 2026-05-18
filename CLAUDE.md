# Devedge — Claude Code Instructions

## Constitution

All work on this project MUST follow the project constitution at
`.specify/memory/constitution.md`. Before planning, speccing, or implementing any feature,
read and apply the principles and quality gates defined there. The constitution takes
precedence over any default behavior.

## Commit Messages

**NEVER add any AI or LLM attribution to commit messages.** No `Co-Authored-By: Claude`,
no `Generated with Claude Code`, no `Co-Authored-By: GitHub Copilot`, no mention of any
AI tool or model. Commit messages MUST only describe the change and its intent.

## Active Technologies
- Go 1.25.5 (from `go.mod`) (001-fix-dns-udp-bind)
- No new persistent storage. The set of authoritative DNS suffixes (001-fix-dns-udp-bind)

## Recent Changes
- 001-fix-dns-udp-bind: Added Go 1.25.5 (from `go.mod`)
