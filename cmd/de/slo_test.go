package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infobloxopen/devedge-sdk/slo"
)

// writeSLOFixture writes a minimal service project (buf config + one proto with a
// single gRPC service + an enriched OpenAPI) into a temp dir and returns it. The
// proto deliberately mentions the word "service" in a line comment and declares a
// bogus service inside a block comment, to prove comment-stripping in the FQN
// derivation.
func writeSLOFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "buf.gen.yaml"), `version: v2
inputs:
  - directory: proto
plugins:
  - local: protoc-gen-go
    out: gen
`)
	mustWrite(t, filepath.Join(dir, "proto", "orders", "v1", "orders.proto"), `syntax = "proto3";
// orders.v1 is the orders service domain (this comment says service, ignore it).
package orders.v1;
option go_package = "example.com/orders/gen/orders/v1;ordersv1";
/* block comment: service NotAService {} must be ignored */
service OrderService {
  rpc GetOrder(GetOrderRequest) returns (Order);
  rpc ListOrders(ListOrdersRequest) returns (ListOrdersResponse);
}
message GetOrderRequest { string name = 1; }
message Order { string name = 1; }
message ListOrdersRequest {}
message ListOrdersResponse { repeated Order orders = 1; }
`)
	mustWrite(t, filepath.Join(dir, "openapi", "orders.openapi.yaml"), `openapi: 3.0.3
info:
  title: Orders API
paths:
  /v1/orders:
    get:
      operationId: OrderService_ListOrders
      x-aip-method: List
    post:
      operationId: OrderService_CreateOrder
      x-aip-method: Create
  /v1/orders/{id}:
    get:
      operationId: OrderService_GetOrder
      x-aip-method: Get
    delete:
      operationId: OrderService_DeleteOrder
      x-aip-method: Delete
`)
	return dir
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const wantFQN = "orders.v1.OrderService"

// TestSLOGenerate_DerivesFQN is the key test: `de slo generate` with no
// --service must derive the gRPC FQN from the protos and stamp it as the
// rpc.service label (OTelRatioSource.Service) on every emitted SLI.
func TestSLOGenerate_DerivesFQN(t *testing.T) {
	dir := writeSLOFixture(t)

	out, err := runDe(t, "slo", "generate", "-C", dir)
	if err != nil {
		t.Fatalf("de slo generate: %v\n%s", err, out)
	}
	if !strings.Contains(out, "rpc.service="+wantFQN) {
		t.Errorf("generate output should report the derived FQN, got:\n%s", out)
	}

	data, err := os.ReadFile(filepath.Join(dir, "slo.yaml"))
	if err != nil {
		t.Fatalf("slo.yaml not written: %v", err)
	}
	doc, err := slo.Parse(data)
	if err != nil {
		t.Fatalf("parse slo.yaml: %v", err)
	}
	if len(doc.SLIs) == 0 {
		t.Fatal("no SLIs emitted")
	}
	// Every derived SLI's ratio source must carry the FQN — otherwise the SLIs
	// aggregate across services (the exact gap this command closes).
	for _, sli := range doc.SLIs {
		if sli.Spec.RatioMetric == nil {
			t.Fatalf("SLI %q has no ratioMetric", sli.Metadata.Name)
		}
		if got := sli.Spec.RatioMetric.Good.Spec.Service; got != wantFQN {
			t.Errorf("SLI %q good.service = %q, want %q", sli.Metadata.Name, got, wantFQN)
		}
		if got := sli.Spec.RatioMetric.Total.Spec.Service; got != wantFQN {
			t.Errorf("SLI %q total.service = %q, want %q", sli.Metadata.Name, got, wantFQN)
		}
	}
}

