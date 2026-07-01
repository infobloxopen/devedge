---
title: Ship a full-stack feature
weight: 10
---

This tutorial builds one feature end to end: a Go backend service and an Angular
micro-frontend that consumes it, both scaffolded, routed, and run with `de`. You
finish with a service reachable over stable HTTPS and a micro-frontend that calls
it with an authenticated session.

The feature is a small "orders" service and a UI that lists orders.

## Before you start

Install the CLI and start the edge (see [Installation](../../getting-started/installation/)):

```bash
de install && de start && de doctor
```

You also need Go and Node installed. The backend uses
[`devedge-sdk`](https://infobloxopen.github.io/devedge-sdk/); the frontend uses
[`devedge-ufe-sdk`](https://github.com/infobloxopen/devedge-ufe-sdk).

## 1. Scaffold the service

```bash
de new service orders
```

This scaffolds an apx-native Go service on `devedge-sdk` — a proto-defined API,
generated gRPC and HTTP handlers, storage, and a `devedge.yaml` that describes
how the service is routed and what it depends on. To model the resource and add
methods, follow the SDK's
[Model a resource](https://infobloxopen.github.io/devedge-sdk/docs/how-to/model-and-persist/model-a-resource/)
and [Define a service](https://infobloxopen.github.io/devedge-sdk/docs/how-to/model-and-persist/define-a-service/)
guides.

## 2. Bring the service up

```bash
de project up
```

`de project up` resolves the target cluster, provisions the service's declared
dependencies (for example Postgres), runs migrations, and registers the service's
routes. When it finishes, the service answers over stable HTTPS:

```text
https://orders.dev.test
```

Iterate with `--watch` to keep the routes alive while you develop.

## 3. Publish the API

```bash
de api publish
```

This publishes the service's OpenAPI v3 specification to the apx catalog. The
same spec is what the micro-frontend generates its typed client from, so the
frontend and backend stay in sync from one source. See the SDK's
[Publish an OpenAPI spec](https://infobloxopen.github.io/devedge-sdk/docs/how-to/operate/publish-openapi/)
guide for the emit-and-publish pipeline.

## 4. Scaffold the micro-frontend

```bash
de ufe new orders-ufe
```

This scaffolds an Angular + single-spa micro-frontend on `devedge-ufe-sdk`,
already wired with a validated nav contribution, a session-aware shell, and a
bearer interceptor. The generated project runs under a local shell that owns the
session.

## 5. Generate the typed client

From the OpenAPI spec published in step 3, generate a typed Angular client:

```bash
npm install --save-dev ng-openapi-gen
npx ng-openapi-gen --input openapi/orders.openapi.yaml --output src/app/api/orders
```

This writes an `OrdersService` and its models. The generated client issues HTTP
calls but does not attach a token — the micro-frontend supplies that in the next
step.

## 6. Wire the session and call the service

In a devedge micro-frontend, the **shell owns the session** and a bearer
interceptor attaches the access token to every request the generated client
makes. Child micro-frontends never authenticate; they receive a read-only
session view as a prop.

The scaffold already registers `provideDevedgeSession` and the bearer
interceptor, so the generated `OrdersService` sends authenticated requests once
you point its base URL at `https://orders.dev.test`. For the exact wiring — how
the shell instantiates OIDC, gates registration on a token, and threads the
session into the micro-frontend — read the annotated
[`examples/fullstack-oss`](https://github.com/infobloxopen/devedge-ufe-sdk/tree/main/examples/fullstack-oss),
which is this same backend-and-frontend pairing as a working example.

## 7. Run it

Start the micro-frontend's shell and check the local wiring:

```bash
pnpm start
pnpm run doctor    # reachability, TLS, CORS, manifest, nav
```

`devedge-ufe doctor` reports any silent-failure conditions — an unreachable
backend, an untrusted certificate, a dropped nav contribution — so a broken seam
surfaces loudly instead of rendering nothing.

## What you built

- A Go service on `devedge-sdk`, reachable at `https://orders.dev.test`, with its
  API published to the apx catalog.
- An Angular micro-frontend on `devedge-ufe-sdk` that calls the service with an
  authenticated, shell-owned session through a generated typed client.

## Next

- Compose several services into one host binary with
  [`de compose`](../../reference/cli/compose/).
- Deploy across tenant cells with [`de cell`](../../reference/cli/cell/).
- Deploy the workload into the resolved cluster with `de project up --deploy`.
