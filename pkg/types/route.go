// Package types defines the shared domain types for devedge.
package types

import "time"

// Route represents a registered hostname-to-upstream mapping with
// lease-based lifecycle management.
type Route struct {
	// Host is the FQDN being registered (e.g. "api.foo.dev.test").
	Host string `json:"host"`

	// Upstream is the backend URL to forward to (e.g. "http://127.0.0.1:3000").
	Upstream string `json:"upstream"`

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

// IsExpired reports whether the route's lease has elapsed.
func (r Route) IsExpired(now time.Time) bool {
	if r.TTL <= 0 {
		return false
	}
	return now.After(r.RenewedAt.Add(r.TTL))
}
