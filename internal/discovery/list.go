package discovery

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"

	"github.com/errorprobe/errorprobe/internal/docker"
)

// ManagedLabel is the Docker label set on all ErrorProbe-managed containers.
// Must match the value set in internal/stack/up.go managedLabel().
const ManagedLabel = "managed-by"

// ManagedLabelValue is the value that identifies an ErrorProbe-managed container.
const ManagedLabelValue = "errorprobe"

// k8sContainerLabel is set by Docker Desktop on containers that back Kubernetes pods.
// We exclude these because they are managed by K8s, not the user's Docker Compose / CLI.
const k8sContainerLabel = "io.kubernetes.docker.type"

// k8sLabelPrefix is the standard prefix used by the kubelet and all Kubernetes
// distributions (Docker Desktop, minikube, k3s, kind, …) to annotate the
// Docker containers they create.  Checking for the presence of ANY key with
// this prefix is the most reliable way to identify kubelet-managed containers
// regardless of which specific labels the distribution happens to set.
const k8sLabelPrefix = "io.kubernetes."

// hasKubernetesLabel returns true if any label key on a container starts with
// the standard Kubernetes label prefix.  This covers control-plane pods
// (kube-apiserver, etcd, coredns, …) across all K8s distributions.
func hasKubernetesLabel(labels map[string]string) bool {
	for k := range labels {
		if strings.HasPrefix(k, k8sLabelPrefix) {
			return true
		}
		if k == k8sContainerLabel {
			return true
		}
	}
	return false
}

// ListRunning returns all running user containers, excluding any containers
// managed by ErrorProbe (those with the label managed-by=errorprobe).
// RestartCount and InfraStatus are populated via a separate inspect call.
func ListRunning(ctx context.Context, dockerClient docker.DockerAPI) ([]ContainerMeta, error) {
	args := filters.NewArgs(filters.Arg("status", "running"))
	summaries, err := dockerClient.ContainerList(ctx, container.ListOptions{Filters: args})
	if err != nil {
		return nil, fmt.Errorf("listing running containers: %w", err)
	}

	var out []ContainerMeta
	for _, s := range summaries {
		// Exclude ErrorProbe-managed containers.
		if s.Labels[ManagedLabel] == ManagedLabelValue {
			continue
		}
		// Exclude any container bearing a Kubernetes orchestration label.
		// Checking the "io.kubernetes." prefix covers all kubelet-managed containers
		// (kube-apiserver, etcd, coredns, …) across Docker Desktop, minikube, k3s,
		// kind, and similar distributions — regardless of which specific label each
		// distribution sets.
		if hasKubernetesLabel(s.Labels) {
			continue
		}

		name := containerName(s.Names)

		meta := ContainerMeta{
			ID:          s.ID,
			Name:        name,
			Image:       s.Image,
			Labels:      s.Labels,
			InfraStatus: s.State,
			Runtime:     "docker",
		}

		// Enrich with inspect data for RestartCount, StartedAt, and mounts.
		info, err := dockerClient.ContainerInspect(ctx, s.ID)
		if err == nil && info.ContainerJSONBase != nil {
			meta.RestartCount = info.RestartCount
			if info.State != nil {
				meta.InfraStatus = strings.ToLower(info.State.Status)
				if t, err := time.Parse(time.RFC3339Nano, info.State.StartedAt); err == nil {
					meta.StartedAt = t
				}
			}
			for _, m := range info.Mounts {
				meta.Mounts = append(meta.Mounts, MountInfo{
					Type:        string(m.Type),
					Name:        m.Name,
					Source:      m.Source,
					Destination: m.Destination,
					ReadOnly:    !m.RW,
				})
			}
		}

		out = append(out, meta)
	}
	return out, nil
}

// containerName returns the friendly name from the Docker names list.
// Docker prefixes names with "/", which is stripped here.
func containerName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}