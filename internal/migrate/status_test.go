package migrate

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestWithConnTimeouts_setsOptions(t *testing.T) {
	got, err := WithConnTimeouts("postgres://u:p@h:5432/db?sslmode=disable", 2*time.Second, 60*time.Second)
	if err != nil {
		t.Fatalf("WithConnTimeouts: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse result: %v", err)
	}
	// The options value must survive URL round-trip with spaces intact.
	opts := u.Query().Get("options")
	if !strings.Contains(opts, "-c lock_timeout=2000") {
		t.Errorf("options missing lock_timeout=2000: %q", opts)
	}
	if !strings.Contains(opts, "-c statement_timeout=60000") {
		t.Errorf("options missing statement_timeout=60000: %q", opts)
	}
	// sslmode must be preserved.
	if u.Query().Get("sslmode") != "disable" {
		t.Errorf("sslmode not preserved: %q", u.Query().Get("sslmode"))
	}
}

func TestWithConnTimeouts_preservesExistingOptions(t *testing.T) {
	got, err := WithConnTimeouts("postgres://h/db?options=-c%20search_path%3Dfoo", 1*time.Second, 0)
	if err != nil {
		t.Fatalf("WithConnTimeouts: %v", err)
	}
	u, _ := url.Parse(got)
	opts := u.Query().Get("options")
	if !strings.Contains(opts, "search_path=foo") {
		t.Errorf("existing options dropped: %q", opts)
	}
	if !strings.Contains(opts, "lock_timeout=1000") {
		t.Errorf("lock_timeout not appended: %q", opts)
	}
	if strings.Contains(opts, "statement_timeout") {
		t.Errorf("statement_timeout should be omitted (0 duration): %q", opts)
	}
}

func TestWithConnTimeouts_zeroIsNoop(t *testing.T) {
	in := "postgres://h/db"
	got, err := WithConnTimeouts(in, 0, 0)
	if err != nil {
		t.Fatalf("WithConnTimeouts: %v", err)
	}
	if got != in {
		t.Errorf("expected unchanged DSN, got %q", got)
	}
}
