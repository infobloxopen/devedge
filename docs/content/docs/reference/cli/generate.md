---
title: de generate
---

> Generated from `de generate --help`. Run `make docs-cli` to refresh.

## `de generate`

```text
Generate code from the service's protos using the PINNED buf CLI and codegen
plugins (see 'de version' toolchain pins), then run 'go mod tidy'.

Hermetic: buf and the protoc-gen-* plugins are pinned by the 'de' binary and run
via 'go run'/'go install' — not resolved off the host PATH — so the same 'de'
version always generates with the same tools. Requires a buf config
(buf.gen.yaml) in the project directory.

Usage:
  de generate [flags]

Flags:
  -C, --dir string   service project directory (default: current directory)
  -h, --help         help for generate
```

