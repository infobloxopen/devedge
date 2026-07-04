package dsn_test

import (
	"strings"
	"testing"

	"github.com/infobloxopen/devedge/internal/dsn"
)

// TestRedact_URLForm verifies a URL-form DSN's userinfo password is blanked
// but the rest of the DSN (useful for diagnosis) survives (SEC-005).
func TestRedact_URLForm(t *testing.T) {
	got := dsn.Redact("postgres://admin:Hunter2Pw@10.0.0.5:5432/prod")
	if strings.Contains(got, "Hunter2Pw") {
		t.Fatalf("password leaked: %q", got)
	}
	want := "postgres://admin:xxxxx@10.0.0.5:5432/prod"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestRedact_SchemelessURLForm covers the //user:pass@host form (no scheme)
// the SEC-005 PoC used via `de migrate up --dsn`.
func TestRedact_SchemelessURLForm(t *testing.T) {
	got := dsn.Redact("//dbadmin:Hunter2Pw@10.0.0.5:5432/prod")
	if strings.Contains(got, "Hunter2Pw") {
		t.Fatalf("password leaked: %q", got)
	}
	if !strings.Contains(got, "dbadmin") || !strings.Contains(got, "xxxxx") {
		t.Errorf("expected username preserved and password redacted, got %q", got)
	}
}

// TestRedact_KeywordValueForm covers the libpq "host=... password=... " form
// the SEC-005 PoC used via DATABASE_URL.
func TestRedact_KeywordValueForm(t *testing.T) {
	got := dsn.Redact("host=db.internal port=5432 user=admin password=SuperSecret123 dbname=prod sslmode=require")
	if strings.Contains(got, "SuperSecret123") {
		t.Fatalf("password leaked: %q", got)
	}
	if !strings.Contains(got, "host=db.internal") || !strings.Contains(got, "password=xxxxx") {
		t.Errorf("expected host preserved and password=xxxxx, got %q", got)
	}
}

// TestRedact_KeywordValueForm_PercentEncoded reproduces the shape de's
// WithConnTimeouts produces: url.Parse+u.String() turns the separating
// spaces into "%20", so the byte immediately before "password" is a digit
// (word character) — a naive \b-anchored regex would miss it.
func TestRedact_KeywordValueForm_PercentEncoded(t *testing.T) {
	got := dsn.Redact("host=db.internal%20port=5432%20user=admin%20password=SuperSecret123%20dbname=prod")
	if strings.Contains(got, "SuperSecret123") {
		t.Fatalf("password leaked: %q", got)
	}
	if !strings.Contains(got, "password=xxxxx") {
		t.Errorf("expected password=xxxxx, got %q", got)
	}
}

// TestRedact_PgPassword covers the pgpassword= alias some tools accept.
func TestRedact_PgPassword(t *testing.T) {
	got := dsn.Redact("host=db pgpassword=SuperSecret123 dbname=prod")
	if strings.Contains(got, "SuperSecret123") {
		t.Fatalf("password leaked: %q", got)
	}
	if !strings.Contains(got, "pgpassword=xxxxx") {
		t.Errorf("expected pgpassword=xxxxx, got %q", got)
	}
}

// TestRedact_Unparseable asserts the fail-closed default: anything Redact
// cannot confidently recognize comes back as "[redacted]", never verbatim.
func TestRedact_Unparseable(t *testing.T) {
	got := dsn.Redact("\x7f not a dsn at all \x00")
	if got != "[redacted]" {
		t.Errorf("got %q, want [redacted]", got)
	}
}

// TestRedact_NoPassword verifies a DSN with no password is not mangled into
// "[redacted]" — Redact should only touch the credential, not the whole
// value, when there is nothing to hide.
func TestRedact_NoPassword(t *testing.T) {
	got := dsn.Redact("postgres://admin@10.0.0.5:5432/prod")
	if got != "postgres://admin@10.0.0.5:5432/prod" {
		t.Errorf("got %q, want unchanged (no password to redact)", got)
	}
}

// TestScrubText_URLForm verifies ScrubText finds and redacts a DSN userinfo
// password embedded inside a larger free-text error message.
func TestScrubText_URLForm(t *testing.T) {
	msg := `Error: database DSN "//dbadmin:Hunter2Pw@10.0.0.5:5432/prod" has no scheme`
	got := dsn.ScrubText(msg)
	if strings.Contains(got, "Hunter2Pw") {
		t.Fatalf("password leaked: %q", got)
	}
	if !strings.Contains(got, "dbadmin:xxxxx@") {
		t.Errorf("expected dbadmin:xxxxx@, got %q", got)
	}
}

// TestScrubText_KeywordValueForm verifies ScrubText finds and redacts a
// libpq password= token embedded inside a larger free-text error message.
func TestScrubText_KeywordValueForm(t *testing.T) {
	msg := `Error: database DSN "host=db.internal port=5432 user=admin password=SuperSecret123 dbname=prod" has no scheme`
	got := dsn.ScrubText(msg)
	if strings.Contains(got, "SuperSecret123") {
		t.Fatalf("password leaked: %q", got)
	}
	if !strings.Contains(got, "password=xxxxx") {
		t.Errorf("expected password=xxxxx, got %q", got)
	}
}
