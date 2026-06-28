// Package futuremod is a servicekit.Module fixture that requires a FUTURE SDK
// version, for the WS-012 P4 `de compose tidy` version-incompatibility
// acceptance: composing it against the current host SDK must be flagged
// incompatible by servicekittest.CompatibleModules.
package futuremod

import (
	"context"

	"github.com/infobloxopen/devedge-sdk/authz"
	"github.com/infobloxopen/devedge-sdk/servicekit"
)

// ImportPath is this fixture's import path, for the compose in-process resolver.
const ImportPath = "github.com/infobloxopen/devedge/testdata/composefixtures/futuremod"

const methodFuture = "/future.v1.FutureService/Do"

// Module is the zero-arg constructor the resolver registers.
func Module() servicekit.Module { return &futureModule{} }

type futureModule struct{}

func (m *futureModule) Descriptor() servicekit.Descriptor {
	return servicekit.Descriptor{
		ID:         "future",
		Version:    "v9.9.9",
		Methods:    []string{methodFuture},
		AuthzRules: []authz.MethodRule{{Method: methodFuture, Public: true}},
		Routes:     []servicekit.RouteDescriptor{{Prefix: "/api/future/v1"}},
		// Requires an SDK far newer than the host — an impossible constraint.
		Requires: servicekit.Compatibility{SDK: ">=99.0.0"},
	}
}

func (m *futureModule) Register(_ context.Context, app *servicekit.App) error {
	app.Server.RecordMethods(methodFuture)
	app.Server.AddRules(m.Descriptor().AuthzRules...)
	return nil
}
