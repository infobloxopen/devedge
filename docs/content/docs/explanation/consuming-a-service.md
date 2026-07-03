---
title: "Consuming a service: CLI, Terraform, and clients from one contract"
weight: 40
---

A devedge service declares **one contract** and every way of consuming it — a Go
client, a TypeScript client, a command-line interface, a Terraform provider — is a
**generated projection** of that contract. You do not hand-write four
integrations that drift apart; you annotate the API once and regenerate the
surfaces. This page explains the model, the four surfaces, and how a single field
annotation shows up correctly in each.

## One contract, four surfaces

A service defines its API in proto. The build produces three coordinated
artifacts from that one source:

- a **gRPC** server,
- a **REST** gateway, and
- an **enriched OpenAPI v3** document.

The OpenAPI document is the **single interchange** every consumer surface reads.
It is *enriched* because it carries the API's semantics, not just its wire shape:
alongside native `required` / `readOnly` / `writeOnly` / `enum`, it emits
`x-aip-resource` (the resource type, pattern, and key), `x-aip-method` (which
standard AIP method each operation is), `x-aip-pagination`, `x-aip-references`,
and `x-aip-field-behavior` (`REQUIRED` / `OUTPUT_ONLY` / `IMMUTABLE` /
`INPUT_ONLY`). A generator reads this one file and knows enough to build a correct
surface — which fields are inputs, which are outputs, which are set-once, and
which are secrets.

Two kinds of surface come out of that contract:

- **Clients** are *emitted and (optionally) published* — a Go module or an npm
  package generated from the spec, versioned with the API.
- **A CLI or a Terraform provider** is *scaffolded once as its own repo*, then
  extended in place: `de cli add` / `de terraform add` generate the commands or
  the resource from a contract into the existing repo. A CLI shell then re-derives
  its command roster from every domain you have added, so domains accumulate into
  one binary.

Both kinds read the contract directly; nothing is transcribed by hand.

## The four surfaces

| Surface | Runtime repo | Command | Consumed as |
|---|---|---|---|
| **Go client** | `apx` | `apx client generate --generator go` — or `de api publish --client --client-generator go` | a typed Go module (`go get`) |
| **TypeScript client** | `apx` (orchestrates `ng-openapi-gen`) | `apx client generate` — or `de api publish --client` | an npm package `@<scope>/<svc>-client` (GitHub Packages) |
| **CLI** | `devedge-cli-sdk` (`clikit`) | `de cli new <name>`, then `de cli add --input <spec> --domain <name>` | your own rebrandable CLI binary |
| **Terraform provider** | `devedge-terraform-sdk` (`tfkit`) | `de terraform new <name>`, then `de terraform add --input <spec> --resource <name>` | a `terraform-provider-<name>` on the Terraform Registry |

`de api publish` drives `apx` to emit clients; `de cli` / `de terraform` drive the
`cligen` / `tfgen` generators to scaffold and grow the CLI and provider. In every
case `de` orchestrates and the SDK supplies the mechanism — the same division of
labor described in [How devedge fits together](../how-devedge-fits/).

Publishing is deliberately gated, not automatic: the TypeScript client publishes
to GitHub Packages only with `--publish-client` (and a `write:packages` token),
and a Terraform provider reaches the registry through a tag-triggered, GPG-signed
GoReleaser workflow. Generating a client or scaffolding a CLI, by contrast, only
reads the contract locally.

The **CLI** is a rebrandable shell (the CLI mirror of a devedge micro-frontend
shell): a `git`/`kubectl`-style root command whose subcommands are the domains you
add. It logs in with a generic OIDC device grant by default. The **Terraform
provider** is shaped for the Terraform Registry from the start — a HashiCorp-style
GoReleaser config, a registry manifest, and a release workflow.

## How `field_behavior` projects onto each surface

The whole point of enriching the contract is that one annotation lands correctly
everywhere. A field marked `REQUIRED` becomes a required CLI flag *and* a required
Terraform attribute *and* a required client field, from the same source:

