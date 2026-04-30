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
// onStatus is called with progress messages; pass nil to suppress output.
func Down(ctx context.Context, cfg *config.Config, purge bool, onStatus func(string)) error {
	cli, err := docker.NewClient()
	if err != nil {
		return fmt.Errorf("connecting to docker: %w", err)
	}
	defer cli.Close()
	return downCore(ctx, cfg, cli, purge, onStatus)
}

// downCore is the testable implementation of Down. It receives an
// already-created Docker client so that tests can inject a mock.
func downCore(ctx context.Context, _ *config.Config, cli docker.DockerAPI, purge bool, onStatus func(string)) error {
	if onStatus == nil {
		onStatus = func(string) {}
	}
	const stopTimeout = 10

	// Stop and remove in reverse order.
	for _, name := range []string{ContainerVector, ContainerGrafana, ContainerLoki} {
		onStatus(fmt.Sprintf("stopping %s…", name))
		if err := cli.StopContainer(ctx, name, stopTimeout); err != nil {
			return err
		}
		onStatus(fmt.Sprintf("removing %s…", name))
		if err := cli.RemoveContainer(ctx, name, false); err != nil {
			return err
		}
	}

	// Remove the shared network.
	onStatus(fmt.Sprintf("removing network %s…", NetworkName))
	if err := cli.RemoveNetwork(ctx, NetworkName); err != nil {
		return err
	}

	// Optionally purge data volumes.
	if purge {
		for _, vol := range []string{VolumeLokiData, VolumeGrafanaData} {
			onStatus(fmt.Sprintf("removing volume %s…", vol))
			if err := cli.RemoveVolume(ctx, vol); err != nil {
				return err
			}
		}
	}

	onStatus("stack down")
	return nil
}