// TestSLOGenerate_FailsLoudWithoutFQN proves the command refuses to emit
// un-service-scoped SLIs: with no derivable service and no --service, it errors.
func TestSLOGenerate_FailsLoudWithoutFQN(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "openapi", "orders.openapi.yaml"), `openapi: 3.0.3
info:
  title: Orders API
paths:
  /v1/orders:
    get:
      operationId: OrderService_ListOrders
      x-aip-method: List
`)
	// No proto files at all → FQN cannot be derived.
	out, err := runDe(t, "slo", "generate", "-C", dir)
	if err == nil {
		t.Fatalf("expected fail-loud on missing FQN, got success:\n%s", out)
	}
	if !strings.Contains(out, "--service") {
		t.Errorf("error should suggest --service, got:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "slo.yaml")); statErr == nil {
		t.Error("slo.yaml must NOT be written when the FQN cannot be determined")
	}
}

// TestSLOGenerate_FailsLoudMultipleServices proves ambiguity fails loud rather
// than guessing a service to scope to.
func TestSLOGenerate_FailsLoudMultipleServices(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "buf.gen.yaml"), "version: v2\ninputs:\n  - directory: proto\n")
	mustWrite(t, filepath.Join(dir, "openapi", "shop.openapi.yaml"), `openapi: 3.0.3
info:
  title: Shop API
paths:
  /v1/orders:
    get:
      operationId: OrderService_ListOrders
      x-aip-method: List
`)
	mustWrite(t, filepath.Join(dir, "proto", "shop", "v1", "shop.proto"), `syntax = "proto3";
package shop.v1;
service OrderService { rpc GetOrder(GetOrderRequest) returns (Order); }
service CartService { rpc GetCart(GetCartRequest) returns (Cart); }
message GetOrderRequest {} message Order {} message GetCartRequest {} message Cart {}
`)
	out, err := runDe(t, "slo", "generate", "-C", dir)
	if err == nil {
		t.Fatalf("expected fail-loud on multiple services, got success:\n%s", out)
	}
	if !strings.Contains(out, "shop.v1.OrderService") || !strings.Contains(out, "shop.v1.CartService") {
		t.Errorf("error should list the candidate services, got:\n%s", out)
	}

	// With --service the ambiguity is resolved and generation succeeds.
	out, err = runDe(t, "slo", "generate", "-C", dir, "--service", "shop.v1.OrderService")
	if err != nil {
		t.Fatalf("de slo generate --service: %v\n%s", err, out)
	}
}

// TestSLO_GenerateLintRender walks the happy path end to end: generate → lint
// (must pass; defaults warn, never error) → render prometheus (must emit a rule).
func TestSLO_GenerateLintRender(t *testing.T) {
	dir := writeSLOFixture(t)

	if _, err := runDe(t, "slo", "generate", "-C", dir); err != nil {
		t.Fatalf("generate: %v", err)
	}
	sloPath := filepath.Join(dir, "slo.yaml")

	lintOut, err := runDe(t, "slo", "lint", sloPath)
	if err != nil {
		t.Fatalf("lint should pass on generated defaults (warns only), got err %v\n%s", err, lintOut)
	}
	if !strings.Contains(lintOut, "un-calibrated") {
		t.Errorf("lint should warn about un-calibrated defaults, got:\n%s", lintOut)
	}

	outDir := filepath.Join(dir, "deploy")
	renderOut, err := runDe(t, "slo", "render", "--target", "prometheus", "--in", sloPath, "--out", outDir)
	if err != nil {
		t.Fatalf("render: %v\n%s", err, renderOut)
	}
	rules, _ := filepath.Glob(filepath.Join(outDir, "*.prometheusrule.yaml"))
	if len(rules) == 0 {
		t.Fatalf("render wrote no PrometheusRule; output:\n%s", renderOut)
	}
}

