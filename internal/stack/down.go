package stack

import (
	"context"
	"fmt"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/docker"
)

// Down stops and removes the observability stack containers and the shared network.
// Order: Vector, Grafana, Loki (reverse of start).
// If purge is true, the named data volumes are also removed.
// The function is idempotent: absent containers/networks/volumes are silently skipped.
func Down(ctx context.Context, cfg *config.Config, purge bool) error {
	cli, err := docker.NewClient()
	if err != nil {
		return fmt.Errorf("connecting to docker: %w", err)
	}
	defer cli.Close()
	return downCore(ctx, cfg, cli, purge)
}

// downCore is the testable implementation of Down. It receives an
// already-created Docker client so that tests can inject a mock.
func downCore(ctx context.Context, _ *config.Config, cli docker.DockerAPI, purge bool) error {
	const stopTimeout = 10

	// Stop and remove in reverse order.
	for _, name := range []string{ContainerVector, ContainerGrafana, ContainerLoki} {
		if err := cli.StopContainer(ctx, name, stopTimeout); err != nil {
			return err
		}
		if err := cli.RemoveContainer(ctx, name, false); err != nil {
			return err
		}
	}

	// Remove the shared network.
	if err := cli.RemoveNetwork(ctx, NetworkName); err != nil {
		return err
	}

	// Optionally purge data volumes.
	if purge {
		for _, vol := range []string{VolumeLokiData, VolumeGrafanaData} {
			if err := cli.RemoveVolume(ctx, vol); err != nil {
				return err
			}
		}
	}

	return nil
}
