---
title: de idp
---

> Generated from `de idp --help`. Run `make docs-cli` to refresh.

## `de idp`

```text
Manage the dev identity-provider launchpad (WS-026).

The dev IdP is a passwordless, Okta-style login + app-tile launchpad. It is a
separate application (github.com/infobloxopen/devedge-idp); this verb group is the
discovery/registration substrate around it:

  de idp clients sync   discover registered apps and write idp-clients.json
  de idp up             route the IdP through the edge at idp.dev.test
  de idp new            guidance for standing up the reference IdP app

The launchpad shows one tile per registered devedge app, discovered from the
edge route registry + kind:Shell rosters. An app declares how it appears by
adding optional 'tile' metadata to its route (devedge.yaml) or kind:Shell.

Usage:
  de idp [command]

Available Commands:
  clients     Manage the dev IdP's OAuth2 clients
  new         Guidance for standing up the dev IdP (see devedge-idp)
  up          Route the dev IdP through the edge at idp.dev.test

Flags:
  -h, --help   help for idp

Use "de idp [command] --help" for more information about a command.
```

### `de idp clients`

```text
Manage the dev IdP's OAuth2 clients

Usage:
  de idp clients [command]

Available Commands:
  sync        Discover devedge apps and write the IdP clients file

Flags:
  -h, --help   help for clients

Use "de idp clients [command] --help" for more information about a command.
```

#### `de idp clients sync`

```text
Discover registered devedge apps and write the IdP clients file.

Apps are discovered from the `de` daemon's route registry (GET /v1/routes)
plus, if present, a local devedge.yaml and/or kind:Shell roster in the working
directory. If the daemon is not running, discovery falls back to the local
config alone (and says so). If a source is unavailable, whatever remains is used.

For each app the sync emits one OAuth2 client the IdP reads:

  - client_id     the app name (route project, else the host's first label)
  - client_secret a guessable dev dummy ("dev-secret-<name>")
  - redirect_uris the app's BFF callback ("https://<host>/callback")
  - tile          launchpad presentation: name (the app's tile displayName, or a
                  title-cased app name), description, icon_url, and launch_url
                  ("https://<host>/" unless the tile overrides it)

Only HTTP apps become tiles (TCP routes — databases, etc. — are skipped).

An app declares its tile in devedge.yaml (or a kind:Shell) under the route's
'tile:' block. NOTE the field names are camelCase YAML keys and DIFFER from the
snake_case JSON in the emitted idp-clients.json (this command maps between them):

  spec:
    routes:
      - host: app.dev.test
        path: /api/orders
        upstream: http://127.0.0.1:8080
        tile:
          displayName: Orders          # -> idp-clients.json tile.name
          description: Manage orders    # -> tile.description
          iconURL: https://.../o.svg    # -> tile.icon_url
          launchURL: https://app.dev.test/api/orders/   # -> tile.launch_url

All tile keys are optional. Unrecognized keys are ignored (standard YAML), so a
typo like 'name:' or 'launch_url:' silently falls back to the auto-derived
title/URL — use the exact camelCase keys above.

--out is written as a FULL REPLACE of the current discovery — it is idempotent
(re-running with the same inputs yields a byte-identical file), but it does NOT
merge with clients already in the file. Any app not in THIS discovery is dropped.
So run it with the daemon up (de start) so every registered app is discovered;
running it from one service's directory with no daemon will drop the other apps'
clients/tiles from a shared idp-clients.json.

Examples:
  de idp clients sync
  de idp clients sync --out ../devedge-idp/idp-clients.json
  de idp clients sync --config shell.yaml

Usage:
  de idp clients sync [flags]

Flags:
      --config string   a local devedge.yaml/kind:Shell to include alongside the daemon; defaults to auto-detecting devedge.yaml and shell.yaml in the working dir
  -h, --help            help for sync
      --out string      path to write the IdP clients file (default "idp-clients.json")
```

### `de idp new`

```text
Point at the reference dev IdP application and, optionally, emit starter files.

This command is intentionally thin: it does NOT scaffold a whole OIDC provider.
The reference dev IdP (passwordless picker + Okta-style tile launchpad) is a
complete application in github.com/infobloxopen/devedge-idp; clone and run that. This
CLI's job is discovery/registration around it:

  de idp clients sync   feed the IdP the app clients + tiles to show
  de idp up             route the IdP through the edge at idp.dev.test

With --emit, a starter devedge.yaml (routing idp.dev.test -> :8080) and a
sample idp-clients.json are written into --dir so the file shapes are concrete.

Usage:
  de idp new [flags]

Flags:
      --dir string   directory to write starter files into (with --emit) (default ".")
      --emit         also write a starter devedge.yaml + sample idp-clients.json
  -h, --help         help for new
```

### `de idp up`

```text
Register a route so the dev IdP is served at idp.dev.test through the
local devedge edge.

This is a thin wrapper over the same route registration `de register` uses; it
does NOT build or run the IdP binary — the reference IdP application lives in
github.com/infobloxopen/devedge-idp. Start the IdP there first (default :8080), then route it:

  1. run the IdP:      git clone https://github.com/infobloxopen/devedge-idp && cd devedge-idp && go run ./cmd/idp
  2. sync its clients: de idp clients sync --out ./idp-clients.json
  3. route it:         de idp up

The browser then reaches the IdP at https://idp.dev.test/.

Usage:
  de idp up [flags]

Flags:
  -h, --help              help for up
      --host string       hostname to serve the IdP at (default "idp.dev.test")
      --port int          local IdP HTTP port (builds the default upstream) (default 8080)
      --upstream string   explicit upstream URL (overrides --port)
```

