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
  override    Serve a local uFE through the edge and print the import-map override to inject it into a live shell
  shell       Scaffold a runnable single-spa shell from a shell.yaml roster

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

A preset is a downstream extension point: an overlay on top of the base scaffold
that rebinds things like the session provider, design system, and nav. Apply one
with either:
  --preset <name>      a built-in preset (the public CLI ships none)
  --preset-dir <path>  a preset directory holding a canonical preset.json
The public CLI ships no built-in preset — overlay your own with --preset-dir
<path>. An unknown built-in preset or a missing/malformed preset.json fails with
a clear error.

Examples:
  de ufe new discovery
  de ufe new widgets --dir ./frontends
  de ufe new tags --shell notesapp-shell.yaml --route tags --dev-port 4202
  de ufe new widgets --shell ""   # scaffold only, no roster wiring
  de ufe new widgets --preset-dir ./my-preset

Usage:
  de ufe new NAME [flags]

Flags:
      --dev-port int        uFE dev-server port for its shell-roster upstream (default 4201)
      --dir string          parent directory to create the uFE in (defaults to .)
  -h, --help                help for new
      --preset string       built-in overlay preset to apply on top of the base (the public CLI ships none)
      --preset-dir string   path to a preset directory (with a canonical preset.json) to overlay a downstream preset on top of the base (the public CLI ships none)
      --route string        hash route the uFE mounts at + CDN path segment (defaults to NAME)
      --shell string        shell config to register the uFE into (created if absent); pass "" to skip roster wiring (default "shell.yaml")
```

### `de ufe override`

```text
Serve a developer's LOCAL uFE through the devedge edge and print the exact
browser import-map override to inject it into a LIVE hosted shell (e.g. a CSP
env) — the "integrated <env>" run mode.

The live-shell dev loop is pure browser-side import-map-overrides (no proxy): you
open the live shell and point one module specifier at your local bundle, and the
shell cross-origin-fetches it. This command wires that up: it registers an edge
route so your running dev server is served at https://<cdn>/<route>/main.js over
TLS the browser trusts, then prints the override snippet to paste into the live
env's DevTools console.

The local bundle must be the single-spa main.js entry, send
Access-Control-Allow-Origin: *, and be reachable over trusted TLS. A 'de ufe new'
uFE served through the edge satisfies all three (mkcert CA trusted after
'de install'; ACAO:* + allowedHosts:'all' in the scaffold).

NAME is the local uFE (its edge path segment and default module specifier). Use
--module when the target shell knows the uFE by a different specifier, and
--namespace for a namespaced shell (e.g. @acme — the key becomes
import-map-override:@acme/<module>). The override uses the standard
import-map-overrides localStorage key, so any shell that supports it (its UI or a
helper global) picks it up. The uFE dev server must be running (pnpm start).
--dry-run prints the snippet without registering the edge route (no daemon needed).

Examples:
  de ufe override notes --env https://your-shell.example.com
  de ufe override notes --env https://your-shell.example.com --namespace @acme --dev-port 4210 --open
  de ufe override discovery --env https://shell.dev.test --module @acme/discovery --route disco

Usage:
  de ufe override NAME [flags]

Flags:
      --cdn string         edge CDN host that serves the local bundle over trusted TLS (default "cdn.dev.test")
      --dev-port int       local uFE dev-server port to serve through the edge (default 4201)
      --dry-run            print the override snippet without registering the edge route (no daemon needed)
      --env string         live hosted shell URL to inject the override into (REQUIRED), e.g. https://your-shell.example.com
  -h, --help               help for override
      --module string      module specifier the target shell knows the uFE by (defaults to NAME)
      --namespace string   specifier namespace of the target shell, e.g. @acme (empty = bare specifier)
      --open               open the live shell in a browser after wiring the override
      --route string       edge path segment for the local uFE (defaults to NAME)
```

### `de ufe shell`

```text
Scaffold a runnable single-spa SHELL (root-config) from a 'kind: Shell'
roster (shell.yaml, as written by 'de ufe new').

The generated shell is the host: it owns the session ONCE, registers every uFE
in the roster by HASH route, loads each uFE's bundle through the browser's native
importmap, and starts single-spa. It renders locally with a no-auth dev session
(flip environment.useDevSession to exercise real OIDC). This is what lets a
developer render their uFE without copying the example shell.

The shell serves on the port in the roster's shellUpstream, so the served port
matches the edge route 'de project up' creates to the shell host. Build + serve
use npx (esbuild + sirv-cli), so no destructive install of a global toolchain is
needed.

A preset is a downstream extension point: an overlay on top of the base shell
that rebinds things like the session provider, design system, and nav shell.
Apply one with either:
  --preset <name>      a built-in preset (the public CLI ships none)
  --preset-dir <path>  a preset directory holding a canonical preset.json
The public CLI ships no built-in preset — overlay your own with --preset-dir
<path>. An unknown built-in preset or a missing/malformed preset.json fails with
a clear error.

Examples:
  de ufe shell
  de ufe shell --shell notesapp-shell.yaml --name notesapp-shell
  de ufe shell --dir ./frontend
  de ufe shell --preset-dir ./my-shell-preset

Usage:
  de ufe shell [flags]

Flags:
      --dir string          parent directory to create the shell in (defaults to .)
  -h, --help                help for shell
      --name string         shell project dir name (defaults to <roster name>-shell)
      --preset string       built-in overlay preset to apply on top of the base shell (the public CLI ships none)
      --preset-dir string   path to a preset directory (with a canonical preset.json) to overlay a downstream preset on top of the base shell (the public CLI ships none)
      --shell string        shell roster (kind: Shell) to scaffold the shell from (default "shell.yaml")
```

