package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/pkg/config"
	"github.com/infobloxopen/devedge/pkg/types"
)

const (
	// defaultIDPHost is where `de idp up` routes the dev IdP through the edge.
	defaultIDPHost = "idp.dev.test"
	// defaultIDPPort is the local HTTP port the reference devedge-idp app listens
	// on; `de idp up` builds the default upstream from it.
	defaultIDPPort = 8080
	// defaultClientsFile is where `de idp clients sync` writes the IdP clients
	// file when --out is omitted.
	defaultClientsFile = "idp-clients.json"
	// idpRepo is the reference dev IdP application `de idp` orchestrates around.
	idpRepo = "github.com/infobloxopen/devedge-idp"
)

// idpCmd is `de idp`, the discovery/registration substrate for the dev IdP
// launchpad (WS-026). The IdP itself is a separate application (devedge-idp);
// this verb group discovers registered devedge apps and hands the IdP the set
// of OAuth2 clients + tiles it should show, and routes the IdP through the edge.
func idpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "idp",
		Short: "Discover apps for and route the dev IdP launchpad",
		Long: `Manage the dev identity-provider launchpad (WS-026).

The dev IdP is a passwordless, Okta-style login + app-tile launchpad. It is a
separate application (` + idpRepo + `); this verb group is the
discovery/registration substrate around it:

  de idp clients sync   discover registered apps and write idp-clients.json
  de idp up             route the IdP through the edge at ` + defaultIDPHost + `
  de idp new            guidance for standing up the reference IdP app

The launchpad shows one tile per registered devedge app, discovered from the
edge route registry + kind:Shell rosters. An app declares how it appears by
adding optional 'tile' metadata to its route (devedge.yaml) or kind:Shell.`,
	}
	cmd.AddCommand(idpClientsCmd(), idpUpCmd(), idpNewCmd())
	return cmd
}

// idpClientsCmd is the `de idp clients` noun, parent of `sync`.
func idpClientsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clients",
		Short: "Manage the dev IdP's OAuth2 clients",
	}
	cmd.AddCommand(idpClientsSyncCmd())
	return cmd
}

