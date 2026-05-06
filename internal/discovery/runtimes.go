package discovery

import (
	"context"
	"sort"

	"github.com/errorprobe/errorprobe/internal/docker"
	"github.com/errorprobe/errorprobe/internal/k8s"
)

// RuntimeSet describes which container runtimes are reachable.
type RuntimeSet struct {
	DockerAvailable bool
	K8sAvailable    bool
}

// DetectRuntimes pings both runtimes independently and returns which are live.
// Both can be true simultaneously (Docker Desktop runs K8s on top of Docker).
func DetectRuntimes(ctx context.Context, dockerClient docker.DockerAPI, k8sClient k8s.K8sAPI) RuntimeSet {
	rs := RuntimeSet{}
	if dockerClient != nil {
		rs.DockerAvailable = dockerClient.Ping(ctx) == nil
	}
	if k8sClient != nil {
		rs.K8sAvailable = k8sClient.IsAvailable(ctx)
	}
	return rs
}

// MergeContainers concatenates docker and k8s slices and sorts the result
// by Runtime then Name for stable, deterministic output.
// Neither input slice is mutated.
func MergeContainers(dockerContainers []ContainerMeta, k8sContainers []ContainerMeta) []ContainerMeta {
	out := make([]ContainerMeta, 0, len(dockerContainers)+len(k8sContainers))
	out = append(out, dockerContainers...)
	out = append(out, k8sContainers...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Runtime != out[j].Runtime {
			return out[i].Runtime < out[j].Runtime
		}
		return out[i].Name < out[j].Name
	})
	return out
}
