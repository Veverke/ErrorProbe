package discovery

import (
	"path"
	"sort"
	"strings"

	"github.com/errorprobe/errorprobe/internal/config"
)

// ApplyPolicy filters containers by the watch policy in cfg.
//
// Patterns in cfg.Containers.Exclude are matched as follows:
//   - "pod/<glob>"       — matched against ContainerMeta.Pod (K8s pod name)
//   - "namespace/<glob>" — matched against ContainerMeta.Namespace
//   - "<glob>"           — matched against ContainerMeta.Name (Docker and K8s)
//
// The result is sorted by Runtime then Name for stable config generation.
// The input slice is never mutated.
func ApplyPolicy(containers []ContainerMeta, cfg *config.Config) []ContainerMeta {
	out := make([]ContainerMeta, 0, len(containers))
	for _, c := range containers {
		if excludedContainer(c, cfg.Containers.Exclude) {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Runtime != out[j].Runtime {
			return out[i].Runtime < out[j].Runtime
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// excludedContainer returns true if the container matches any exclusion pattern.
func excludedContainer(c ContainerMeta, patterns []string) bool {
	for _, pat := range patterns {
		if strings.HasPrefix(pat, "pod/") {
			glob := strings.TrimPrefix(pat, "pod/")
			if matched, err := path.Match(glob, c.Pod); err == nil && matched {
				return true
			}
		} else if strings.HasPrefix(pat, "namespace/") {
			glob := strings.TrimPrefix(pat, "namespace/")
			if matched, err := path.Match(glob, c.Namespace); err == nil && matched {
				return true
			}
		} else {
			if matched, err := path.Match(pat, c.Name); err == nil && matched {
				return true
			}
		}
	}
	return false
}
