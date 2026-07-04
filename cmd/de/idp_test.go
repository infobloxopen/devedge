package main

import (
	"bytes"
	"encoding/json"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"testing"

	"github.com/infobloxopen/devedge/pkg/types"
)

// runIDP executes `de idp <args...>` against a fresh root command, returning
// combined output and the error. Mirrors runUFE.
func runIDP(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := rootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"idp"}, args...))
	err := root.Execute()
	return buf.String(), err
}

// TestIDPCommandRegistered verifies `de idp` and its subcommands are wired into
// the root command.
func TestIDPCommandRegistered(t *testing.T) {
	root := rootCmd()
	idp, _, err := root.Find([]string{"idp"})
	if err != nil || idp.Name() != "idp" {
		t.Fatalf("`de idp` not registered: %v", err)
	}
	for _, path := range [][]string{
		{"idp", "clients", "sync"},
		{"idp", "up"},
		{"idp", "new"},
	} {
		c, _, err := root.Find(path)
		if err != nil || c.Name() != path[len(path)-1] {
			t.Errorf("`de %v` not registered: %v", path, err)
		}
	}
}

// TestBuildIDPClients covers the full contract: client_id (= app name), dummy
// secret, redirect_uri (from the host), and tile — including grouping a
// multi-route app (shell) into one client and skipping non-HTTP routes.
func TestBuildIDPClients(t *testing.T) {
	routes := []types.Route{
		// A plain service app, no tile: defaults apply.
		{Host: "orders.app.dev.test", Project: "orders", Upstream: "http://127.0.0.1:3000"},
		// A shell app spread over 3 routes with the same project: the tile rides
		// the catch-all; CDN + API routes must NOT create their own tiles.
		{Host: "notesapp.dev.test", Project: "notesapp", Upstream: "http://127.0.0.1:4200",
			Tile: &types.Tile{DisplayName: "Notes", Description: "Take notes", IconURL: "https://cdn/notes.svg", LaunchURL: "https://notesapp.dev.test/home"}},
		{Host: "cdn.dev.test", Path: "/notes", StripPrefix: true, Project: "notesapp", Upstream: "http://127.0.0.1:4201"},
		{Host: "notesapp.dev.test", Path: "/api", StripPrefix: true, Project: "notesapp", Upstream: "http://127.0.0.1:8080"},
		// A TCP dependency: not a launchpad tile.
		{Host: "pg.db.dev.test", Project: "pgdb", Protocol: types.ProtocolTCP, Upstream: "127.0.0.1:5432"},
	}

	clients := buildIDPClients(routes)
	if len(clients) != 2 {
		t.Fatalf("got %d clients, want 2 (orders, notesapp); tcp app must be skipped: %+v", len(clients), clients)
	}

	byID := map[string]idpClient{}
	for _, c := range clients {
		byID[c.ClientID] = c
	}

	// orders: defaulted tile.
	o, ok := byID["orders"]
	if !ok {
		t.Fatalf("missing orders client")
	}
	if o.ClientSecret != "dev-secret-orders" {
		t.Errorf("orders secret = %q, want dev-secret-orders", o.ClientSecret)
	}
	if len(o.RedirectURIs) != 1 || o.RedirectURIs[0] != "https://orders.app.dev.test/callback" {
		t.Errorf("orders redirect_uris = %v", o.RedirectURIs)
	}
	if o.Tile.Name != "Orders" || o.Tile.LaunchURL != "https://orders.app.dev.test/" {
		t.Errorf("orders tile = %+v, want name=Orders launch=https://orders.app.dev.test/", o.Tile)
	}
	if o.Tile.Description != "" || o.Tile.IconURL != "" {
		t.Errorf("orders tile description/icon should default to empty, got %+v", o.Tile)
	}

	// notesapp: one client from 3 routes; declared tile wins.
	n, ok := byID["notesapp"]
	if !ok {
		t.Fatalf("missing notesapp client")
	}
	if n.Tile.Name != "Notes" || n.Tile.Description != "Take notes" || n.Tile.IconURL != "https://cdn/notes.svg" {
		t.Errorf("notesapp tile = %+v, want the declared metadata", n.Tile)
	}
	if n.Tile.LaunchURL != "https://notesapp.dev.test/home" {
		t.Errorf("notesapp launch = %q, want the declared launchURL", n.Tile.LaunchURL)
	}
	// redirect is derived from the tile-bearing (shell) host, not the CDN host.
	if n.RedirectURIs[0] != "https://notesapp.dev.test/callback" {
		t.Errorf("notesapp redirect = %v, want the shell host", n.RedirectURIs)
	}
}

