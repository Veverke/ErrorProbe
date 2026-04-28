// export_test.go exposes internal functions for use in package tests.
// It is compiled only when running tests.
package stack

import (
	"context"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/docker"
)

// UpCore exposes the internal upCore function for unit testing with an injected
// Docker client and a controllable poll function.
func UpCore(ctx context.Context, cfg *config.Config, cli docker.DockerAPI, onStatus func(string)) error {
	return upCore(ctx, cfg, cli, onStatus)
}

// DownCore exposes the internal downCore function for unit testing.
func DownCore(ctx context.Context, cfg *config.Config, cli docker.DockerAPI, purge bool) error {
	return downCore(ctx, cfg, cli, purge)
}

// SetPollFn replaces the package-level health-poll function.
// Call with the original PollUntilReady to restore after the test.
func SetPollFn(fn func(context.Context, *config.Config, func(string)) error) {
	pollFn = fn
}

