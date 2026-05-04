package discovery

import "time"

// MountInfo describes a single mount point attached to a container.
type MountInfo struct {
	Type        string // "volume", "bind", "tmpfs"
	Name        string // volume name (non-empty for named volumes)
	Source      string // host path (bind mounts) or volume storage path
	Destination string // path inside the container
	ReadOnly    bool
}

// ContainerMeta holds the metadata for a single discovered container.
type ContainerMeta struct {
	ID           string
	Name         string
	Image        string
	Labels       map[string]string
	StartedAt    time.Time
	RestartCount int
	InfraStatus  string      // "running" | "restarting" | "exited" | "paused"
	Runtime      string      // "docker" | "k8s"
	Mounts       []MountInfo // volumes and bind mounts attached to the container (Docker only)

	// K8s-specific fields (zero-valued for Docker containers).
	Pod         string // pod name
	Namespace   string // Kubernetes namespace
	Node        string // node the pod is scheduled on
	PrevExitMsg string // last error line from previous container instance (K8s only, set on restart)
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
