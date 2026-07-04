---
title: de sync
---

> Generated from `de sync --help`. Run `make docs-cli` to refresh.

## `de sync`

```text
Write .devedge/make/devedge.mk — the managed Makefile fragment whose targets
delegate to the 'de' build verbs (generate/build/test/lint/image/migrate-lint).

The fragment carries a "DO NOT EDIT" header and is regenerated idempotently. The
build logic lives in 'de', so the fragment's behavior cannot drift; only the set
of targets changes. Your top-level Makefile stays hand-owned and just reads it:

    -include .devedge/make/devedge.mk
    # project-specific targets below

'de doctor' flags a stale or hand-edited fragment.

Usage:
  de sync [flags]

Flags:
  -C, --dir string   service project directory (default: current directory)
  -h, --help         help for sync
```

