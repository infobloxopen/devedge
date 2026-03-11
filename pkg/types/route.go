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
)

// Route represents a registered hostname-to-upstream mapping with
// lease-based lifecycle management.
type Route struct {
	// Host is the FQDN being registered (e.g. "api.foo.dev.test").
	Host string `json:"host"`

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
