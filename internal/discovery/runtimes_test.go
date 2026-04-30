package discovery_test

import (
"context"
"errors"
"testing"

"github.com/docker/docker/api/types/container"
"github.com/stretchr/testify/assert"
"github.com/stretchr/testify/require"

"github.com/errorprobe/errorprobe/internal/discovery"
"github.com/errorprobe/errorprobe/internal/docker"
)

// pingFailDocker is a docker.DockerAPI whose Ping always returns an error.
type pingFailDocker struct {
stubDockerForReconciler
}

func (p *pingFailDocker) Ping(_ context.Context) error { return errors.New("ping failed") }

func (p *pingFailDocker) ContainerList(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
return nil, nil
}

func (p *pingFailDocker) ContainerInspect(_ context.Context, _ string) (container.InspectResponse, error) {
return container.InspectResponse{}, nil
}

func (p *pingFailDocker) StartContainer(_ context.Context, _ docker.ContainerSpec) error {
return nil
}

// ---------------------------------------------------------------------------
// T5.12 — DetectRuntimes tests
// ---------------------------------------------------------------------------

func TestDetectRuntimes_BothAvailable(t *testing.T) {
doc := &stubDockerForReconciler{}
k8 := &stubK8sAPI{}
rs := discovery.DetectRuntimes(context.Background(), doc, k8)
assert.True(t, rs.DockerAvailable)
assert.True(t, rs.K8sAvailable)
}

func TestDetectRuntimes_K8sUnavailable(t *testing.T) {
doc := &stubDockerForReconciler{}
k8 := &stubK8sAPI{pingErr: errors.New("k8s down")}
rs := discovery.DetectRuntimes(context.Background(), doc, k8)
assert.True(t, rs.DockerAvailable)
assert.False(t, rs.K8sAvailable)
}

func TestDetectRuntimes_DockerUnavailable(t *testing.T) {
k8 := &stubK8sAPI{}
rs := discovery.DetectRuntimes(context.Background(), &pingFailDocker{}, k8)
assert.False(t, rs.DockerAvailable)
assert.True(t, rs.K8sAvailable)
}

func TestDetectRuntimes_NilClients(t *testing.T) {
rs := discovery.DetectRuntimes(context.Background(), nil, nil)
assert.False(t, rs.DockerAvailable)
assert.False(t, rs.K8sAvailable)
}

// ---------------------------------------------------------------------------
// T5.12 — MergeContainers tests
// ---------------------------------------------------------------------------

func TestMergeContainers_CombinesBoth(t *testing.T) {
dockerC := []discovery.ContainerMeta{
{Name: "zap", Runtime: "docker"},
{Name: "alpha", Runtime: "docker"},
}
k8sC := []discovery.ContainerMeta{
{Name: "beta/c1", Runtime: "k8s"},
}
merged := discovery.MergeContainers(dockerC, k8sC)
require.Len(t, merged, 3)
assert.Equal(t, "docker", merged[0].Runtime)
assert.Equal(t, "docker", merged[1].Runtime)
assert.Equal(t, "k8s", merged[2].Runtime)
assert.Equal(t, "alpha", merged[0].Name)
assert.Equal(t, "zap", merged[1].Name)
}

func TestMergeContainers_EmptyK8s(t *testing.T) {
dockerC := []discovery.ContainerMeta{
{Name: "z", Runtime: "docker"},
{Name: "a", Runtime: "docker"},
}
merged := discovery.MergeContainers(dockerC, nil)
require.Len(t, merged, 2)
assert.Equal(t, "a", merged[0].Name)
assert.Equal(t, "z", merged[1].Name)
}

func TestMergeContainers_EmptyDocker(t *testing.T) {
k8sC := []discovery.ContainerMeta{
{Name: "pod/c2", Runtime: "k8s"},
{Name: "pod/c1", Runtime: "k8s"},
}
merged := discovery.MergeContainers(nil, k8sC)
require.Len(t, merged, 2)
assert.Equal(t, "pod/c1", merged[0].Name)
}