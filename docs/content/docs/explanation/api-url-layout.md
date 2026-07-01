---
title: API URL layout
weight: 30
---

The API URL layout is the shape of the paths a service exposes — the arrangement
of domain, version, and resource in a URL like `/api/ipam/v1/ip-spaces/prod`. It
gives every devedge service a predictable, readable address so a caller can find
a resource without reading the service's proto. Read this page when you scaffold
a service, route one at the edge, or decide how a new API should look.

The layout is a configurable seam, named `apilayout` in the SDK. A layout is a
mechanism, not a policy: the same `(domain, version, resource)` coordinates
render different paths under different named strategies, and each caller — the
service scaffold, the edge shell, this documentation — selects a strategy without
knowing how any other renders one.

## The product-friendly REST default

The default layout is `product-rest`. It renders:

```text
/api/{domain}/v{major}/{resource-plural}/{id}
```

For example, a service in the `ipam` domain that owns IP spaces exposes:

```text
/api/ipam/v1/ip-spaces/prod
```

Two properties make this the default. The path reads as plain English, so a
caller can guess a resource's address from its name. And each domain carries its
own major version, so `ipam` can move to `v2` without disturbing any other
domain on the same host.

## The platform and discovery variant

The `k8s-apis` layout renders the Kubernetes API group/version/resource shape:

```text
/apis/{group}/{version}/{resource}
```

For example:

```text
/apis/ipam.infoblox.com/v1/ip-spaces/prod
```

This layout also expects an `apiVersion` and a `kind` in the request body, the
way a Kubernetes object carries them. It suits a declarative or control-plane
surface, where a client applies desired state and a discovery client enumerates
group-versioned resources.

Choose between the two by who calls the API:

| You are building | Choose | Why |
|------------------|--------|-----|
| A REST API for a product or a customer | `product-rest` | Readable paths; each domain versions independently. |
| A declarative or control-plane surface | `k8s-apis` | Group/version/resource discovery; `apiVersion`/`kind` bodies. |

## Naming rules

A layout assumes the segments below follow the industry and organization
convention. The scaffold and the `apilayout` seam validate them.

| Segment | Rule | Example |
|---------|------|---------|
| Domain (`product-rest`) | Short product domain, lower-kebab | `ipam`, `dns` |
| Group (`k8s-apis`) | Fully qualified, dotted API group | `ipam.infoblox.com` |
| Version | `v` then a major number, optionally a stability suffix | `v1`, `v1beta1`, `v2` |
| Resource | Plural collection name, lower-kebab | `ip-spaces` |
| Kind | Singular resource name, PascalCase, in the request body | `IpSpace` |
| Proto package | The domain's package at its major version | `infoblox.ipam.v1` |

## The version-before-resource rule

Version always precedes the resource. The version describes the contract used to
interpret everything after it, so it belongs before the collection it governs, at
the segment right after the domain or group.

The rejected arrangement puts the version after the resource:

```text
/api/{domain}/{resource}/{version}
```

This reads backward — the reader meets the resource before learning which
version's contract defines it — and it makes a domain's major-version bump vary
per collection instead of moving the whole domain at once. The `apilayout` seam
does not offer this arrangement, and the `make lint-api-paths` check fails a
build whose proto `google.api.http` paths use it.

{{< callout type="warning" >}}
**Put the version segment right after the domain or group, never after a
collection.** A path like `/api/ipam/ip-spaces/v1/...` is the version-after-resource
anti-pattern; write `/api/ipam/v1/ip-spaces/...` instead.
{{< /callout >}}

## How the layout is set

You choose a layout when you scaffold a service and, separately, when you route
services behind a shell. The default is `product-rest` in both places, so most
services need no choice at all.

- **Scaffold a service.** `de new service <name> --api-layout <layout> --domain <domain>`
  selects the layout and the product domain for the new service. The scaffold
  emits a `devedge.yaml` route that fronts the service at the app host under
  `/api/{domain}` and strips that prefix, so the public URL is product-rest while
  the service keeps serving its own `/v{version}/{resource}` paths. Two services
  under different domains share one host without colliding.
- **Route behind a shell.** A `kind: Shell` document sets `spec.api.layout`
  (default `product-rest`). Each backend listed under `spec.api.services` carries
  a `domain`, and the edge routes it at `/api/{domain}` — again with the prefix
  stripped — so several domains coexist on one shell origin.

In both cases the edge composes the `/api/{domain}` prefix and strips it before
forwarding, so a service never encodes the domain in its own paths. The service
serves `/v{version}/{resource}`; the edge makes the public URL product-rest.

## See also

- [Ship a full-stack feature](../../tutorial/ship-a-full-stack-feature/) — scaffold,
  route, and run a service and its micro-frontend end to end.
- [Serve the shell and route micro-frontends](https://infobloxopen.github.io/devedge-ufe-sdk/docs/how-to/serve-the-shell-and-route-microfrontends/) —
  the frontend SDK's how-to for running the shell that fronts the API.
- [How devedge fits together](how-devedge-fits/) — where the edge, the SDK, and
  the shell each own a responsibility.
</content>
</invoke>