// idpClientsSyncCmd is `de idp clients sync` — the keystone. It discovers apps
// from the daemon route registry (+ any local kind:Shell / devedge.yaml) and
// rewrites the IdP clients file from that discovery. It is idempotent: the file
// is a pure function of the current app set, so re-running with the same apps
// produces byte-identical output.
func idpClientsSyncCmd() *cobra.Command {
	var out, configPath string

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Discover devedge apps and write the IdP clients file",
		Long: `Discover registered devedge apps and write the IdP clients file.

Apps are discovered from the ` + "`de`" + ` daemon's route registry (GET /v1/routes)
plus, if present, a local devedge.yaml and/or kind:Shell roster in the working
directory. If the daemon is not running, discovery falls back to the local
config alone (and says so). If a source is unavailable, whatever remains is used.

For each app the sync emits one OAuth2 client the IdP reads:

  - client_id     the app name (route project, else the host's first label)
  - client_secret a guessable dev dummy ("dev-secret-<name>")
  - redirect_uris the app's BFF callback ("https://<host>/callback")
  - tile          launchpad presentation: name (the app's tile displayName, or a
                  title-cased app name), description, icon_url, and launch_url
                  ("https://<host>/" unless the tile overrides it)

Only HTTP apps become tiles (TCP routes — databases, etc. — are skipped). The
output rewrites --out in full (idempotent merge/replace from current discovery).

Examples:
  de idp clients sync
  de idp clients sync --out ../devedge-idp/idp-clients.json
  de idp clients sync --config shell.yaml`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()

			routes, sources := gatherIDPRoutes(cmd, configPath)
			if len(routes) == 0 {
				return fmt.Errorf("no apps discovered: the daemon is not running and no local devedge.yaml/shell.yaml was found — run `de project up`, or pass --config")
			}

			clients := buildIDPClients(routes)
			if len(clients) == 0 {
				return fmt.Errorf("discovered %d route(s) but none are HTTP apps eligible for a launchpad tile", len(routes))
			}

			data, err := json.MarshalIndent(clients, "", "  ")
			if err != nil {
				return fmt.Errorf("encode clients: %w", err)
			}
			data = append(data, '\n')
			if err := os.WriteFile(out, data, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", out, err)
			}

			fmt.Fprintf(w, "%s %s %s\n",
				colorSuccess.Sprintf("wrote %d client(s) to", len(clients)),
				colorHost.Sprint(out),
				colorLabel.Sprintf("(source: %s)", strings.Join(sources, ", ")),
			)
			for _, cl := range clients {
				fmt.Fprintf(w, "  %s %s %s\n", colorHost.Sprint(cl.ClientID), colorLabel.Sprint("->"), cl.Tile.LaunchURL)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", defaultClientsFile, "path to write the IdP clients file")
	cmd.Flags().StringVar(&configPath, "config", "", "a local devedge.yaml/kind:Shell to include alongside the daemon; defaults to auto-detecting devedge.yaml and shell.yaml in the working dir")
	return cmd
}

// gatherIDPRoutes collects the routes to discover apps from: the daemon route
// registry (best-effort) plus any local config files. Every source is
// best-effort — an unavailable one is logged to stderr and skipped, so `sync`
// works whether the daemon is up, down, or the app is only described locally.
func gatherIDPRoutes(cmd *cobra.Command, configPath string) (routes []types.Route, sources []string) {
	warn := cmd.ErrOrStderr()

	// 1. The daemon's live route registry.
	if dr, err := newClient().List(context.Background()); err != nil {
		fmt.Fprintf(warn, "%s daemon route registry unavailable (%v); using local config only\n", colorWarning.Sprint("note:"), err)
	} else if len(dr) > 0 {
		routes = append(routes, dr...)
		sources = append(sources, "daemon")
	}

	// 2. Local config files (devedge.yaml / kind:Shell). Grouping by app in
	// buildIDPClients dedupes apps that appear in both the daemon and a file.
	var files []string
	if configPath != "" {
		files = []string{configPath}
	} else {
		for _, f := range []string{"devedge.yaml", "shell.yaml"} {
			if _, err := os.Stat(f); err == nil {
				files = append(files, f)
			}
		}
	}
	for _, f := range files {
		res, err := config.LoadResource(f)
		if err != nil {
			fmt.Fprintf(warn, "%s skipping %s: %v\n", colorWarning.Sprint("note:"), f, err)
			continue
		}
		rr, err := res.ToRoutes()
		if err != nil {
			fmt.Fprintf(warn, "%s skipping %s: %v\n", colorWarning.Sprint("note:"), f, err)
			continue
		}
		routes = append(routes, rr...)
		sources = append(sources, f)
	}
	return routes, sources
}

// idpTile is the launchpad presentation for a client in idp-clients.json. Its
// fields serialize without omitempty so the object always carries all four keys
// (empty string when unset), matching the shared contract the IdP consumes.
type idpTile struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IconURL     string `json:"icon_url"`
	LaunchURL   string `json:"launch_url"`
}

// idpClient is one OAuth2 client entry in idp-clients.json (the shared contract
// the dev IdP reads).
type idpClient struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	RedirectURIs []string `json:"redirect_uris"`
	Tile         idpTile  `json:"tile"`
}

