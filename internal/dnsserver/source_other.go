//go:build !darwin

package dnsserver

import (
	"context"
	"log/slog"
)

// noopSuffixSource is the non-darwin SuffixSource. It always returns an
// empty list so the DNS endpoint binds (for parity with macOS) but
// answers REFUSED to every query.
type noopSuffixSource struct{}

// NewNoopSuffixSource constructs the no-op SuffixSource.
func NewNoopSuffixSource() SuffixSource { return noopSuffixSource{} }

// NewPlatformSuffixSource returns the platform-default SuffixSource.
// On non-darwin platforms this is a no-op source.
func NewPlatformSuffixSource(_ *slog.Logger) SuffixSource {
	return NewNoopSuffixSource()
}

func (noopSuffixSource) List(ctx context.Context) ([]ConfiguredSuffix, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []ConfiguredSuffix{}, nil
}

func (noopSuffixSource) Name() string { return "noop" }
