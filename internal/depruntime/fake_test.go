package depruntime

import (
	"context"
	"fmt"
	"sync"
)

// fakeProvisioner is an in-memory Provisioner for unit tests. It records calls,
// supports idempotency assertions, and can simulate readiness/provision failures.
type fakeProvisioner struct {
	mu sync.Mutex

	instances map[Engine]int  // EnsureInstance call count per engine
	databases map[string]int  // EnsureDatabase call count per binding key
	dropped   map[string]int  // DropDatabase call count per binding key
	readyAfter map[Engine]int // becomes ready after N Ready() calls
	readyCalls map[Engine]int

	failInstance map[Engine]error
	failDatabase map[string]error
	neverReady   map[Engine]bool

	host string
	port map[Engine]int
}

func newFake() *fakeProvisioner {
	return &fakeProvisioner{
		instances: map[Engine]int{}, databases: map[string]int{}, dropped: map[string]int{},
		readyAfter: map[Engine]int{}, readyCalls: map[Engine]int{},
		failInstance: map[Engine]error{}, failDatabase: map[string]error{}, neverReady: map[Engine]bool{},
		host: "dep.dev.test", port: map[Engine]int{EnginePostgres: 5432, EngineRedis: 6379},
	}
}

func bkey(b Binding) string { return string(b.Engine) + "/" + b.Service + "/" + b.Dependency }

func (f *fakeProvisioner) EnsureInstance(_ context.Context, ref InstanceRef) (Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failInstance[ref.Engine]; err != nil {
		return Instance{}, err
	}
	f.instances[ref.Engine]++
	return Instance{Engine: ref.Engine, Host: string(ref.Engine) + "." + f.host, Port: f.port[ref.Engine]}, nil
}

func (f *fakeProvisioner) Ready(_ context.Context, ref InstanceRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.neverReady[ref.Engine] {
		return fmt.Errorf("connection refused")
	}
	f.readyCalls[ref.Engine]++
	if f.readyCalls[ref.Engine] < f.readyAfter[ref.Engine] {
		return fmt.Errorf("not ready yet")
	}
	return nil
}

func (f *fakeProvisioner) EnsureDatabase(_ context.Context, b Binding) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failDatabase[bkey(b)]; err != nil {
		return err
	}
	f.databases[bkey(b)]++
	return nil
}

func (f *fakeProvisioner) DropDatabase(_ context.Context, b Binding) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropped[bkey(b)]++
	return nil
}
