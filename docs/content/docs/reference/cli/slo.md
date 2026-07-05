---
title: de slo
---

> Generated from `de slo --help`. Run `make docs-cli` to refresh.

## `de slo`

```text
Turn a service's API contract into reliability artifacts (WS-025).

'de slo' orchestrates the devedge-sdk 'slo' seam:

  generate   Derive GOOD default OpenSLO SLOs from the enriched OpenAPI, scoped
             to the service's gRPC FQN (the rpc.service label).
  lint       Validate OpenSLO docs and run the fail-loud three-layer classifier
             (a Layer-0 signal declared as an SLI is rejected).
  render     Project a doc to Prometheus/Cortex rules, a Grafana dashboard, or
             Loki LogQL rules. --preset-dir consumes an internal emitter overlay.
  check      Query a Prometheus/Cortex API for each SLO's current SLI and
             error-budget consumption, to CALIBRATE the un-calibrated defaults.
  kpis       Print the Layer-0 API KPI reference (golden signals + RED + USE).

The scaffold already ships a GOOD default slo.yaml; regenerate after adding
custom methods, calibrate with 'de slo check', then render to deploy the
burn-rate rules. See the define-slo skill for authoring guidance.

Usage:
  de slo [command]

Available Commands:
  check       Query Prometheus/Cortex for each SLO's current SLI vs its target
  generate    Derive default OpenSLO SLOs from the service's enriched OpenAPI
  kpis        Print the Layer-0 API KPI reference (golden signals + RED + USE)
  lint        Validate OpenSLO docs and run the fail-loud three-layer classifier
  render      Project an OpenSLO doc to prometheus|grafana|loki artifacts

Flags:
  -h, --help   help for slo

Use "de slo [command] --help" for more information about a command.
```

### `de slo check`

```text
Query a Prometheus/Cortex-compatible HTTP API (/api/v1/query) for each SLO's
CURRENT SLI ratio over its window and its error-budget consumption, so you can
CALIBRATE the un-calibrated default targets against a measured baseline.

The Prometheus/Cortex base URL is taken from --prometheus-url, else $PROMETHEUS_URL
or $CORTEX_URL, else spec.monitoring.prometheusUrl in devedge.yaml. With none set,
this prints how to point it at Cortex and exits non-zero.

This reads only; it changes nothing. Use the reported "current" ratio as the new
objective (a hair below it) and drop the devedge.io/uncalibrated marker.

Usage:
  de slo check [flags]

Flags:
  -C, --dir string              service project directory (default: current directory)
  -h, --help                    help for check
      --in string               input OpenSLO YAML (default "slo.yaml")
      --prometheus-url string   Prometheus/Cortex base URL (its /api/v1/query is queried)
```

### `de slo generate`

```text
Derive GOOD default OpenSLO SLOs (availability + latency, read/write groups,
a 28d window, burn-rate alerts, a mandatory error-budget policy — all marked
un-calibrated) from the service's enriched OpenAPI, and write slo.yaml.

Run with no flags from a service project: the OpenAPI is located at
openapi/<svc>.openapi.yaml (produced by 'de generate') and the gRPC service FQN
is derived from the project's .proto files.

Right after a scaffold the intermediate OpenAPI is not on disk yet, so this runs
'de generate' first to produce it (pass --no-generate to skip that and fail loud
if it is missing). Give an explicit --openapi to derive from a spec elsewhere.

The FQN (proto package + service name, e.g. orders.v1.OrderService) becomes the
rpc.service label on every derived SLI. The OpenAPI does not carry it, so without
it the SLIs would aggregate across services. If the FQN cannot be determined,
this fails loud — pass --service to set it explicitly.

Usage:
  de slo generate [flags]

Flags:
  -C, --dir string       service project directory (default: current directory)
  -h, --help             help for generate
      --no-generate      do not run 'de generate' when the OpenAPI is missing; fail loud instead
      --openapi string   enriched OpenAPI YAML (default: the single openapi/*.openapi.yaml)
      --out string       output path (- for stdout) (default "slo.yaml")
      --service string   rpc.service label (proto FQN, e.g. orders.v1.OrderService); derived from protos when unset
```

### `de slo kpis`

```text
Print the Layer-0 API KPI reference (golden signals + RED + USE)

Usage:
  de slo kpis [flags]

Flags:
  -h, --help   help for kpis
```

### `de slo lint`

```text
Validate one or more OpenSLO docs and run the WS-025 three-layer classifier.

The classifier REJECTS a category error (e.g. a Layer-0 saturation signal such
as cpu/memory/queue-depth declared as an SLI, or an SLO with no error-budget
policy) with an error-severity finding, and WARNS on an un-calibrated default or
a placeholder error-budget policy. Any error-severity finding exits non-zero;
warnings alone exit 0 so a fresh scaffold's slo.yaml lints green.

Pass --fail-on-warn (alias --strict) to exit non-zero on ANY finding, including
warnings — a production CI gate that refuses to promote un-calibrated SLOs or a
placeholder error-budget policy.

With no file argument it lints slo.yaml in the current directory.

Usage:
  de slo lint [files...] [flags]

Flags:
      --fail-on-warn    exit non-zero on ANY finding, including warnings (a strict production CI gate)
      --format string   output format: text|json (default "text")
  -h, --help            help for lint
      --strict          alias for --fail-on-warn
```

### `de slo render`

```text
Project an OpenSLO doc to a monitoring backend and write the artifacts:

  --target prometheus   a Cortex-ruler PrometheusRule (SLI recording rules +
                        multi-window multi-burn-rate alerts)
  --target grafana      an SLO overview dashboard
  --target loki         LogQL recording rules for log-derived SLIs

--preset-dir <dir> renders from <dir>/<target>.tmpl instead of the built-in
open-core emitter, when that template exists. This is the seam the INTERNAL
Grafana-Operator overlay uses: point it at the overlay's preset directory
(e.g. a checkout of Infoblox-CTO/devedge-sdk-internal/slo/preset) to emit the
operator-flavored artifacts, e.g.

    de slo render --target grafana --preset-dir ../devedge-sdk-internal/slo/preset

With no --out the artifacts are written to stdout.

Usage:
  de slo render [flags]

Flags:
  -h, --help                help for render
      --in string           input OpenSLO YAML (default "slo.yaml")
      --out string          output directory (- or empty for stdout)
      --preset-dir string   directory of <target>.tmpl emitter overrides (internal overlay seam)
      --target string       prometheus|grafana|loki
```

