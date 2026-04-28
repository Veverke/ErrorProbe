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
				Labels: map[string]string{"com.errorprobe.managed": "true"},
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
