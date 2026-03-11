package daemon

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/infobloxopen/devedge/internal/registry"
	"github.com/infobloxopen/devedge/pkg/types"
)

func testAPI(t *testing.T) (*API, *registry.Registry) {
	t.Helper()
	reg := registry.New()
	logger := slog.Default()
	return NewAPI(reg, logger), reg
}

func TestListRoutes_empty(t *testing.T) {
	api, _ := testAPI(t)
	req := httptest.NewRequest("GET", "/v1/routes", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var routes []types.Route
	json.NewDecoder(w.Body).Decode(&routes)
	if len(routes) != 0 {
		t.Errorf("expected empty list, got %d", len(routes))
	}
}

func TestRegisterAndGet(t *testing.T) {
	api, _ := testAPI(t)

	// Register
	body := `{"host":"api.foo.dev.test","upstream":"http://127.0.0.1:3000","project":"foo"}`
	req := httptest.NewRequest("PUT", "/v1/routes", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201, body: %s", w.Code, w.Body.String())
	}

	// Get
	req = httptest.NewRequest("GET", "/v1/routes/api.foo.dev.test", nil)
	w = httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200", w.Code)
	}

	var route types.Route
	json.NewDecoder(w.Body).Decode(&route)
	if route.Upstream != "http://127.0.0.1:3000" {
		t.Errorf("upstream = %q", route.Upstream)
	}
}

func TestRegister_conflict(t *testing.T) {
	api, reg := testAPI(t)
	reg.Register(types.Route{Host: "x.dev.test", Owner: "alice"})

	body := `{"host":"x.dev.test","upstream":"http://127.0.0.1:1","owner":"bob"}`
	req := httptest.NewRequest("PUT", "/v1/routes", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}

func TestDeregisterRoute(t *testing.T) {
	api, reg := testAPI(t)
	reg.Register(types.Route{Host: "x.dev.test"})

	req := httptest.NewRequest("DELETE", "/v1/routes/x.dev.test", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}

	if _, ok := reg.Lookup("x.dev.test"); ok {
		t.Error("route should be removed")
	}
}

func TestDeregisterRoute_notFound(t *testing.T) {
	api, _ := testAPI(t)

	req := httptest.NewRequest("DELETE", "/v1/routes/nope.dev.test", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestDeregisterProject(t *testing.T) {
	api, reg := testAPI(t)
	reg.Register(types.Route{Host: "a.dev.test", Project: "foo"})
	reg.Register(types.Route{Host: "b.dev.test", Project: "foo"})
	reg.Register(types.Route{Host: "c.dev.test", Project: "bar"})

	req := httptest.NewRequest("DELETE", "/v1/projects/foo", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]int
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["removed"] != 2 {
		t.Errorf("removed = %d, want 2", resp["removed"])
	}
}

func TestStatus(t *testing.T) {
	api, _ := testAPI(t)

	req := httptest.NewRequest("GET", "/v1/status", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "running" {
		t.Errorf("status = %v", resp["status"])
	}
}

func TestRegister_validation(t *testing.T) {
	api, _ := testAPI(t)

	body := `{"host":"","upstream":""}`
	req := httptest.NewRequest("PUT", "/v1/routes", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
