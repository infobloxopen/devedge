---
title: de ci
---

> Generated from `de ci --help`. Run `make docs-cli` to refresh.

## `de ci`

```text
CI helpers for ephemeral, per-run clusters

Usage:
  de ci [command]

Available Commands:
  run         Run a command against a dedicated ephemeral cluster, torn down on exit

Flags:
  -h, --help   help for ci

Use "de ci [command] --help" for more information about a command.
```

### `de ci run`

```text
Create a dedicated, per-run ephemeral cluster (devedge-ci-<runid>), run the
wrapped command with that cluster's context available via the environment
(DEVEDGE_KUBECONTEXT), and tear the cluster down when the command exits — on
success, failure, or interrupt. The wrapped command's exit code is propagated.

The user's global kube context is never changed.

Usage:
  de ci run -- COMMAND [ARGS...] [flags]

Flags:
  -h, --help   help for run
```

