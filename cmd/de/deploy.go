package main

import (
	"context"
	"fmt"

	"github.com/infobloxopen/devedge/internal/cluster"
	"github.com/infobloxopen/devedge/internal/deploy"
	"github.com/infobloxopen/devedge/internal/depruntime"
	"github.com/infobloxopen/devedge/pkg/config"
)

// deployWorkload deploys the service's declared workload onto the resolved cluster
// (005). It is invoked by `de project up --deploy` after the cluster is ensured and
// dependencies are provisioned.
func deployWorkload(res config.Resource, target cluster.ClusterTarget) error {
	w := workloadOf(res)
	if w == nil {
		return fmt.Errorf("--deploy: %q declares no spec.workload to deploy", res.Project())
	}
	hostname := serviceHostname(res)
	if hostname == "" {
		return fmt.Errorf("--deploy: %q has no spec.dev.hostname for routing", res.Project())
	}

	wl := deploy.Workload{
		Service:  res.Project(),
		Port:     w.Port,
		Replicas: w.EffectiveReplicas(),
		Hostname: hostname,
		Deps:     depEnvs(res),
	}
	src := deploy.ImageSource{Image: w.Image}
	if w.Build != nil {
		src.Build = &deploy.BuildSource{Context: w.Build.Context, Dockerfile: w.Build.Dockerfile}
	}

	d := deploy.NewDeployer(target.KubeContext, target.Namespace, target.Name)
	st, err := d.Deploy(context.Background(), wl, src)
	if err != nil {
		return fmt.Errorf("deploy %q: %w", res.Project(), err)
	}
	fmt.Printf("%s %s %s %s\n",
		colorLabel.Sprint("deployed:"),
		colorHost.Sprint(res.Project()),
		colorLabel.Sprintf("-> cluster %s (%d replica(s))", target.Name, st.Replicas),
		"https://"+st.Hostname)
	return nil
}

// removeWorkload uninstalls a deployed service's workload release on `down`. It is
// a no-op when the service declares no workload, and helm uninstall ignores an
// absent release, so it is safe for never-deployed projects.
func removeWorkload(res config.Resource, target cluster.ClusterTarget) error {
	if workloadOf(res) == nil {
		return nil
	}
	d := deploy.NewDeployer(target.KubeContext, target.Namespace, target.Name)
	return d.Remove(context.Background(), res.Project())
}

func workloadOf(res config.Resource) *config.WorkloadSpec {
	if wd, ok := res.(config.WorkloadDeclarer); ok {
		return wd.Workload()
	}
	return nil
}

func serviceHostname(res config.Resource) string {
	if svc, ok := res.(*config.ServiceConfig); ok {
		return svc.Spec.Dev.Hostname
	}
	return ""
}

// depEnvs builds the service-chart dependency env wiring from the resource's
// declared dependencies (matching `de project chart`).
func depEnvs(res config.Resource) []deploy.DepEnv {
	dd, ok := res.(config.DependencyDeclarer)
	if !ok {
		return nil
	}
	deps := dd.Dependencies()
	engineCount := map[string]int{}
	for _, d := range deps {
		engineCount[d.Engine]++
	}
	out := make([]deploy.DepEnv, 0, len(deps))
	for _, d := range deps {
		out = append(out, deploy.DepEnv{
			Name:    d.Name,
			Engine:  d.Engine,
			Version: d.Version,
			EnvVar:  depruntime.EnvVarName(depruntime.Engine(d.Engine), d.Name, engineCount[d.Engine] > 1),
		})
	}
	return out
}
