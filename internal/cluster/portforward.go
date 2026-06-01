package cluster

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// forwardingRe matches kubectl's "Forwarding from 127.0.0.1:<port> -> <remote>".
var forwardingRe = regexp.MustCompile(`Forwarding from 127\.0\.0\.1:(\d+)`)

// PortForward is a supervised `kubectl port-forward` bound to an ephemeral
// 127.0.0.1 port. It is how a host-run service reaches an in-cluster dependency:
// the assigned LocalPort goes into the dependency's DSN (the indirect DSN hides
// the dynamic port from the app). Lives until Stop.
type PortForward struct {
	LocalPort int

	cancel context.CancelFunc
	mu     sync.Mutex
	done   bool
}

// StartPortForward forwards target's remotePort to an ephemeral local port and
// returns once kubectl reports the forward is established (bounded by a timeout).
// target is a kubectl reference such as "statefulset/devedge-postgres".
func StartPortForward(kubeContext, namespace, target string, remotePort int) (*PortForward, error) {
	ctx, cancel := context.WithCancel(context.Background())

	args := []string{}
	if kubeContext != "" {
		args = append(args, "--context", kubeContext)
	}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, "port-forward", target, fmt.Sprintf(":%d", remotePort), "--address", "127.0.0.1")

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start port-forward to %s: %w", target, err)
	}

	pf := &PortForward{cancel: cancel}
	go func() { _ = cmd.Wait(); pf.markDone() }()

	portCh := make(chan int, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if m := forwardingRe.FindStringSubmatch(sc.Text()); m != nil {
				p, _ := strconv.Atoi(m[1])
				select {
				case portCh <- p:
				default:
				}
			}
		}
	}()

	select {
	case p := <-portCh:
		pf.LocalPort = p
		return pf, nil
	case <-time.After(20 * time.Second):
		cancel()
		return nil, fmt.Errorf("port-forward to %s did not establish in time: %s", target, stderr.String())
	}
}

func (pf *PortForward) markDone() {
	pf.mu.Lock()
	pf.done = true
	pf.mu.Unlock()
}

// Alive reports whether the forward process is still running.
func (pf *PortForward) Alive() bool {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	return !pf.done
}

// Stop terminates the forward.
func (pf *PortForward) Stop() { pf.cancel() }
