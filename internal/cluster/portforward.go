package cluster

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// PortForward is a supervised client-go port-forward bound to an ephemeral
// 127.0.0.1 port. It is how a host-run service reaches an in-cluster dependency:
// the assigned LocalPort goes into the dependency's DSN (the indirect DSN hides
// the dynamic port from the app). Lives until Stop.
type PortForward struct {
	LocalPort int

	stopCh   chan struct{}
	stopOnce sync.Once
	mu       sync.Mutex
	done     bool
}

// Stop terminates the forward. Safe to call more than once.
func (pf *PortForward) Stop() {
	pf.stopOnce.Do(func() { close(pf.stopCh) })
}

// Alive reports whether the forward is still running.
func (pf *PortForward) Alive() bool {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	return !pf.done
}

func (pf *PortForward) markDone() {
	pf.mu.Lock()
	pf.done = true
	pf.mu.Unlock()
}

// parsePortForwardTarget converts a kubectl-style target reference to the pod
// name to forward to. Only "statefulset/<name>" is supported — it maps to the
// StatefulSet's first replica "<name>-0". All other formats return an error.
func parsePortForwardTarget(target string) (string, error) {
	const prefix = "statefulset/"
	if !strings.HasPrefix(target, prefix) {
		return "", fmt.Errorf("unsupported port-forward target %q: only statefulset/<name> is supported", target)
	}
	name := strings.TrimPrefix(target, prefix)
	if name == "" {
		return "", fmt.Errorf("unsupported port-forward target %q: statefulset name is empty", target)
	}
	return name + "-0", nil
}

// StartPortForward establishes a port-forward to remotePort on the pod resolved
// from target (format: "statefulset/<name>"), using client-go's native SPDY
// portforwarder. No kubectl subprocess is spawned.
//
// kubeContext selects the kube config context; empty uses the current context.
// namespace is the pod's namespace.
// Returns once the forward is established with LocalPort set.
func StartPortForward(kubeContext, namespace, target string, remotePort int) (*PortForward, error) {
	pod, err := parsePortForwardTarget(target)
	if err != nil {
		return nil, err
	}

	// Load REST config from the default kubeconfig rules (respects $KUBECONFIG).
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	if kubeContext != "" {
		overrides.CurrentContext = kubeContext
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig for port-forward to %s: %w", target, err)
	}

	// Build the SPDY dialer targeting the pod's portforward subresource.
	rawURL := fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s/portforward", cfg.Host, namespace, pod)
	pfURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse portforward URL: %w", err)
	}

	roundTripper, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("build SPDY transport for port-forward to %s: %w", target, err)
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, pfURL)

	stopCh := make(chan struct{})
	readyCh := make(chan struct{}, 1)

	pfw, err := portforward.New(
		dialer,
		[]string{fmt.Sprintf("0:%d", remotePort)},
		stopCh,
		readyCh,
		io.Discard,
		io.Discard,
	)
	if err != nil {
		close(stopCh)
		return nil, fmt.Errorf("create port-forward to %s: %w", target, err)
	}

	pf := &PortForward{stopCh: stopCh}

	fwdErr := make(chan error, 1)
	go func() {
		err := pfw.ForwardPorts()
		pf.markDone()
		fwdErr <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	select {
	case <-readyCh:
		// Forward is established; read the OS-assigned local port.
		ports, err := pfw.GetPorts()
		if err != nil || len(ports) == 0 {
			pf.Stop()
			return nil, fmt.Errorf("port-forward to %s: could not read assigned port: %v", target, err)
		}
		pf.LocalPort = int(ports[0].Local)
		return pf, nil

	case err := <-fwdErr:
		if err == nil {
			err = fmt.Errorf("port-forward exited before ready")
		}
		return nil, fmt.Errorf("port-forward to %s failed: %w", target, err)

	case <-ctx.Done():
		pf.Stop()
		return nil, fmt.Errorf("port-forward to %s did not establish within 30s", target)
	}
}
