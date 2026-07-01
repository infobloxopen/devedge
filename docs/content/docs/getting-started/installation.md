---
title: Installation
weight: 10
---

Install the `de` CLI, then start the daemon that runs the local edge.

## Install the CLI

With Homebrew:

```bash
brew tap infobloxopen/tap
brew install --cask infobloxopen/tap/devedge
```

Or build from source:

```bash
make build    # binaries in ./bin/
```

## Start the edge

```bash
de install    # install the daemon, the mkcert CA, and DNS config
de start      # start the background daemon
de doctor     # verify everything is healthy
```

`de install` adds a local certificate authority so that `*.dev.test` hostnames
serve trusted HTTPS, and it configures DNS to resolve those hostnames to the
local edge. `de doctor` reports the state of each component and points at the
fix for anything that is not ready.

## Next

- [Ship a full-stack feature](../tutorial/ship-a-full-stack-feature/) — build a
  service and a micro-frontend end to end.
- [CLI reference](../reference/cli/) — every `de` command.
