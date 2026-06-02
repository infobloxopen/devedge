// Package deploy installs a service's workload onto a resolved cluster (005). It is
// the portable orchestration — resolve the image, render + install the embedded
// service Helm chart, wait for Ready — with image build/load isolated behind the
// ImageBuilder adapter and cluster ops behind helm (Principle IV).
package deploy

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/infobloxopen/devedge/internal/cluster"
	"github.com/infobloxopen/devedge/internal/helm"
)

// DepEnv is a dependency's env-var wiring for the service chart.
type DepEnv struct {
	Name    string
	Engine  string
	Version string
	EnvVar  string
}

// Workload is the resolved deployable form of a service.
type Workload struct {
	Service  string // service name (slugged for cluster resources)
	Port     int
	Replicas int
	Hostname string // dev hostname for the Ingress
	Deps     []DepEnv
}

// ImageSource is where the workload image comes from: a pre-built reference, or a
// build from the project (FR-011).
type ImageSource struct {
	Image string
	Build *BuildSource
}

// BuildSource describes a project build. Tag is the image tag devedge builds to
// (set by the Deployer when empty).
type BuildSource struct {
	Context    string
	Dockerfile string
	Tag        string
}

// ImageBuilder resolves an ImageSource into an image reference runnable on the
// cluster: a reference passes through; a build is built and loaded into the cluster.
type ImageBuilder interface {
	EnsureImage(ctx context.Context, src ImageSource, clusterName string) (string, error)
}

// Status is the observable outcome of a deploy (FR-009).
type Status struct {
	Release  string
	Image    string
	Replicas int
	Hostname string
}

// Deployer installs a service workload onto a resolved cluster via the embedded
// service Helm chart.
type Deployer struct {
	Helm        *helm.Helm
	Builder     ImageBuilder
	Namespace   string
	ClusterName string
	Logger      *slog.Logger // observability (resolve/deploy/teardown); defaults to slog.Default()
}

// NewDeployer targets the resolved cluster's context + dependency namespace.
func NewDeployer(kubeContext, namespace, clusterName string) *Deployer {
	return &Deployer{
		Helm:        helm.New(kubeContext),
		Builder:     DockerK3dBuilder{},
		Namespace:   namespace,
		ClusterName: clusterName,
	}
}

func (d *Deployer) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}

// Deploy resolves the image, then renders + installs the service chart and waits
// for the workload to be Ready (`helm upgrade --install --wait`). Idempotent — a
// re-deploy converges the release with no duplicate workload (FR-005).
func (d *Deployer) Deploy(ctx context.Context, w Workload, src ImageSource) (Status, error) {
	release := cluster.ProjectSlug(w.Service)
	if src.Build != nil && src.Build.Tag == "" {
		src.Build.Tag = buildTag(release)
	}
	d.log().Info("resolving workload image", "service", w.Service, "cluster", d.ClusterName, "build", src.Build != nil)
	image, err := d.Builder.EnsureImage(ctx, src, d.ClusterName)
	if err != nil {
		return Status{}, fmt.Errorf("resolve image: %w", err)
	}
	d.log().Info("deploying workload", "service", w.Service, "release", release, "image", image, "cluster", d.ClusterName)
	values := chartValues(release, image, w.Port, w.Replicas, w.Hostname, w.Deps)
	if err := d.Helm.Install(ctx, helm.ChartService, release, d.Namespace, values); err != nil {
		return Status{}, fmt.Errorf("deploy workload %q: %w", w.Service, err)
	}
	d.log().Info("workload deployed", "service", w.Service, "release", release, "hostname", w.Hostname)
	return Status{Release: release, Image: image, Replicas: effectiveReplicas(w.Replicas), Hostname: w.Hostname}, nil
}

// Remove uninstalls the service's workload release (footprint-only — FR-006).
func (d *Deployer) Remove(ctx context.Context, service string) error {
	d.log().Info("removing workload", "service", service, "release", cluster.ProjectSlug(service), "cluster", d.ClusterName)
	return d.Helm.Uninstall(ctx, cluster.ProjectSlug(service), d.Namespace)
}

// chartValues builds the service-chart values (contracts/cli-and-chart.md).
func chartValues(name, image string, port, replicas int, hostname string, deps []DepEnv) map[string]any {
	dv := make([]map[string]any, 0, len(deps))
	for _, d := range deps {
		dv = append(dv, map[string]any{
			"name":    d.Name,
			"engine":  d.Engine,
			"version": d.Version,
			"envVar":  d.EnvVar,
		})
	}
	return map[string]any{
		"service": map[string]any{
			"name":     name,
			"image":    image,
			"port":     port,
			"replicas": effectiveReplicas(replicas),
			"hostname": hostname,
			// Skip the prod-abstraction DependencyClaim CR in dev: there is no CRD
			// in a local cluster, and the in-cluster DSN Secret is written directly.
			"noDependencyClaims": true,
		},
		"dependencies": dv,
	}
}

func effectiveReplicas(r int) int {
	if r <= 0 {
		return 1
	}
	return r
}
