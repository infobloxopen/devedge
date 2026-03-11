package daemon

import (
	"html/template"
	"net/http"
	"time"

	"github.com/infobloxopen/devedge/internal/registry"
)

// addUIRoutes registers the web UI handler on the given mux.
func addUIRoutes(mux *http.ServeMux, reg *registry.Registry) {
	mux.HandleFunc("GET /ui", func(w http.ResponseWriter, r *http.Request) {
		routes := reg.List()
		type viewRoute struct {
			Host      string
			Upstream  string
			Project   string
			Source    string
			Owner     string
			TTL       string
			CreatedAt string
			RenewedAt string
		}

		var data []viewRoute
		for _, rt := range routes {
			ttl := "none"
			if rt.TTL > 0 {
				ttl = rt.TTL.String()
			}
			data = append(data, viewRoute{
				Host:      rt.Host,
				Upstream:  rt.Upstream,
				Project:   rt.Project,
				Source:    rt.Source,
				Owner:     rt.Owner,
				TTL:       ttl,
				CreatedAt: rt.CreatedAt.Format(time.RFC3339),
				RenewedAt: rt.RenewedAt.Format(time.RFC3339),
			})
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		uiTemplate.Execute(w, data)
	})
}

var uiTemplate = template.Must(template.New("ui").Parse(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>Devedge Dashboard</title>
<style>
  body { font-family: system-ui, sans-serif; margin: 2rem; background: #f8f9fa; }
  h1 { color: #212529; }
  table { border-collapse: collapse; width: 100%; background: white; box-shadow: 0 1px 3px rgba(0,0,0,.1); }
  th, td { padding: 0.75rem 1rem; text-align: left; border-bottom: 1px solid #dee2e6; }
  th { background: #343a40; color: white; font-weight: 500; }
  tr:hover { background: #f1f3f5; }
  .empty { color: #868e96; font-style: italic; padding: 2rem; text-align: center; }
  code { background: #e9ecef; padding: 0.15rem 0.4rem; border-radius: 3px; font-size: 0.9em; }
  .badge { display: inline-block; padding: 0.15rem 0.5rem; border-radius: 3px; font-size: 0.8em; }
  .badge-project { background: #d0ebff; color: #1864ab; }
  .badge-source { background: #e3faeb; color: #2b8a3e; }
</style>
</head>
<body>
<h1>Devedge Dashboard</h1>
{{if .}}
<table>
<thead>
<tr>
  <th>Host</th>
  <th>Upstream</th>
  <th>Project</th>
  <th>Source</th>
  <th>Owner</th>
  <th>TTL</th>
  <th>Created</th>
  <th>Renewed</th>
</tr>
</thead>
<tbody>
{{range .}}
<tr>
  <td><code>{{.Host}}</code></td>
  <td><code>{{.Upstream}}</code></td>
  <td>{{if .Project}}<span class="badge badge-project">{{.Project}}</span>{{end}}</td>
  <td>{{if .Source}}<span class="badge badge-source">{{.Source}}</span>{{end}}</td>
  <td>{{.Owner}}</td>
  <td>{{.TTL}}</td>
  <td>{{.CreatedAt}}</td>
  <td>{{.RenewedAt}}</td>
</tr>
{{end}}
</tbody>
</table>
{{else}}
<div class="empty">No active routes. Register one with <code>de register HOST UPSTREAM</code></div>
{{end}}
</body>
</html>
`))
