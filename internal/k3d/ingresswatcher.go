// IngressWatcher provides a lightweight alternative to external-dns for
// automatically registering Kubernetes Ingress hostnames with devedge.
//
// It shells out to kubectl to watch Ingress resources and registers/deregisters
// hostnames as they appear and disappear. This avoids pulling in the full
// client-go dependency while still providing automatic Ingress-to-devedge sync.
package k3d

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// IngressWatcherConfig configures the Ingress watcher.
type IngressWatcherConfig struct {
	Context     string // kubectl context
	Namespace   string // namespace to watch, "" for all
	DevedgeURL  string // devedge daemon API URL
	IngressPort string // host port for the k3d ingress (e.g. "8081")
	Logger      *slog.Logger
}

// ingressEvent is a minimal representation of a kubectl watch event.
type ingressEvent struct {
	Type   string `json:"type"`
	Object struct {
		Metadata struct {
			Name        string            `json:"name"`
			Namespace   string            `json:"namespace"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
		Spec struct {
			Rules []struct {
				Host string `json:"host"`
			} `json:"rules"`
		} `json:"spec"`
	} `json:"object"`
}

// WatchIngresses starts watching Kubernetes Ingress objects and registers
// their hostnames with the devedge daemon. It blocks until the context is
// cancelled.
func WatchIngresses(ctx context.Context, cfg IngressWatcherConfig) error {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.IngressPort == "" {
		cfg.IngressPort = "80"
	}

	args := []string{"get", "ingress", "--watch", "-o", "json"}
	if cfg.Context != "" {
		args = append([]string{"--context", cfg.Context}, args...)
	}
	if cfg.Namespace != "" {
		args = append(args, "-n", cfg.Namespace)
	} else {
		args = append(args, "--all-namespaces")
	}

	cfg.Logger.Info("starting ingress watcher", "context", cfg.Context)

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start kubectl watch: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	// kubectl --watch -o json outputs one JSON object per line.
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "{") {
			continue
		}

		var event ingressEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			cfg.Logger.Warn("parse ingress event", "err", err)
			continue
		}

		// Check for opt-in annotation.
		if event.Object.Metadata.Annotations["devedge.io/expose"] != "true" {
			continue
		}

		for _, rule := range event.Object.Spec.Rules {
			if rule.Host == "" {
				continue
			}

			switch event.Type {
			case "ADDED", "MODIFIED":
				upstream := fmt.Sprintf("http://127.0.0.1:%s", cfg.IngressPort)
				cfg.Logger.Info("registering ingress host",
					"host", rule.Host,
					"upstream", upstream,
					"ingress", event.Object.Metadata.Name,
				)
				registerViaHTTP(ctx, cfg.DevedgeURL, rule.Host, upstream)

			case "DELETED":
				cfg.Logger.Info("deregistering ingress host",
					"host", rule.Host,
					"ingress", event.Object.Metadata.Name,
				)
				deregisterViaHTTP(ctx, cfg.DevedgeURL, rule.Host)
			}
		}
	}

	return cmd.Wait()
}

// registerViaHTTP calls the devedge daemon to register a route.
func registerViaHTTP(ctx context.Context, baseURL, host, upstream string) {
	body := fmt.Sprintf(`{"host":%q,"upstream":%q,"owner":"ingress-watcher","source":"k8s-ingress"}`, host, upstream)
	req, _ := newHTTPRequest(ctx, "PUT", baseURL+"/v1/routes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpDo(req)
	if err != nil {
		slog.Error("register failed", "host", host, "err", err)
		return
	}
	resp.Body.Close()
}

// deregisterViaHTTP calls the devedge daemon to remove a route.
func deregisterViaHTTP(ctx context.Context, baseURL, host string) {
	req, _ := newHTTPRequest(ctx, "DELETE", baseURL+"/v1/routes/"+host, nil)
	resp, err := httpDo(req)
	if err != nil {
		slog.Error("deregister failed", "host", host, "err", err)
		return
	}
	resp.Body.Close()
}
