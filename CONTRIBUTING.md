# Contributing to devedge

## Conventions

### Config key naming: camelCase for manifests, snake_case elsewhere

devedge uses two config-key conventions, chosen by **document type** (not author preference).
Match the convention of the surface you are editing:

- **camelCase** in Kubernetes-style manifest documents — anything with
  `apiVersion`/`kind`/`metadata`/`spec` (`devedge.yaml`, `kind:Shell`). These mirror Kubernetes,
  where `apiVersion` and `kind` are fixed keys; the rest follows: `stripPrefix`, `backendTLS`,
  `shellUpstream`, and a route tile's `displayName`/`launchURL`/`iconURL`.
- **snake_case** everywhere else — the daemon JSON API (`/v1/routes`: `strip_prefix`,
  `backend_tls`) and the flat, hand-edited data files (`idp-clients.yaml`: `client_id`,
  `redirect_uris`, `launch_url`). This matches the Go `json` struct tags and the OAuth2/OIDC spec
  (`client_id`, `redirect_uris` are snake_case by definition).

A struct serialized to both surfaces carries both tags, e.g.
`json:"display_name" yaml:"displayName"`. The same concept can therefore appear in two cases in two
files — a launchpad tile is `displayName` in `devedge.yaml` but `name`/`launch_url` in
`idp-clients.yaml`; `de idp clients sync` translates between them, and each file stays consistent
with its own convention. Pick the case from the file type; don't "fix" one file to match the other.

The canonical statement of this rule lives in the devedge-sdk style guide:
<https://github.com/infobloxopen/devedge-sdk/blob/main/docs/STYLE-GUIDE.md> ("Configuration key
naming").
