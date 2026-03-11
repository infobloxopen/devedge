// Package client provides a Go client for the devedged Unix socket API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/infobloxopen/devedge/internal/daemon"
	"github.com/infobloxopen/devedge/pkg/types"
)

// Client communicates with the devedged daemon.
type Client struct {
	http       *http.Client
	socketPath string
}

// New creates a client targeting the given Unix socket path.
func New(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", socketPath)
				},
			},
		},
	}
}

// NewDefault creates a client using the default socket path.
func NewDefault() *Client {
	return New(daemon.DefaultSocketPath())
}

// Status returns the daemon status.
func (c *Client) Status(ctx context.Context) (map[string]any, error) {
	var result map[string]any
	err := c.get(ctx, "/v1/status", &result)
	return result, err
}

// List returns all active routes.
func (c *Client) List(ctx context.Context) ([]types.Route, error) {
	var routes []types.Route
	err := c.get(ctx, "/v1/routes", &routes)
	return routes, err
}

// Lookup returns a route by host.
func (c *Client) Lookup(ctx context.Context, host string) (types.Route, error) {
	var route types.Route
	err := c.get(ctx, "/v1/routes/"+host, &route)
	return route, err
}

// Register creates or renews a route.
func (c *Client) Register(ctx context.Context, req daemon.RegisterRequest) error {
	body, _ := json.Marshal(req)
	resp, err := c.do(ctx, "PUT", "/v1/routes", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated {
		return nil
	}
	return readError(resp)
}

// Deregister removes a route by host.
func (c *Client) Deregister(ctx context.Context, host string) error {
	resp, err := c.do(ctx, "DELETE", "/v1/routes/"+host, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return readError(resp)
}

// DeregisterProject removes all routes for a project.
func (c *Client) DeregisterProject(ctx context.Context, project string) (int, error) {
	resp, err := c.do(ctx, "DELETE", "/v1/projects/"+project, nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, readError(resp)
	}
	var result map[string]int
	json.NewDecoder(resp.Body).Decode(&result)
	return result["removed"], nil
}

func (c *Client) get(ctx context.Context, path string, v any) error {
	resp, err := c.do(ctx, "GET", path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	url := "http://devedge" + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable (is devedged running?): %w", err)
	}
	return resp, nil
}

func readError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("daemon error (%d): %s", resp.StatusCode, bytes.TrimSpace(body))
}
