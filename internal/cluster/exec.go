package cluster

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// KubectlExec runs a command inside a workload's pod via `kubectl exec`, used to
// provision per-service isolation (psql / redis-cli) inside a shared dependency
// instance. target is a pod or controller reference resolvable by kubectl, e.g.
// "statefulset/devedge-postgres". An empty kubeContext uses the current context.
//
// stdin, when non-empty, is piped to the command (kubectl exec -i).
func KubectlExec(ctx context.Context, kubeContext, namespace, target, stdin string, cmdArgs ...string) (string, error) {
	args := []string{}
	if kubeContext != "" {
		args = append(args, "--context", kubeContext)
	}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, "exec")
	if stdin != "" {
		args = append(args, "-i")
	}
	args = append(args, target, "--")
	args = append(args, cmdArgs...)

	var stdout, stderr bytes.Buffer
	c := exec.CommandContext(ctx, "kubectl", args...)
	if stdin != "" {
		c.Stdin = bytes.NewReader([]byte(stdin))
	}
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return stdout.String(), fmt.Errorf("kubectl exec %s: %w: %s", target, err, stderr.String())
	}
	return stdout.String(), nil
}
