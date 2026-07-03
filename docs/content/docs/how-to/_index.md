---
title: How-to guides
weight: 30
---

Task-oriented guides for operating devedge. Each command below has complete,
generated reference; the narrative operate guides are being migrated here from
the [project README](https://github.com/infobloxopen/devedge#readme).

## Operate

{{< cards >}}
  {{< card link="../reference/cli/register/" title="Route a host or service" subtitle="Register and inspect routes on the local edge." >}}
  {{< card link="../reference/cli/cluster/" title="Use a k3d cluster" subtitle="Create, attach, and watch Kubernetes clusters." >}}
  {{< card link="../reference/cli/ci/" title="Run in CI" subtitle="Run tests against an ephemeral cluster." >}}
  {{< card link="../reference/cli/cell/" title="Deploy across cells" subtitle="Deploy cells and move tenants between them." >}}
  {{< card link="define-slos/" title="Define and ship SLOs" subtitle="Derive, lint, calibrate, and render SLI/SLOs with de slo." >}}
{{< /cards >}}

{{< callout type="info" >}}
Bringing a project up, publishing its API, and scaffolding a micro-frontend are
covered end to end in [Ship a full-stack feature](../tutorial/ship-a-full-stack-feature/).
{{< /callout >}}
