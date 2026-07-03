---
title: Define and ship SLOs
weight: 50
---

Turn a service's API contract into reliability targets with `de slo` (WS-025):
derive GOOD default SLOs from the OpenAPI, lint them through the fail-loud
classifier, render them to your monitoring backend, and calibrate them against a
measured baseline.

`de slo` orchestrates the devedge-sdk `slo` seam — the OpenSLO derivation,
classifier, and Prometheus/Grafana/Loki emitters — so you never hand-write a
Prometheus rule or pick a target out of the air. For the *authoring* judgement
(picking the SLI type, the window, the error-budget policy), see the
[`define-slo` skill](https://github.com/infobloxopen/devedge-sdk/tree/main/.claude/skills/define-slo).

## Before you start

You need a devedge service with a generated, enriched OpenAPI. `de generate`
(or `make generate`) produces `openapi/<svc>.openapi.yaml`. The scaffold already
ships a good default `slo.yaml`; the steps below regenerate and operate on it.

## 1. Generate the default SLOs

From the service directory:

```bash
de slo generate
```

This locates `openapi/<svc>.openapi.yaml`, derives the gRPC **service FQN**
(proto package + service name, e.g. `orders.v1.OrderService`) from the project's
`.proto` files, and writes `slo.yaml`: grouped read/write availability and
latency SLOs, a 28-day window, multi-window multi-burn-rate alerts, and a
mandatory error-budget policy — all marked `devedge.io/uncalibrated: "true"`.

The derived FQN becomes the `rpc.service` label on every SLI. This matters: the
OpenAPI does not carry the FQN, so without it the SLIs would omit the
`rpc_service` matcher and silently aggregate across services. If `de slo generate`
cannot determine a single service, it **fails loud** — pass `--service` to set it:

```bash
de slo generate --service orders.v1.OrderService
```

Other overrides: `--openapi <path>` (default: the single `openapi/*.openapi.yaml`)
and `--out <path>` (default: `slo.yaml`; `-` for stdout).

{{< callout type="info" >}}
Regenerate after you add custom methods beyond the standard CRUD — the grouping
picks them up. `make slo` runs `de slo generate` for you.
{{< /callout >}}

## 2. Lint

```bash
de slo lint slo.yaml
```

The classifier enforces the three-layer separation and **rejects** a category
error (a Layer-0 saturation signal — cpu/memory/queue-depth — declared as an SLI,
or an SLO with no error-budget policy) with an error-severity finding that exits
non-zero. An un-calibrated default is a **warning**, not an error, so the freshly
generated file lints clean. Use `--format json` for machine-readable output.

## 3. Calibrate against a baseline

Un-calibrated targets must not page anyone. Point `de slo check` at your
Prometheus/Cortex API to read each SLO's **current** SLI and error-budget
consumption over its window:

```bash
de slo check --prometheus-url http://localhost:9009/prometheus
```

The URL is taken from `--prometheus-url`, else `$PROMETHEUS_URL` / `$CORTEX_URL`,
else `spec.monitoring.prometheusUrl` in `devedge.yaml`. Cortex serves the query
API under its Prometheus path prefix (e.g. `/prometheus` or `/api/prom`) — use
that base URL. The queries are service-scoped by the same `rpc_service` FQN the
SLIs carry, so `check` measures exactly what the SLOs define.

Set each objective from the reported current ratio (a hair below it), drop the
`devedge.io/uncalibrated` marker, and re-run `de slo lint`.

## 4. Render and deploy

Project the doc to your backend:

```bash
# Cortex-ruler PrometheusRule (recording rules + burn-rate alerts)
de slo render --target prometheus --in slo.yaml --out deploy/prometheus

# Grafana SLO dashboard
de slo render --target grafana --in slo.yaml --out deploy/grafana

# Loki LogQL rules (for log-derived SLIs)
de slo render --target loki --in slo.yaml --out deploy/loki
```

With no `--out` the artifacts go to stdout. The open-core emitters target vanilla
Cortex + Grafana.

### Internal Grafana-Operator overlay

`--preset-dir <dir>` renders from `<dir>/<target>.tmpl` instead of the built-in
emitter, when that template exists. This is how the **internal** Grafana-Operator
overlay is consumed — point it at a checkout of the overlay's preset directory
(`Infoblox-CTO/devedge-sdk-internal/slo/preset`):

```bash
de slo render --target grafana \
  --preset-dir ../devedge-sdk-internal/slo/preset \
  --in slo.yaml --out deploy/grafana
```

A missing preset falls back to the built-in emitter, so the same command works
with or without the internal overlay on hand.

## Reference

`de slo kpis` prints the Layer-0 API KPI reference (the four golden signals, RED
per method, USE for resources, in OTel semantic-convention terms) — the
always-on signals that diagnose but never page alone. The full command surface is
in the [`de slo` CLI reference](../reference/cli/slo/).
