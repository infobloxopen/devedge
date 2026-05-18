// Package dnsserver answers DNS queries for hostnames inside the set of
// configured authoritative suffixes (see data-model.md and the protocol
// contract in specs/001-fix-dns-udp-bind/contracts/dns-protocol.md).
package dnsserver

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ConfiguredSuffix is a DNS suffix the daemon answers for.
//
// Name is always the canonical form: lowercased, no leading or trailing
// dot. Use NewConfiguredSuffix to construct one; it validates the syntax
// and canonicalizes the input.
type ConfiguredSuffix struct {
	Name string
}

// NewConfiguredSuffix canonicalizes and validates a suffix string.
// Empty or syntactically-invalid names return an error.
func NewConfiguredSuffix(name string) (ConfiguredSuffix, error) {
	canon, err := canonicalizeName(name)
	if err != nil {
		return ConfiguredSuffix{}, err
	}
	return ConfiguredSuffix{Name: canon}, nil
}

func canonicalizeName(name string) (string, error) {
	s := strings.TrimSpace(name)
	s = strings.TrimSuffix(s, ".")
	s = strings.ToLower(s)
	if s == "" {
		return "", fmt.Errorf("dns suffix is empty")
	}
	if len(s) > 253 {
		return "", fmt.Errorf("dns suffix %q exceeds 253 octets", name)
	}
	labels := strings.Split(s, ".")
	for _, lbl := range labels {
		if !isValidLabel(lbl) {
			return "", fmt.Errorf("dns suffix %q has invalid label %q", name, lbl)
		}
	}
	return s, nil
}

func isValidLabel(lbl string) bool {
	if lbl == "" || len(lbl) > 63 {
		return false
	}
	if lbl[0] == '-' || lbl[len(lbl)-1] == '-' {
		return false
	}
	for i := 0; i < len(lbl); i++ {
		c := lbl[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-':
		default:
			return false
		}
	}
	return true
}

// AuthoritativeSet is a thread-safe set of ConfiguredSuffix values that
// the DNS handler consults on every query.
type AuthoritativeSet struct {
	mu   sync.RWMutex
	keys map[string]struct{}
}

// NewAuthoritativeSet returns an empty set ready for use.
func NewAuthoritativeSet() *AuthoritativeSet {
	return &AuthoritativeSet{keys: make(map[string]struct{})}
}

// Replace atomically replaces the set's contents. Returns the suffixes
// added and removed relative to the prior contents for logging by the
// polling loop. Each input suffix is taken as-is (callers must already
// have canonicalized via NewConfiguredSuffix).
func (s *AuthoritativeSet) Replace(next []ConfiguredSuffix) (added, removed []ConfiguredSuffix) {
	want := make(map[string]struct{}, len(next))
	for _, cs := range next {
		want[cs.Name] = struct{}{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for k := range want {
		if _, ok := s.keys[k]; !ok {
			added = append(added, ConfiguredSuffix{Name: k})
		}
	}
	for k := range s.keys {
		if _, ok := want[k]; !ok {
			removed = append(removed, ConfiguredSuffix{Name: k})
		}
	}
	s.keys = want

	sortSuffixes(added)
	sortSuffixes(removed)
	return added, removed
}

// Match returns the longest configured suffix that queryName equals or
// is a subdomain of, and whether any match was found. Matching is
// case-insensitive and tolerates a trailing dot on the query name.
func (s *AuthoritativeSet) Match(queryName string) (ConfiguredSuffix, bool) {
	q := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(queryName)), ".")
	if q == "" {
		return ConfiguredSuffix{}, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var best string
	for k := range s.keys {
		if q == k || strings.HasSuffix(q, "."+k) {
			if len(k) > len(best) {
				best = k
			}
		}
	}
	if best == "" {
		return ConfiguredSuffix{}, false
	}
	return ConfiguredSuffix{Name: best}, true
}

// Snapshot returns a sorted copy of the current set.
func (s *AuthoritativeSet) Snapshot() []ConfiguredSuffix {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ConfiguredSuffix, 0, len(s.keys))
	for k := range s.keys {
		out = append(out, ConfiguredSuffix{Name: k})
	}
	sortSuffixes(out)
	return out
}

func sortSuffixes(in []ConfiguredSuffix) {
	sort.Slice(in, func(i, j int) bool { return in[i].Name < in[j].Name })
}
