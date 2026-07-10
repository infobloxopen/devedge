# uFE scaffold presets

A **preset** is an overlay applied on top of the base `de ufe new` scaffold.
After the base is written, the preset renders each of its files and writes them
into the generated project, **overriding** any base file at the same path (it
never removes base files).

There are two ways to apply a preset:

- `de ufe new <name> --preset <builtin>` — a **built-in** preset embedded in the
  CLI. The public `devedge` repo ships **none** (only this contract README), so
  any `--preset <name>` here fails with a clear error pointing at `--preset-dir`.
- `de ufe new <name> --preset-dir <path>` — an **out-of-tree** preset directory
  on disk holding a canonical `preset.json` (below). This is the downstream
  extension point: how a consumer overlays their own preset.

## Canonical `preset.json` schema

A preset directory contains a `preset.json` manifest plus the source template
files it references. The manifest shape is exactly:

```json
{
  "name": "string",
  "description": "string",
  "files": [
    { "path": "target/path/in/project", "template": "source/file/in/preset/dir" }
  ]
}
```

- `name` (required) — the preset identifier.
- `description` (required in prose; may be empty) — a one-line human summary.
- `files` (required, non-empty) — the overlay entries. For each entry:
  - `template` — a path, **relative to the preset directory**, to the source
    file. It is read and rendered as a Go `text/template` against the SAME
    template data as the base scaffold (`.AppID`, `.Name`, `.TitleName`,
    `.Package`, `.DevPort`, `.Versions`). An unknown field fails loud
    (`missingkey=error`).
  - `path` — a path, **relative to the generated project root**, where the
    rendered file is written, overriding any base file already there.
- `path` and `template` must be relative and must not escape their roots
  (no leading `/`, no `..`). The `__name__` path placeholder is substituted in
  `path` exactly as in the base tree.

A missing or malformed `preset.json` — bad JSON, missing `name`, empty `files`,
or an entry missing `path`/`template` — fails loud with a clear message naming
the offending field.

## The public CLI ships NO built-in preset

The public `devedge` repo intentionally contains **zero** bundled preset
content. A preset is a **downstream extension point** — a deploy chart, a
design-system binding, an OIDC/session config, and so on — that a consumer
supplies out-of-tree and overlays with `--preset-dir`, exactly as `opaauthz`
binds privately onto the public `authz.Authorizer` seam. Apply it with:

```sh
de ufe new <name> --preset-dir <path-to-your-preset>
```

That directory must ship a `preset.json` conforming to the canonical schema
above.
