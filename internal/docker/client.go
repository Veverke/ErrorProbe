package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
)

// NetworkName is the Docker network shared by all errorprobe-managed containers.
const NetworkName = "errorprobe-net"

// Client wraps the Docker SDK client.
type Client struct {
	cli sdkAPI
}

// NewClient creates a new Client, negotiates the API version, and pings the daemon.
func NewClient() (*Client, error) {
	sdk, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	c := &Client{cli: sdk}
	if err := c.Ping(context.Background()); err != nil {
		_ = sdk.Close()
		return nil, err
	}
	return c, nil
}

// newClientWithSDK creates a Client using a provided sdkAPI (for testing).
func newClientWithSDK(sdk sdkAPI) *Client {
	return &Client{cli: sdk}
}

// Close releases the underlying Docker client connection.
func (c *Client) Close() error {
	return c.cli.Close()
}

// Ping checks that the Docker daemon is reachable.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.cli.Ping(ctx)
	if err != nil {
		return fmt.Errorf("pinging docker daemon: %w", err)
	}
	return nil
}

// ImageExists returns true when the image is present in the local cache.
func (c *Client) ImageExists(ctx context.Context, img string) (bool, error) {
	_, _, err := c.cli.ImageInspectWithRaw(ctx, img)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspecting image %s: %w", img, err)
	}
	return true, nil
}

// PullImage pulls the image from the registry, calling onProgress for each
// status line. It is a no-op when the image is already present locally.
func (c *Client) PullImage(ctx context.Context, img string, onProgress func(string)) error {
	exists, err := c.ImageExists(ctx, img)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	reader, err := c.cli.ImagePull(ctx, img, pullOptions())
	if err != nil {
		return fmt.Errorf("pulling %s: %w", img, err)
	}
	defer reader.Close()

	dec := json.NewDecoder(reader)
	for {
		var event struct {
			Status string `json:"status"`
			ID     string `json:"id"`
			Error  string `json:"error"`
		}
		if err := dec.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("reading pull progress for %s: %w", img, err)
		}
		if event.Error != "" {
			return fmt.Errorf("pulling %s: %s", img, event.Error)
		}
		if onProgress != nil && event.Status != "" {
			line := event.Status
			if event.ID != "" {
				line = event.ID + ": " + line
			}
			onProgress(line)
		}
	}
	return nil
}

// ContainerRunning returns true when a container with the given name is running.
func (c *Client) ContainerRunning(ctx context.Context, name string) (bool, error) {
	info, err := c.cli.ContainerInspect(ctx, name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspecting container %s: %w", name, err)
	}
	return info.State.Running, nil
}

// ContainerID returns the full container ID for the given name, or "" if absent.
func (c *Client) ContainerID(ctx context.Context, name string) (string, error) {
	info, err := c.cli.ContainerInspect(ctx, name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("inspecting container %s: %w", name, err)
	}
	return info.ID, nil
}

// SendSignal sends a POSIX signal (e.g. "SIGHUP") to the named container.
func (c *Client) SendSignal(ctx context.Context, containerName string, signal string) error {
	if err := c.cli.ContainerKill(ctx, containerName, signal); err != nil {
		return fmt.Errorf("sending signal %s to %s: %w", signal, containerName, err)
	}
	return nil
}
