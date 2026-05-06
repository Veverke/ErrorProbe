package discovery_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/discovery"
	"github.com/errorprobe/errorprobe/internal/k8s"
)

// stubK8sAPI implements k8s.K8sAPI for testing.
type stubK8sAPI struct {
	pingErr error
	pods    []k8s.PodInfo
	listErr error
}

func (s *stubK8sAPI) Ping(_ context.Context) error { return s.pingErr }
func (s *stubK8sAPI) IsAvailable(ctx context.Context) bool {
	return s.Ping(ctx) == nil
}
func (s *stubK8sAPI) ListPods(_ context.Context) ([]k8s.PodInfo, error) {
	return s.pods, s.listErr
}
func (s *stubK8sAPI) ApplyVectorDaemonSet(_ context.Context, _, _ string) error { return nil }
func (s *stubK8sAPI) DeleteVectorDaemonSet(_ context.Context) error              { return nil }
func (s *stubK8sAPI) GetPreviousLogs(_ context.Context, _, _, _ string, _ int) (string, error) {
	return "", nil
}

func emptyCfg() *config.Config { return &config.Config{} }

// T5.11 — ListRunningK8s tests

// TestListRunningK8s_RunningPodsReturned: two running pods in default namespace → 2 results.
func TestListRunningK8s_RunningPodsReturned(t *testing.T) {
	stub := &stubK8sAPI{pods: []k8s.PodInfo{
		{Name: "app-1", Namespace: "default", Phase: "Running", Containers: []k8s.ContainerInfo{{Name: "web", Image: "nginx:latest", Running: true}}},
		{Name: "app-2", Namespace: "default", Phase: "Running", Containers: []k8s.ContainerInfo{{Name: "api", Image: "api:v1", Running: true}}},
	}}
	result, err := discovery.ListRunningK8s(context.Background(), stub, emptyCfg(), nil)
	require.NoError(t, err)
	assert.Len(t, result, 2)
}

// TestListRunningK8s_NonRunningFiltered: Pending pod → excluded.
func TestListRunningK8s_NonRunningFiltered(t *testing.T) {
	stub := &stubK8sAPI{pods: []k8s.PodInfo{
		{Name: "pending-1", Namespace: "default", Phase: "Pending", Containers: []k8s.ContainerInfo{{Name: "web", Running: false}}},
	}}
	result, err := discovery.ListRunningK8s(context.Background(), stub, emptyCfg(), nil)
	require.NoError(t, err)
	assert.Empty(t, result)
}

// TestListRunningK8s_SystemNamespacesExcluded: kube-system pod → excluded by default.
func TestListRunningK8s_SystemNamespacesExcluded(t *testing.T) {
	stub := &stubK8sAPI{pods: []k8s.PodInfo{
		{Name: "coredns", Namespace: "kube-system", Phase: "Running", Containers: []k8s.ContainerInfo{{Name: "coredns", Running: true}}},
		{Name: "proxy", Namespace: "kube-public", Phase: "Running", Containers: []k8s.ContainerInfo{{Name: "proxy", Running: true}}},
		{Name: "node", Namespace: "kube-node-lease", Phase: "Running", Containers: []k8s.ContainerInfo{{Name: "heartbeat", Running: true}}},
		// ErrorProbe's own Vector DaemonSet — must also be excluded by default.
		{Name: "errorprobe-vector-abcde", Namespace: "errorprobe", Phase: "Running", Containers: []k8s.ContainerInfo{{Name: "vector", Running: true}}},
	}}
	result, err := discovery.ListRunningK8s(context.Background(), stub, emptyCfg(), nil)
	require.NoError(t, err)
	assert.Empty(t, result)
}

// TestListRunningK8s_MetadataMapping: Pod, Namespace, Node, RestartCount → ContainerMeta fields.
func TestListRunningK8s_MetadataMapping(t *testing.T) {
	stub := &stubK8sAPI{pods: []k8s.PodInfo{
		{
			Name: "worker-1", Namespace: "production", Node: "node-a", Phase: "Running",
			Containers: []k8s.ContainerInfo{{Name: "app", Image: "myapp:1.0", Running: true, RestartCount: 2}},
		},
	}}
	result, err := discovery.ListRunningK8s(context.Background(), stub, emptyCfg(), nil)
	require.NoError(t, err)
	require.Len(t, result, 1)
	cm := result[0]
	assert.Equal(t, "worker-1", cm.Pod)
	assert.Equal(t, "production", cm.Namespace)
	assert.Equal(t, "node-a", cm.Node)
	assert.Equal(t, 2, cm.RestartCount)
	assert.Equal(t, "myapp:1.0", cm.Image)
}

// TestListRunningK8s_RuntimeField: all results have Runtime == "k8s".
func TestListRunningK8s_RuntimeField(t *testing.T) {
	stub := &stubK8sAPI{pods: []k8s.PodInfo{
		{Name: "app", Namespace: "default", Phase: "Running", Containers: []k8s.ContainerInfo{{Name: "c1", Running: true}}},
	}}
	result, err := discovery.ListRunningK8s(context.Background(), stub, emptyCfg(), nil)
	require.NoError(t, err)
	for _, c := range result {
		assert.Equal(t, "k8s", c.Runtime)
	}
}

// TestListRunningK8s_ConfigExclude: custom exclude_namespaces from config.
func TestListRunningK8s_ConfigExclude(t *testing.T) {
	stub := &stubK8sAPI{pods: []k8s.PodInfo{
		{Name: "mon-pod", Namespace: "monitoring", Phase: "Running", Containers: []k8s.ContainerInfo{{Name: "prom", Running: true}}},
		{Name: "app-pod", Namespace: "default", Phase: "Running", Containers: []k8s.ContainerInfo{{Name: "app", Running: true}}},
	}}
	cfg := &config.Config{K8s: config.K8sConfig{ExcludeNamespaces: []string{"monitoring"}}}
	result, err := discovery.ListRunningK8s(context.Background(), stub, cfg, nil)
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "default", result[0].Namespace)
}
