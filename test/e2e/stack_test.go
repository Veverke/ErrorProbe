//go:build integration

package e2e_test

// Tests in this file exercise the full EP stack lifecycle (ep up / ep down).
// They are the heaviest tests: stack.Up() pulls Docker images on first run and
// starts real Vector, Loki, and Grafana containers.
//
// All tests call ensureStackDown both before (isolation) and after (cleanup)
// via t.Cleanup, so the Docker environment is identical to before the suite ran.
//
// Run individually with:
//
//	go test -tags integration -run TestStack ./test/e2e/ -timeout 10m -v

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/docker"
	"github.com/errorprobe/errorprobe/internal/stack"
)

// loadDefaultConfig loads configuration using EP's default resolution:
// project-local errorprobe.yaml → global ~/.errorprobe/config.yaml → built-in defaults.
func loadDefaultConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load("")
	require.NoError(t, err)
	return cfg
}

// ensureStackDown brings down any running EP stack and removes all volumes.
// Errors are silently ignored — it is a best-effort pre/post condition.
func ensureStackDown(cfg *config.Config) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	_ = stack.Down(ctx, cfg, true /*purge*/, nil)
}

// ---------------------------------------------------------------------------
// TestStack_Up_StartsAllContainers (#1)
// ---------------------------------------------------------------------------

func TestStack_Up_StartsAllContainers(t *testing.T) {
	cfg := loadDefaultConfig(t)
	ensureStackDown(cfg)
	t.Cleanup(func() { ensureStackDown(cfg) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	err := stack.Up(ctx, cfg, func(msg string) { t.Logf("[up] %s", msg) })
	require.NoError(t, err)

	cli, err := docker.NewClient()
	require.NoError(t, err)
	defer cli.Close()

	running, err := stack.IsStackRunning(ctx, cfg, cli)
	require.NoError(t, err)
	assert.True(t, running,
		"all three stack containers (loki, vector, grafana) must be running after ep up")
}

// ---------------------------------------------------------------------------
// TestStack_Up_Idempotent (#2)
// ---------------------------------------------------------------------------

func TestStack_Up_Idempotent(t *testing.T) {
	cfg := loadDefaultConfig(t)
	ensureStackDown(cfg)
	t.Cleanup(func() { ensureStackDown(cfg) })

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	require.NoError(t, stack.Up(ctx, cfg, nil), "first up must succeed")

	// Second call against an already-running stack must be a no-op (idempotent).
	err := stack.Up(ctx, cfg, func(msg string) { t.Logf("[second up] %s", msg) })
	require.NoError(t, err, "second up against a running stack must not return an error")

	cli, err := docker.NewClient()
	require.NoError(t, err)
	defer cli.Close()

	running, err := stack.IsStackRunning(ctx, cfg, cli)
	require.NoError(t, err)
	assert.True(t, running, "stack must remain running after a second up call")
}

// ---------------------------------------------------------------------------
// TestStack_Down_StopsContainers (#4)
// ---------------------------------------------------------------------------

func TestStack_Down_StopsContainers(t *testing.T) {
	cfg := loadDefaultConfig(t)
	ensureStackDown(cfg)
	t.Cleanup(func() { ensureStackDown(cfg) })

	upCtx, upCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer upCancel()
	require.NoError(t, stack.Up(upCtx, cfg, nil))

	// Down without --purge: containers removed, volumes preserved.
	downCtx, downCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer downCancel()
	err := stack.Down(downCtx, cfg, false /*purge*/, func(msg string) { t.Logf("[down] %s", msg) })
	require.NoError(t, err)

	cli, err := docker.NewClient()
	require.NoError(t, err)
	defer cli.Close()

	running, err := stack.IsStackRunning(context.Background(), cfg, cli)
	require.NoError(t, err)
	assert.False(t, running, "all containers must be stopped after ep down")

	// Named data volumes must survive when --purge is not given.
	for _, vol := range []string{stack.VolumeLokiData, stack.VolumeGrafanaData} {
		exists, err := cli.VolumeExists(context.Background(), vol)
		require.NoError(t, err)
		assert.True(t, exists, "volume %q must survive ep down without --purge", vol)
	}
}

// ---------------------------------------------------------------------------
// TestStack_Down_Purge_RemovesVolumes (#5)
// ---------------------------------------------------------------------------

func TestStack_Down_Purge_RemovesVolumes(t *testing.T) {
	cfg := loadDefaultConfig(t)
	ensureStackDown(cfg)
	// No additional cleanup needed — purge leaves nothing behind.

	upCtx, upCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer upCancel()
	require.NoError(t, stack.Up(upCtx, cfg, nil))

	downCtx, downCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer downCancel()
	require.NoError(t, stack.Down(downCtx, cfg, true /*purge*/, nil))

	cli, err := docker.NewClient()
	require.NoError(t, err)
	defer cli.Close()

	for _, vol := range []string{stack.VolumeLokiData, stack.VolumeGrafanaData} {
		exists, err := cli.VolumeExists(context.Background(), vol)
		require.NoError(t, err)
		assert.False(t, exists, "volume %q must be removed by ep down --purge", vol)
	}
}

// ---------------------------------------------------------------------------
// TestStack_Up_PortConflict (#3)
// ---------------------------------------------------------------------------

func TestStack_Up_PortConflict(t *testing.T) {
	cfg := loadDefaultConfig(t)
	ensureStackDown(cfg)
	t.Cleanup(func() { ensureStackDown(cfg) })

	// Bind Loki's port before calling Up so CheckPorts detects a conflict.
	// CheckPorts retries for up to 10 s before giving up, so this test takes
	// roughly 10 s by design — that is the intended behaviour being tested.
	lokiAddr := fmt.Sprintf("127.0.0.1:%d", cfg.Stack.Loki.Port)
	ln, err := net.Listen("tcp", lokiAddr)
	if err != nil {
		t.Skipf("cannot bind %s to simulate port conflict (already in use): %v", lokiAddr, err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = stack.Up(ctx, cfg, nil)
	require.Error(t, err, "ep up must fail when the Loki port is already bound")
	assert.Contains(t, err.Error(), "port conflict",
		"error message must identify the cause as a port conflict")
}
