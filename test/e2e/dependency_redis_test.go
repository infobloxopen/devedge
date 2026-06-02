package e2e

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/infobloxopen/devedge/internal/depruntime"
)

// resp does a minimal RESP request/response over conn (enough for AUTH/SET/GET),
// so the redis e2e needs no host redis-cli or third-party client.
func resp(rw *bufio.ReadWriter, args ...string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := rw.WriteString(b.String()); err != nil {
		return "", err
	}
	if err := rw.Flush(); err != nil {
		return "", err
	}
	line, err := rw.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	switch {
	case strings.HasPrefix(line, "+"): // simple string
		return line[1:], nil
	case strings.HasPrefix(line, "-"): // error
		return "", fmt.Errorf("redis error: %s", line[1:])
	case strings.HasPrefix(line, "$"): // bulk string: read the payload line
		if line == "$-1" {
			return "", nil
		}
		payload, err := rw.ReadString('\n')
		return strings.TrimRight(payload, "\r\n"), err
	default:
		return line, nil
	}
}

// TestRedisDependency_e2e: install shared Redis via Helm, provision an isolated
// ACL user + key namespace, and connect over the reported endpoint to AUTH and
// round-trip a namespaced key — proving redis provisioning + the port-forward
// connectivity (Slice B). Uses a tiny hand-rolled RESP client (no host redis-cli).
func TestRedisDependency_e2e(t *testing.T) {
	requireE2E(t)
	kubeCtx := ephemeralCluster(t)

	prov := depruntime.NewHelmProvisioner(kubeCtx)
	t.Cleanup(prov.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	inst, err := prov.EnsureInstance(ctx, depruntime.InstanceRef{Engine: depruntime.EngineRedis, Version: "7"})
	if err != nil {
		t.Fatalf("EnsureInstance: %v", err)
	}

	ready := false
	for i := 0; i < 30; i++ {
		if err := prov.Ready(ctx, depruntime.InstanceRef{Engine: depruntime.EngineRedis}); err == nil {
			ready = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !ready {
		t.Fatal("redis did not become ready")
	}

	b, err := depruntime.NewBinding("e2esvc", depruntime.Dep{Name: "cache", Engine: depruntime.EngineRedis, Port: 6379})
	if err != nil {
		t.Fatal(err)
	}
	if err := prov.EnsureDatabase(ctx, b); err != nil {
		t.Fatalf("EnsureDatabase: %v", err)
	}

	// Connect over the forwarded port as the provisioned ACL user and round-trip
	// a key inside the binding's namespace.
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", inst.Port), 5*time.Second)
	if err != nil {
		t.Fatalf("dial forwarded redis: %v", err)
	}
	defer conn.Close()
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	if out, err := resp(rw, "AUTH", b.User, b.Password); err != nil || out != "OK" {
		t.Fatalf("AUTH as %s: out=%q err=%v", b.User, out, err)
	}
	key := b.KeyNamespace + "greeting"
	if out, err := resp(rw, "SET", key, "hello"); err != nil || out != "OK" {
		t.Fatalf("SET %s: out=%q err=%v", key, out, err)
	}
	got, err := resp(rw, "GET", key)
	if err != nil {
		t.Fatalf("GET %s: %v", key, err)
	}
	if got != "hello" {
		t.Fatalf("GET %s = %q, want hello", key, got)
	}
	fmt.Printf("e2e: redis ACL user %q round-tripped %q over 127.0.0.1:%d\n", b.User, key, inst.Port)
}
