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
}
