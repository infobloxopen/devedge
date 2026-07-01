# uFE scaffold presets

A **preset** is an overlay applied on top of the base `de ufe new` scaffold. It
is a directory under this `presets/` tree containing:

- `preset.json` — a manifest: `{ "name", "description", "files": [...] }`.
- the overlay files listed in `files` (rendered as Go `text/template` when
  suffixed `.tmpl`, using the same template data as the base scaffold).

After the base scaffold is written, `de ufe new <name> --preset <preset>`
renders the overlay and writes each file into the generated project,
**overriding** any base file at the same path (it never removes base files).

## The public CLI ships NO proprietary preset

The public `devedge` repo intentionally contains **zero** Infoblox-specific
content. The `infoblox-cto` preset — the FeatureFlag-CRD Helm chart, the
Infoblox design-system wiring, and the Okta OIDC configuration — is provided
by the private **`Infoblox-CTO/devedge-ufe-sdk-internal`** repo, exactly as
`opaauthz` binds privately onto the public `authz.Authorizer` seam.

`--preset <name>` for an unknown/absent preset fails with a clear error that
names what is available and points at where `infoblox-cto` lives.
