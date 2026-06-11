package health

import (
	"context"
	"crypto/tls"
	"net/http"
	"time"
)

type HTTPProber struct {
	TargetURL string
	Timeout   time.Duration
	Interval  time.Duration
	Client    *http.Client
}

// Probe polls until 2xx or done.
// Returns (true, nil) on 2xx.
// Returns (false, nil) on timeout.
// Returns (false, err) if outer ctx was cancelled.
func (p *HTTPProber) Probe(ctx context.Context) (bool, error) {
	effectiveTimeout := p.Timeout
	if effectiveTimeout <= 0 {
		effectiveTimeout = 30 * time.Second
	}

	effectiveInterval := p.Interval
	if effectiveInterval <= 0 {
		effectiveInterval = 500 * time.Millisecond
	}

	client := p.Client
	if client == nil {
		client = &http.Client{
			// Cap a single request below two poll cycles so one slow request
			// does not block the next tick.
			Timeout: effectiveInterval * 2,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				// Skip-verify is intentional for local dev probing and is
				// scoped only to this client.
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		}
	}

	inner, cancel := context.WithTimeout(ctx, effectiveTimeout)
	defer cancel()

	// Fire one probe immediately before starting the ticker so an
	// already-healthy service returns on the first call with no interval delay.
	if p.check(inner, client) {
		return true, nil
	}

	ticker := time.NewTicker(effectiveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if p.check(inner, client) {
				return true, nil
			}
		case <-inner.Done():
			if err := ctx.Err(); err != nil {
				// Outer context cancelled by the caller.
				return false, ctx.Err()
			}
			// Internal timeout exhausted; not an error.
			return false, nil
		}
	}
}

// check fires a single GET against the target. It returns true on a 2xx
// response. Transport errors and non-2xx responses are treated as not-ready.
func (p *HTTPProber) check(ctx context.Context, client *http.Client) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.TargetURL, nil)
	if err != nil {
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
