---
name: new-service
description: >-
  Bootstrap a new backend service on devedge from a tiny prompt, as a CONSUMER of
  the devedge-sdk (not a devedge maintainer). Use whenever the request is "build a
  <X> service with devedge", "scaffold a devedge service", "start a new service on
  devedge / devedge-sdk", or a greenfield developer whose only context is "we use
  devedge". Drives the working flow end to end: pin the SDK version, scaffold with
  `de new service`, model the user's resource(s), generate, build, run, and
  round-trip over HTTP.
---

# Bootstrap a devedge service

Turn a one-line prompt ("build an `orders` service with devedge") into a running
gRPC+REST service. You are a CONSUMER of `devedge-sdk`: the API is whatever the
published docs and the `de` / `devedge-sdk` CLIs expose — do not read SDK source
to learn the API.

## 0. Prerequisites (once)

- `de` on PATH (`de version`). If missing, install per the docs
  ([Getting started](https://infobloxopen.github.io/devedge/docs/getting-started/)).
- The local edge running: `de doctor` should be green (`de install && de start` if not).

## 1. Pin the SDK version — do NOT use `@latest`

`de new service` forwards to the `devedge-sdk` scaffold binary. Installing it with
`@latest` silently drifts the generated service across runs. Pick ONE version and
pin it:

```bash
# Choose a version: the newest release, or match what `de` builds against.
de version                       # shows de's own toolchain / SDK pins
# then install that exact tag (example — replace with the chosen version):
go install github.com/infobloxopen/devedge-sdk/cmd/devedge-sdk@v0.52.0
```

Record the chosen version; step 2 scaffolds against it and step 3 confirms the
service's `go.mod` pins the same one.

## 2. Scaffold

```bash
de new service <name> --resource <Resource>
# e.g.  de new service orders --resource Order
# ent backend instead of gorm:   --backend ent
# custom Go module path (forward flags after --):  -- --module github.com/acme/orders
```

This creates `<name>/` with an annotated proto, generated models + repository +
authz-gated server, and a `devedge.yaml` routing the service through the edge.

Confirm the pin took: the service's `go.mod` should `require
github.com/infobloxopen/devedge-sdk <the version from step 1>`. If it drifted,
`go get github.com/infobloxopen/devedge-sdk@<version> && go mod tidy` in the
service dir.

## 3. Model the user's resource(s)

Edit the service's `.proto` (usually `proto/<domain>/v1/<name>.proto`) to match
what the user asked for:

- Add fields to the resource message.
- Add resources / RPCs as needed; keep every RPC's `(infoblox.authz.v1.rule)`
  annotation (the boot gate refuses to start if a method is undeclared).
- Follow AIP CRUD (Get/List/Create/Update/Delete) — the scaffold's shape is the
  reference.

## 4. Generate, build, run

```bash
cd <name>
de generate     # or: make generate — pinned buf + plugins, then go mod tidy
de build        # or: go build ./...
```

Run it (two options):

- Standalone: run the built binary, or
- Through the edge: `de project up` (serves it over stable HTTPS at the routed
  host, e.g. `https://app.dev.test/api/<domain>/v1/...`).

## 5. Round-trip over HTTP

Prove it works — create then read a resource over the REST gateway. Dev auth: send
the dev subject header.

```bash
# Adjust host/port to how you ran it (edge host or the local gateway addr).
curl -sS -X POST https://app.dev.test/api/orders/v1/orders \
  -H 'Grpc-Metadata-X-Dev-Subject: dev' \
  -H 'content-type: application/json' \
  -d '{"order":{"display_name":"first"}}'

curl -sS https://app.dev.test/api/orders/v1/orders \
  -H 'Grpc-Metadata-X-Dev-Subject: dev'
```

A created resource that reads back is the done signal. If DNS/TLS is involved and
a host does not resolve, run `de doctor`.

## Reliability (optional, recommended)

The scaffold ships a GOOD default `slo.yaml`. After adding custom methods:
`de slo generate` (re-derives from the API contract) → `de slo lint` (green on a
fresh scaffold; add `--fail-on-warn` to gate CI on calibration).

## Notes

- Authority for the API is the published docs + the CLIs' `--help`, not SDK source.
- The `devedge-sdk` scaffold binary is a separate install; step 1's pin is the one
  thing that keeps repeated bootstraps reproducible.
