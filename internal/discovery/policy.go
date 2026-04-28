package discovery

import (
	"path"
	"sort"

	"github.com/errorprobe/errorprobe/internal/config"
)

// ApplyPolicy filters containers by the watch policy in cfg.
// Containers whose name matches any glob in cfg.Containers.Exclude are removed.
// The result is sorted by container name for stable config generation.
// The input slice is never mutated.
func ApplyPolicy(containers []ContainerMeta, cfg *config.Config) []ContainerMeta {
	out := make([]ContainerMeta, 0, len(containers))
	for _, c := range containers {
		if excluded(c.Name, cfg.Containers.Exclude) {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// excluded returns true if name matches any glob pattern.
func excluded(name string, patterns []string) bool {
	for _, pat := range patterns {
		matched, err := path.Match(pat, name)
		if err == nil && matched {
			return true
		}
	}
	return false
}
