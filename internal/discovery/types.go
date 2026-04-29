package discovery

import "time"

// ContainerMeta holds the metadata for a single discovered container.
type ContainerMeta struct {
	ID           string
	Name         string
	Image        string
	Labels       map[string]string
	StartedAt    time.Time
	RestartCount int
	InfraStatus  string // "running" | "restarting" | "exited" | "paused"
	Runtime      string // "docker" (K8s added in Phase 5)
}

// WatchSet is the approved set of containers at a point in time.
type WatchSet struct {
	Containers  []ContainerMeta
	GeneratedAt time.Time
}

// Diff returns the containers added to and removed from ws relative to previous.
// A container is identified by its ID.
func (ws WatchSet) Diff(previous WatchSet) (added []ContainerMeta, removed []ContainerMeta) {
	curr := make(map[string]ContainerMeta, len(ws.Containers))
	for _, c := range ws.Containers {
		curr[c.ID] = c
	}
	prev := make(map[string]ContainerMeta, len(previous.Containers))
	for _, c := range previous.Containers {
		prev[c.ID] = c
	}

	for id, c := range curr {
		if _, ok := prev[id]; !ok {
			added = append(added, c)
		}
	}
	for id, c := range prev {
		if _, ok := curr[id]; !ok {
			removed = append(removed, c)
		}
	}
	return added, removed
}
