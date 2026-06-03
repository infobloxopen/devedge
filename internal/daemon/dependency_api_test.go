package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/infobloxopen/devedge/internal/depruntime"
	"github.com/infobloxopen/devedge/internal/registry"
)

// apiFakeProv is a no-op Provisioner: every binding provisions and is ready.
type apiFakeProv struct{}

func (apiFakeProv) EnsureInstance(_ context.Context, ref depruntime.InstanceRef) (depruntime.Instance, error) {
	return depruntime.Instance{Engine: ref.Engine, Host: string(ref.Engine) + ".dev.test", Port: 5432}, nil
}
func (apiFakeProv) Ready(context.Context, depruntime.InstanceRef) error            { return nil }
func (apiFakeProv) EnsureDatabase(context.Context, depruntime.Binding) error       { return nil }
func (apiFakeProv) EnsureConnSecret(context.Context, depruntime.Binding) error     { return nil }
func (apiFakeProv) EnsureMigrationStore(context.Context, depruntime.Binding) error { return nil }
func (apiFakeProv) DropDatabase(context.Context, depruntime.Binding) error         { return nil }

func depAPI(t *testing.T) http.Handler {
	t.Helper()
	factory := func(string, string) depruntime.Provisioner { return apiFakeProv{} }
	mgr := NewDepManager(factory, t.TempDir(), 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return NewAPI(registry.New(), mgr, slog.Default()).Handler()
}

// T006: the new dependency endpoints upsert/report/release and never echo creds.
func TestDependencyEndpoints(t *testing.T) {
	h := depAPI(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	body, _ := json.Marshal(ApplyRequest{Dependencies: []DependencyRequest{{Name: "db", Engine: "postgres", Port: 5432}}})
	req, _ := http.NewRequest("PUT", srv.URL+"/v1/services/webhooks/dependencies", bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var results []depruntime.Result
	if err := json.Unmarshal(raw, &results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(results) != 1 || !results[0].Ready() {
		t.Fatalf("want 1 Ready result, got %+v", results)
	}
	if results[0].EnvVarName != "DATABASE_URL" || !strings.HasPrefix(results[0].EnvVarValue, "fsnotify://postgres/") {
		t.Errorf("unexpected env var: %+v", results[0])
	}
	// No credentials in the response body (real DSN lives only in the file).
	for _, leak := range []string{"password", "postgres://"} {
		if strings.Contains(strings.ToLower(string(raw)), leak) {
			t.Errorf("response leaks %q: %s", leak, raw)
		}
	}

	// GET returns the same observed state.
	gresp, _ := http.Get(srv.URL + "/v1/services/webhooks/dependencies")
	graw, _ := io.ReadAll(gresp.Body)
	gresp.Body.Close()
	if !strings.Contains(string(graw), "DATABASE_URL") {
		t.Errorf("GET missing result: %s", graw)
	}

	// DELETE releases (204).
	dreq, _ := http.NewRequest("DELETE", srv.URL+"/v1/services/webhooks/dependencies", nil)
	dresp, _ := http.DefaultClient.Do(dreq)
	if dresp.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE status = %d, want 204", dresp.StatusCode)
	}
}

// T006: the dependency endpoints are disabled (501) when no manager is wired,
// proving they are strictly additive and the route API path is independent.
func TestDependencyEndpoints_disabledWhenNoManager(t *testing.T) {
	h := NewAPI(registry.New(), nil, slog.Default()).Handler()
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/v1/services/x/dependencies")
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", resp.StatusCode)
	}
}
