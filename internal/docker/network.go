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
