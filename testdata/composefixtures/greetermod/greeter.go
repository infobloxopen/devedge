// Package greetermod is a minimal, real servicekit.Module fixture for WS-012 P4
// acceptance: it exposes the zero-arg Module() constructor the proposal's
// composed main.go calls (orders.Module() / billing.Module()). It declares its
// gRPC methods + public authz rules on the shared server so the host boot gate
// passes over the composed union — a faithful, dependency-light stand-in for a
// generated module (no grpc/proto codegen needed to prove composition + boot).
package greetermod

import (
	"context"

	"github.com/infobloxopen/devedge-sdk/authz"
	"github.com/infobloxopen/devedge-sdk/servicekit"
)

// ImportPath is this fixture module's import path, used to register it in the
// compose in-process resolver for `de compose tidy`.
const ImportPath = "github.com/infobloxopen/devedge/testdata/composefixtures/greetermod"

const (
	methodSayHello = "/greeter.v1.GreeterService/SayHello"
	methodListHi   = "/greeter.v1.GreeterService/ListGreetings"
)

// Module is the zero-arg constructor the generated composed main.go calls.
func Module() servicekit.Module { return &greeterModule{} }

type greeterModule struct{}

func (m *greeterModule) Descriptor() servicekit.Descriptor {
	return servicekit.Descriptor{
		ID:      "greeter",
		Version: "v0.1.0",
		Methods: []string{methodSayHello, methodListHi},
		// Public exemptions so the host's fail-closed completeness gate passes
		// without an authorizer (every method has a rule).
		AuthzRules: []authz.MethodRule{
			{Method: methodSayHello, Public: true},
			{Method: methodListHi, Public: true},
		},
		Routes:    []servicekit.RouteDescriptor{{Prefix: "/api/greeter/v1"}},
		Resources: []servicekit.ResourceDescriptor{{Name: "greeter.greeting", Plural: "greetings"}},
	}
}

// Register wires the module onto the shared server: it records its methods + rules
// so the union completeness gate sees them. A generated module would here call
// Register<Svc>WithRepository (gRPC + gateway + authz); this fixture records the
// surface directly to stay dependency-light while exercising the same gate.
func (m *greeterModule) Register(_ context.Context, app *servicekit.App) error {
	app.Server.RecordMethods(methodSayHello, methodListHi)
	app.Server.AddRules(m.Descriptor().AuthzRules...)
	return nil
}
