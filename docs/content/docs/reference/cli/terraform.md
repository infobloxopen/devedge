---
title: de terraform
---

> Generated from `de terraform --help`. Run `make docs-cli` to refresh.

## `de terraform`

```text
Scaffold and extend registry-publishable Terraform providers

Usage:
  de terraform [command]

Aliases:
  terraform, tf

Available Commands:
  add         Generate a Terraform resource and register it in the provider
  new         Scaffold a new registry-publishable Terraform provider

Flags:
  -h, --help   help for terraform

Use "de terraform [command] --help" for more information about a command.
```

### `de terraform add`

```text
Generate a Terraform resource from an enriched OpenAPI v3 spec and
register it in a provider created by 'de terraform new'.

It runs the devedge-terraform-sdk tfgen generator against the provider repo,
writing internal/provider/<resource>_resource*.go and rewriting
internal/provider/resources_gen.go so the provider serves the new resource. The
hand-written internal/provider/provider.go seam is never overwritten. Re-running
for the same resource regenerates it in place.

The provider type name is taken from --provider, else derived from the module's
terraform-provider-<name> suffix.

Examples:
  de terraform add --input widgets.openapi.yaml --resource widget
  de terraform add --input ../svc/openapi/svc.openapi.yaml --resource order --dir ./terraform-provider-toy

Usage:
  de terraform add [flags]

Flags:
      --dir string        the provider repo directory (defaults to .)
  -h, --help              help for add
      --input string      path to the enriched OpenAPI v3 spec (required)
      --provider string   provider type name (defaults to the module's terraform-provider-<name> suffix)
      --resource string   resource (TF name) to add (required)
```

### `de terraform new`

```text
Scaffold a new registry-publishable Terraform provider wired to the
open-core github.com/infobloxopen/devedge-terraform-sdk tfkit runtime.

The generated repo is a terraform-provider-<name> Go module shaped for the
Terraform Registry: a HashiCorp-style GoReleaser config (zip archives +
GPG-signed SHA256SUMS + the registry manifest), a terraform-registry-manifest.json,
and a tag-triggered release workflow. It owns a small hand-written seam
(internal/provider/provider.go composing tfkit) and builds as-is; add resources
afterwards with 'de terraform add'.

Apply an overlay on top of the base scaffold with:
  --preset-dir <path>  a preset directory holding a canonical preset.json
The public devedge repo ships no proprietary preset; a product-specific preset
(a concrete auth binding, branding) is applied with --preset-dir. A
missing/malformed preset.json fails with a clear error.

Examples:
  de terraform new toy
  de terraform new toy --module github.com/acme/terraform-provider-toy
  de terraform new toy --org acme --dir ./providers
  de terraform new toy --preset-dir ../devedge-terraform-sdk-internal/preset/acme

Usage:
  de terraform new NAME [flags]

Flags:
      --dir string          parent directory to create the provider in (defaults to .)
  -h, --help                help for new
      --module string       Go module path for the generated provider (defaults to github.com/<org>/terraform-provider-<name>)
      --org string          Terraform Registry namespace / default module owner (defaults to infobloxopen)
      --preset-dir string   path to a preset directory (with a canonical preset.json) to overlay on top of the base
```

