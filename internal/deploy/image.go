package deploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// DockerK3dBuilder builds images with `docker build` and loads them into the k3d
// cluster with `k3d image import` (no external registry). A pre-built reference
// passes through unchanged.
type DockerK3dBuilder struct{}

// EnsureImage returns the image to deploy. For a reference source it returns the
// reference as-is. For a build source it builds the image and imports it into the
// named k3d cluster, returning the built tag.
func (DockerK3dBuilder) EnsureImage(ctx context.Context, src ImageSource, clusterName string) (string, error) {
	if src.Build == nil {
		return src.Image, nil
	}
	b := src.Build
	if b.Tag == "" {
		return "", fmt.Errorf("build image tag not set")
	}
	dockerfile := b.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}

	build := exec.CommandContext(ctx, "docker", "build",
		"-t", b.Tag, "-f", filepath.Join(b.Context, dockerfile), b.Context)
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return "", fmt.Errorf("docker build %s: %w", b.Tag, err)
	}

	imp := exec.CommandContext(ctx, "k3d", "image", "import", b.Tag, "-c", clusterName)
	imp.Stdout = os.Stderr
	imp.Stderr = os.Stderr
	if err := imp.Run(); err != nil {
		return "", fmt.Errorf("k3d image import %s into %s: %w", b.Tag, clusterName, err)
	}
	return b.Tag, nil
}

// buildTag derives the deterministic local tag devedge builds a service to.
func buildTag(serviceSlug string) string {
	return "devedge-local/" + serviceSlug + ":dev"
}
