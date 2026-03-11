// Package externaldns implements the external-dns webhook provider protocol.
//
// When external-dns runs with --provider=webhook, it calls a webhook server
// to manage DNS records. This package translates those calls into devedge
// route registrations, making Kubernetes Ingress objects automatically
// resolvable on the developer's machine.
//
// Protocol: https://github.com/kubernetes-sigs/external-dns/blob/master/docs/tutorials/webhook-provider.md
package externaldns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// DevedgeClient is the interface for communicating with the devedge daemon.
type DevedgeClient interface {
	RegisterRoute(ctx context.Context, host, upstream string) error
	DeregisterRoute(ctx context.Context, host string) error
}

// HTTPDevedgeClient calls the devedge daemon HTTP API.
type HTTPDevedgeClient struct {
	BaseURL string
	Client  *http.Client
}

// RegisterRoute registers a route via the devedge API.
func (c *HTTPDevedgeClient) RegisterRoute(ctx context.Context, host, upstream string) error {
	body, _ := json.Marshal(map[string]string{
		"host":     host,
		"upstream": upstream,
		"owner":    "external-dns",
		"source":   "external-dns",
	})
	req, _ := http.NewRequestWithContext(ctx, "PUT", c.BaseURL+"/v1/routes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("devedge API error (%d): %s", resp.StatusCode, b)
	}
	return nil
}

// DeregisterRoute removes a route via the devedge API.
func (c *HTTPDevedgeClient) DeregisterRoute(ctx context.Context, host string) error {
	req, _ := http.NewRequestWithContext(ctx, "DELETE", c.BaseURL+"/v1/routes/"+host, nil)
	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("devedge API error (%d): %s", resp.StatusCode, b)
	}
	return nil
}

// Endpoint matches the external-dns Endpoint type.
type Endpoint struct {
	DNSName    string   `json:"dnsName"`
	Targets    []string `json:"targets"`
	RecordType string   `json:"recordType"`
	RecordTTL  int      `json:"recordTTL,omitempty"`
}

// Changes matches the external-dns plan.Changes type.
type Changes struct {
	Create    []*Endpoint `json:"Create"`
	UpdateNew []*Endpoint `json:"UpdateNew"`
	UpdateOld []*Endpoint `json:"UpdateOld"`
	Delete    []*Endpoint `json:"Delete"`
}

// Webhook implements the external-dns webhook provider HTTP API.
type Webhook struct {
	client DevedgeClient
	domain string // only manage records under this domain (e.g. "dev.test")
	logger *slog.Logger
	mux    *http.ServeMux
}

// NewWebhook creates a webhook server.
func NewWebhook(client DevedgeClient, domain string, logger *slog.Logger) *Webhook {
	w := &Webhook{
		client: client,
		domain: domain,
		logger: logger,
		mux:    http.NewServeMux(),
	}

	// external-dns webhook protocol endpoints.
	w.mux.HandleFunc("GET /", w.negotiate)
	w.mux.HandleFunc("GET /records", w.getRecords)
	w.mux.HandleFunc("POST /records", w.applyChanges)
	w.mux.HandleFunc("POST /adjustendpoints", w.adjustEndpoints)

	return w
}

// Handler returns the http.Handler.
func (w *Webhook) Handler() http.Handler {
	return w.mux
}

// negotiate returns the domain filter for external-dns.
func (w *Webhook) negotiate(rw http.ResponseWriter, r *http.Request) {
	// Return the domain filter so external-dns only sends us relevant records.
	resp := map[string]any{
		"domainFilter": map[string]any{
			"filters": []string{w.domain},
		},
	}
	rw.Header().Set("Content-Type", "application/external.dns.webhook+json;version=1")
	json.NewEncoder(rw).Encode(resp)
}

// getRecords returns current records. For devedge, we return empty since
// the daemon is the source of truth and external-dns doesn't need to diff
// against our state.
func (w *Webhook) getRecords(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Content-Type", "application/external.dns.webhook+json;version=1")
	json.NewEncoder(rw).Encode([]Endpoint{})
}

// applyChanges processes create/update/delete changes from external-dns.
func (w *Webhook) applyChanges(rw http.ResponseWriter, r *http.Request) {
	var changes Changes
	if err := json.NewDecoder(r.Body).Decode(&changes); err != nil {
		http.Error(rw, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Process creates.
	for _, ep := range changes.Create {
		if err := w.upsertEndpoint(ctx, ep); err != nil {
			w.logger.Error("create failed", "host", ep.DNSName, "err", err)
		}
	}

	// Process updates (apply the new version).
	for _, ep := range changes.UpdateNew {
		if err := w.upsertEndpoint(ctx, ep); err != nil {
			w.logger.Error("update failed", "host", ep.DNSName, "err", err)
		}
	}

	// Process deletes.
	for _, ep := range changes.Delete {
		if err := w.deleteEndpoint(ctx, ep); err != nil {
			w.logger.Error("delete failed", "host", ep.DNSName, "err", err)
		}
	}

	rw.WriteHeader(http.StatusNoContent)
}

// adjustEndpoints is called by external-dns to let the provider normalize
// endpoints. We return them as-is.
func (w *Webhook) adjustEndpoints(rw http.ResponseWriter, r *http.Request) {
	var endpoints []*Endpoint
	json.NewDecoder(r.Body).Decode(&endpoints)
	rw.Header().Set("Content-Type", "application/external.dns.webhook+json;version=1")
	json.NewEncoder(rw).Encode(endpoints)
}

func (w *Webhook) upsertEndpoint(ctx context.Context, ep *Endpoint) error {
	if !w.isManaged(ep.DNSName) {
		return nil
	}

	// For A records, the target is an IP. For devedge, we register the hostname
	// pointing to loopback. The actual upstream doesn't matter for DNS-only
	// records; devedge's /etc/hosts handles resolution. But if the Ingress also
	// has a known service, external-dns may provide it as a target.
	w.logger.Info("upsert", "host", ep.DNSName, "type", ep.RecordType, "targets", ep.Targets)

	// Register as a route pointing to the k3d ingress (loopback).
	// The actual upstream will be the cluster ingress.
	upstream := "http://127.0.0.1:80"
	if len(ep.Targets) > 0 && strings.Contains(ep.Targets[0], ":") {
		upstream = ep.Targets[0]
	}

	return w.client.RegisterRoute(ctx, ep.DNSName, upstream)
}

func (w *Webhook) deleteEndpoint(ctx context.Context, ep *Endpoint) error {
	if !w.isManaged(ep.DNSName) {
		return nil
	}
	w.logger.Info("delete", "host", ep.DNSName)
	return w.client.DeregisterRoute(ctx, ep.DNSName)
}

func (w *Webhook) isManaged(name string) bool {
	return strings.HasSuffix(name, "."+w.domain) || name == w.domain
}
