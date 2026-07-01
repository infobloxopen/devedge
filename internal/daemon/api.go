// Package daemon implements the devedged control plane HTTP API served
// over a Unix domain socket.
package daemon

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"sync"

	"github.com/infobloxopen/devedge/internal/cluster"
	"github.com/infobloxopen/devedge/internal/depruntime"
	"github.com/infobloxopen/devedge/internal/registry"
	"github.com/infobloxopen/devedge/pkg/types"
)

// TLSStatus describes which CA the proxy signs per-host TLS certificates
// with. Mode is "mkcert" when leafs chain to the locally-trusted mkcert root
// and "self-signed" when the proxy fell back to an ephemeral CA that no
// client trusts (issue #8). It is part of GET /v1/status so `de status` and
// `de doctor` can flag the fallback instead of reporting healthy.
type TLSStatus struct {
	Mode   string `json:"mode"`             // "mkcert" or "self-signed"
	CARoot string `json:"caroot,omitempty"` // resolved CAROOT when Mode == "mkcert"
	Reason string `json:"reason,omitempty"` // why the proxy fell back, when self-signed
}

// doctorTools are the CLI tools the toolchain check resolves. Kept in sync
// with platform.daemonTools (this package cannot import internal/platform:
// platform imports daemon).
var doctorTools = []string{"helm", "kubectl", "k3d", "docker", "mkcert"}

// ToolInfo describes one tool in the daemon's toolchain check.
type ToolInfo struct {
	Name  string `json:"name"`
	Found bool   `json:"found"`
	Path  string `json:"path,omitempty"` // absolute path when Found
}

// ToolchainResponse is the body returned by GET /v1/doctor/toolchain. It
// reports the tools the daemon can resolve and the PATH it searched, so
// callers see the daemon's execution environment — not the shell's (issue #9).
type ToolchainResponse struct {
	Tools        []ToolInfo `json:"tools"`
	PathSearched string     `json:"path_searched"`
}

// API exposes the route registry over HTTP.
type API struct {
	reg    *registry.Registry
	deps   *DepManager
	logger *slog.Logger
	mux    *http.ServeMux

	mu  sync.RWMutex
	tls *TLSStatus // nil until the server reports the proxy's CA state
}

// NewAPI creates an HTTP API backed by the given registry and (optional)
// dependency manager. The route endpoints are unchanged; dependency endpoints
// are additive (a nil deps manager disables them with 501).
func NewAPI(reg *registry.Registry, deps *DepManager, logger *slog.Logger) *API {
	a := &API{reg: reg, deps: deps, logger: logger, mux: http.NewServeMux()}
	a.mux.HandleFunc("GET /v1/routes", a.listRoutes)
	a.mux.HandleFunc("GET /v1/routes/{host}", a.getRoute)
	a.mux.HandleFunc("PUT /v1/routes", a.registerRoute)
	a.mux.HandleFunc("DELETE /v1/routes/{host}", a.deregisterRoute)
	a.mux.HandleFunc("DELETE /v1/projects/{project}", a.deregisterProject)
	a.mux.HandleFunc("GET /v1/status", a.status)
	// Dependency runtime (additive; route API above is unchanged).
	a.mux.HandleFunc("PUT /v1/services/{service}/dependencies", a.applyDependencies)
	a.mux.HandleFunc("GET /v1/services/{service}/dependencies", a.getDependencies)
	a.mux.HandleFunc("DELETE /v1/services/{service}/dependencies", a.releaseDependencies)
	// Doctor: toolchain check from the daemon's vantage (the daemon's real
	// PATH/HOME under launchd, not the invoking shell's).
	a.mux.HandleFunc("GET /v1/doctor/toolchain", a.toolchainCheck)
	addUIRoutes(a.mux, reg)
	return a
}

// toolchainCheck runs exec.LookPath for each daemon tool using the daemon's
// runtime PATH. This is the canonical way to know whether the daemon can
// actually exec helm/kubectl/k3d/docker — the user's shell resolving them
// says nothing about the launchd environment.
func (a *API) toolchainCheck(w http.ResponseWriter, r *http.Request) {
	resp := ToolchainResponse{
		PathSearched: os.Getenv("PATH"),
		Tools:        make([]ToolInfo, 0, len(doctorTools)),
	}
	for _, name := range doctorTools {
		info := ToolInfo{Name: name}
		if p, err := exec.LookPath(name); err == nil {
			info.Found = true
			info.Path = p
		}
		resp.Tools = append(resp.Tools, info)
	}
	writeJSON(w, http.StatusOK, resp)
}

// DependencyRequest is one declared dependency in an ApplyRequest.
type DependencyRequest struct {
	Name      string `json:"name"`
	Engine    string `json:"engine"`
	Version   string `json:"version,omitempty"`
	Port      int    `json:"port,omitempty"`
	Dedicated bool   `json:"dedicated,omitempty"` // FR-016: isolated per-service instance
	// Migrations/Seed are absolute host paths resolved CLI-side from the project
	// root (006). The daemon runs on the same host, so the paths are valid here.
	Migrations string `json:"migrations,omitempty"`
	Seed       string `json:"seed,omitempty"`
}

