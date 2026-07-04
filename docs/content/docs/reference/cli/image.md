---
title: de image
---

> Generated from `de image --help`. Run `make docs-cli` to refresh.

## `de image`

```text
Build a container image for the service with the PINNED ko:

  - distroless-static base image (nonroot),
  - reproducible Go build (GOFLAGS=-trimpath),
  - multi-arch (linux/amd64,linux/arm64 by default).

By default the image is built to the local Docker daemon (--repo ko.local,
--push=false). Set --repo to a registry and --push to publish.

The build target defaults to './...' (all main packages), which suits a registry
push. For a local single-image build, pass the service main explicitly after a
'--' separator, e.g.:

    de image --push=false -- ./cmd/myservice

Any tokens after '--' are forwarded verbatim to ko.

Usage:
  de image [-- KO_ARGS...] [flags]

Flags:
      --base string       base image (distroless-static by default) (default "gcr.io/distroless/static:nonroot")
  -C, --dir string        service project directory (default: current directory)
  -h, --help              help for image
      --platform string   target platforms (default "linux/amd64,linux/arm64")
      --push              push the image (default: build to the local Docker daemon)
      --repo string       image repository (KO_DOCKER_REPO) (default "ko.local")
```

