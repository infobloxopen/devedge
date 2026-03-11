package client

import (
	"context"
	"log/slog"
	"time"

	"github.com/infobloxopen/devedge/internal/daemon"
)

// Heartbeat periodically re-registers routes to keep leases alive.
// It blocks until the context is cancelled.
func (c *Client) Heartbeat(ctx context.Context, routes []daemon.RegisterRequest, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, r := range routes {
				if err := c.Register(ctx, r); err != nil {
					logger.Warn("heartbeat failed", "host", r.Host, "err", err)
				}
			}
			logger.Debug("heartbeat sent", "routes", len(routes))
		}
	}
}
