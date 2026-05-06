package discovery_test

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/discovery"
	"github.com/errorprobe/errorprobe/internal/docker"
)

// listDockerStub satisfies docker.DockerAPI; only ContainerList and ContainerInspect matter.
type listDockerStub struct {
	summaries []container.Summary
	listErr   error
	inspectFn func(ctx context.Context, id string) (container.InspectResponse, error)
}

func (s *listDockerStub) Close() error                                                { return nil }
func (s *listDockerStub) Ping(_ context.Context) error                                { return nil }
func (s *listDockerStub) ImageExists(_ context.Context, _ string) (bool, error)       { return false, nil }
func (s *listDockerStub) PullImage(_ context.Context, _ string, _ func(string)) error { return nil }
func (s *listDockerStub) ContainerRunning(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (s *listDockerStub) ContainerID(_ context.Context, _ string) (string, error)        { return "", nil }
func (s *listDockerStub) NetworkExists(_ context.Context, _ string) (bool, error)        { return false, nil }
func (s *listDockerStub) CreateNetwork(_ context.Context, _ string) error                { return nil }
func (s *listDockerStub) RemoveNetwork(_ context.Context, _ string) error                { return nil }
func (s *listDockerStub) DisconnectFromNetwork(_ context.Context, _, _ string) error      { return nil }
func (s *listDockerStub) DisconnectNetworkEndpoints(_ context.Context, _ string) []string      { return nil }
func (s *listDockerStub) VolumeExists(_ context.Context, _ string) (bool, error)         { return false, nil }
func (s *listDockerStub) CreateVolume(_ context.Context, _ string) error                 { return nil }
func (s *listDockerStub) RemoveVolume(_ context.Context, _ string) error                 { return nil }
func (s *listDockerStub) SendSignal(_ context.Context, _ string, _ string) error         { return nil }
func (s *listDockerStub) StartContainer(_ context.Context, _ docker.ContainerSpec) error { return nil }
func (s *listDockerStub) StopContainer(_ context.Context, _ string, _ int) error         { return nil }
func (s *listDockerStub) RemoveContainer(_ context.Context, _ string, _ bool) error      { return nil }

func (s *listDockerStub) ContainerList(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
	return s.summaries, s.listErr
}

func (s *listDockerStub) ContainerInspect(ctx context.Context, id string) (container.InspectResponse, error) {
	if s.inspectFn != nil {
		return s.inspectFn(ctx, id)
	}
	return container.InspectResponse{}, nil
}

func TestListRunning_ExcludesManagedContainers(t *testing.T) {
	stub := &listDockerStub{
		summaries: []container.Summary{
			{
				ID:     "user1",
				Names:  []string{"/my-app"},
				Image:  "nginx:latest",
				State:  "running",
				Labels: map[string]string{},
			},
			{
				ID:     "ep1",
				Names:  []string{"/errorprobe-vector"},
				Image:  "vector:latest",
				State:  "running",
				Labels: map[string]string{"managed-by": "errorprobe"},
			},
		},
	}

	result, err := discovery.ListRunning(context.Background(), stub)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "my-app", result[0].Name)
}

func TestListRunning_MapsFields(t *testing.T) {
	startedAt := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	stub := &listDockerStub{
		summaries: []container.Summary{
			{
				ID:     "abc123",
				Names:  []string{"/payments-api"},
				Image:  "payments:v1",
				State:  "running",
				Labels: map[string]string{"app": "payments"},
			},
		},
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					RestartCount: 3,
					State: &container.State{
						Status:    "running",
						StartedAt: startedAt.Format(time.RFC3339Nano),
					},
				},
			}, nil
		},
	}

	result, err := discovery.ListRunning(context.Background(), stub)
	require.NoError(t, err)
	require.Len(t, result, 1)
	c := result[0]
	assert.Equal(t, "abc123", c.ID)
	assert.Equal(t, "payments-api", c.Name)
	assert.Equal(t, "payments:v1", c.Image)
	assert.Equal(t, "running", c.InfraStatus)
	assert.Equal(t, 3, c.RestartCount)
	assert.Equal(t, "docker", c.Runtime)
	assert.Equal(t, startedAt.UTC(), c.StartedAt.UTC())
}

func TestListRunning_ListError(t *testing.T) {
	stub := &listDockerStub{listErr: assert.AnError}
	_, err := discovery.ListRunning(context.Background(), stub)
	assert.Error(t, err)
}

func TestListRunning_EmptyList(t *testing.T) {
	stub := &listDockerStub{summaries: []container.Summary{}}
	result, err := discovery.ListRunning(context.Background(), stub)
	require.NoError(t, err)
	assert.Empty(t, result)
}

// TestListRunning_ExcludesKubeletContainers verifies that containers bearing
// any "io.kubernetes.*" label are excluded from Docker discovery.  This covers
// control-plane pods (kube-apiserver, etcd, coredns, …) exposed via the Docker
// socket by Docker Desktop, minikube, k3s, kind, and similar distributions —
// regardless of which specific kubernetes label the distribution happens to set.
func TestListRunning_ExcludesKubeletContainers(t *testing.T) {
	stub := &listDockerStub{
		summaries: []container.Summary{
			{
				ID:    "user1",
				Names: []string{"/my-app"},
				State: "running",
				Labels: map[string]string{},
			},
			{
				// io.kubernetes.pod.name — set by most kubelet distributions.
				ID:    "k8s1",
				Names: []string{"/kube-apiserver"},
				State: "running",
				Labels: map[string]string{
					"io.kubernetes.pod.name": "kube-apiserver-minikube",
				},
			},
			{
				// io.kubernetes.pod.namespace — another standard kubelet label.
				ID:    "k8s2",
				Names: []string{"/etcd"},
				State: "running",
				Labels: map[string]string{
					"io.kubernetes.pod.name":      "etcd-minikube",
					"io.kubernetes.pod.namespace": "kube-system",
				},
			},
			{
				// io.kubernetes.docker.type — Docker Desktop specific label.
				ID:    "k8s3",
				Names: []string{"/coredns"},
				State: "running",
				Labels: map[string]string{
					"io.kubernetes.docker.type": "container",
				},
			},
		},
	}

	result, err := discovery.ListRunning(context.Background(), stub)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "my-app", result[0].Name)
}