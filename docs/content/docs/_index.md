---
title: Documentation
next: getting-started
---

devedge is the command line for the platform. It gives every project stable
HTTPS hostnames on one shared entry point, and it scaffolds, routes, composes,
and deploys both halves of a feature — a Go backend service and its Angular
micro-frontend.

## Where things live

The platform is three repositories. This portal is the front door; each SDK
keeps its own reference documentation, versioned with its code.

| Repository | What it is | Documentation |
|---|---|---|
| **devedge** | The `de` CLI, the local edge router, and the orchestration surface. | This portal. |
| **devedge-sdk** | The Go service framework — proto-first services, fail-closed authz, storage. | [infobloxopen.github.io/devedge-sdk](https://infobloxopen.github.io/devedge-sdk/) |
| **devedge-ufe-sdk** | The Angular micro-frontend SDK — session seam, nav validation, bearer wiring. | [github.com/infobloxopen/devedge-ufe-sdk](https://github.com/infobloxopen/devedge-ufe-sdk) |

## Start here

{{< cards >}}
  {{< card link="getting-started/" title="Getting started" subtitle="Install devedge and bring up your first service." >}}
  {{< card link="tutorial/ship-a-full-stack-feature/" title="Ship a full-stack feature" subtitle="Build a Go service and an Angular micro-frontend end to end." >}}
  {{< card link="how-to/" title="How-to guides" subtitle="Operate the edge, clusters, cells, and deployments." >}}
  {{< card link="reference/" title="Reference" subtitle="Every de command, generated from the binary." >}}
  {{< card link="explanation/" title="Explanation" subtitle="How devedge, devedge-sdk, and devedge-ufe-sdk fit together." >}}
{{< /cards >}}
