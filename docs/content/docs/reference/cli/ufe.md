---
title: de ufe
---

> Generated from `de ufe --help`. Run `make docs-cli` to refresh.

## `de ufe`

```text
Scaffold and manage devedge micro-frontends (uFEs)

Usage:
  de ufe [command]

Available Commands:
  new         Scaffold a new Angular + single-spa micro-frontend

Flags:
  -h, --help   help for ufe

Use "de ufe [command] --help" for more information about a command.
```

### `de ufe new`

```text
Scaffold a new Angular-15 + single-spa micro-frontend wired to the
open-core @infobloxopen/devedge-ufe-* SDK.

The generated uFE is correct on first run: its default nav group validates
against a dev GroupRegistry (so it renders, not silently drops), its app route
matches the manifest, the session is provided into Angular DI, and HTTP calls
carry the Bearer token. It ships no Angular-2-era deadweight and no committed
lockfile.

Roster wiring (WS-018): after scaffolding, the new uFE is also registered into a
'kind: Shell' roster so a shell picks it up — the same one-line addition as
'de compose add'. The entry is {id: <name>, route: <route>, upstream:
http://127.0.0.1:<dev-port>}, upserted by id (an existing same-id entry is
updated in place, never duplicated). If --shell names a file that does not exist,
a sensible default shell is created containing just this uFE. Pass --shell "" to
skip roster wiring entirely (scaffold only).

Apply an overlay on top of the base scaffold with either:
  --preset <name>      a built-in preset (the public CLI ships none)
  --preset-dir <path>  a preset directory holding a canonical preset.json
The public CLI ships no proprietary preset; the 'infoblox-cto' preset is
provided by the private Infoblox-CTO/devedge-ufe-sdk-internal repo — apply it
with --preset-dir <repo>/preset/infoblox-cto. An unknown built-in preset or a
missing/malformed preset.json fails with a clear error.

Examples:
  de ufe new discovery
  de ufe new widgets --dir ./frontends
  de ufe new tags --shell notesapp-shell.yaml --route tags --dev-port 4202
  de ufe new widgets --shell ""   # scaffold only, no roster wiring
  de ufe new widgets --preset-dir ../devedge-ufe-sdk-internal/preset/infoblox-cto

Usage:
  de ufe new NAME [flags]

Flags:
      --dev-port int        uFE dev-server port for its shell-roster upstream (default 4201)
      --dir string          parent directory to create the uFE in (defaults to .)
  -h, --help                help for new
      --preset string       built-in overlay preset to apply on top of the base (the public CLI ships none)
      --preset-dir string   path to a preset directory (with a canonical preset.json) to overlay on top of the base — e.g. the private infoblox-cto preset
      --route string        hash route the uFE mounts at + CDN path segment (defaults to NAME)
      --shell string        shell config to register the uFE into (created if absent); pass "" to skip roster wiring (default "shell.yaml")
```

