// The onboarding walk-through e2e (feature 007, FR-010/SC-002) — the
// platform's recurring acceptance probe: scaffold a service, perform the
// scripted resource rename (US4's flow), regenerate, run it against real
// provisioned dependencies (003/006 mechanics), exercise authz-governed CRUD
// through the REST gateway against real Postgres, then deploy it in-cluster
// (005/006: image build + schema hook) and probe again. Gated behind
// DEVEDGE_E2E=1 plus the k3d toolchain (like 003–006) and the generation
// toolchain (buf + protoc plugins).
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/cluster"
	"github.com/infobloxopen/devedge/internal/deploy"
	"github.com/infobloxopen/devedge/internal/depruntime"
	"github.com/infobloxopen/devedge/internal/helm"
	"github.com/infobloxopen/devedge/internal/scaffold"
)

// renameResource performs the scripted example-resource rename the scaffold's
// AGENTS.md documents: WebhookEndpoint → Note, in every code/schema file.
// Case-sensitive, longest-token-first so the result is a consistent resource.
func renameResource(t *testing.T, proj string) {
	t.Helper()
	replacements := []struct{ old, new string }{
		{"WebhookEndpoints", "Notes"},
		{"WebhookEndpoint", "Note"},
		{"webhook_endpoints", "notes"},
		{"webhook_endpoint", "note"},
		{"webhook-endpoints", "notes"},
		{"webhook-endpoint", "note"},
		{"Endpoints", "Notes"},
		{"Endpoint", "Note"},
		{"endpoints", "notes"},
		{"endpoint", "note"},
	}
	exts := map[string]bool{".proto": true, ".go": true, ".sql": true}
	err := filepath.WalkDir(proj, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !exts[filepath.Ext(path)] || strings.Contains(path, "internal/gen/") ||
			strings.Contains(path, "third_party/") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s := string(b)
		// The gateway's generated *HandlerFromEndpoint suffix means "from a
		// gRPC endpoint address" — it is not the resource name. Guard it.
		const guard = "HANDLER_FROM_GRPC_ADDR_GUARD"
		s = strings.ReplaceAll(s, "HandlerFromEndpoint", guard)
		for _, r := range replacements {
			s = strings.ReplaceAll(s, r.old, r.new)
		}
		s = strings.ReplaceAll(s, guard, "HandlerFromEndpoint")
		return os.WriteFile(path, []byte(s), 0o644)
	})
	if err != nil {
		t.Fatalf("rename pass: %v", err)
	}
	// File renames: the proto and the migration pair carry the resource name.
	renames := map[string]string{
		"proto/notesvc/v1/webhook_endpoint.proto":   "proto/notesvc/v1/note.proto",
		"db/migrations/001_webhook_endpoints.up.sql":   "db/migrations/001_notes.up.sql",
		"db/migrations/001_webhook_endpoints.down.sql": "db/migrations/001_notes.down.sql",
	}
	for from, to := range renames {
		if err := os.Rename(filepath.Join(proj, from), filepath.Join(proj, to)); err != nil {
			t.Fatalf("rename %s: %v", from, err)
		}
	}
	// Regenerate from scratch — stale generated code must not survive.
	if err := os.RemoveAll(filepath.Join(proj, "internal", "gen")); err != nil {
		t.Fatal(err)
	}
}

func runIn(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out.String())
	}
	return out.String()
}

