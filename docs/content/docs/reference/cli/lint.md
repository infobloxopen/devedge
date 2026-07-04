---
title: de lint
---

> Generated from `de lint --help`. Run `make docs-cli` to refresh.

## `de lint`

```text
Lint the service. If the project has a golangci-lint config
(.golangci.yml/.yaml/.toml/.json) the PINNED golangci-lint is run via 'go run';
otherwise it falls back to 'go vet ./...'. The pinned linter means the same 'de'
version lints with the same rules on every host.

Usage:
  de lint [flags]

Flags:
  -C, --dir string   service project directory (default: current directory)
  -h, --help         help for lint
```

