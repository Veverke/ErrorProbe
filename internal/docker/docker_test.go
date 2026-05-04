package docker_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/docker"
)

// ---------------------------------------------------------------------------
// Fake SDK implementation (satisfies docker.sdkAPI — accessed via NewTestClient)
// ---------------------------------------------------------------------------

// fakeSDK is a configurable fake of the inner Docker SDK interface.
// Test cases set fields to control return values.
type fakeSDK struct {
	pingErr error

	existingImages map[string]bool
	pullReader     io.ReadCloser
	pullErr        error

	containers    map[string]*fakeContainer
	containerList []container.Summary
	listErr       error
	killErr       error
	createErr     error
	startErr      error
	stopErr       error
	removeErr     error

	networks     map[string]bool
	createNetErr error
	removeNetErr error
	connectErr   error

	volumes      map[string]bool
	createVolErr error
	removeVolErr error
}

type fakeContainer struct {
	id      string
	running bool
}

func newFakeSDK() *fakeSDK {
	return &fakeSDK{
		existingImages: map[string]bool{},
		containers:     map[string]*fakeContainer{},
		networks:       map[string]bool{},
		volumes:        map[string]bool{},
	}
}

func (f *fakeSDK) Close() error { return nil }

func (f *fakeSDK) Ping(_ context.Context) (types.Ping, error) {
	return types.Ping{}, f.pingErr
}

func (f *fakeSDK) ImageInspectWithRaw(_ context.Context, id string) (image.InspectResponse, []byte, error) {
	if f.existingImages[id] {
		return image.InspectResponse{ID: id}, nil, nil
	}
	return image.InspectResponse{}, nil, errdefs.ErrNotFound
}

func (f *fakeSDK) ImagePull(_ context.Context, _ string, _ image.PullOptions) (io.ReadCloser, error) {
	if f.pullErr != nil {
		return nil, f.pullErr
	}
	if f.pullReader != nil {
		return f.pullReader, nil
	}
	// Return a minimal valid pull stream.
	body := `{"status":"Pull complete","id":"sha256:abc"}` + "\n"
	return io.NopCloser(strings.NewReader(body)), nil
}

func (f *fakeSDK) ContainerInspect(_ context.Context, name string) (container.InspectResponse, error) {
	c, ok := f.containers[name]
	if !ok {
		return container.InspectResponse{}, errdefs.ErrNotFound
	}
	state := &container.State{Running: c.running}
	return container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			ID:    c.id,
			State: state,
		},
	}, nil
}

func (f *fakeSDK) ContainerKill(_ context.Context, _ string, _ string) error { return f.killErr }

func (f *fakeSDK) ContainerList(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	// If containerList was set explicitly, honour it (used by discovery tests).
	if f.containerList != nil {
		return f.containerList, nil
	}
	// Build a list from the containers map; only include running containers.
	// This makes ContainerRunning work without callers needing to pre-populate containerList.
	var out []container.Summary
	for name, c := range f.containers {
		if c.running {
			out = append(out, container.Summary{
				ID:    c.id,
				Names: []string{"/" + name},
				State: "running",
			})
		}
	}
	return out, nil
}

func (f *fakeSDK) ContainerCreate(_ context.Context, _ *container.Config, _ *container.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, name string) (container.CreateResponse, error) {
	if f.createErr != nil {
		return container.CreateResponse{}, f.createErr
	}
	f.containers[name] = &fakeContainer{id: "fake-id-" + name, running: false}
	return container.CreateResponse{ID: "fake-id-" + name}, nil
}

func (f *fakeSDK) ContainerStart(_ context.Context, name string, _ container.StartOptions) error {
	if f.startErr != nil {
		return f.startErr
	}
	if c, ok := f.containers[name]; ok {
		c.running = true
	}
	return nil
}

func (f *fakeSDK) ContainerStop(_ context.Context, name string, _ container.StopOptions) error {
	if f.stopErr != nil {
		return f.stopErr
	}
	if _, ok := f.containers[name]; !ok {
		return errdefs.ErrNotFound
	}
	return nil
}

