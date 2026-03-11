package types

import (
	"testing"
	"time"
)

func TestRoute_IsExpired(t *testing.T) {
	now := time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		route   Route
		want    bool
	}{
		{
			name: "zero TTL never expires",
			route: Route{
				RenewedAt: now.Add(-time.Hour),
				TTL:       0,
			},
			want: false,
		},
		{
			name: "within TTL is not expired",
			route: Route{
				RenewedAt: now.Add(-10 * time.Second),
				TTL:       30 * time.Second,
			},
			want: false,
		},
		{
			name: "past TTL is expired",
			route: Route{
				RenewedAt: now.Add(-60 * time.Second),
				TTL:       30 * time.Second,
			},
			want: true,
		},
		{
			name: "exactly at TTL boundary is not expired",
			route: Route{
				RenewedAt: now.Add(-30 * time.Second),
				TTL:       30 * time.Second,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.route.IsExpired(now)
			if got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}
