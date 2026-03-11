// Package daemon implements the devedged control plane HTTP API served
// over a Unix domain socket.
package daemon

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/infobloxopen/devedge/internal/registry"
	"github.com/infobloxopen/devedge/pkg/types"
)

// API exposes the route registry over HTTP.
type API struct {
	reg    *registry.Registry
	logger *slog.Logger
	mux    *http.ServeMux
}

// NewAPI creates an HTTP API backed by the given registry.
func NewAPI(reg *registry.Registry, logger *slog.Logger) *API {
	a := &API{reg: reg, logger: logger, mux: http.NewServeMux()}
	a.mux.HandleFunc("GET /v1/routes", a.listRoutes)
	a.mux.HandleFunc("GET /v1/routes/{host}", a.getRoute)
	a.mux.HandleFunc("PUT /v1/routes", a.registerRoute)
	a.mux.HandleFunc("DELETE /v1/routes/{host}", a.deregisterRoute)
	a.mux.HandleFunc("DELETE /v1/projects/{project}", a.deregisterProject)
	a.mux.HandleFunc("GET /v1/status", a.status)
	addUIRoutes(a.mux, reg)
	return a
}

// Handler returns the http.Handler for the API.
func (a *API) Handler() http.Handler {
	return a.mux
}

// RegisterRequest is the JSON body for route registration.
type RegisterRequest struct {
	Host     string `json:"host"`
	Upstream string `json:"upstream"`
	Project  string `json:"project,omitempty"`
	Owner    string `json:"owner,omitempty"`
	TTL      string `json:"ttl,omitempty"`
}

func (a *API) listRoutes(w http.ResponseWriter, r *http.Request) {
	routes := a.reg.List()
	writeJSON(w, http.StatusOK, routes)
}

func (a *API) getRoute(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	route, ok := a.reg.Lookup(host)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, route)
}

func (a *API) registerRoute(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Host == "" || req.Upstream == "" {
		http.Error(w, "host and upstream are required", http.StatusBadRequest)
		return
	}

	route := types.Route{
		Host:     req.Host,
		Upstream: req.Upstream,
		Project:  req.Project,
		Owner:    req.Owner,
		Source:   "api",
	}

	if req.TTL != "" {
		d, err := parseDuration(req.TTL)
		if err != nil {
			http.Error(w, "invalid ttl: "+err.Error(), http.StatusBadRequest)
			return
		}
		route.TTL = d
	}

	if err := a.reg.Register(route); err != nil {
		a.logger.Warn("register conflict", "host", req.Host, "err", err)
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	a.logger.Info("route registered", "host", req.Host, "upstream", req.Upstream)
	writeJSON(w, http.StatusCreated, route)
}

func (a *API) deregisterRoute(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if !a.reg.Deregister(host) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	a.logger.Info("route deregistered", "host", host)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) deregisterProject(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	n := a.reg.DeregisterProject(project)
	a.logger.Info("project deregistered", "project", project, "routes_removed", n)
	writeJSON(w, http.StatusOK, map[string]int{"removed": n})
}

func (a *API) status(w http.ResponseWriter, r *http.Request) {
	routes := a.reg.List()
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "running",
		"routes": len(routes),
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
