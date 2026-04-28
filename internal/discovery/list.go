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
const ManagedLabel = "com.errorprobe.managed"

// ListRunning returns all running user containers, excluding any containers
// managed by ErrorProbe (those with the label com.errorprobe.managed=true).
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
		if s.Labels[ManagedLabel] == "true" {
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

		// Enrich with inspect data for RestartCount and StartedAt.
		info, err := dockerClient.ContainerInspect(ctx, s.ID)
		if err == nil && info.ContainerJSONBase != nil {
			meta.RestartCount = info.RestartCount
			if info.State != nil {
				meta.InfraStatus = strings.ToLower(info.State.Status)
				if t, err := time.Parse(time.RFC3339Nano, info.State.StartedAt); err == nil {
					meta.StartedAt = t
				}
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