| `field_behavior` | Client type | CLI flag (`clikit`) | Terraform schema (`tfkit`) |
|---|---|---|---|
| `REQUIRED` | required field | required flag (`MarkFlagRequired`, on `create`) | `Required` |
| `OUTPUT_ONLY` | response-only (`readOnly`) | no input flag — appears only in output | `Computed` + `UseStateForUnknown` |
| `IMMUTABLE` | set at create | create-only flag (dropped from `update`) | `RequiresReplace()` |
| `INPUT_ONLY` (secret) | write-only (`writeOnly`) | `--<flag>-stdin`, read from stdin, never echoed | `Sensitive` |
| `enum` | typed / validated value | flag help lists the allowed values | enum validator |

Because these projections are derived, the surfaces cannot disagree with the
contract or with each other. Change the annotation, regenerate, and every surface
moves together.

## Open core, private overlay

The runtimes and generators are open source under
[`infobloxopen`](https://github.com/infobloxopen): `devedge-cli-sdk` (`clikit` +
`cligen`) and `devedge-terraform-sdk` (`tfkit` + `tfgen`) are mechanism, not
policy. Product-specific pieces — Infoblox's Okta / PDS authentication, branding,
extra commands — bind privately through an overlay, without forking the open SDK.

The seam is the same one devedge uses everywhere. A scaffolded CLI shell owns a
small `session.go` that constructs its auth provider; the open core ships a
generic OIDC device grant there, and an internal preset replaces it:

```
de cli new ib --preset-dir ../devedge-cli-sdk-internal/preset/infoblox-cli
```

This is the CLI analog of the backend's `opaauthz` → `authz.Authorizer` binding
(see [How devedge fits together](../how-devedge-fits/#why-the-split)): the public
interface is defined in the open SDK, and the private, product-specific
implementation binds at scaffold time. The public repositories ship no proprietary
preset; a missing or malformed preset fails loudly.

## The catalog is the roster

`apx` maintains a **catalog** — the discovery index of every published service's
OpenAPI spec. It answers "which services exist, and where is each one's contract?"
That index is what makes a **multi-domain** CLI tractable: you compose one CLI by
adding each service's contract to it.

The CLI makes this concrete. `de cli add` generates the domain under `gen/<name>`
and then re-derives the shell's wiring from *every* domain present under `gen/` —
so adding a second service's contract accumulates it alongside the first, and one
shell binary carries every domain you have added:

```
de cli new shop --module example.com/shop
de cli add --input widgets.openapi.yaml --domain widgets
de cli add --input orders.openapi.yaml  --domain orders
# `shop --help` now lists both `widgets` and `orders`
```

This is the CLI mirror of composing a suite from many services: the contract is
the unit of composition, and the catalog is the roster of contracts to draw from.

The Terraform provider composes the same way: `de terraform add` re-derives the
provider's resource registration from *every* resource generated under
`internal/provider/`, so adding each service's contract accumulates its resources
into one `terraform-provider-<app>` — a single provider covering many domains.

## The repositories

| Repository | Role |
|---|---|
| [`infobloxopen/devedge`](https://github.com/infobloxopen/devedge) | the `de` CLI that orchestrates every command above |
| [`infobloxopen/devedge-sdk`](https://github.com/infobloxopen/devedge-sdk) | the Go service framework that emits the enriched OpenAPI contract |
| [`infobloxopen/apx`](https://github.com/infobloxopen/apx) | the API lifecycle tool: catalog, client generation, publishing |
| [`infobloxopen/devedge-cli-sdk`](https://github.com/infobloxopen/devedge-cli-sdk) | the CLI runtime (`clikit`) and generator (`cligen`) |
| [`infobloxopen/devedge-terraform-sdk`](https://github.com/infobloxopen/devedge-terraform-sdk) | the Terraform runtime (`tfkit`) and generator (`tfgen`) |

To see the backend/frontend half of the same idea — one API contract feeding a
typed client into a micro-frontend — walk
[Ship a full-stack feature](../../tutorial/ship-a-full-stack-feature/).
