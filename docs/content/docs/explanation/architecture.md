---
title: Architecture
weight: 20
---

devedge runs a small local edge and a route registry, and resolves where each
project's workload runs. This page explains those pieces at a conceptual level.

## The edge and the daemon

`de start` runs a background daemon that owns one shared entry point on ports 80
and 443. `de install` adds a local certificate authority (via mkcert) and
configures DNS so that `*.dev.test` hostnames resolve to the edge and serve
trusted HTTPS. Every project shares this one entry point rather than each running
its own proxy on a different port.

## Routes and leases

A route maps a hostname to an upstream — a host process, a container, or a
service in a cluster. Routes are registered dynamically, either explicitly with
`de register`, from a project's `devedge.yaml` with `de project up`, or
automatically from Kubernetes Ingress objects. Routes carry a lease; a project
that keeps running renews its lease, and routes for a stopped project expire on
their own. `de project down` releases only the requesting project's routes — it
never removes the shared cluster or another project's footprint.

## Cluster topology

`de project up` resolves the cluster a workload runs in:

- **Shared dev cluster** — the default. Projects coexist in one local k3d
  cluster with per-service logical isolation.
- **Ephemeral CI cluster** — `de ci run` creates a dedicated, throwaway cluster
  for a test run and tears it down on exit.
- **Dedicated cluster** — a project can opt into its own cluster when logical
  isolation inside the shared cluster is not enough.

## Cells

For deployment, `de cell` runs version-pinned cells, each serving a subset of
tenants. Cells provide isolation rather than load balancing: a tenant is assigned
to a cell, and moving a tenant between cells follows a controlled sequence — a
storage fence and outbox epochs keep a move safe from split-brain writes.

## Composition

`de compose` builds several service modules into one host process as static
composition — the modules are imported and run together, not loaded as plugins.
The same module runs standalone or composed; the host, not the module, decides.
