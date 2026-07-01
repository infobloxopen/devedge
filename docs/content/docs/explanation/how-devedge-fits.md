---
title: How devedge fits together
weight: 10
---

The platform is three repositories with one division of labor: **devedge
orchestrates, and each SDK supplies a mechanism.** This page explains where a
responsibility lives, so you know which repository to reach for.

## The three repositories

- **devedge** is the `de` CLI, the local edge router, and the orchestration
  surface. It scaffolds services and micro-frontends, provisions their
  dependencies, registers their routes, composes them, and deploys them. It owns
  process and topology — not the code inside a service.
- **devedge-sdk** is the Go service framework. A service defines its API in
  proto; the SDK generates the server, storage, and the fail-closed
  authorization and tenant-isolation layers. It owns the shape and behavior of a
  backend service.
- **devedge-ufe-sdk** is the Angular micro-frontend SDK. It supplies the session
  seam, loud nav-contribution validation, and the bearer wiring that attaches a
  token to outbound requests. It owns the shape and behavior of a frontend
  module.

`de new service` scaffolds on `devedge-sdk`; `de ufe new` scaffolds on
`devedge-ufe-sdk`. The CLI drives both, but the two SDKs are where the code you
write lives.

## The seam between backend and frontend

A full-stack feature meets at two contracts:

- **The API contract.** A service publishes its OpenAPI v3 spec with
  `de api publish`. The micro-frontend generates a typed client from that same
  spec, so both sides move from one source of truth.
- **The session contract.** The frontend shell owns the session and attaches the
  access token to every request the generated client makes. The backend enforces
  authorization and tenant isolation on the requests it receives. Neither side
  trusts the other's boundary; each enforces its own.

[Ship a full-stack feature](../../tutorial/ship-a-full-stack-feature/) walks this
seam end to end.

## Why the split

Each SDK is independently useful and independently versioned. A team can adopt
the Go framework without the frontend SDK, or the reverse. Keeping the CLI
separate from both means the platform can scaffold, route, and deploy code it did
not generate, and each SDK can release on its own cadence. The public seams — the
authorization interface in `devedge-sdk`, the session provider in
`devedge-ufe-sdk` — are where private, product-specific implementations bind
without forking the SDK.
