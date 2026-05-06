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

// k8sPodNameLabel is set by the kubelet on every Docker container it creates, regardless
// of the Kubernetes distribution (Docker Desktop, minikube, k3s, kind, …).
// Checking this label catches K8s control-plane containers (kube-apiserver, etcd, …)
// that do not carry the Docker-Desktop-specific k8sContainerLabel.
const k8sPodNameLabel = "io.kubernetes.pod.name"

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
		// Exclude Kubernetes infrastructure containers (Docker Desktop exposes them
		// as regular Docker containers via the Docker API).
		if _, ok := s.Labels[k8sContainerLabel]; ok {
			continue
		}
		// Exclude any container started by the kubelet (standard label across all
		// K8s distributions: Docker Desktop, minikube, k3s, kind, …).
		// This catches control-plane pods (kube-apiserver, etcd, coredns, …) that
		// do not carry the Docker-Desktop-specific k8sContainerLabel.
		if _, ok := s.Labels[k8sPodNameLabel]; ok {
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