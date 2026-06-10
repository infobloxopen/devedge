package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infobloxopen/devedge/internal/scaffold"
)

// TestScaffoldSmoke proves FR-003/SC-001: a freshly scaffolded project
// generates, builds, and passes its own tests with zero manual edits — and
// the boot-time authz gate (US1.3/SC-003) is observable: removing one
// annotation makes startup fail naming the method.
//
// Requires network (Go module downloads) and the generation toolchain (buf +
// protoc-gen-go/-go-grpc/-grpc-gateway on PATH); skipped in -short mode and
// when the toolchain is missing.
func TestScaffoldSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode (needs network + toolchain)")
	}
	for _, tool := range []string{"buf", "protoc-gen-go", "protoc-gen-go-grpc", "protoc-gen-grpc-gateway"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}

	parent := t.TempDir()
	if err := scaffold.Render(scaffold.Params{Name: "smokesvc", ParentDir: parent}); err != nil {
		t.Fatalf("render: %v", err)
	}
	proj := filepath.Join(parent, "smokesvc")

	run := func(name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = proj
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out.String())
		}
		return out.String()
	}

	// The walk-through's first three steps, exactly as a developer runs them.
	run("make", "generate")
	run("go", "build", "./...")
	out := run("go", "test", "./...")
	if strings.Contains(out, "FAIL") {
		t.Fatalf("generated project tests failed:\n%s", out)
	}

	// Boot gate, positive: with every RPC declared, startup proceeds past
	// authz and fails only on the missing DATABASE_URL.
	bin := filepath.Join(t.TempDir(), "smokesvc")
	run("go", "build", "-o", bin, "./cmd/smokesvc")
	cmd := exec.Command(bin, "serve")
	cmd.Dir = proj
	combined, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("serve without DATABASE_URL should fail")
	}
	if !strings.Contains(string(combined), "DATABASE_URL") {
		t.Fatalf("expected DATABASE_URL error (gate passed), got:\n%s", combined)
	}

	// Boot gate, negative (SC-003): drop one annotation → regenerate → the
	// service refuses to start and names the undeclared method.
	protoPath := filepath.Join(proj, "proto", "smokesvc", "v1", "webhook_endpoint.proto")
	src, err := os.ReadFile(protoPath)
	if err != nil {
		t.Fatal(err)
	}
	stripped := strings.Replace(string(src),
		`option (infoblox.authz.v1.rule) = {verb: "delete", resource: "webhook-endpoint"};`, "", 1)
	if stripped == string(src) {
		t.Fatal("could not strip the delete annotation (template drift?)")
	}
	if err := os.WriteFile(protoPath, []byte(stripped), 0o644); err != nil {
		t.Fatal(err)
	}
	run("make", "generate")
	run("go", "build", "-o", bin, "./cmd/smokesvc")
	cmd = exec.Command(bin, "serve")
	cmd.Dir = proj
	combined, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatal("serve with an undeclared method must refuse to start")
	}
	for _, want := range []string{"refusing to start", "DeleteWebhookEndpoint"} {
		if !strings.Contains(string(combined), want) {
			t.Fatalf("boot-gate output missing %q:\n%s", want, combined)
		}
	}
}
