package dsn

import (
	"net/url"
	"regexp"
)

// passwordKVRE matches a libpq/PostgreSQL keyword=value password token
// (password=... or pgpassword=...), case-insensitive. It deliberately has no
// leading word-boundary anchor: a keyword/value DSN that has passed through
// url.Query-style escaping (e.g. de's connection-timeout wiring) can turn the
// separating space before "password" into "%20", whose trailing digit is a
// word character — a \b anchor would then fail to match right where it
// matters. It does not attempt to parse the full keyword/value grammar
// (quoted values with embedded spaces) — the goal is redaction, not
// round-tripping.
var passwordKVRE = regexp.MustCompile(`(?i)(pgpassword|password)=\S+`)

// urlUserPassRE matches a URL-form userinfo pair, with or without a scheme
// (scheme://user:pass@ or the bare //user:pass@ a `--dsn` value may use), so
// a DSN embedded inside a larger free-text string (e.g. an error message)
// can be scrubbed without fully parsing the surrounding text.
var urlUserPassRE = regexp.MustCompile(`//([^/\s:@]+):([^/\s@]+)@`)

// Redact returns dsn with any embedded password removed, safe to include in
// a log line, error message, or CI output (SEC-005: `de migrate` / SDK
// persistence/migrate must never format a raw DSN into an error). It
// recognizes two DSN shapes:
//
//   - URL form, e.g. postgres://user:pw@host:port/db — the url.Userinfo
//     password is blanked (postgres://user:xxxxx@host:port/db).
//   - libpq keyword/value form, e.g. "host=... user=... password=... " — the
//     password=/pgpassword=... token is stripped.
//
// Anything it cannot confidently recognize returns "[redacted]" rather than
// risk leaking a secret in a shape it does not understand.
func Redact(raw string) string {
	if passwordKVRE.MatchString(raw) {
		return passwordKVRE.ReplaceAllString(raw, "${1}=xxxxx")
	}
	if u, err := url.Parse(raw); err == nil && (u.Scheme != "" || u.Host != "") {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
		}
		return u.String()
	}
	return "[redacted]"
}

// ScrubText returns s with any DSN-shaped password it can recognize removed
// — a best-effort belt-and-suspenders pass over free-form text (such as a
// final error string on its way to stderr) that might embed a DSN, rather
// than a full DSN string itself. Use Redact when the input is known to be
// exactly one DSN.
func ScrubText(s string) string {
	s = passwordKVRE.ReplaceAllString(s, "${1}=xxxxx")
	s = urlUserPassRE.ReplaceAllString(s, "//${1}:xxxxx@")
	return s
}
