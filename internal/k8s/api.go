package k8s

import "context"

// K8sAPI defines all Kubernetes operations used by ErrorProbe.
// Implemented by *Client; can be mocked in unit tests.
type K8sAPI interface {
	// Ping verifies connectivity to the cluster by calling ServerVersion.
	Ping(ctx context.Context) error

	// IsAvailable returns true when Ping succeeds; never returns an error.
	IsAvailable(ctx context.Context) bool

	// ListPods returns all pods across all namespaces.
	ListPods(ctx context.Context) ([]PodInfo, error)

	// ApplyVectorDaemonSet creates or updates the Vector DaemonSet and all
	// supporting RBAC / ConfigMap resources in the "errorprobe" namespace.
	// vectorConfigTOML is the rendered vector.toml content for the DaemonSet.
	ApplyVectorDaemonSet(ctx context.Context, image, vectorConfigTOML string) error

	// DeleteVectorDaemonSet removes the Vector DaemonSet and supporting
	// resources created by ApplyVectorDaemonSet.
	DeleteVectorDaemonSet(ctx context.Context) error

	// GetPreviousLogs returns the last tailLines log lines from the previous
	// terminated instance of containerName in the given pod/namespace.
	// Returns an empty string when no previous container log is available.
	GetPreviousLogs(ctx context.Context, namespace, podName, containerName string, tailLines int) (string, error)
}
