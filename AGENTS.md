# AGENTS.md

Guidance for coding agents (Claude Code and compatible tools) working in or with
this repository.

## Bootstrapping a service on devedge

If you are helping a developer **build a new service on devedge** (a consumer of
`devedge-sdk`), use the discoverable Claude Code skill instead of re-deriving the
entry point:

- Skill: [`.claude/skills/new-service/SKILL.md`](.claude/skills/new-service/SKILL.md)
- Triggers on prompts like "build a `<X>` service with devedge".
- It pins the SDK version, scaffolds with `de new service`, models the resource,
  generates, builds, runs, and round-trips over HTTP.

Docs: [Use with Claude Code](docs/content/docs/getting-started/use-with-claude-code.md).

## Working ON the devedge repo

devedge is the `de` CLI + `devedged` local dev-edge daemon (Go). Maintainer-facing
skills live under [`.claude/skills/`](.claude/skills/) (`run-tests`, `build-run`,
`verify-change`); see that directory's README.

- Build: `make build` (produces `de`, `devedged`, `devedge-dns-webhook`).
- Test / vet: `make test` / `make lint`.
- CLI reference docs are generated — after changing a command, run
  `make docs-cli` and commit; CI enforces this with `make docs-cli-check`.

## API authority

For anything about the service-building API, the authority is the published docs
and the `de` / `devedge-sdk` CLIs (`--help`) — not SDK source.