// TestSLOLint_RejectsSaturationSignal proves the classifier gate is wired: a
// saturation signal declared as an SLI is an error, so `de slo lint` exits
// non-zero.
func TestSLOLint_RejectsSaturationSignal(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.slo.yaml")
	mustWrite(t, bad, `apiVersion: openslo/v1
kind: SLI
metadata:
  name: cpu-utilization
spec:
  description: cpu saturation as an SLI (category error)
  thresholdMetric:
    metricSource:
      type: devedge/otel-rpc
      query: avg(cpu_utilization)
`)
	out, err := runDe(t, "slo", "lint", bad)
	if err == nil {
		t.Fatalf("lint must reject a saturation-signal-as-SLI, got success:\n%s", out)
	}
}

// TestSLOCheck queries a stubbed Prometheus API and reports the current ratio.
// It also asserts the built query is service-scoped (carries rpc_service), i.e.
// `check` measures the same series the derived SLIs define.
func TestSLOCheck(t *testing.T) {
	dir := writeSLOFixture(t)
	if _, err := runDe(t, "slo", "generate", "-C", dir); err != nil {
		t.Fatalf("generate: %v", err)
	}

	var sawServiceScopedQuery bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query().Get("query"); strings.Contains(q, `rpc_service="`+wantFQN+`"`) {
			sawServiceScopedQuery = true
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1700000000,"0.9995"]}]}}`))
	}))
	defer srv.Close()

	out, err := runDe(t, "slo", "check", "-C", dir, "--prometheus-url", srv.URL)
	if err != nil {
		t.Fatalf("de slo check: %v\n%s", err, out)
	}
	if !sawServiceScopedQuery {
		t.Errorf("check query was not service-scoped (missing rpc_service=%q)", wantFQN)
	}
	if !strings.Contains(out, "99.950%") {
		t.Errorf("check should report the current ratio 99.950%%, got:\n%s", out)
	}
}

// TestSLOCheck_NoURL prints guidance and exits non-zero when no backend is set.
func TestSLOCheck_NoURL(t *testing.T) {
	dir := writeSLOFixture(t)
	if _, err := runDe(t, "slo", "generate", "-C", dir); err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Ensure no ambient env leaks a URL into this case.
	t.Setenv("PROMETHEUS_URL", "")
	t.Setenv("CORTEX_URL", "")
	out, err := runDe(t, "slo", "check", "-C", dir)
	if err == nil {
		t.Fatalf("expected error with no URL, got success:\n%s", out)
	}
	if !strings.Contains(out, "Cortex") {
		t.Errorf("error should explain how to point at Cortex, got:\n%s", out)
	}
}

// TestSLOKpis prints the Layer-0 KPI reference.
func TestSLOKpis(t *testing.T) {
	out, err := runDe(t, "slo", "kpis")
	if err != nil {
		t.Fatalf("de slo kpis: %v", err)
	}
	if !strings.Contains(out, "Layer-0 API KPIs") {
		t.Errorf("kpis output missing the reference header, got:\n%s", out)
	}
}

// TestDeriveServiceFQNs_IgnoresCommentsAndVendored unit-tests the derivation:
// only the real service is returned; commented/vendored ones are skipped.
func TestDeriveServiceFQNs_IgnoresCommentsAndVendored(t *testing.T) {
	dir := writeSLOFixture(t)
	// Add a vendored infra proto under third_party/ and a google-package proto —
	// both must be ignored.
	mustWrite(t, filepath.Join(dir, "proto", "third_party", "authz.proto"),
		"syntax=\"proto3\";\npackage infoblox.authz.v1;\nservice AuthzService { rpc X(Y) returns (Z); }\nmessage Y{} message Z{}\n")
	mustWrite(t, filepath.Join(dir, "proto", "google", "api", "annotations.proto"),
		"syntax=\"proto3\";\npackage google.api;\nservice AnnotationsService { rpc X(Y) returns (Z); }\nmessage Y{} message Z{}\n")

	fqns, err := deriveServiceFQNs(dir)
	if err != nil {
		t.Fatalf("deriveServiceFQNs: %v", err)
	}
	if len(fqns) != 1 || fqns[0] != wantFQN {
		t.Errorf("deriveServiceFQNs = %v, want [%s]", fqns, wantFQN)
	}
}
