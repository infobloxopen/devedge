package compose

import (
	"context"
	"fmt"

	"github.com/infobloxopen/devedge-sdk/servicekit"
	"github.com/infobloxopen/devedge/pkg/config"
)

// SmokeResult is the outcome of an in-process composition smoke (the boot path
// servicekittest.AssertComposition uses: descriptor union validation + a host
// boot over the union with the fail-closed completeness gate, no real DB).
type SmokeResult struct {
	// Resolved are the import paths smoke-booted in-process.
	Resolved []string
	// Unresolved are the import paths the resolver could not link (not booted).
	Unresolved []string
	// RealDBRan reports whether the real-DB migration path executed. Always false
	// here: the in-process smoke runs no migrations + no shared DB (no Docker).
	// The real-DB path runs only from the generated cmd/<name> smoke test where a
	// MigrationRunner + Database are supplied (gated on Docker).
	RealDBRan bool
}

// Smoke runs the descriptor-union validation + the in-process host boot gate for a
// composition's RESOLVABLE member modules (those linked into the running process).
// It is the non-test analogue of servicekittest.AssertComposition's in-process
// path: it validates the union and boots the composed host with a cancelled
// context so server.Serve runs the fail-closed completeness gate over the union
// without binding a listener. Unresolved external members are reported, not booted
// (they smoke-test from the generated cmd/<name> module where they are linked).
func Smoke(c *config.Composition, r ModuleResolver) (SmokeResult, error) {
	if r == nil {
		r = RegistryResolver{}
	}
	refs, err := ResolveModuleRefs(c)
	if err != nil {
		return SmokeResult{}, err
	}

	var res SmokeResult
	var mods []servicekit.Module
	for _, ref := range refs {
		m, ok := r.Resolve(ref.ImportPath)
		if !ok {
			res.Unresolved = append(res.Unresolved, ref.ImportPath)
			continue
		}
		res.Resolved = append(res.Resolved, ref.ImportPath)
		mods = append(mods, m)
	}

	if len(mods) == 0 {
		return res, nil
	}

	// Descriptor-union validation (reuses the SDK source of truth).
	if err := servicekit.ValidateModules(mods); err != nil {
		return res, fmt.Errorf("composition validation: %w", err)
	}

	// In-process host boot with a cancelled context: server.Serve runs the
	// fail-closed union completeness gate, then returns immediately (no listener).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := servicekit.Run(servicekit.HostConfig{
		Modules:  mods,
		GRPCAddr: ":0",
		Context:  ctx,
	}); err != nil {
		return res, fmt.Errorf("composition boot gate: %w", err)
	}
	return res, nil
}
