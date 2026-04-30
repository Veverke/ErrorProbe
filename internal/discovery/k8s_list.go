package discovery

import (
	"context"
	"fmt"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/k8s"
)

// defaultExcludeNamespaces are the system namespaces excluded by default when
// the config does not specify k8s.exclude_namespaces.
var defaultExcludeNamespaces = []string{
	"kube-system",
	"kube-public",
	"kube-node-lease",
}

// restartingThreshold is the minimum RestartCount that causes a container to be
// reported as infraStatus "restarting". Set to 1 to flag any restart; raise the
// value (e.g. 3) to tolerate pods that restart once after a rolling deploy.
const restartingThreshold = 1

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
			if c.RestartCount >= restartingThreshold {
				// Treat any nonzero restart count as "restarting"; this is
				// informational only — health state comes from log events.
				infraStatus = "restarting"
			}

			out = append(out, ContainerMeta{
				// ID is synthetic: namespace/pod/container — unique within cluster.
				ID:           pod.Namespace + "/" + pod.Name + "/" + c.Name,
				Name:         pod.Name + "/" + c.Name,
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
