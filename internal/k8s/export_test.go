package k8s

import (
	"time"

	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// NewClientWithFake creates a *Client backed by a fake Kubernetes clientset.
// The fake clientset's Discovery().ServerVersion() succeeds by default.
func NewClientWithFake(fake *k8sfake.Clientset) *Client {
	return newClientWithCS(fake)
}

// StartedAt exposes the unexported startedAt helper for testing.
func StartedAt(containers []ContainerInfo) time.Time {
	return startedAt(containers)
}
