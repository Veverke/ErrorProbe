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
	// DisplayName is the normalised name used for display only.
	// It is computed by ApplyPolicy from Containers.DisplayNamePatterns and
	// equals Name when no pattern matches.  All internal tracking (health keys,
	// log labels, Loki queries) still uses Name.
	DisplayName  string
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

// HealthKey returns the canonical key used to cross-reference this container
// in the health snapshot.
//
// For K8s containers (Namespace non-empty) the key is "namespace/container_name",
// which is stable across pod restarts and unique across namespaces.
// For Docker containers the key is the bare container name, which Docker enforces
// to be globally unique on the host.
//
// This is the single authoritative implementation — all callers (TUI, status,
// check, health engine) derive their lookup keys through this method or its
// LogEvent-side mirror in the health package.
func (c ContainerMeta) HealthKey() string {
	if c.Namespace != "" {
		return c.Namespace + "/" + c.Name
	}
	return c.Name
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
