//go:build integration

package e2e_test

// Tests in this file exercise container discovery using real Docker containers
// created by testcontainers-go. Ryuk (the testcontainers reaper sidecar) plus
// explicit t.Cleanup callbacks guarantee all containers are removed after each
// test, regardless of test outcome.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/discovery"
	"github.com/errorprobe/errorprobe/internal/docker"
)

// startAlpineContainer starts an alpine:latest container running `sleep 600`.
// name must be unique across concurrent tests (use a timestamp suffix).
// Returns the container name as Docker reports it (no leading slash).
// Registers t.Cleanup to terminate the container.
func startAlpineContainer(t *testing.T, name string) string {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image: "alpine:latest",
		Cmd:   []string{"sh", "-c", "sleep 600"},
		Name:  name,
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "failed to start stub alpine container %q", name)

	t.Cleanup(func() {
		_ = c.Terminate(context.Background())
	})

	cname, err := c.Name(ctx)
	require.NoError(t, err)
	return strings.TrimPrefix(cname, "/")
}

// newDockerClient creates an EP Docker client and registers Close in t.Cleanup.
func newDockerClient(t *testing.T) docker.DockerAPI {
	t.Helper()
	cli, err := docker.NewClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

// uniqueName returns a container name that is unique per test run.
func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// ---------------------------------------------------------------------------
// TestList_ShowsDockerContainers (#7)
// ---------------------------------------------------------------------------

func TestList_ShowsDockerContainers(t *testing.T) {
	name := startAlpineContainer(t, uniqueName("ep-e2e-stub"))

	cli := newDockerClient(t)
	containers, err := discovery.ListRunning(context.Background(), cli)
	require.NoError(t, err)

	var found bool
	for _, c := range containers {
		if c.Name == name {
			found = true
			assert.Equal(t, "docker", c.Runtime)
			assert.Equal(t, "running", c.InfraStatus)
		}
	}
	assert.True(t, found, "stub container %q must appear in ListRunning output", name)
}

// ---------------------------------------------------------------------------
// TestList_ExcludePolicy (#13)
// ---------------------------------------------------------------------------

func TestList_ExcludePolicy(t *testing.T) {
	nameKept := startAlpineContainer(t, uniqueName("ep-e2e-kept"))
	nameExcl := startAlpineContainer(t, uniqueName("ep-e2e-excl"))

	cli := newDockerClient(t)
	raw, err := discovery.ListRunning(context.Background(), cli)
	require.NoError(t, err)

	cfg := &config.Config{
		Containers: config.Containers{Exclude: []string{nameExcl}},
	}
	approved := discovery.ApplyPolicy(raw, cfg)

	approvedNames := make(map[string]bool, len(approved))
	for _, c := range approved {
		approvedNames[c.Name] = true
	}

	assert.True(t, approvedNames[nameKept],
		"container %q must pass the policy", nameKept)
	assert.False(t, approvedNames[nameExcl],
		"container %q must be filtered out by the exclude rule", nameExcl)
}

// ---------------------------------------------------------------------------
// TestList_IncludePolicy (#14)
// ---------------------------------------------------------------------------

func TestList_IncludePolicy(t *testing.T) {
	nameAllow := startAlpineContainer(t, uniqueName("ep-e2e-allow"))
	nameDeny := startAlpineContainer(t, uniqueName("ep-e2e-deny"))

	cli := newDockerClient(t)
	raw, err := discovery.ListRunning(context.Background(), cli)
	require.NoError(t, err)

	// Include-list: only the "allow" container should pass.
	cfg := &config.Config{
		Containers: config.Containers{Include: []string{nameAllow}},
	}
	approved := discovery.ApplyPolicy(raw, cfg)

	approvedNames := make(map[string]bool, len(approved))
	for _, c := range approved {
		approvedNames[c.Name] = true
	}

	assert.True(t, approvedNames[nameAllow],
		"container matching the include-list must pass")
	assert.False(t, approvedNames[nameDeny],
		"container not in the include-list must be excluded")
}

// ---------------------------------------------------------------------------
// TestList_EPManagedContainerExcluded (#7 — EP label exclusion)
// ---------------------------------------------------------------------------

func TestList_EPManagedContainerExcluded(t *testing.T) {
	// A container bearing managed-by=errorprobe must never appear in ListRunning,
	// even when it is running. This is what prevents EP's own stack containers
	// (loki, vector, grafana) from being watched by the discovery loop.
	ctx := context.Background()
	name := uniqueName("ep-e2e-managed")

	req := testcontainers.ContainerRequest{
		Image:  "alpine:latest",
		Cmd:    []string{"sh", "-c", "sleep 600"},
		Name:   name,
		Labels: map[string]string{"managed-by": "errorprobe"},
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })

	cli := newDockerClient(t)
	containers, err := discovery.ListRunning(ctx, cli)
	require.NoError(t, err)

	for _, cont := range containers {
		assert.NotEqual(t, name, cont.Name,
			"EP-managed container must be excluded from ListRunning")
	}
}