// buildIDPClients synthesizes the IdP client set from discovered routes. It
// groups routes into apps (by route project, else the host's first label), picks
// each app's front-door route, and emits one client per app. Only HTTP apps are
// eligible (a launchpad tile is something you open in a browser). Output is
// sorted by client_id so the file is deterministic and the sync idempotent.
func buildIDPClients(routes []types.Route) []idpClient {
	type acc struct {
		id    string
		best  types.Route
		score int
	}
	groups := map[string]*acc{}
	var order []string

	for _, r := range routes {
		if r.EffectiveProtocol() != types.ProtocolHTTP {
			continue // TCP routes (databases, gRPC) are not launchpad tiles
		}
		id := deriveClientID(r)
		if id == "" {
			continue
		}
		// Prefer the route the app declared a tile on, then a catch-all (the app
		// root), then anything. A declared tile always wins, so its host drives
		// launch/redirect and its metadata is the tile shown.
		score := 0
		if r.Tile != nil {
			score += 100
		}
		if r.Path == "" {
			score += 10
		}
		if g, ok := groups[id]; ok {
			if score > g.score {
				g.best, g.score = r, score
			}
			continue
		}
		groups[id] = &acc{id: id, best: r, score: score}
		order = append(order, id)
	}

	clients := make([]idpClient, 0, len(order))
	for _, id := range order {
		clients = append(clients, synthClient(groups[id].id, groups[id].best))
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].ClientID < clients[j].ClientID })
	return clients
}

// synthClient builds one client entry from an app's id and its front-door route.
func synthClient(id string, primary types.Route) idpClient {
	host := primary.Host
	name := titleCaseName(id)
	launch := fmt.Sprintf("https://%s/", host)
	desc, icon := "", ""
	if t := primary.Tile; t != nil {
		if t.DisplayName != "" {
			name = t.DisplayName
		}
		if t.LaunchURL != "" {
			launch = t.LaunchURL
		}
		desc, icon = t.Description, t.IconURL
	}
	return idpClient{
		ClientID:     id,
		ClientSecret: "dev-secret-" + id,
		RedirectURIs: []string{fmt.Sprintf("https://%s/callback", host)},
		Tile:         idpTile{Name: name, Description: desc, IconURL: icon, LaunchURL: launch},
	}
}

// deriveClientID names the app a route belongs to: its project when set,
// otherwise the first DNS label of its host (e.g. "orders" from
// "orders.app.dev.test").
func deriveClientID(r types.Route) string {
	if r.Project != "" {
		return r.Project
	}
	if i := strings.IndexByte(r.Host, '.'); i > 0 {
		return r.Host[:i]
	}
	return r.Host
}

// titleCaseName turns an app id into a default tile label: "orders" -> "Orders",
// "notes-app" -> "Notes App".
func titleCaseName(s string) string {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
	for i, f := range fields {
		fields[i] = strings.ToUpper(f[:1]) + f[1:]
	}
	if len(fields) == 0 {
		return s
	}
	return strings.Join(fields, " ")
}

