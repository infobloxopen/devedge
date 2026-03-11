package version

import (
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	s := String()
	if !strings.HasPrefix(s, "devedge ") {
		t.Fatalf("expected prefix 'devedge ', got %q", s)
	}
}