// TestBuildIDPClients_ClientIDFromHost covers the no-project fallback: the app id
// is the host's first DNS label.
func TestBuildIDPClients_ClientIDFromHost(t *testing.T) {
	clients := buildIDPClients([]types.Route{
		{Host: "widgets.app.dev.test", Upstream: "http://127.0.0.1:3000"},
	})
	if len(clients) != 1 || clients[0].ClientID != "widgets" {
		t.Fatalf("got %+v, want one client id=widgets", clients)
	}
}

// TestBuildIDPClients_Idempotent proves the output is a deterministic function of
// the app set: reordering the input yields byte-identical JSON, so re-running the
// sync is a stable merge/replace.
func TestBuildIDPClients_Idempotent(t *testing.T) {
	a := []types.Route{
		{Host: "orders.app.dev.test", Project: "orders", Upstream: "http://127.0.0.1:3000"},
		{Host: "notes.app.dev.test", Project: "notes", Upstream: "http://127.0.0.1:3001"},
	}
	b := []types.Route{a[1], a[0]} // reversed

	ja, _ := json.Marshal(buildIDPClients(a))
	jb, _ := json.Marshal(buildIDPClients(b))
	if !bytes.Equal(ja, jb) {
		t.Errorf("output not order-independent:\n a=%s\n b=%s", ja, jb)
	}
}

// TestSynthClient_ContractShape pins the exact idp-clients.json shape against the
// shared contract, so a drift from what the IdP consumes fails loudly.
func TestSynthClient_ContractShape(t *testing.T) {
	data, err := json.MarshalIndent(
		[]idpClient{synthClient("orders", types.Route{Host: "orders.app.dev.test", Project: "orders"})},
		"", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `[
  {
    "client_id": "orders",
    "client_secret": "dev-secret-orders",
    "redirect_uris": [
      "https://orders.app.dev.test/callback"
    ],
    "tile": {
      "name": "Orders",
      "description": "",
      "icon_url": "",
      "launch_url": "https://orders.app.dev.test/"
    }
  }
]`
	if string(data) != want {
		t.Errorf("contract drift:\n got:\n%s\n want:\n%s", data, want)
	}
}

// TestIDPClientsSync_FromConfig runs the end-to-end sync against a local
// devedge.yaml (no daemon needed — the daemon source is best-effort) and asserts
// the written idp-clients.json carries the expected client, plus idempotency
// (two consecutive runs are byte-identical).
func TestIDPClientsSync_FromConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "devedge.yaml")
	if err := os.WriteFile(cfg, []byte(`apiVersion: devedge.infoblox.dev/v1alpha1
kind: Config
metadata:
  name: orders
spec:
  routes:
    - host: orders.app.dev.test
      upstream: http://127.0.0.1:3000
      tile:
        displayName: Orders
        launchURL: https://orders.app.dev.test/
`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "idp-clients.yaml")

	if _, err := runIDP(t, "clients", "sync", "--config", cfg, "--out", out); err != nil {
		t.Fatalf("sync: %v", err)
	}
	first, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}

	var clients []idpClient
	if err := yaml.Unmarshal(first, &clients); err != nil {
		t.Fatalf("decode out: %v", err)
	}
	var got *idpClient
	for i := range clients {
		if clients[i].ClientID == "orders" {
			got = &clients[i]
		}
	}
	if got == nil {
		t.Fatalf("orders client not in output: %s", first)
	}
	if got.ClientSecret != "dev-secret-orders" ||
		got.RedirectURIs[0] != "https://orders.app.dev.test/callback" ||
		got.Tile.Name != "Orders" || got.Tile.LaunchURL != "https://orders.app.dev.test/" {
		t.Errorf("orders client = %+v", *got)
	}

	// Idempotent: a second run over the same source rewrites identical bytes.
	if _, err := runIDP(t, "clients", "sync", "--config", cfg, "--out", out); err != nil {
		t.Fatalf("sync (2nd): %v", err)
	}
	second, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read out (2nd): %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("sync not idempotent:\n first:\n%s\n second:\n%s", first, second)
	}
}

// TestIDPNew_Emit verifies the thin `de idp new --emit` writes a parseable
// starter devedge.yaml + sample idp-clients.json.
func TestIDPNew_Emit(t *testing.T) {
	dir := t.TempDir()
	out, err := runIDP(t, "new", "--emit", "--dir", dir)
	if err != nil {
		t.Fatalf("idp new --emit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "devedge.yaml")); err != nil {
		t.Errorf("starter devedge.yaml not written: %v", err)
	}
	sample := filepath.Join(dir, "idp-clients.yaml")
	data, err := os.ReadFile(sample)
	if err != nil {
		t.Fatalf("sample idp-clients.yaml not written: %v", err)
	}
	var clients []idpClient
	if err := yaml.Unmarshal(data, &clients); err != nil {
		t.Errorf("sample idp-clients.yaml is not valid: %v", err)
	}
	if len(clients) == 0 {
		t.Errorf("sample idp-clients.yaml is empty; output:\n%s", out)
	}
}
