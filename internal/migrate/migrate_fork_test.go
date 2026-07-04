package migrate

import (
	"strings"
	"testing"
	"time"
)

// assertNoSecretLeak fails the test if got contains needle (case-sensitive) or
// a literal "password=<secret>"/"pgpassword=<secret>" substring — the SEC-005
// regression: a raw DSN's cleartext password must never reach a returned
// error string.
func assertNoSecretLeak(t *testing.T, got, needle string) {
	t.Helper()
	if strings.Contains(got, needle) {
		t.Fatalf("secret %q leaked into error: %q", needle, got)
	}
}

// TestToPgxURL_NoScheme_RedactsKeywordValueDSN reproduces the SEC-005 PoC's
// first case (DATABASE_URL as a libpq keyword/value string with no scheme):
// the "no scheme" error must not carry the cleartext password.
func TestToPgxURL_NoScheme_RedactsKeywordValueDSN(t *testing.T) {
	dsn := "host=db.internal port=5432 user=admin password=SuperSecret123 dbname=prod sslmode=require"
	_, err := toPgxURL(dsn)
	if err == nil {
		t.Fatal("expected an error for a scheme-less DSN")
	}
	assertNoSecretLeak(t, err.Error(), "SuperSecret123")
	if !strings.Contains(err.Error(), "password=xxxxx") {
		t.Errorf("expected a redacted password=xxxxx in the error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "has no scheme") {
		t.Errorf("expected the error to still be actionable, got: %v", err)
	}
}

// TestToPgxURL_NoScheme_RedactsURLUserinfoDSN reproduces the SEC-005 PoC's
// second case (--dsn as a scheme-less //user:pass@host URL).
func TestToPgxURL_NoScheme_RedactsURLUserinfoDSN(t *testing.T) {
	dsn := "//dbadmin:Hunter2Pw@10.0.0.5:5432/prod"
	_, err := toPgxURL(dsn)
	if err == nil {
		t.Fatal("expected an error for a scheme-less DSN")
	}
	assertNoSecretLeak(t, err.Error(), "Hunter2Pw")
	if !strings.Contains(err.Error(), "dbadmin:xxxxx@") {
		t.Errorf("expected the username preserved and password redacted, got: %v", err)
	}
}

// TestToPgxURL_UnsupportedScheme_NoDSNAtAll is the control the assessment
// asked to preserve: an unsupported scheme reports only the scheme, never
// the DSN (it never carried a secret to begin with).
func TestToPgxURL_UnsupportedScheme_NoDSNAtAll(t *testing.T) {
	_, err := toPgxURL("mysql://root:MyPassw0rd@127.0.0.1:3306/prod")
	if err == nil {
		t.Fatal("expected an error for an unsupported scheme")
	}
	assertNoSecretLeak(t, err.Error(), "MyPassw0rd")
	if !strings.Contains(err.Error(), `"mysql"`) {
		t.Errorf("expected the scheme reported, got: %v", err)
	}
}

// TestToPgxURL_WellFormedDSN_StillWorks is a non-regression check: a
// well-formed postgres:// DSN still normalizes to the pgx5 scheme (the fix
// must not disturb the happy path).
func TestToPgxURL_WellFormedDSN_StillWorks(t *testing.T) {
	got, err := toPgxURL("postgres://admin:WellFormedPw999@127.0.0.1:5432/prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got, "pgx5://") {
		t.Errorf("expected pgx5:// scheme, got %q", got)
	}
}

// TestWithConnTimeouts_ParseError_RedactsDSN exercises WithConnTimeouts' own
// url.Parse error branch: a malformed DSN's *url.Error must not surface the
// raw dsn (its Error() text embeds it) into the returned error.
func TestWithConnTimeouts_ParseError_RedactsDSN(t *testing.T) {
	// A control character makes url.Parse fail outright.
	dsn := "postgres://admin:Hunter2Pw@10.0.0.5:5432/prod\x7f"
	_, err := WithConnTimeouts(dsn, 2*time.Second, 0)
	if err == nil {
		t.Fatal("expected a parse error")
	}
	assertNoSecretLeak(t, err.Error(), "Hunter2Pw")
}

// TestPqDSN_ParseError_RedactsDSN exercises seed.go's pqDSN parse-error
// branch the same way.
func TestPqDSN_ParseError_RedactsDSN(t *testing.T) {
	dsn := "postgres://admin:Hunter2Pw@10.0.0.5:5432/prod\x7f"
	_, err := pqDSN(dsn)
	if err == nil {
		t.Fatal("expected a parse error")
	}
	assertNoSecretLeak(t, err.Error(), "Hunter2Pw")
}
