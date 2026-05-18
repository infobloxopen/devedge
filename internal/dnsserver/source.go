package dnsserver

import (
	"context"
	"sync"
	"time"
)

const (
	// pollInterval is how often the DNS server re-reads the SuffixSource.
	pollInterval = 5 * time.Second
	// pollTimeout bounds a single SuffixSource.List call.
	pollTimeout = 2 * time.Second
)

// SuffixSource enumerates the set of DNS suffixes the daemon should be
// authoritative for. The platform-specific implementation is selected at
// compile time via build tags.
type SuffixSource interface {
	// List returns the currently configured suffixes. The returned slice
	// is safe for the caller to retain — the source MUST NOT mutate it
	// after returning. Ordering is not significant.
	//
	// Errors are considered transient: the polling loop logs them and
	// keeps the prior in-memory set.
	List(ctx context.Context) ([]ConfiguredSuffix, error)

	// Name returns a short identifier used in log messages. Callers
	// MUST NOT rely on its value beyond debug output.
	Name() string
}

// staticSuffixSource is a test-only SuffixSource that returns a
// configured list. It is safe for concurrent use.
type staticSuffixSource struct {
	mu       sync.Mutex
	suffixes []ConfiguredSuffix
}

// NewStaticSuffixSource constructs a staticSuffixSource. Each input
// suffix is canonicalized; invalid inputs cause a panic. Intended for
// tests and integration harnesses.
func NewStaticSuffixSource(names ...string) *staticSuffixSource {
	s := &staticSuffixSource{}
	s.mustSet(names)
	return s
}

// Set replaces the source's contents. Invalid names panic.
func (s *staticSuffixSource) Set(names []string) {
	s.mustSet(names)
}

func (s *staticSuffixSource) mustSet(names []string) {
	out := make([]ConfiguredSuffix, 0, len(names))
	for _, n := range names {
		cs, err := NewConfiguredSuffix(n)
		if err != nil {
			panic("staticSuffixSource: " + err.Error())
		}
		out = append(out, cs)
	}
	s.mu.Lock()
	s.suffixes = out
	s.mu.Unlock()
}

func (s *staticSuffixSource) List(ctx context.Context) ([]ConfiguredSuffix, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ConfiguredSuffix, len(s.suffixes))
	copy(out, s.suffixes)
	return out, nil
}

func (s *staticSuffixSource) Name() string { return "static" }
