// Package types defines the shared domain types for devedge.
package types

import "time"

// Protocol identifies the routing protocol for a route.
type Protocol string

const (
	// ProtocolHTTP is the default — HTTP/HTTPS with Host header routing.
	ProtocolHTTP Protocol = "http"

	// ProtocolTCP is for non-HTTP endpoints (databases, gRPC, binary
	// protocols). Uses SNI-based TLS termination on the frontend and
	// raw TCP forwarding to the backend.
	ProtocolTCP Protocol = "tcp"

	// EdgeIP is the dedicated loopback address for devedge's Traefik proxy.
	// Using a separate address (not 127.0.0.1) avoids port conflicts with
	// other local services. All devedge hostnames resolve to this address.
	EdgeIP = "127.0.0.2"
)

// Route represents a registered hostname-to-upstream mapping with
// lease-based lifecycle management.
type Route struct {
	// Host is the FQDN being registered (e.g. "api.foo.dev.test").
	Host string `json:"host"`

	// Path is an optional URL path prefix that further distinguishes routes
	// sharing a Host. Empty matches any path — the host's catch-all. When set
	// (e.g. "/api"), the route only matches requests whose path is under the
	// prefix, selected by longest-prefix match. This lets one host fan out to
	// several upstreams (e.g. "/" → shell, "/api" → backend).
	Path string `json:"path,omitempty"`

	// StripPrefix, when true, removes Path from the request path before
	// forwarding to Upstream — needed when a backend gateway serves "/v1/..."
	// behind an "/api" prefix. Ignored when Path is empty.
	StripPrefix bool `json:"strip_prefix,omitempty"`

	// Upstream is the backend address to forward to.
	// For HTTP: "http://127.0.0.1:3000"
	// For TCP:  "127.0.0.1:5432" (host:port, no scheme)
	Upstream string `json:"upstream"`

	// Protocol is "http" (default) or "tcp".
	Protocol Protocol `json:"protocol,omitempty"`

	// BackendTLS enables TLS on the connection to the upstream.
	// When false (default), the proxy terminates TLS and forwards
	// plaintext to the backend. When true, the proxy re-encrypts
	// traffic to the backend (TLS-to-TLS).
	BackendTLS bool `json:"backend_tls,omitempty"`

	// Project groups routes for bulk lifecycle operations.
	Project string `json:"project,omitempty"`

	// Tile is optional launchpad-presentation metadata (WS-026). When set, the
	// dev IdP launchpad renders this app as an Okta-style tile, and
	// `de idp clients sync` reads it when synthesizing the app's client entry.
	// Nil for routes that opt out — which is every route by default, so existing
	// routes parse and behave identically.
	Tile *Tile `json:"tile,omitempty"`

	// Source identifies how the route was registered (cli, project-file,
	// docker, k3d-adapter).
	Source string `json:"source,omitempty"`

	// Owner is an opaque identifier for the entity holding the lease.
	Owner string `json:"owner,omitempty"`

	// TTL is the lease duration. A zero value means no automatic expiry.
	TTL time.Duration `json:"ttl,omitempty"`

	// CreatedAt is when the route was first registered.
	CreatedAt time.Time `json:"created_at"`

	// RenewedAt is when the lease was last renewed.
	RenewedAt time.Time `json:"renewed_at"`
}

// Tile is optional launchpad-presentation metadata for an app (WS-026). When an
// app declares it, the dev IdP launchpad renders the app as an Okta-style tile.
// Every field is optional; a route or shell with no Tile behaves exactly as
// before. The JSON tags carry it over the daemon API (/v1/routes); the YAML tags
// let an app declare it in devedge.yaml or a kind:Shell document.
type Tile struct {
	// DisplayName is the tile's human label. When empty, `de idp clients sync`
	// falls back to a title-cased app name.
	DisplayName string `json:"display_name,omitempty" yaml:"displayName,omitempty"`
	// Description is an optional short blurb shown under the tile.
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	// IconURL is an optional icon shown on the tile.
	IconURL string `json:"icon_url,omitempty" yaml:"iconURL,omitempty"`
	// LaunchURL is where the tile opens. When empty, it defaults to the app's
	// root (https://<host>/).
	LaunchURL string `json:"launch_url,omitempty" yaml:"launchURL,omitempty"`
}

// EffectiveProtocol returns the route's protocol, defaulting to HTTP.
func (r Route) EffectiveProtocol() Protocol {
	if r.Protocol == ProtocolTCP {
		return ProtocolTCP
	}
	return ProtocolHTTP
}

// IsExpired reports whether the route's lease has elapsed.
func (r Route) IsExpired(now time.Time) bool {
	if r.TTL <= 0 {
		return false
	}
	return now.After(r.RenewedAt.Add(r.TTL))
}
