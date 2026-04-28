package docker

import (
	"context"

	"github.com/docker/docker/api/types/container"
)

// DockerAPI defines all Docker operations used by errorprobe.
// It is implemented by *Client and can be mocked in unit tests.
type DockerAPI interface {
	Close() error
	Ping(ctx context.Context) error
	PullImage(ctx context.Context, img string, onProgress func(string)) error
	ImageExists(ctx context.Context, img string) (bool, error)
	ContainerRunning(ctx context.Context, name string) (bool, error)
	ContainerID(ctx context.Context, name string) (string, error)
	SendSignal(ctx context.Context, containerName string, signal string) error
	ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
	ContainerInspect(ctx context.Context, id string) (container.InspectResponse, error)

	NetworkExists(ctx context.Context, name string) (bool, error)
	CreateNetwork(ctx context.Context, name string) error
	RemoveNetwork(ctx context.Context, name string) error

	StartContainer(ctx context.Context, spec ContainerSpec) error
	StopContainer(ctx context.Context, name string, timeoutSecs int) error
	RemoveContainer(ctx context.Context, name string, force bool) error

	VolumeExists(ctx context.Context, name string) (bool, error)
	CreateVolume(ctx context.Context, name string) error
	RemoveVolume(ctx context.Context, name string) error
}
