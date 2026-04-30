package k8s

import "time"

// PodInfo is a lightweight snapshot of a single pod returned by ListPods.
type PodInfo struct {
	Name      string
	Namespace string
	Node      string
	Phase     string // "Running", "Pending", etc.
	Labels    map[string]string

	Containers []ContainerInfo
}

// ContainerInfo holds per-container state within a pod.
type ContainerInfo struct {
	Name         string
	Image        string
	Ready        bool
	RestartCount int
	StartedAt    time.Time
	Running      bool
}