func httpJSON(t *testing.T, method, url string, headers map[string]string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestScaffoldOnboarding_e2e(t *testing.T) {
	requireE2E(t)
	for _, tool := range []string{"buf", "protoc-gen-go", "protoc-gen-go-grpc", "protoc-gen-grpc-gateway"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("skipping: %q not on PATH", tool)
		}
	}
	kubeCtx := ephemeralCluster(t)
	clusterName := strings.TrimPrefix(kubeCtx, "k3d-")
	const svc = "notesvc"

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// --- US1 + US4: scaffold, scripted rename, regenerate, build, test. ---
	parent := t.TempDir()
	if err := scaffold.Render(scaffold.Params{Name: svc, ParentDir: parent}); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	proj := filepath.Join(parent, svc)
	renameResource(t, proj)
	runIn(t, proj, "make", "generate")
	runIn(t, proj, "go", "build", "./...")
	if out := runIn(t, proj, "go", "test", "./..."); strings.Contains(out, "FAIL") {
		t.Fatalf("renamed project tests failed:\n%s", out)
	}

	// --- US2: deps + migrations the local-run way (the `de project up` seam),
	//     then serve and exercise authz-governed CRUD via the REST gateway. ---
	prov := depruntime.NewHelmProvisioner(kubeCtx)
	t.Cleanup(prov.Close)
	rec := depruntime.NewReconciler(prov, t.TempDir(), 3*time.Minute)

	res := rec.Reconcile(ctx, svc, []depruntime.Dep{{
		Name: "db", Engine: depruntime.EnginePostgres, Port: 5432,
		Migrations: filepath.Join(proj, "db", "migrations"),
	}}, cluster.EnvDev)[0]
	if !res.Ready() {
		t.Fatalf("db not ready: state=%s err=%s", res.State, res.Err)
	}
	connStr := connStrFor(t, res)
	if got, err := psqlScalar(t, ctx, connStr, "SELECT to_regclass('public.notes') IS NOT NULL"); err != nil || got != "t" {
		t.Fatalf("migrations should have created notes: got=%q err=%v", got, err)
	}

	bin := filepath.Join(t.TempDir(), svc)
	runIn(t, proj, "go", "build", "-o", bin, "./cmd/"+svc)

	const httpAddr = "127.0.0.1:18080"
	serveCmd := exec.CommandContext(ctx, bin, "serve")
	serveCmd.Dir = proj
	serveCmd.Env = append(os.Environ(),
		"DATABASE_URL=fsnotify://postgres/"+res.DSNFilePath,
		"HTTP_ADDR="+httpAddr,
		"GRPC_ADDR=127.0.0.1:19090",
	)
	var serveOut bytes.Buffer
	serveCmd.Stdout, serveCmd.Stderr = &serveOut, &serveOut
	if err := serveCmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	t.Cleanup(func() { _ = serveCmd.Process.Kill() })

	base := "http://" + httpAddr
	ready := false
	for i := 0; i < 50; i++ {
		if resp, err := http.Get(base + "/v1/notes"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("service did not become ready:\n%s", serveOut.String())
	}

	// CRUD round-trip through real Postgres.
	code, body := httpJSON(t, http.MethodPost, base+"/v1/notes", nil,
		map[string]any{"url": "https://example.test/hook", "secret": "s3", "event_filters": []string{"created"}})
	if code != http.StatusOK {
		t.Fatalf("create: HTTP %d: %s", code, body)
	}
	var created struct {
		Id string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.Id == "" {
		t.Fatalf("create response missing id: %s", body)
	}
	code, body = httpJSON(t, http.MethodGet, base+"/v1/notes/"+created.Id, nil, nil)
	if code != http.StatusOK || !strings.Contains(string(body), "example.test/hook") {
		t.Fatalf("get: HTTP %d: %s", code, body)
	}
	if got, err := psqlScalar(t, ctx, connStr, "SELECT count(*) FROM notes"); err != nil || got != "1" {
		t.Fatalf("row should be in Postgres: got=%q err=%v", got, err)
	}

	// Deny path: a non-granted subject is rejected (fail-closed authz at
	// runtime, not just at boot). The gateway maps PermissionDenied to 403.
	code, body = httpJSON(t, http.MethodGet, base+"/v1/notes",
		map[string]string{"Grpc-Metadata-x-dev-subject": "intruder"}, nil)
	if code != http.StatusForbidden {
		t.Fatalf("deny probe: want 403, got %d: %s", code, body)
	}

	// --- US3: deploy — image built from the scaffolded Dockerfile, schema
	//     hook (the scaffolded `migrate up`, a no-op here: already current),
	//     same CRUD visible from the in-cluster workload. ---
	b, err := depruntime.NewBinding(svc, depruntime.Dep{Name: "db", Engine: depruntime.EnginePostgres, Port: 5432})
	if err != nil {
		t.Fatal(err)
	}
	// NewBinding mints a fresh password; re-ensure the database so the role's
	// password matches this binding before the Secret is written (the daemon's
	// deploy path uses one binding for both — this e2e drives the seams).
	if err := prov.EnsureDatabase(ctx, b); err != nil {
		t.Fatalf("EnsureDatabase: %v", err)
	}
	if err := prov.EnsureConnSecret(ctx, b); err != nil {
		t.Fatalf("EnsureConnSecret: %v", err)
	}
	if err := prov.EnsureMigrationStore(ctx, b); err != nil {
		t.Fatalf("EnsureMigrationStore: %v", err)
	}

	wl := deploy.Workload{
		Service: svc, Port: 8080, Replicas: 1, Hostname: svc + ".dev.test",
		Deps: []deploy.DepEnv{{Name: "db", Engine: "postgres", EnvVar: "DATABASE_URL"}},
		Migrations: &deploy.MigrationDeploy{
			SecretName:    cluster.ProjectSlug(svc) + "-db-dsn",
			DownStorePVC:  depruntime.DownStorePVCName(svc, "db"),
			DownStorePath: "/var/lib/devedge/downstore",
		},
	}
	deployer := deploy.NewDeployer(kubeCtx, helm.DefaultNamespace, clusterName)
	if _, err := deployer.Deploy(ctx, wl, deploy.ImageSource{
		Build: &deploy.BuildSource{Context: proj, Tag: svc + ":e2e"},
	}); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	t.Cleanup(func() { _ = deployer.Remove(context.Background(), svc) })

	// Probe the in-cluster workload through a port-forward: the note created
	// in local-run mode is visible (same isolated DB, migrated schema).
	pfCtx, pfCancel := context.WithCancel(ctx)
	defer pfCancel()
	pf := exec.CommandContext(pfCtx, "kubectl", "--context", kubeCtx, "-n", helm.DefaultNamespace,
		"port-forward", "deploy/"+svc, "18081:8080")
	if err := pf.Start(); err != nil {
		t.Fatalf("port-forward: %v", err)
	}
	deployed := false
	var lastBody []byte
	for i := 0; i < 50; i++ {
		if resp, err := http.Get("http://127.0.0.1:18081/v1/notes"); err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				deployed, lastBody = true, body
				break
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	if !deployed {
		t.Fatal("in-cluster workload did not serve through the port-forward")
	}
	if !strings.Contains(string(lastBody), created.Id) {
		t.Fatalf("deployed list should contain the note created locally: %s", lastBody)
	}

	// --- down: the workload is removable; the project can return to local-run. ---
	if err := deployer.Remove(ctx, svc); err != nil {
		t.Fatalf("remove: %v", err)
	}
}
