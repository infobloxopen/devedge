---
title: Use with Claude Code
weight: 20
---

Bootstrap a new service on devedge from a one-line prompt, with an agent driving
the working flow for you. devedge ships a Claude Code skill, `new-service`, that
turns "build an `orders` service with devedge" into a running gRPC+REST service:
it pins the SDK version, scaffolds with `de new service`, models your resource,
generates, builds, runs, and round-trips over HTTP.

## Discover the skill

The skill lives in the devedge repository at
`.claude/skills/new-service/SKILL.md`. Claude Code discovers skills from the
`.claude/skills/` directory of a workspace, so you get it one of two ways:

- **Clone devedge into your workspace** (or open it alongside your project) so
  `.claude/skills/new-service/` is on disk, then start Claude Code there.
- **Copy the skill** into your own project's `.claude/skills/new-service/`.

Then just ask, in plain language:

> build an `orders` service with devedge, with an Order resource

Claude Code matches the request to `new-service` and drives the flow. The
authoritative reference for the API is always these docs and the `de` /
`devedge-sdk` CLIs (`--help`), not SDK internals.

## What the skill does

1. **Pins the SDK version** — never `@latest`, so repeated bootstraps are
   reproducible (this closes the version-drift gap).
2. **Scaffolds** with `de new service <name> --resource <Resource>`.
3. **Models** your resource(s) in the generated `.proto` (keeping the authz
   annotation on every RPC).
4. **Generates + builds** with `de generate` and `de build`.
5. **Runs** the service (standalone or through the edge with `de project up`).
6. **Round-trips** a create/read over the REST gateway to prove it works.

## Without an agent

The same flow is a short sequence of commands — see
[Ship a full-stack feature](../../tutorial/ship-a-full-stack-feature/) and
`de new service --help`. The skill just wraps that flow so a greenfield developer
does not have to reverse-engineer the entry point.

## Packaging as a plugin (follow-up)

Shipping the in-repo skill needs no credentials — it works the moment
`.claude/skills/new-service/` is on disk. Packaging it as a Claude Code plugin /
marketplace entry (so it installs without cloning devedge) is a follow-up that
depends on marketplace setup, tracked separately.
