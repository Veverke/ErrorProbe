package stack

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/docker"
	"github.com/errorprobe/errorprobe/internal/logger"
	"github.com/errorprobe/errorprobe/internal/pid"
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
func downCore(ctx context.Context, cfg *config.Config, cli docker.DockerAPI, purge bool, onStatus func(string)) error {
	if onStatus == nil {
		onStatus = func(string) {}
	}

	// Kill the background 'ep up' process FIRST, before any Docker API calls,
	// unless cmd/down.go already did it (which it does when called via the CLI).
	// This path is retained so that stack.Down() callers (tests, future API) also
	// benefit from the kill without depending on cmd/down.go.
	pidPath := cfg.StateDir() + "ep.pid"
	res, killErr := pid.KillRunning(pidPath)
	logger.Debug("ep up kill attempt",
		"pid_file", pidPath,
		"found", res.Found,
		"pid", res.PID,
		"killed", res.Killed,
		"kill_err", res.KillErr,
		"wait_err", res.WaitErr,
		"err", killErr,
	)
	if !res.Found {
		_ = pid.KillByName("ep")
	}
	// Wait for the pipe to be ready for HEAVY container operations.
	// Ping goes through a fast path in Docker Desktop and succeeds even when
	// the container subsystem is blocked. ContainerList is in the same queue
	// as kill/inspect/remove, so 3 consecutive successes means the pipe is
	// genuinely unblocked for what we're about to do.
	consecutive := 0
	var lastList []container.Summary
	for range 30 {
		listCtx, listCancel := context.WithTimeout(ctx, 3*time.Second)
		list, listErr := cli.ContainerList(listCtx, container.ListOptions{All: true})
		listCancel()
		if listErr == nil {
			consecutive++
			lastList = list
			logger.Debug("container list ok", "count", len(list), "consecutive", consecutive)
			if consecutive >= 3 {
				break
			}
		} else {
			consecutive = 0
			lastList = nil
			logger.Debug("container list failed", "err", listErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	logger.Debug("pipe ready, starting teardown", "containers_visible", len(lastList))
	for _, c := range lastList {
		name := ""
		if len(c.Names) > 0 {
			name = c.Names[0]
		}
		id := c.ID
		if len(id) > 12 {
			id = id[:12]
		}
		logger.Debug("visible container", "name", name, "state", c.State, "status", c.Status, "id", id)
	}

	// Force-remove all containers concurrently.
	//
	// On this system, containerd is saturated by kubelet polling 28+ k8s
	// containers. Any API call that goes through containerd (kill, inspect,
	// stop) reliably times out. force=true on RemoveContainer asks Docker to
	// kill+remove atomically in one request, holding the connection open until
	// Docker is done — no separate kill or inspect polling needed.
	//
	// We use a 15-minute per-container timeout via a derived context so that
	// Ctrl-C (parent ctx) still cancels. All three fire concurrently so the
	// wall-clock total is also bounded by 15 minutes.
	onStatus("removing containers…")
	{
		var (
			wg  sync.WaitGroup
			mu  sync.Mutex
			now = time.Now()
		)
		for _, name := range []string{ContainerGrafana, ContainerLoki, ContainerVector} {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				removeCtx, removeCancel := context.WithTimeout(ctx, 15*time.Minute)
				err := cli.RemoveContainer(removeCtx, name, true)
				removeCancel()
				elapsed := time.Since(now).Round(time.Millisecond)
				logger.Debug("force remove done", "container", name, "elapsed", elapsed, "err", err)
				if err != nil && !strings.Contains(err.Error(), "No such container") {
					mu.Lock()
					onStatus("⚠ " + err.Error())
					mu.Unlock()
				}
			}(name)
		}
		wg.Wait()
	}

	// Remove the shared network.
	// On Docker Desktop / Windows, force-removing a container is asynchronous:
	// the container process is killed but HNS endpoint cleanup takes 15-30 s per
	// container. Poll for up to 60 s, re-disconnecting endpoints on each attempt.
	onStatus(fmt.Sprintf("removing network %s\u2026", NetworkName))
	var netErr error
	for attempt := 0; attempt < 60; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(1 * time.Second):
			}
		}
		// Re-inspect and disconnect on every attempt: Docker Desktop cleans up
		// one container at a time, so new endpoints become removable gradually.
		discCtx, discCancel := context.WithTimeout(ctx, 5*time.Second)
		endpoints := cli.DisconnectNetworkEndpoints(discCtx, NetworkName)
		discCancel()
		if len(endpoints) > 0 {
			logger.Debug("disconnected network endpoints", "attempt", attempt, "endpoints", endpoints)
		}

		netCtx, netCancel := context.WithTimeout(ctx, 5*time.Second)
		netErr = cli.RemoveNetwork(netCtx, NetworkName)
		netCancel()
		if netErr == nil || !strings.Contains(netErr.Error(), "active endpoints") {
			break
		}
	}
	if netErr != nil {
		return netErr
	}

	// Optionally purge data volumes.
	if purge {
		for _, vol := range []string{VolumeLokiData, VolumeGrafanaData} {
			onStatus(fmt.Sprintf("removing volume %s…", vol))
			volCtx, volCancel := context.WithTimeout(ctx, 10*time.Second)
			volErr := cli.RemoveVolume(volCtx, vol)
			volCancel()
			if volErr != nil {
				return volErr
			}
		}

		// Remove the user profile directory (~/.errorprobe/).
		dataDir := cfg.DataDir()
		onStatus(fmt.Sprintf("removing user profile data at %s", dataDir))
		logger.Close() // release this process's own log handle

		// On Windows, killing ep up releases its log file handle asynchronously —
		// the OS may not free the handle immediately after TerminateProcess returns.
		// Retry RemoveAll for up to 5 seconds to give the OS time to close handles.
		var removeErr error
		for attempt := 0; attempt < 10; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(500 * time.Millisecond):
				}
			}
			removeErr = os.RemoveAll(dataDir)
			if removeErr == nil {
				break
			}
			logger.Debug("remove data dir attempt failed", "attempt", attempt+1, "err", removeErr)
		}
		if removeErr != nil {
			return fmt.Errorf("removing data directory %s: %w", dataDir, removeErr)
		}
	}

	onStatus("stack down")
	return nil
}