// ApplyRequest is the PUT .../dependencies body: the declared dependencies plus
// the resolved cluster target they should be provisioned against (004). An empty
// KubeContext preserves the pre-topology behavior (current kube context).
type ApplyRequest struct {
	KubeContext string `json:"kubeContext,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	// Environment is the CLI-resolved operating mode ("dev"/"ephemeral"); it gates the dev
	// seed step (skipped in ephemeral/CI, FR-013). Empty defaults to dev. The daemon cannot
	// detect CI itself (DEVEDGE_ENV lives in the `de` process), so the CLI passes it.
	Environment  string              `json:"environment,omitempty"`
	Dependencies []DependencyRequest `json:"dependencies"`
}

func (a *API) applyDependencies(w http.ResponseWriter, r *http.Request) {
	if a.deps == nil {
		http.Error(w, "dependency runtime not enabled", http.StatusNotImplemented)
		return
	}
	var req ApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	deps := make([]depruntime.Dep, len(req.Dependencies))
	for i, q := range req.Dependencies {
		deps[i] = depruntime.Dep{Name: q.Name, Engine: depruntime.Engine(q.Engine), Version: q.Version, Port: q.Port, Dedicated: q.Dedicated, Migrations: q.Migrations, Seed: q.Seed}
	}
	target := Target{KubeContext: req.KubeContext, Namespace: req.Namespace}
	env := cluster.Environment(req.Environment)
	if env == "" {
		env = cluster.EnvDev
	}
	results := a.deps.Apply(r.Context(), r.PathValue("service"), target, deps, env)
	writeJSON(w, http.StatusOK, results)
}

func (a *API) getDependencies(w http.ResponseWriter, r *http.Request) {
	if a.deps == nil {
		http.Error(w, "dependency runtime not enabled", http.StatusNotImplemented)
		return
	}
	writeJSON(w, http.StatusOK, a.deps.Get(r.PathValue("service")))
}

func (a *API) releaseDependencies(w http.ResponseWriter, r *http.Request) {
	if a.deps == nil {
		http.Error(w, "dependency runtime not enabled", http.StatusNotImplemented)
		return
	}
	clean := r.URL.Query().Get("clean") == "true"
	if err := a.deps.Release(r.Context(), r.PathValue("service"), clean); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Handler returns the http.Handler for the API.
func (a *API) Handler() http.Handler {
	return a.mux
}

// RegisterRequest is the JSON body for route registration.
type RegisterRequest struct {
	Host        string `json:"host"`
	Upstream    string `json:"upstream"`
	Protocol    string `json:"protocol,omitempty"`     // "http" (default) or "tcp"
	BackendTLS  bool   `json:"backend_tls,omitempty"`  // TLS to upstream
	Path        string `json:"path,omitempty"`         // URL path prefix; "" = host catch-all
	StripPrefix bool   `json:"strip_prefix,omitempty"` // trim Path before forwarding
	Project     string `json:"project,omitempty"`
	Owner       string `json:"owner,omitempty"`
	TTL         string `json:"ttl,omitempty"`
}

func (a *API) listRoutes(w http.ResponseWriter, r *http.Request) {
	routes := a.reg.List()
	writeJSON(w, http.StatusOK, routes)
}

func (a *API) getRoute(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	// An optional ?path= narrows the lookup to a specific (host, path) route;
	// otherwise the host's catch-all (or its "/" match) is returned.
	var route types.Route
	var ok bool
	if p := r.URL.Query().Get("path"); p != "" {
		route, ok = a.reg.Lookup(host, p)
	} else {
		route, ok = a.reg.Lookup(host)
	}
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
		Host:        req.Host,
		Upstream:    req.Upstream,
		Protocol:    types.Protocol(req.Protocol),
		BackendTLS:  req.BackendTLS,
		Path:        req.Path,
		StripPrefix: req.StripPrefix,
		Project:     req.Project,
		Owner:       req.Owner,
		Source:      "api",
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
	// An optional ?path= removes just that one (host, path) route; without it,
	// every route registered under the host is removed.
	var removed bool
	if p := r.URL.Query().Get("path"); p != "" {
		removed = a.reg.Deregister(host, p)
	} else {
		removed = a.reg.Deregister(host)
	}
	if !removed {
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

// SetTLSStatus records the proxy's CA state for the status endpoint. The
// server calls it once at startup, after the proxy has loaded (or failed to
// load) the mkcert CA.
func (a *API) SetTLSStatus(st TLSStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tls = &st
}

func (a *API) tlsStatus() *TLSStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.tls
}

func (a *API) status(w http.ResponseWriter, r *http.Request) {
	routes := a.reg.List()
	st := map[string]any{
		"status": "running",
		"routes": len(routes),
	}
	if tls := a.tlsStatus(); tls != nil {
		st["tls"] = tls
	}
	writeJSON(w, http.StatusOK, st)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
