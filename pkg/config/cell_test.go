package config

import (
	"testing"
)

const validCell = `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Cell
metadata:
  name: myapi-canary
spec:
  service: myapi
  image: registry/myapi:v1.2.0
  replicas: 2
  cell: canary
  defaultCell: default
  controllerClass: ""
`

func TestParseCell_Valid(t *testing.T) {
	c, err := ParseCell([]byte(validCell))
	if err != nil {
		t.Fatalf("ParseCell: %v", err)
	}
	if c.Project() != "myapi-canary" {
		t.Errorf("Project() = %q, want myapi-canary", c.Project())
	}
	if c.Spec.Service != "myapi" {
		t.Errorf("Service = %q, want myapi", c.Spec.Service)
	}
	if c.Spec.Cell != "canary" {
		t.Errorf("Cell = %q, want canary", c.Spec.Cell)
	}
	if c.Spec.Replicas != 2 {
		t.Errorf("Replicas = %d, want 2", c.Spec.Replicas)
	}

	routes, err := c.ToRoutes()
	if err != nil {
		t.Fatalf("ToRoutes: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("ToRoutes: want empty, got %d routes", len(routes))
	}
}

func TestParseCell_DefaultReplicas(t *testing.T) {
	input := []byte(`apiVersion: devedge.infoblox.dev/v1alpha1
kind: Cell
metadata:
  name: svc-v2
spec:
  service: svc
  cell: v2
  image: svc:v2
`)
	c, err := ParseCell(input)
	if err != nil {
		t.Fatalf("ParseCell: %v", err)
	}
	if c.Spec.Replicas != 1 {
		t.Errorf("default Replicas = %d, want 1", c.Spec.Replicas)
	}
}

func TestMarshalCell_RoundTrip(t *testing.T) {
	c, err := ParseCell([]byte(validCell))
	if err != nil {
		t.Fatalf("ParseCell: %v", err)
	}
	data, err := MarshalCell(c)
	if err != nil {
		t.Fatalf("MarshalCell: %v", err)
	}
	c2, err := ParseCell(data)
	if err != nil {
		t.Fatalf("ParseCell round-trip: %v", err)
	}
	if c2.Project() != c.Project() {
		t.Errorf("Project mismatch: %q vs %q", c2.Project(), c.Project())
	}
	if c2.Spec.Service != c.Spec.Service {
		t.Errorf("Service mismatch: %q vs %q", c2.Spec.Service, c.Spec.Service)
	}
	if c2.Spec.Cell != c.Spec.Cell {
		t.Errorf("Cell mismatch: %q vs %q", c2.Spec.Cell, c.Spec.Cell)
	}
	if c2.Spec.Image != c.Spec.Image {
		t.Errorf("Image mismatch: %q vs %q", c2.Spec.Image, c.Spec.Image)
	}
	if c2.Spec.Replicas != c.Spec.Replicas {
		t.Errorf("Replicas mismatch: %d vs %d", c2.Spec.Replicas, c.Spec.Replicas)
	}
}

func TestParseCell_MissingService(t *testing.T) {
	input := []byte(`apiVersion: devedge.infoblox.dev/v1alpha1
kind: Cell
metadata:
  name: test
spec:
  cell: canary
  image: img:v1
`)
	_, err := ParseCell(input)
	if err == nil {
		t.Fatal("expected error for missing spec.service")
	}
}

func TestParseCell_MissingCell(t *testing.T) {
	input := []byte(`apiVersion: devedge.infoblox.dev/v1alpha1
kind: Cell
metadata:
  name: test
spec:
  service: svc
  image: img:v1
`)
	_, err := ParseCell(input)
	if err == nil {
		t.Fatal("expected error for missing spec.cell")
	}
}

func TestParseResource_Cell_dispatch(t *testing.T) {
	res, err := ParseResource([]byte(validCell))
	if err != nil {
		t.Fatalf("ParseResource: %v", err)
	}
	cell, ok := res.(*Cell)
	if !ok {
		t.Fatalf("ParseResource returned %T, want *Cell", res)
	}
	if cell.Project() != "myapi-canary" {
		t.Errorf("Project() = %q, want myapi-canary", cell.Project())
	}
}
