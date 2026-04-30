package stack

import (
	"context"
	"fmt"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/docker"
)

// IsStackRunning reports whether all three managed containers (Loki, Grafana,
// Vector) are currently in the running state.
//
// Returns (true, nil) when all containers are running.
// Returns (false, nil) when one or more containers are not running.
// Returns (false, err) on Docker API errors.
func IsStackRunning(ctx context.Context, cfg *config.Config, cli docker.DockerAPI) (bool, error) {
	for _, name := range []string{ContainerLoki, ContainerGrafana, ContainerVector} {
		running, err := cli.ContainerRunning(ctx, name)
		if err != nil {
			return false, fmt.Errorf("checking container %s: %w", name, err)
		}
		if !running {
			return false, nil
		}
	}
	return true, nil
}
