---
title: de start
---

> Generated from `de start --help`. Run `make docs-cli` to refresh.

## `de start`

```text
Start the devedge daemon.

If a daemon of a DIFFERENT build is already running — version skew after a client
upgrade, the cause of silent route mis-registration (#56) — 'de start' replaces it
(stop + start) so the running binary matches this client. Pass --no-replace to
only warn and leave the stale daemon running.

Usage:
  de start [flags]

Flags:
  -h, --help         help for start
      --no-replace   on daemon version skew, only warn; do not stop+start to replace the running daemon
```

