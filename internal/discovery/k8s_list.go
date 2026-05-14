package discovery

import (
	"context"
	"fmt"
	"time"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/k8s"
	"github.com/errorprobe/errorprobe/internal/pbr"
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

// ListRunningK8s returns ContainerMeta for every running container in every
// running pod, across all non-system namespaces.
//
// Only pods in Running phase are included. Within each pod, only containers
// whose state is Running (not init containers) are included.
//
// Excluded namespaces default to kube-system / kube-public / kube-node-lease;
// this can be overridden via cfg.K8s.ExcludeNamespaces.
//
// rules is the compiled PBR rule set used to derive InfraStatus for each
// container. Pass nil or an empty slice to fall back to built-in behaviour.
func ListRunningK8s(ctx context.Context, k8sClient k8s.K8sAPI, cfg *config.Config, rules []pbr.Rule) ([]ContainerMeta, error) {
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

			// When StartedAt is unknown we cannot safely compute uptime, so we
			// skip PBR infra evaluation entirely (a zero uptime would falsely
			// satisfy uptime < 2m, triggering the builtin-k8s-restarting rule).
			// "running" is used as the conservative default in that case.
			var infraStatus string
			if c.StartedAt.IsZero() {
				infraStatus = "running"
			} else {
				infraStatus = inferInfraStatus(rules, pbr.InfraContainer{
					Name:         c.Name,
					Namespace:    pod.Namespace,
					Runtime:      "k8s",
					RestartCount: c.RestartCount,
					Uptime:       time.Since(c.StartedAt),
					Phase:        pod.Phase,
				})
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

// inferInfraStatus evaluates the PBR infra rules against meta to determine the
// container's infrastructure status string.
// Returns "restarting" / "running" / or any custom state from a matched rule.
// Falls back to "running" when no rule matches.
func inferInfraStatus(rules []pbr.Rule, meta pbr.InfraContainer) string {
	if len(rules) == 0 {
		return "running"
	}
	result := pbr.Evaluate(rules, pbr.EvalContext{
		Infra: &meta,
	})
	if result.State == "" {
		return "running"
	}
	// Normalise to lowercase for the InfraStatus field convention.
	switch result.State {
	case "RESTARTING":
		return "restarting"
	case "OK":
		return "running"
	default:
		return result.State
	}
}
