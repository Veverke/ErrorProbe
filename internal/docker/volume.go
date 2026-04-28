package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/errdefs"
)

// VolumeExists returns true when a named Docker volume with the given name exists.
func (c *Client) VolumeExists(ctx context.Context, name string) (bool, error) {
	vols, err := c.cli.VolumeList(ctx, volume.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("listing volumes: %w", err)
	}
	for _, v := range vols.Volumes {
		if v.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// CreateVolume creates a named Docker volume.
// It is idempotent: returns nil if the volume already exists.
func (c *Client) CreateVolume(ctx context.Context, name string) error {
	_, err := c.cli.VolumeCreate(ctx, volume.CreateOptions{Name: name})
	if err != nil {
		// Docker returns a conflict if the volume already exists.
		if errdefs.IsConflict(err) {
			return nil
		}
		return fmt.Errorf("creating volume %s: %w", name, err)
	}
	return nil
}

// RemoveVolume removes the named Docker volume.
// Returns nil if the volume does not exist.
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	if err := c.cli.VolumeRemove(ctx, name, false); err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("removing volume %s: %w", name, err)
	}
	return nil
}
