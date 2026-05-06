package discovery

import (
	"context"
	"fmt"
	"time"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/k8s"
)

// defaultExcludeNamespaces are the system namespaces excluded by default when
// the config does not specify k8s.exclude_namespaces.
var defaultExcludeNamespaces = []string{
	"kube-system",
	"kube-public",
	"kube-node-lease",
	// ErrorProbe deploys its own Vector DaemonSet here; exclude it from
	// the user's watch set just like the managed-by label does for Docker.
	"errorprobe",
}

// recentRestartWindow is how long after the current container instance started
// that a non-zero RestartCount is considered an active restart event.
// Once a container has been running stably beyond this window, infraStatus
// reverts to "running" — avoiding false RESTARTING for pods that restarted
// once long ago (e.g. during a rolling deploy).
const recentRestartWindow = 2 * time.Minute

// ListRunningK8s returns ContainerMeta for every running container in every
// running pod, across all non-system namespaces.
//
// Only pods in Running phase are included. Within each pod, only containers
// whose state is Running (not init containers) are included.
//
// Excluded namespaces default to kube-system / kube-public / kube-node-lease;
// this can be overridden via cfg.K8s.ExcludeNamespaces.
func ListRunningK8s(ctx context.Context, k8sClient k8s.K8sAPI, cfg *config.Config) ([]ContainerMeta, error) {
	pods, err := k8sClient.ListPods(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing k8s pods: %w", err)
	}

	excludeNS := cfg.K8s.ExcludeNamespaces
	if len(excludeNS) == 0 {
		excludeNS = defaultExcludeNamespaces
	}
	nsExcluded := make(map[string]bool, len(excludeNS))
	for _, ns := range excludeNS {
		nsExcluded[ns] = true
	}

	var out []ContainerMeta
	for _, pod := range pods {
		if pod.Phase != "Running" {
			continue
		}
		if nsExcluded[pod.Namespace] {
			continue
		}

		for _, c := range pod.Containers {
			if !c.Running {
				continue
			}

			infraStatus := "running"
			if c.RestartCount > 0 && !c.StartedAt.IsZero() && time.Since(c.StartedAt) < recentRestartWindow {
				// Only flag as restarting when the current instance is fresh AND has
				// a non-zero restart count — i.e. it crashed recently. Containers
				// that restarted long ago (cumulative count) are considered stable.
				infraStatus = "restarting"
			}

			out = append(out, ContainerMeta{
				// ID is synthetic: namespace/pod/container — unique within cluster.
				ID:           pod.Namespace + "/" + pod.Name + "/" + c.Name,
				Name:         c.Name,
				Image:        c.Image,
				Labels:       pod.Labels,
				StartedAt:    c.StartedAt,
				RestartCount: c.RestartCount,
				InfraStatus:  infraStatus,
				Runtime:      "k8s",
				Pod:          pod.Name,
				Namespace:    pod.Namespace,
				Node:         pod.Node,
			})
		}
	}
	return out, nil
}
