---
title: de build
---

> Generated from `de build --help`. Run `make docs-cli` to refresh.

## `de build`

```text
Compile every package in the service with 'go build -trimpath ./...'.

'-trimpath' is applied so the build is reproducible — the same source yields a
byte-identical binary regardless of the checkout path.

Usage:
  de build [flags]

Flags:
  -C, --dir string   service project directory (default: current directory)
  -h, --help         help for build
```

