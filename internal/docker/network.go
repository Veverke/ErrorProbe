package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/errdefs"
)

// NetworkExists returns true when a Docker network with the given name exists.
func (c *Client) NetworkExists(ctx context.Context, name string) (bool, error) {
	nets, err := c.cli.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("listing networks: %w", err)
	}
	for _, n := range nets {
		if n.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// CreateNetwork creates a Docker bridge network with the given name.
// It is idempotent: returns nil if the network already exists.
func (c *Client) CreateNetwork(ctx context.Context, name string) error {
	_, err := c.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
	})
	if err != nil {
		if errdefs.IsConflict(err) {
			return nil
		}
		return fmt.Errorf("creating network %s: %w", name, err)
	}
	return nil
}

// DisconnectFromNetwork disconnects the named container from the named network.
// Errors are silently ignored — this is a best-effort cleanup step.
func (c *Client) DisconnectFromNetwork(ctx context.Context, networkName, containerName string) error {
	// force=true removes the endpoint even if the container is not running.
	if err := c.cli.NetworkDisconnect(ctx, networkName, containerName, true); err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("disconnecting %s from network %s: %w", containerName, networkName, err)
	}
	return nil
}

// DisconnectNetworkEndpoints enumerates all containers still registered as
// endpoints on networkName (via NetworkInspect) and force-disconnects each one
// by container ID. On Docker Desktop / Windows, force-removing a container
// releases its name binding immediately but the network endpoint record keyed
// by container ID persists indefinitely; ContainerList cannot find these ghost
// containers, but NetworkInspect.Containers still returns their IDs.
// All errors are silently swallowed — this is a best-effort pre-cleanup step.
func (c *Client) DisconnectNetworkEndpoints(ctx context.Context, networkName string) []string {
	inspect, err := c.cli.NetworkInspect(ctx, networkName, network.InspectOptions{})
	if err != nil {
		return nil
	}
	var found []string
	for containerID, ep := range inspect.Containers {
		short := containerID
		if len(short) > 12 {
			short = short[:12]
		}
		found = append(found, ep.Name+"("+short+")")
		_ = c.cli.NetworkDisconnect(ctx, networkName, containerID, true)
	}
	return found
}

// RemoveNetwork removes the named Docker network.
// Returns nil if the network does not exist.
func (c *Client) RemoveNetwork(ctx context.Context, name string) error {
	if err := c.cli.NetworkRemove(ctx, name); err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("removing network %s: %w", name, err)
	}
	return nil
}