// idpUpCmd is `de idp up` — route the dev IdP through the edge. A thin wrapper
// over the same route registration `de register` uses; it does NOT build or run
// the IdP binary (that lives in devedge-idp).
func idpUpCmd() *cobra.Command {
	var host, upstream string
	var port int

	cmd := &cobra.Command{
		Use:   "up",
		Short: "Route the dev IdP through the edge at idp.dev.test",
		Long: `Register a route so the dev IdP is served at ` + defaultIDPHost + ` through the
local devedge edge.

This is a thin wrapper over the same route registration ` + "`de register`" + ` uses; it
does NOT build or run the IdP binary — the reference IdP application lives in
` + idpRepo + `. Start the IdP there first (default :` + fmt.Sprint(defaultIDPPort) + `), then route it:

  1. run the IdP:      git clone https://` + idpRepo + ` && cd devedge-idp && make run
  2. sync its clients: de idp clients sync --out ./idp-clients.json
  3. route it:         de idp up

The browser then reaches the IdP at https://` + defaultIDPHost + `/.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if upstream == "" {
				upstream = fmt.Sprintf("http://127.0.0.1:%d", port)
			}
			if err := newClient().Register(context.Background(), daemon.RegisterRequest{
				Host:     host,
				Upstream: upstream,
				Project:  "idp",
				Owner:    "de-idp",
			}); err != nil {
				return fmt.Errorf("register %s: %w", host, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s %s\n",
				colorSuccess.Sprint("routed dev IdP"), colorHost.Sprint(host), colorLabel.Sprint("->"), upstream)
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", defaultIDPHost, "hostname to serve the IdP at")
	cmd.Flags().IntVar(&port, "port", defaultIDPPort, "local IdP HTTP port (builds the default upstream)")
	cmd.Flags().StringVar(&upstream, "upstream", "", "explicit upstream URL (overrides --port)")
	return cmd
}

// idpNewCmd is `de idp new` — deliberately thin. The full OIDC provider is the
// devedge-idp repo's job; this prints guidance and, with --emit, drops a starter
// devedge.yaml + sample idp-clients.json so the wiring is obvious.
func idpNewCmd() *cobra.Command {
	var dir string
	var emit bool

	cmd := &cobra.Command{
		Use:   "new",
		Short: "Guidance for standing up the dev IdP (see devedge-idp)",
		Long: `Point at the reference dev IdP application and, optionally, emit starter files.

This command is intentionally thin: it does NOT scaffold a whole OIDC provider.
The reference dev IdP (passwordless picker + Okta-style tile launchpad) is a
complete application in ` + idpRepo + `; clone and run that. This
CLI's job is discovery/registration around it:

  de idp clients sync   feed the IdP the app clients + tiles to show
  de idp up             route the IdP through the edge at ` + defaultIDPHost + `

With --emit, a starter devedge.yaml (routing ` + defaultIDPHost + ` -> :` + fmt.Sprint(defaultIDPPort) + `) and a
sample idp-clients.json are written into --dir so the file shapes are concrete.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "%s\n", colorHeader.Sprint("The dev IdP is a standalone application."))
			fmt.Fprintf(w, "Clone and run the reference implementation:\n")
			fmt.Fprintf(w, "  git clone https://%s\n", idpRepo)
			fmt.Fprintf(w, "  cd devedge-idp && make run    %s\n\n", colorLabel.Sprintf("# serves on :%d", defaultIDPPort))
			fmt.Fprintf(w, "Then wire it into the edge from your project:\n")
			fmt.Fprintf(w, "  de idp up                     %s\n", colorLabel.Sprintf("# route %s -> the IdP", defaultIDPHost))
			fmt.Fprintf(w, "  de idp clients sync           %s\n", colorLabel.Sprint("# discover apps -> idp-clients.json"))

			if !emit {
				fmt.Fprintf(w, "\n%s pass --emit to write a starter devedge.yaml + sample idp-clients.json\n", colorLabel.Sprint("tip:"))
				return nil
			}

			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", dir, err)
			}
			cfgPath := filepath.Join(dir, "devedge.yaml")
			if err := os.WriteFile(cfgPath, []byte(starterIDPConfig), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", cfgPath, err)
			}
			sample, err := json.MarshalIndent([]idpClient{
				synthClient("orders", types.Route{Host: "orders.app.dev.test", Project: "orders"}),
			}, "", "  ")
			if err != nil {
				return err
			}
			sample = append(sample, '\n')
			samplePath := filepath.Join(dir, defaultClientsFile)
			if err := os.WriteFile(samplePath, sample, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", samplePath, err)
			}

			fmt.Fprintf(w, "\n%s %s\n", colorSuccess.Sprint("wrote"), colorHost.Sprint(cfgPath))
			fmt.Fprintf(w, "%s %s\n", colorSuccess.Sprint("wrote"), colorHost.Sprint(samplePath))
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "directory to write starter files into (with --emit)")
	cmd.Flags().BoolVar(&emit, "emit", false, "also write a starter devedge.yaml + sample idp-clients.json")
	return cmd
}

// starterIDPConfig is the devedge.yaml `de idp new --emit` drops: a kind:Config
// routing the IdP host to its local port, carrying a launchpad tile so the IdP
// shows up as a tile itself if you sync from this file.
const starterIDPConfig = `apiVersion: devedge.infoblox.dev/v1alpha1
kind: Config
metadata:
  name: idp
spec:
  routes:
    - host: ` + defaultIDPHost + `
      upstream: http://127.0.0.1:8080
      tile:
        displayName: Dev IdP
        description: Passwordless dev identity provider
        launchURL: https://` + defaultIDPHost + `/
`