func (f *fakeSDK) ContainerRemove(_ context.Context, name string, _ container.RemoveOptions) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	if _, ok := f.containers[name]; !ok {
		return errdefs.ErrNotFound
	}
	delete(f.containers, name)
	return nil
}

func (f *fakeSDK) NetworkConnect(_ context.Context, _, _ string, _ *network.EndpointSettings) error {
	return f.connectErr
}

func (f *fakeSDK) NetworkDisconnect(_ context.Context, _, _ string, _ bool) error { return nil }

func (f *fakeSDK) NetworkInspect(_ context.Context, name string, _ network.InspectOptions) (network.Inspect, error) {
	if _, ok := f.networks[name]; !ok {
		return network.Inspect{}, errdefs.ErrNotFound
	}
	return network.Inspect{Name: name}, nil
}

func (f *fakeSDK) NetworkList(_ context.Context, _ network.ListOptions) ([]network.Summary, error) {
	var out []network.Summary
	for name := range f.networks {
		out = append(out, network.Summary{Name: name})
	}
	return out, nil
}

func (f *fakeSDK) NetworkCreate(_ context.Context, name string, _ network.CreateOptions) (network.CreateResponse, error) {
	if f.createNetErr != nil {
		return network.CreateResponse{}, f.createNetErr
	}
	f.networks[name] = true
	return network.CreateResponse{ID: "net-" + name}, nil
}

func (f *fakeSDK) NetworkRemove(_ context.Context, name string) error {
	if f.removeNetErr != nil {
		return f.removeNetErr
	}
	if _, ok := f.networks[name]; !ok {
		return errdefs.ErrNotFound
	}
	delete(f.networks, name)
	return nil
}

func (f *fakeSDK) VolumeList(_ context.Context, _ volume.ListOptions) (volume.ListResponse, error) {
	var vols []*volume.Volume
	for name := range f.volumes {
		n := name
		vols = append(vols, &volume.Volume{Name: n})
	}
	return volume.ListResponse{Volumes: vols}, nil
}

func (f *fakeSDK) VolumeCreate(_ context.Context, opts volume.CreateOptions) (volume.Volume, error) {
	if f.createVolErr != nil {
		return volume.Volume{}, f.createVolErr
	}
	f.volumes[opts.Name] = true
	return volume.Volume{Name: opts.Name}, nil
}

func (f *fakeSDK) VolumeRemove(_ context.Context, name string, _ bool) error {
	if f.removeVolErr != nil {
		return f.removeVolErr
	}
	if _, ok := f.volumes[name]; !ok {
		return errdefs.ErrNotFound
	}
	delete(f.volumes, name)
	return nil
}

// ---------------------------------------------------------------------------
// T1.13 — docker.Client unit tests (no Docker daemon needed)
// ---------------------------------------------------------------------------

func newTestClient(f *fakeSDK) *docker.Client {
	return docker.NewTestClient(f)
}

func TestPing_Success(t *testing.T) {
	f := newFakeSDK()
	c := newTestClient(f)
	assert.NoError(t, c.Ping(context.Background()))
}

