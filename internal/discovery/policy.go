package discovery

import (
	"path"
	"regexp"
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
// cfg.Containers.Include, when non-empty, acts as an allow-list: after the
// Exclude pass only containers matching at least one Include pattern survive.
// This enables "infra-only" mode (e.g. Include: ["namespace/kube-system"]).
//
// After filtering, each container's DisplayName is computed from
// cfg.Containers.DisplayNamePatterns (falling back to config.DefaultDisplayNamePatterns
// when the field is empty).
//
// The result is sorted by Runtime then Name for stable config generation.
// The input slice is never mutated.
func ApplyPolicy(containers []ContainerMeta, cfg *config.Config) []ContainerMeta {
	out := make([]ContainerMeta, 0, len(containers))
	for _, c := range containers {
		if excludedContainer(c, cfg.Containers.Exclude) {
			continue
		}
		if len(cfg.Containers.Include) > 0 && !includedContainer(c, cfg.Containers.Include) {
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

	// Compute display names.
	dispPatterns := cfg.Containers.DisplayNamePatterns
	if len(dispPatterns) == 0 {
		dispPatterns = config.DefaultDisplayNamePatterns
	}
	compiled := compileDisplayPatterns(dispPatterns)
	for i := range out {
		out[i].DisplayName = applyDisplayPatterns(out[i].Name, compiled)
	}

	return out
}

// compileDisplayPatterns compiles a list of regex strings.  Invalid patterns
// are silently skipped so a bad user-supplied regex doesn't crash the tool.
func compileDisplayPatterns(patterns []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		if re, err := regexp.Compile(p); err == nil {
			out = append(out, re)
		}
	}
	return out
}

// applyDisplayPatterns returns the first capture group of the first pattern that
// matches name, or name itself when no pattern matches.
func applyDisplayPatterns(name string, patterns []*regexp.Regexp) string {
	for _, re := range patterns {
		if m := re.FindStringSubmatch(name); len(m) >= 2 {
			return m[1]
		}
	}
	return name
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

// includedContainer returns true if the container matches any include pattern.
// Uses the same pattern syntax as excludedContainer.
func includedContainer(c ContainerMeta, patterns []string) bool {
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
