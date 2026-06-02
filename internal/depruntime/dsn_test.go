package depruntime

import (
	"strings"
	"testing"
)

// T007: inClusterDSN derives a DSN reachable over the engine's in-cluster Service
// DNS using the binding's per-service credentials, for both the shared instance
// and a dedicated per-service instance.
func TestInClusterDSN(t *testing.T) {
	b := Binding{
		Service: "my-svc", Dependency: "db", Engine: EnginePostgres,
		Database: "my_svc_db", User: "my_svc_db", Password: "s3cret",
	}

	got, err := inClusterDSN(b, "devedge-deps")
	if err != nil {
		t.Fatalf("inClusterDSN: %v", err)
	}
	if !strings.HasPrefix(got, "postgres://") {
		t.Errorf("expected a postgres DSN, got %q", got)
	}
	if !strings.Contains(got, "@devedge-postgres.devedge-deps.svc.cluster.local:5432/") {
		t.Errorf("expected in-cluster Service DNS host, got %q", got)
	}
	if !strings.Contains(got, "my_svc_db") {
		t.Errorf("expected per-service database/user in DSN, got %q", got)
	}

	// A dedicated instance resolves to its own per-service Service DNS.
	bd := b
	bd.Dedicated = true
	gotD, err := inClusterDSN(bd, "devedge-deps")
	if err != nil {
		t.Fatalf("inClusterDSN(dedicated): %v", err)
	}
	if !strings.Contains(gotD, "@devedge-postgres-my-svc.devedge-deps.svc.cluster.local:5432/") {
		t.Errorf("dedicated instance should resolve to its own Service DNS, got %q", gotD)
	}
}