func TestPing_Error(t *testing.T) {
	f := newFakeSDK()
	f.pingErr = errors.New("daemon unreachable")
	c := newTestClient(f)
	err := c.Ping(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pinging docker daemon")
}

func TestImageExists_Present(t *testing.T) {
	f := newFakeSDK()
	f.existingImages["grafana/loki:3.0.0"] = true
	c := newTestClient(f)
	ok, err := c.ImageExists(context.Background(), "grafana/loki:3.0.0")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestImageExists_Absent(t *testing.T) {
	f := newFakeSDK()
	c := newTestClient(f)
	ok, err := c.ImageExists(context.Background(), "grafana/loki:3.0.0")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestPullImage_AlreadyPresent_NoOp(t *testing.T) {
	f := newFakeSDK()
	f.existingImages["grafana/loki:3.0.0"] = true
	f.pullErr = errors.New("should not be called")
	c := newTestClient(f)
	err := c.PullImage(context.Background(), "grafana/loki:3.0.0", nil)
	assert.NoError(t, err, "pull should be skipped when image is already present")
}

func TestPullImage_Missing_Pulls(t *testing.T) {
	f := newFakeSDK()
	var received []string
	c := newTestClient(f)
	err := c.PullImage(context.Background(), "grafana/loki:3.0.0", func(s string) {
		received = append(received, s)
	})
	require.NoError(t, err)
	assert.NotEmpty(t, received, "onProgress should have been called")
}

func TestPullImage_StreamContainsError(t *testing.T) {
	f := newFakeSDK()
	body := `{"error":"pull access denied"}` + "\n"
	f.pullReader = io.NopCloser(bytes.NewBufferString(body))
	c := newTestClient(f)
	err := c.PullImage(context.Background(), "private/image:latest", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pull access denied")
}

func TestContainerRunning_True(t *testing.T) {
	f := newFakeSDK()
	f.containers["errorprobe-loki"] = &fakeContainer{id: "abc123", running: true}
	c := newTestClient(f)
	running, err := c.ContainerRunning(context.Background(), "errorprobe-loki")
	require.NoError(t, err)
	assert.True(t, running)
}

func TestContainerRunning_Stopped(t *testing.T) {
	f := newFakeSDK()
	f.containers["errorprobe-loki"] = &fakeContainer{id: "abc123", running: false}
	c := newTestClient(f)
	running, err := c.ContainerRunning(context.Background(), "errorprobe-loki")
	require.NoError(t, err)
	assert.False(t, running)
}

func TestContainerRunning_False(t *testing.T) {
	f := newFakeSDK()
	c := newTestClient(f)
	running, err := c.ContainerRunning(context.Background(), "errorprobe-loki")
	require.NoError(t, err)
	assert.False(t, running)
}

func TestContainerID_Found(t *testing.T) {
	f := newFakeSDK()
	f.containers["loki"] = &fakeContainer{id: "deadbeef", running: true}
	c := newTestClient(f)
	id, err := c.ContainerID(context.Background(), "loki")
	require.NoError(t, err)
	assert.Equal(t, "deadbeef", id)
}

func TestContainerID_NotFound(t *testing.T) {
	f := newFakeSDK()
	c := newTestClient(f)
	id, err := c.ContainerID(context.Background(), "nonexistent")
	require.NoError(t, err)
	assert.Empty(t, id)
}

func TestSendSignal_OK(t *testing.T) {
	f := newFakeSDK()
	c := newTestClient(f)
	err := c.SendSignal(context.Background(), "loki", "SIGHUP")
	assert.NoError(t, err)
}

func TestNetworkExists_True(t *testing.T) {
	f := newFakeSDK()
	f.networks["errorprobe-net"] = true
	c := newTestClient(f)
	ok, err := c.NetworkExists(context.Background(), "errorprobe-net")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestNetworkExists_False(t *testing.T) {
	f := newFakeSDK()
	c := newTestClient(f)
	ok, err := c.NetworkExists(context.Background(), "errorprobe-net")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestCreateNetwork_New(t *testing.T) {
	f := newFakeSDK()
	c := newTestClient(f)
	err := c.CreateNetwork(context.Background(), "errorprobe-net")
	require.NoError(t, err)
	assert.True(t, f.networks["errorprobe-net"])
}

func TestCreateNetwork_Idempotent(t *testing.T) {
	f := newFakeSDK()
	f.networks["errorprobe-net"] = true
	// Simulate a conflict error on duplicate create — CreateNetwork should return nil.
	f.createNetErr = errdefs.ErrConflict
	c := newTestClient(f)
	err := c.CreateNetwork(context.Background(), "errorprobe-net")
	assert.NoError(t, err, "creating an existing network should be idempotent")
}

func TestRemoveNetwork_OK(t *testing.T) {
	f := newFakeSDK()
	f.networks["errorprobe-net"] = true
	c := newTestClient(f)
	err := c.RemoveNetwork(context.Background(), "errorprobe-net")
	require.NoError(t, err)
	assert.False(t, f.networks["errorprobe-net"])
}

func TestRemoveNetwork_NotFound_NoError(t *testing.T) {
	f := newFakeSDK()
	c := newTestClient(f)
	err := c.RemoveNetwork(context.Background(), "errorprobe-net")
	assert.NoError(t, err)
}

func TestStartContainer_New(t *testing.T) {
	f := newFakeSDK()
	c := newTestClient(f)
	spec := docker.ContainerSpec{
		Name:  "test-container",
		Image: "grafana/loki:3.0.0",
		Ports: []docker.PortBinding{{HostPort: "3100", ContainerPort: "3100"}},
	}
	err := c.StartContainer(context.Background(), spec)
	require.NoError(t, err)
	assert.True(t, f.containers["test-container"].running)
}

func TestStartContainer_AlreadyRunning_NoOp(t *testing.T) {
	f := newFakeSDK()
	f.containers["loki"] = &fakeContainer{id: "abc", running: true}
	f.createErr = errors.New("should not be called")
	c := newTestClient(f)
	err := c.StartContainer(context.Background(), docker.ContainerSpec{Name: "loki", Image: "grafana/loki:3.0.0"})
	assert.NoError(t, err)
}

func TestStopContainer_OK(t *testing.T) {
	f := newFakeSDK()
	f.containers["loki"] = &fakeContainer{id: "abc", running: true}
	c := newTestClient(f)
	err := c.StopContainer(context.Background(), "loki", 10)
	assert.NoError(t, err)
}

func TestStopContainer_NotFound_NoError(t *testing.T) {
	f := newFakeSDK()
	c := newTestClient(f)
	err := c.StopContainer(context.Background(), "nonexistent", 10)
	assert.NoError(t, err)
}

func TestRemoveContainer_OK(t *testing.T) {
	f := newFakeSDK()
	f.containers["loki"] = &fakeContainer{id: "abc", running: false}
	c := newTestClient(f)
	err := c.RemoveContainer(context.Background(), "loki", false)
	assert.NoError(t, err)
	assert.Nil(t, f.containers["loki"])
}

func TestRemoveContainer_NotFound_NoError(t *testing.T) {
	f := newFakeSDK()
	c := newTestClient(f)
	err := c.RemoveContainer(context.Background(), "nonexistent", true)
	assert.NoError(t, err)
}

func TestVolumeExists_True(t *testing.T) {
	f := newFakeSDK()
	f.volumes["errorprobe-loki-data"] = true
	c := newTestClient(f)
	ok, err := c.VolumeExists(context.Background(), "errorprobe-loki-data")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestVolumeExists_False(t *testing.T) {
	f := newFakeSDK()
	c := newTestClient(f)
	ok, err := c.VolumeExists(context.Background(), "errorprobe-loki-data")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestCreateVolume_OK(t *testing.T) {
	f := newFakeSDK()
	c := newTestClient(f)
	err := c.CreateVolume(context.Background(), "errorprobe-loki-data")
	require.NoError(t, err)
	assert.True(t, f.volumes["errorprobe-loki-data"])
}

func TestRemoveVolume_OK(t *testing.T) {
	f := newFakeSDK()
	f.volumes["errorprobe-loki-data"] = true
	c := newTestClient(f)
	err := c.RemoveVolume(context.Background(), "errorprobe-loki-data")
	require.NoError(t, err)
	assert.False(t, f.volumes["errorprobe-loki-data"])
}

func TestClose_OK(t *testing.T) {
	f := newFakeSDK()
	c := newTestClient(f)
	assert.NoError(t, c.Close())
}

func TestImageExists_Error(t *testing.T) {
	f := newFakeSDK()
	f.existingImages = nil // force nil map to trigger a "non-not-found" path
	// Simulate a generic (non-not-found) error from ImageInspectWithRaw.
	// We override the stub to return a generic error.
	f2 := &fakeSDKWithInspectError{fakeSDK: f, inspectErr: errors.New("internal error")}
	c := docker.NewTestClient(f2)
	_, err := c.ImageExists(context.Background(), "some/image:tag")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting image")
}

func TestSendSignal_Error(t *testing.T) {
	f := newFakeSDK()
	f.killErr = errors.New("kill failed")
	c := newTestClient(f)
	err := c.SendSignal(context.Background(), "loki", "SIGHUP")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sending signal")
}

func TestStopContainer_Error(t *testing.T) {
	f := newFakeSDK()
	f.containers["loki"] = &fakeContainer{id: "abc", running: true}
	f.stopErr = errors.New("stop failed")
	c := newTestClient(f)
	err := c.StopContainer(context.Background(), "loki", 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stopping container")
}

func TestRemoveContainer_Error(t *testing.T) {
	f := newFakeSDK()
	f.containers["loki"] = &fakeContainer{id: "abc"}
	f.removeErr = errors.New("remove failed")
	c := newTestClient(f)
	err := c.RemoveContainer(context.Background(), "loki", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing container")
}

func TestCreateVolume_ConflictIsNoOp(t *testing.T) {
	f := newFakeSDK()
	f.createVolErr = errdefs.ErrConflict
	c := newTestClient(f)
	// Conflict should be treated as idempotent success.
	err := c.CreateVolume(context.Background(), "myvol")
	assert.NoError(t, err, "conflict on volume create should be treated as idempotent success")
}

func TestStartContainer_NetworkConnectConflict(t *testing.T) {
	f := newFakeSDK()
	f.connectErr = errdefs.ErrConflict // already connected — no error
	c := newTestClient(f)
	err := c.StartContainer(context.Background(), docker.ContainerSpec{
		Name: "loki", Image: "test:latest",
		Networks: []string{"net1", "net2"},
	})
	assert.NoError(t, err)
}

func TestStartContainer_CreateConflict_StartsExisting(t *testing.T) {
	f := newFakeSDK()
	f.createErr = errdefs.ErrConflict // container already exists, just start it
	c := newTestClient(f)
	err := c.StartContainer(context.Background(), docker.ContainerSpec{
		Name: "loki", Image: "test:latest",
	})
	assert.NoError(t, err)
}

func TestStartContainer_CreateError_NonConflict(t *testing.T) {
	f := newFakeSDK()
	f.createErr = errors.New("no disk space")
	c := newTestClient(f)
	err := c.StartContainer(context.Background(), docker.ContainerSpec{
		Name: "loki", Image: "test:latest",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating container")
}

func TestStartContainer_NetworkConnectError(t *testing.T) {
	f := newFakeSDK()
	f.connectErr = errors.New("network unreachable")
	c := newTestClient(f)
	err := c.StartContainer(context.Background(), docker.ContainerSpec{
		Name: "loki", Image: "test:latest",
		Networks: []string{"net1", "net2"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting container")
}

func TestPullImage_ImageExistsError(t *testing.T) {
	f := newFakeSDK()
	f2 := &fakeSDKWithInspectError{fakeSDK: f, inspectErr: errors.New("storage error")}
	c := docker.NewTestClient(f2)
	err := c.PullImage(context.Background(), "grafana/loki:3.0.0", nil)
	require.Error(t, err)
	// The error comes from ImageExists → inspecting image
	assert.Contains(t, err.Error(), "inspecting image")
}

// ---------------------------------------------------------------------------
// Helper: fakeSDK variant that returns a non-not-found error from ImageInspect
// ---------------------------------------------------------------------------

type fakeSDKWithInspectError struct {
	*fakeSDK
	inspectErr error
}

func (f *fakeSDKWithInspectError) ImageInspectWithRaw(_ context.Context, _ string) (image.InspectResponse, []byte, error) {
	return image.InspectResponse{}, nil, f.inspectErr
}

// fakeSDKWithContainerError returns a non-not-found error from ContainerInspect.
type fakeSDKWithContainerError struct {
	*fakeSDK
	containerErr error
}

func (f *fakeSDKWithContainerError) ContainerInspect(_ context.Context, _ string) (container.InspectResponse, error) {
	return container.InspectResponse{}, f.containerErr
}

// fakeSDKWithListErrors returns errors from list operations.
type fakeSDKWithListErrors struct {
	*fakeSDK
	netListErr error
	volListErr error
}

func (f *fakeSDKWithListErrors) NetworkList(_ context.Context, _ network.ListOptions) ([]network.Summary, error) {
	if f.netListErr != nil {
		return nil, f.netListErr
	}
	return f.fakeSDK.NetworkList(context.Background(), network.ListOptions{})
}

func (f *fakeSDKWithListErrors) VolumeList(_ context.Context, _ volume.ListOptions) (volume.ListResponse, error) {
	if f.volListErr != nil {
		return volume.ListResponse{}, f.volListErr
	}
	return f.fakeSDK.VolumeList(context.Background(), volume.ListOptions{})
}

// fakeSDKWithListError returns an error from ContainerList.
type fakeSDKWithListError struct {
	*fakeSDK
	listErr error
}

func (f *fakeSDKWithListError) ContainerList(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
	return nil, f.listErr
}

func TestContainerRunning_ListError(t *testing.T) {
	f := newFakeSDK()
	f2 := &fakeSDKWithListError{fakeSDK: f, listErr: errors.New("list failed")}
	c := docker.NewTestClient(f2)
	_, err := c.ContainerRunning(context.Background(), "loki")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing container")
}

func TestContainerID_InspectError(t *testing.T) {
	f := newFakeSDK()
	f2 := &fakeSDKWithContainerError{fakeSDK: f, containerErr: errors.New("inspect failed")}
	c := docker.NewTestClient(f2)
	_, err := c.ContainerID(context.Background(), "loki")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting container")
}

func TestNetworkExists_ListError(t *testing.T) {
	f := newFakeSDK()
	f2 := &fakeSDKWithListErrors{fakeSDK: f, netListErr: errors.New("list failed")}
	c := docker.NewTestClient(f2)
	_, err := c.NetworkExists(context.Background(), "errorprobe-net")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing networks")
}

func TestCreateNetwork_Error(t *testing.T) {
	f := newFakeSDK()
	f.createNetErr = errors.New("network create failed")
	c := newTestClient(f)
	err := c.CreateNetwork(context.Background(), "errorprobe-net")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating network")
}

func TestRemoveNetwork_Error(t *testing.T) {
	f := newFakeSDK()
	f.networks["errorprobe-net"] = true
	f.removeNetErr = errors.New("remove failed")
	c := newTestClient(f)
	err := c.RemoveNetwork(context.Background(), "errorprobe-net")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing network")
}

func TestVolumeExists_ListError(t *testing.T) {
	f := newFakeSDK()
	f2 := &fakeSDKWithListErrors{fakeSDK: f, volListErr: errors.New("vol list failed")}
	c := docker.NewTestClient(f2)
	_, err := c.VolumeExists(context.Background(), "myvol")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing volumes")
}

func TestCreateVolume_Error(t *testing.T) {
	f := newFakeSDK()
	f.createVolErr = errors.New("volume create failed")
	c := newTestClient(f)
	err := c.CreateVolume(context.Background(), "myvol")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating volume")
}

func TestRemoveVolume_Error(t *testing.T) {
	f := newFakeSDK()
	f.volumes["myvol"] = true
	f.removeVolErr = errors.New("remove failed")
	c := newTestClient(f)
	err := c.RemoveVolume(context.Background(), "myvol")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing volume")
}

func TestStartContainer_WithMultipleNetworks(t *testing.T) {
	f := newFakeSDK()
	f.networks["net1"] = true
	f.networks["net2"] = true
	c := newTestClient(f)
	spec := docker.ContainerSpec{
		Name:     "multi-net",
		Image:    "test:latest",
		Networks: []string{"net1", "net2"},
	}
	err := c.StartContainer(context.Background(), spec)
	assert.NoError(t, err)
}

func TestStartContainer_StartError(t *testing.T) {
	f := newFakeSDK()
	f.startErr = errors.New("start failed")
	c := newTestClient(f)
	err := c.StartContainer(context.Background(), docker.ContainerSpec{Name: "loki", Image: "test:latest"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting container")
}
