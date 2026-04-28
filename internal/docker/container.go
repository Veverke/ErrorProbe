package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/errdefs"
)

// PortBinding maps a host port to a container port.
type PortBinding struct {
	HostPort      string
	ContainerPort string
	Protocol      string // "tcp" or "udp"; defaults to "tcp"
}

// Mount is a host-path bind mount.
type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}

// VolumeMount attaches a named Docker volume to a container path.
type VolumeMount struct {
	Name   string // named volume
	Target string // mount point inside the container
}

// ContainerSpec describes a container to be created and started.
type ContainerSpec struct {
	Name     string
	Image    string
	Cmd      []string
	Env      []string
	Ports    []PortBinding
	Mounts   []Mount
	Volumes  []VolumeMount
	Networks []string
	Labels   map[string]string
}

// StartContainer creates and starts a container described by spec.
// It is idempotent: if the container already exists and is running, it returns nil.
func (c *Client) StartContainer(ctx context.Context, spec ContainerSpec) error {
	// If already running, nothing to do.
	running, err := c.ContainerRunning(ctx, spec.Name)
	if err != nil {
		return err
	}
	if running {
		return nil
	}

	// Build mount list.
	var mounts []mount.Mount
	for _, m := range spec.Mounts {
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}
	for _, v := range spec.Volumes {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeVolume,
			Source: v.Name,
			Target: v.Target,
		})
	}

	// Build port bindings.
	portBindings := buildPortBindings(spec.Ports)
	exposedPorts := buildExposedPorts(spec.Ports)

	containerConfig := &container.Config{
		Image:        spec.Image,
		Cmd:          spec.Cmd,
		Env:          spec.Env,
		ExposedPorts: exposedPorts,
		Labels:       spec.Labels,
	}

	networkMode := "bridge"
	var netConfig *network.NetworkingConfig
	if len(spec.Networks) > 0 {
		networkMode = spec.Networks[0]
		netConfig = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				spec.Networks[0]: {},
			},
		}
	}

	hostConfig := &container.HostConfig{
		Mounts:       mounts,
		PortBindings: portBindings,
		NetworkMode:  container.NetworkMode(networkMode),
	}

	resp, err := c.cli.ContainerCreate(ctx, containerConfig, hostConfig, netConfig, nil, spec.Name)
	if err != nil {
		// Container may already exist (stopped); try to start it.
		if !errdefs.IsConflict(err) {
			return fmt.Errorf("creating container %s: %w", spec.Name, err)
		}
	} else {
		_ = resp // ID available if needed
	}

	// Connect additional networks.
	for i, netName := range spec.Networks {
		if i == 0 {
			continue // already set in NetworkingConfig
		}
		if err := c.cli.NetworkConnect(ctx, netName, spec.Name, nil); err != nil {
			if !errdefs.IsConflict(err) {
				return fmt.Errorf("connecting container %s to network %s: %w", spec.Name, netName, err)
			}
		}
	}

	if err := c.cli.ContainerStart(ctx, spec.Name, container.StartOptions{}); err != nil {
		return fmt.Errorf("starting container %s: %w", spec.Name, err)
	}
	return nil
}

// StopContainer stops the named container with the given timeout.
func (c *Client) StopContainer(ctx context.Context, name string, timeoutSecs int) error {
	timeout := timeoutSecs
	stopOpts := container.StopOptions{Timeout: &timeout}
	if err := c.cli.ContainerStop(ctx, name, stopOpts); err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("stopping container %s: %w", name, err)
	}
	return nil
}

// RemoveContainer removes the named container.
// If force is true the container is killed before removal.
// Returns nil if the container does not exist.
func (c *Client) RemoveContainer(ctx context.Context, name string, force bool) error {
	if err := c.cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: force}); err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("removing container %s: %w", name, err)
	}
	return nil
}
