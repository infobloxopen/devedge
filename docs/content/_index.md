---
title: devedge
layout: hextra-home
---

{{< hextra/hero-badge >}}
  <div class="hx:w-2 hx:h-2 hx:rounded-full hx:bg-primary-400"></div>
  <span>Early — APIs will change</span>
  {{< icon name="arrow-circle-right" attributes="height=14" >}}
{{< /hextra/hero-badge >}}

<div class="hx:mt-6 hx:mb-6">
{{< hextra/hero-headline >}}
  One CLI for a service&nbsp;<br class="hx:sm:block hx:hidden" />and its micro-frontend
{{< /hextra/hero-headline >}}
</div>

<div class="hx:mb-12">
{{< hextra/hero-subtitle >}}
  devedge scaffolds, routes, composes, and deploys a Go backend and its Angular&nbsp;<br class="hx:sm:block hx:hidden" />micro-frontend from one command line — over stable local HTTPS hostnames.
{{< /hextra/hero-subtitle >}}
</div>

<div class="hx:mb-6">
{{< hextra/hero-button text="Ship a full-stack feature" link="docs/tutorial/ship-a-full-stack-feature/" >}}
</div>

<div class="hx:mt-6"></div>

{{< hextra/feature-grid >}}
  {{< hextra/feature-card
    title="Scaffold both halves"
    subtitle="de new service scaffolds a Go service on devedge-sdk; de ufe new scaffolds an Angular micro-frontend on devedge-ufe-sdk. One developer builds the whole feature."
    icon="cog"
  >}}
  {{< hextra/feature-card
    title="Stable local hostnames"
    subtitle="Every app, container, and k3d cluster gets an HTTPS hostname on one shared 80/443 entry point. Register routes dynamically; the daemon keeps them alive."
    icon="server"
  >}}
  {{< hextra/feature-card
    title="Publish the API"
    subtitle="de api publish sends a service's OpenAPI v3 to the apx catalog. Generate a typed Angular client from the spec and host it in the micro-frontend."
    icon="key"
  >}}
  {{< hextra/feature-card
    title="Compose into a suite"
    subtitle="de compose builds several service modules into one host binary — static composition, no plugins. The same modules run standalone or composed."
    icon="puzzle"
  >}}
  {{< hextra/feature-card
    title="Deploy across cells"
    subtitle="de cell deploys version-pinned cells per tenant subset and moves tenants safely between them, with a storage fence and outbox epochs."
    icon="shield-check"
  >}}
  {{< hextra/feature-card
    title="Two SDKs, one story"
    subtitle="The Go framework (devedge-sdk) and the micro-frontend SDK (devedge-ufe-sdk) each have their own reference docs. This portal connects them into one journey."
    icon="lock-closed"
  >}}
{{< /hextra/feature-grid >}}
