package cluster

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// recordingProvider is a fake Provider that tracks create/delete calls and an
// in-memory set of clusters, so EnsureCluster's policy is unit-tested without
// k3d/kubectl. Optional errors simulate a missing tool / failed create.
type recordingProvider struct {
	mu        sync.Mutex
	clusters  map[string]bool
	creates   int
	deletes   int
	createErr error
	listErr   error
}

func newRecordingProvider(present ...string) *recordingProvider {
	p := &recordingProvider{clusters: map[string]bool{}}
	for _, n := range present {
		p.clusters[n] = true
	}
	return p
}

func (p *recordingProvider) Name() string                { return "fake" }
func (p *recordingProvider) KubeContext(n string) string { return "fake-" + n }
func (p *recordingProvider) HostGateway() string         { return "host.fake.internal" }

func (p *recordingProvider) Create(opts CreateOptions) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.createErr != nil {
		return p.createErr
	}
	p.creates++
	p.clusters[opts.Name] = true
	return nil
}

func (p *recordingProvider) Delete(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deletes++
	delete(p.clusters, name)
	return nil
}

func (p *recordingProvider) List() ([]ClusterInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listErr != nil {
		return nil, p.listErr
	}
	out := make([]ClusterInfo, 0, len(p.clusters))
	for n := range p.clusters {
		out = append(out, ClusterInfo{Name: n})
	}
	return out, nil
}

func (p *recordingProvider) FindIngressPort(string) (string, error) { return "8081", nil }

func (p *recordingProvider) count() (creates, deletes int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.creates, p.deletes
}

func (p *recordingProvider) has(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.clusters[name]
}

// newTestEnsurer wires an Ensurer over the fake provider with injected bootstrap
// seams (no kubectl/helm). bootstrapped controls the IsBootstrapped probe; the
// returned counter records how many times bootstrap ran.
func newTestEnsurer(t *testing.T, p Provider, bootstrapped bool) (*Ensurer, *int) {
	t.Helper()
	var calls int
	var mu sync.Mutex
	e := NewEnsurer(p)
	e.LockDir = t.TempDir()
	e.Bootstrap = func(context.Context, Provider, string, bool) error {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil
	}
	e.IsBootstrapped = func(context.Context, string) (bool, error) { return bootstrapped, nil }
	return e, &calls
}

// CT (T006): reuse-if-present — a present, bootstrapped cluster is reused with no
// second create and no re-bootstrap (the fast path; FR-011).
func TestEnsureCluster_reuseIfPresent(t *testing.T) {
	p := newRecordingProvider("devedge")
	e, bootstraps := newTestEnsurer(t, p, true)

	if err := e.EnsureCluster(context.Background(), "devedge"); err != nil {
		t.Fatalf("EnsureCluster: %v", err)
	}
	if c, _ := p.count(); c != 0 {
		t.Errorf("creates = %d, want 0 (reuse)", c)
	}
	if *bootstraps != 0 {
		t.Errorf("bootstraps = %d, want 0 (already bootstrapped)", *bootstraps)
	}
}

// CT (T006): create-once-under-host-lock — concurrent ensures of the same absent
// cluster yield exactly one create (FR-011 edge case).
func TestEnsureCluster_createOnceUnderLock(t *testing.T) {
	p := newRecordingProvider()
	e, _ := newTestEnsurer(t, p, true)

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = e.EnsureCluster(context.Background(), "devedge")
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	if c, _ := p.count(); c != 1 {
		t.Errorf("creates = %d, want exactly 1 under the host lock", c)
	}
	if !p.has("devedge") {
		t.Error("cluster not present after ensure")
	}
}

// CT (T006): present-but-unbootstrapped → reconcile (re-bootstrap, no re-create).
func TestEnsureCluster_reconcileUnbootstrapped(t *testing.T) {
	p := newRecordingProvider("devedge")
	e, bootstraps := newTestEnsurer(t, p, false)

	if err := e.EnsureCluster(context.Background(), "devedge"); err != nil {
		t.Fatalf("EnsureCluster: %v", err)
	}
	if c, _ := p.count(); c != 0 {
		t.Errorf("creates = %d, want 0 (already present)", c)
	}
	if *bootstraps != 1 {
		t.Errorf("bootstraps = %d, want 1 (reconcile)", *bootstraps)
	}
}

// CT (T006): failed create → actionable retryable error with no half-created
// leftover (FR-012).
func TestEnsureCluster_failedCreateNoLeftover(t *testing.T) {
	p := newRecordingProvider()
	p.createErr = errors.New("k3d: boom")
	e, _ := newTestEnsurer(t, p, true)

	err := e.EnsureCluster(context.Background(), "devedge")
	if err == nil {
		t.Fatal("expected error on failed create")
	}
	if !strings.Contains(err.Error(), "retry") {
		t.Errorf("error not actionable/retryable: %v", err)
	}
	if _, d := p.count(); d == 0 {
		t.Error("expected best-effort cleanup (Delete) after failed create")
	}
	if p.has("devedge") {
		t.Error("half-created cluster left behind after failed create")
	}
}

// CT (T006): a missing cluster tool surfaces as an actionable error (FR-012).
func TestEnsureCluster_missingTool(t *testing.T) {
	p := newRecordingProvider()
	p.listErr = errors.New("k3d not found in PATH")
	e, _ := newTestEnsurer(t, p, true)

	err := e.EnsureCluster(context.Background(), "devedge")
	if err == nil {
		t.Fatal("expected error when the cluster tool is unavailable")
	}
	if !strings.Contains(err.Error(), "installed") && !strings.Contains(err.Error(), "k3d") {
		t.Errorf("error not actionable: %v", err)
	}
}
