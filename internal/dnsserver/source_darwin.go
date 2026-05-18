//go:build darwin

package dnsserver

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// defaultResolverDir is the macOS per-domain resolver-framework directory.
const defaultResolverDir = "/etc/resolver"

// darwinSuffixSource lists /etc/resolver/ and returns one ConfiguredSuffix
// per file whose name is a syntactically valid DNS name.
type darwinSuffixSource struct {
	dir    string
	logger *slog.Logger
}

// NewDarwinSuffixSource returns a SuffixSource that reads /etc/resolver/.
func NewDarwinSuffixSource(logger *slog.Logger) SuffixSource {
	if logger == nil {
		logger = slog.Default()
	}
	return &darwinSuffixSource{dir: defaultResolverDir, logger: logger}
}

// newDarwinSuffixSourceWithDir is for tests so they can target a tempdir.
func newDarwinSuffixSourceWithDir(dir string, logger *slog.Logger) *darwinSuffixSource {
	if logger == nil {
		logger = slog.Default()
	}
	return &darwinSuffixSource{dir: dir, logger: logger}
}

// NewPlatformSuffixSource returns the platform-default SuffixSource.
// On darwin this reads /etc/resolver/.
func NewPlatformSuffixSource(logger *slog.Logger) SuffixSource {
	return NewDarwinSuffixSource(logger)
}

func (s *darwinSuffixSource) List(ctx context.Context) ([]ConfiguredSuffix, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []ConfiguredSuffix{}, nil
		}
		return nil, err
	}

	out := make([]ConfiguredSuffix, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		// Require a regular file. Resolve symlinks via Stat so a symlink
		// pointing at a regular file still counts.
		info, err := os.Stat(filepath.Join(s.dir, name))
		if err != nil {
			s.logger.Debug("dnsserver.source.stat_failed", "file", name, "err", err)
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		cs, err := NewConfiguredSuffix(name)
		if err != nil {
			s.logger.Debug("dnsserver.source.skipped_invalid_name", "file", name, "err", err)
			continue
		}
		out = append(out, cs)
	}
	return out, nil
}

func (s *darwinSuffixSource) Name() string { return "darwin-etc-resolver" }
