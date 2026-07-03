// Package echomod is the second minimal servicekit.Module fixture for WS-012 P4
// acceptance (composed alongside greetermod). Same shape: a zero-arg Module()
// constructor, a distinct module ID / gRPC service / route prefix so a two-module
// composition validates conflict-free and boots over the union.
package echomod

import (
	"context"

	"github.com/infobloxopen/devedge-sdk/authz"
	"github.com/infobloxopen/devedge-sdk/servicekit"
)

// ImportPath is this fixture module's import path, used to register it in the
// compose in-process resolver for `de compose tidy`.
const ImportPath = "github.com/infobloxopen/devedge/testdata/composefixtures/echomod"

const (
	methodEcho     = "/echo.v1.EchoService/Echo"
	methodEchoList = "/echo.v1.EchoService/ListEchoes"
)

// Module is the zero-arg constructor the in-process resolver (`de compose tidy` /
// smoke) uses to link this fixture.
func Module() servicekit.Module { return &echoModule{} }

// NewModule is the uniform, resource-agnostic seam a generated composed host
// (`de compose build`) calls across every member. This fixture holds no
// repository, so it ignores the shared DB handle (a *gorm.DB is assignable to the
// `any` parameter) and returns the same module.
func NewModule(any) servicekit.Module { return Module() }

// Models reports this fixture owns no domain models for the host's dev AutoMigrate.
func Models() []any { return nil }

type echoModule struct{}

func (m *echoModule) Descriptor() servicekit.Descriptor {
	return servicekit.Descriptor{
		ID:      "echo",
		Version: "v0.2.0",
		Methods: []string{methodEcho, methodEchoList},
		AuthzRules: []authz.MethodRule{
			{Method: methodEcho, Public: true},
			{Method: methodEchoList, Public: true},
		},
		Routes:    []servicekit.RouteDescriptor{{Prefix: "/api/echo/v1"}},
		Resources: []servicekit.ResourceDescriptor{{Name: "echo.echo", Plural: "echoes"}},
	}
}

func (m *echoModule) Register(_ context.Context, app *servicekit.App) error {
	app.Server.RecordMethods(methodEcho, methodEchoList)
	app.Server.AddRules(m.Descriptor().AuthzRules...)
	return nil
}
