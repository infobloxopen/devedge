# Terraform provider scaffold presets

A **preset** is an overlay applied on top of the base `de terraform new`
scaffold. After the base is written, the preset renders each of its files and
writes them into the generated project, **overriding** any base file at the same
path (it never removes base files).

The public `devedge` repo ships **no** built-in preset (only this contract
README). Presets are applied out-of-tree with `--preset-dir`:

- `de terraform new <name> --preset-dir <path>` — a preset directory on disk
  holding a canonical `preset.json` (below). This is how a proprietary preset
  (a product-specific auth binding, branding, extra files) is applied without any
  proprietary content living in the public repo.

The most common preset target is `internal/provider/provider.go` — the
hand-written seam — replaced to bind a concrete auth flow while leaving the
generated resource code untouched.

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
- `description` — a one-line human summary.
- `files` (required, non-empty) — the overlay entries. For each entry:
  - `template` — a path, **relative to the preset directory**, to the source
    file. It is read and rendered as a Go `text/template` against the SAME
    template data as the base scaffold (`.Name`, `.RepoName`, `.Module`, `.Org`,
    `.EnvPrefix`, `.GoVersion`, `.TitleName`, `.Versions.SDK`,
    `.Versions.Framework`, `.Versions.Validators`). An unknown field fails loud
    (`missingkey=error`).
  - `path` — a path, **relative to the generated project root**, where the
    rendered file is written, overriding any base file at that path. It must be
    relative and stay within the project.

This is the Terraform mirror of the `de cli new` preset seam: mechanism in the
open core, product-specific policy applied privately.
