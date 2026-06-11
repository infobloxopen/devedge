package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestProbe_WaitsForHealthy verifies that the prober retries until it gets a 2xx
// response. The server returns 503 for the first 3 requests, then 200.
func TestProbe_WaitsForHealthy(t *testing.T) {
	var callCount atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n < 4 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &HTTPProber{
		TargetURL: srv.URL,
		Interval:  20 * time.Millisecond,
		Timeout:   5 * time.Second,
	}

	ok, err := p.Probe(context.Background())

	if !ok {
		t.Errorf("expected ok=true, got false")
	}
	if err != nil {
		t.Errorf("expected err=nil, got %v", err)
	}
	if n := callCount.Load(); n < 4 {
		t.Errorf("expected at least 4 calls, got %d", n)
	}
}

// TestProbe_TimesOut verifies that a server that never becomes healthy causes
// Probe to return (false, nil) — timeout is not an error condition.
func TestProbe_TimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := &HTTPProber{
		TargetURL: srv.URL,
		Interval:  20 * time.Millisecond,
		Timeout:   100 * time.Millisecond,
	}

	ok, err := p.Probe(context.Background())

	if ok {
		t.Errorf("expected ok=false, got true")
	}
	if err != nil {
		t.Errorf("expected err=nil (timeout is not an error), got %v", err)
	}
}

// TestProbe_ContextCancel verifies that when the caller cancels the outer context,
// Probe returns (false, non-nil error) wrapping the cancellation.
func TestProbe_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel the context after a short delay so the prober is mid-loop.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	p := &HTTPProber{
		TargetURL: srv.URL,
		Interval:  20 * time.Millisecond,
		Timeout:   5 * time.Second,
	}

	ok, err := p.Probe(ctx)

	if ok {
		t.Errorf("expected ok=false, got true")
	}
	if err == nil {
		t.Errorf("expected non-nil error on context cancellation, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected error to wrap context.Canceled, got %v", err)
	}
}

// TestProbe_ImmediateHealthy verifies that a server that is already healthy causes
// Probe to return (true, nil) after exactly one HTTP call.
func TestProbe_ImmediateHealthy(t *testing.T) {
	var callCount atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &HTTPProber{
		TargetURL: srv.URL,
		Interval:  20 * time.Millisecond,
		Timeout:   5 * time.Second,
	}

	ok, err := p.Probe(context.Background())

	if !ok {
		t.Errorf("expected ok=true, got false")
	}
	if err != nil {
		t.Errorf("expected err=nil, got %v", err)
	}
	if n := callCount.Load(); n != 1 {
		t.Errorf("expected exactly 1 call, got %d", n)
	}
}
