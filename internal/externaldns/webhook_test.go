package externaldns

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockClient records calls for testing.
type mockClient struct {
	registered   map[string]string
	deregistered []string
}

func newMockClient() *mockClient {
	return &mockClient{registered: make(map[string]string)}
}

func (m *mockClient) RegisterRoute(_ context.Context, host, upstream string) error {
	m.registered[host] = upstream
	return nil
}

func (m *mockClient) DeregisterRoute(_ context.Context, host string) error {
	m.deregistered = append(m.deregistered, host)
	delete(m.registered, host)
	return nil
}

func TestNegotiate(t *testing.T) {
	mc := newMockClient()
	w := NewWebhook(mc, "dev.test", slog.Default())

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	w.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)

	df, ok := resp["domainFilter"].(map[string]any)
	if !ok {
		t.Fatal("missing domainFilter")
	}
	filters, _ := df["filters"].([]any)
	if len(filters) == 0 || filters[0] != "dev.test" {
		t.Errorf("filters = %v", filters)
	}
}

func TestApplyChanges_create(t *testing.T) {
	mc := newMockClient()
	w := NewWebhook(mc, "dev.test", slog.Default())

	changes := Changes{
		Create: []*Endpoint{
			{DNSName: "api.foo.dev.test", RecordType: "A", Targets: []string{"127.0.0.1"}},
			{DNSName: "web.foo.dev.test", RecordType: "A", Targets: []string{"127.0.0.1"}},
		},
	}
	body, _ := json.Marshal(changes)

	req := httptest.NewRequest("POST", "/records", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	w.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rr.Code)
	}

	if len(mc.registered) != 2 {
		t.Errorf("registered %d, want 2", len(mc.registered))
	}
	if _, ok := mc.registered["api.foo.dev.test"]; !ok {
		t.Error("api.foo.dev.test not registered")
	}
}

func TestApplyChanges_delete(t *testing.T) {
	mc := newMockClient()
	w := NewWebhook(mc, "dev.test", slog.Default())

	changes := Changes{
		Delete: []*Endpoint{
			{DNSName: "old.foo.dev.test", RecordType: "A"},
		},
	}
	body, _ := json.Marshal(changes)

	req := httptest.NewRequest("POST", "/records", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	w.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rr.Code)
	}
	if len(mc.deregistered) != 1 || mc.deregistered[0] != "old.foo.dev.test" {
		t.Errorf("deregistered = %v", mc.deregistered)
	}
}

func TestApplyChanges_ignores_unmanaged_domain(t *testing.T) {
	mc := newMockClient()
	w := NewWebhook(mc, "dev.test", slog.Default())

	changes := Changes{
		Create: []*Endpoint{
			{DNSName: "api.example.com", RecordType: "A", Targets: []string{"1.2.3.4"}},
		},
	}
	body, _ := json.Marshal(changes)

	req := httptest.NewRequest("POST", "/records", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	w.Handler().ServeHTTP(rr, req)

	if len(mc.registered) != 0 {
		t.Errorf("should not register non-managed domain, got %v", mc.registered)
	}
}

func TestAdjustEndpoints(t *testing.T) {
	mc := newMockClient()
	w := NewWebhook(mc, "dev.test", slog.Default())

	endpoints := []*Endpoint{
		{DNSName: "api.foo.dev.test", RecordType: "A"},
	}
	body, _ := json.Marshal(endpoints)

	req := httptest.NewRequest("POST", "/adjustendpoints", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	w.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}

	var result []*Endpoint
	json.NewDecoder(rr.Body).Decode(&result)
	if len(result) != 1 {
		t.Errorf("expected 1 endpoint back, got %d", len(result))
	}
}
