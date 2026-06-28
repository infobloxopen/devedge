package compose_test

import (
	"testing"

	"github.com/infobloxopen/devedge-sdk/servicekit"
	"github.com/infobloxopen/devedge-sdk/servicekittest"
	"github.com/infobloxopen/devedge/testdata/composefixtures/echomod"
	"github.com/infobloxopen/devedge/testdata/composefixtures/greetermod"
)

// SC-003: `de compose test` invokes servicekittest.AssertComposition against the
// composition's modules. This test runs that harness directly against the two
// fixture modules — the same call `de compose test` documents — proving the
// composition boots over the union and shuts down cleanly.
//
// Real-DB path: NOT exercised here. The fixtures declare no migrations and the
// composition declares no shared database, so AssertComposition runs entirely
// in-process (no Docker, no testcontainers). The real-DB path runs only when a
// composition declares a shared DB + the modules carry migrations (then the
// generated smoke test supplies a MigrationRunner + Database, gated on Docker).
func TestAssertComposition_Fixtures_InProcess(t *testing.T) {
	mods := []servicekit.Module{greetermod.Module(), echomod.Module()}
	servicekittest.AssertComposition(t, mods)
}

// AssertCompatible against the host runtime the SDK ships at — the fixtures carry
// no SDK floor, so they are always compatible.
func TestAssertCompatible_Fixtures(t *testing.T) {
	mods := []servicekit.Module{greetermod.Module(), echomod.Module()}
	servicekittest.AssertCompatible(t, mods, servicekittest.HostRequires{
		SDK: "v0.28.0",
		Go:  "1.26.0",
	})
}
