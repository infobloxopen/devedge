package daemon

import (
	"fmt"
	"time"
)

// parseDuration parses a duration string, supporting both Go-style ("30s")
// and integer-seconds for JSON convenience.
func parseDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	return d, nil
}
