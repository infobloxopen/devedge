// Package dupmod is a deliberately-CONFLICTING servicekit.Module fixture for the
// WS-012 P4 `de compose tidy` conflict-detection acceptance: it declares the SAME
// HTTP route prefix as greetermod ("/api/greeter/v1"), so composing the two must
// be rejected by servicekit.ValidateModules with a duplicate-prefix conflict.
package dupmod

import (
	"context"

	"github.com/infobloxopen/devedge-sdk/authz"
	"github.com/infobloxopen/devedge-sdk/servicekit"
)

// ImportPath is this fixture's import path, for the compose in-process resolver.
const ImportPath = "github.com/infobloxopen/devedge/testdata/composefixtures/dupmod"

const methodDup = "/dup.v1.DupService/Do"

// Module is the zero-arg constructor the resolver registers.
func Module() servicekit.Module { return &dupModule{} }

type dupModule struct{}

func (m *dupModule) Descriptor() servicekit.Descriptor {
	return servicekit.Descriptor{
		ID:         "dup",
		Version:    "v0.1.0",
		Methods:    []string{methodDup},
		AuthzRules: []authz.MethodRule{{Method: methodDup, Public: true}},
		// SAME prefix as greetermod -> a duplicate-route-prefix conflict.
		Routes: []servicekit.RouteDescriptor{{Prefix: "/api/greeter/v1"}},
	}
}

func (m *dupModule) Register(_ context.Context, app *servicekit.App) error {
	app.Server.RecordMethods(methodDup)
	app.Server.AddRules(m.Descriptor().AuthzRules...)
	return nil
}
