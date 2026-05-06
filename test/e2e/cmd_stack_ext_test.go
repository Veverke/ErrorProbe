//go:build integration

package e2e_test

// cmd_stack_ext_test.go extends the core stack lifecycle tests (stack_test.go)
// with coverage for `ep restart`, `ep check`, and `ep logs` commands.
//
// These tests require a running EP stack (Vector / Loki / Grafana) and a real
// Docker daemon. They are intentionally grouped into a separate file so they
// can be run in isolation when debugging:
//
//	go test -tags integration -run TestStack_Restart ./test/e2e/ -v -timeout 10m

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/docker"
	"github.com/errorprobe/errorprobe/internal/health"
	"github.com/errorprobe/errorprobe/internal/stack"
)

// ---------------------------------------------------------------------------
// Helpers (stack-extension tests only)
// ---------------------------------------------------------------------------

// lokiPort returns the configured Loki HTTP port.
func lokiPort(t *testing.T) int {
	t.Helper()
	return loadDefaultConfig(t).Stack.Loki.Port
}

// pushLogsToLoki sends one or more log lines directly to Loki's push API.
// Each line is associated with the given container label and severity label.
// ts must be a Unix nanoseconds timestamp; pass 0 to use time.Now().
func pushLogsToLoki(t *testing.T, port int, container, level, message string, ts int64) {
	t.Helper()
	if ts == 0 {
		ts = time.Now().UnixNano()
	}
	body := map[string]any{
		"streams": []map[string]any{
			{
				"stream": map[string]string{
					"container": container,
					"level":     level,
				},
				"values": [][]string{
					{strconv.FormatInt(ts, 10), message},
				},
			},
		},
	}
	payload, err := json.Marshal(body)
	require.NoError(t, err)

	url := fmt.Sprintf("http://127.0.0.1:%d/loki/api/v1/push", port)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Less(t, resp.StatusCode, 300,
		"Loki push must succeed (got HTTP %d)", resp.StatusCode)
}

// waitForLokiReady polls the Loki /ready endpoint until it returns HTTP 200.
func waitForLokiReady(t *testing.T, port int, timeout time.Duration) {
	t.Helper()
	u := fmt.Sprintf("http://127.0.0.1:%d/ready", port)
	waitFor(t, timeout, func() bool {
		resp, err := http.Get(u) //nolint:noctx
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
}

// stackIsRunning is a convenience wrapper for the assertion pattern used below.
func stackIsRunning(t *testing.T) bool {
	t.Helper()
	cfg := loadDefaultConfig(t)
	cli, err := docker.NewClient()
	require.NoError(t, err)
	defer cli.Close()
	running, err := stack.IsStackRunning(context.Background(), cfg, cli)
	require.NoError(t, err)
	return running
}

// ---------------------------------------------------------------------------
// TestStack_Restart_CyclesStack
// ---------------------------------------------------------------------------

// TestStack_Restart_CyclesStack verifies that `ep restart` (without --purge)
// brings the stack down and back up and leaves it in a running state.
func TestStack_Restart_CyclesStack(t *testing.T) {
	cfg := loadDefaultConfig(t)
	ensureStackDown(cfg)
	t.Cleanup(func() { ensureStackDown(cfg) })

	// Bring the stack up first.
	upCtx, upCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer upCancel()
	require.NoError(t, stack.Up(upCtx, cfg, nil))

	// Run `ep restart` via subprocess (it calls down then up internally).
	stdout, stderr, exitCode := runEP(t, tempHome(t), "restart")
	t.Logf("restart stdout: %s", stdout)
	t.Logf("restart stderr: %s", stderr)
	require.Equal(t, 0, exitCode, "ep restart must exit 0")

	assert.True(t, stackIsRunning(t), "stack must be running after ep restart")
}

// ---------------------------------------------------------------------------
// TestStack_Restart_Purge
// ---------------------------------------------------------------------------

// TestStack_Restart_Purge verifies that `ep restart --purge` succeeds and
// leaves the stack running after a full volume-wipe cycle.
func TestStack_Restart_Purge(t *testing.T) {
	cfg := loadDefaultConfig(t)
	ensureStackDown(cfg)
	t.Cleanup(func() { ensureStackDown(cfg) })

	upCtx, upCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer upCancel()
	require.NoError(t, stack.Up(upCtx, cfg, nil))

	stdout, stderr, exitCode := runEP(t, tempHome(t), "restart", "--purge")
	t.Logf("restart --purge stdout: %s", stdout)
	t.Logf("restart --purge stderr: %s", stderr)
	require.Equal(t, 0, exitCode, "ep restart --purge must exit 0")

	assert.True(t, stackIsRunning(t), "stack must be running after ep restart --purge")
}

// ---------------------------------------------------------------------------
// TestStack_Check_AllOK_ExitsZero
// ---------------------------------------------------------------------------

// TestStack_Check_AllOK_ExitsZero verifies that `ep check` exits 0 when all
// watched containers are in the OK state.
func TestStack_Check_AllOK_ExitsZero(t *testing.T) {
	cfg := loadDefaultConfig(t)
	ensureStackDown(cfg)
	t.Cleanup(func() { ensureStackDown(cfg) })

	upCtx, upCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer upCancel()
	require.NoError(t, stack.Up(upCtx, cfg, nil))

	home := tempHome(t)
	writeSnapshot(t, home, snapOK("healthy-svc"))

	stdout, _, exitCode := runEP(t, home, "check")
	t.Logf("check stdout: %s", stdout)
	assert.Equal(t, 0, exitCode, "ep check must exit 0 when all containers are OK")
	assert.Contains(t, stdout, "healthy", "check output must confirm healthy state")
}

// ---------------------------------------------------------------------------
// TestStack_Check_WithErrors_ExitsOne
// ---------------------------------------------------------------------------

// TestStack_Check_WithErrors_ExitsOne verifies that `ep check` exits 1 when a
// container is in the HAS_ERRORS state and fail_on is HAS_ERRORS (default).
func TestStack_Check_WithErrors_ExitsOne(t *testing.T) {
	cfg := loadDefaultConfig(t)
	ensureStackDown(cfg)
	t.Cleanup(func() { ensureStackDown(cfg) })

	upCtx, upCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer upCancel()
	require.NoError(t, stack.Up(upCtx, cfg, nil))

	home := tempHome(t)
	writeSnapshot(t, home, snapErrors("broken-svc", "connection refused", 5))

	_, _, exitCode := runEP(t, home, "check")
	assert.Equal(t, 1, exitCode, "ep check must exit 1 when a container has errors")
}

// ---------------------------------------------------------------------------
// TestStack_Check_JSON_Output
// ---------------------------------------------------------------------------

// TestStack_Check_JSON_Output verifies that `ep check --json` exits 1 and
// produces parseable JSON describing the failing containers.
func TestStack_Check_JSON_Output(t *testing.T) {
	cfg := loadDefaultConfig(t)
	ensureStackDown(cfg)
	t.Cleanup(func() { ensureStackDown(cfg) })

	upCtx, upCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer upCancel()
	require.NoError(t, stack.Up(upCtx, cfg, nil))

	home := tempHome(t)
	writeSnapshot(t, home, snapErrors("api-svc", "timeout", 3))

	stdout, _, exitCode := runEP(t, home, "check", "--json")
	assert.Equal(t, 1, exitCode)

	var result struct {
		OK      bool `json:"ok"`
		Failing []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"failing"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &result),
		"--json output must be valid JSON")
	assert.False(t, result.OK)
	require.NotEmpty(t, result.Failing, "failing list must be non-empty")
	assert.Equal(t, "api-svc", result.Failing[0].Name)
	assert.Equal(t, string(health.StateHasErrors), result.Failing[0].State)
}

// ---------------------------------------------------------------------------
// TestStack_Logs_StreamsFromLoki
// ---------------------------------------------------------------------------

// TestStack_Logs_StreamsFromLoki pushes a log line directly into Loki and
// verifies that `ep logs <container>` emits it to stdout within the timeout.
func TestStack_Logs_StreamsFromLoki(t *testing.T) {
	cfg := loadDefaultConfig(t)
	ensureStackDown(cfg)
	t.Cleanup(func() { ensureStackDown(cfg) })

	upCtx, upCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer upCancel()
	require.NoError(t, stack.Up(upCtx, cfg, nil))

	port := lokiPort(t)
	waitForLokiReady(t, port, 30*time.Second)

	container := uniqueName("logtest")
	pushLogsToLoki(t, port, container, "info", "hello-from-loki-push", 0)

	// `ep logs` blocks until killed; kill it after 10 s.
	stdout, _, _ := runEPWithTimeout(t, tempHome(t), 10*time.Second, "logs", container)
	assert.Contains(t, stdout, "hello-from-loki-push",
		"pushed log line must appear in ep logs output")
}

// ---------------------------------------------------------------------------
// TestStack_Logs_ErrorsOnly
// ---------------------------------------------------------------------------

// TestStack_Logs_ErrorsOnly verifies that `ep logs --errors-only` filters out
// non-error log lines.
func TestStack_Logs_ErrorsOnly(t *testing.T) {
	cfg := loadDefaultConfig(t)
	ensureStackDown(cfg)
	t.Cleanup(func() { ensureStackDown(cfg) })

	upCtx, upCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer upCancel()
	require.NoError(t, stack.Up(upCtx, cfg, nil))

	port := lokiPort(t)
	waitForLokiReady(t, port, 30*time.Second)

	container := uniqueName("logtest-erronly")
	now := time.Now().UnixNano()
	// Push an info line (must be excluded) and an error line (must be included).
	pushLogsToLoki(t, port, container, "info", "routine-info-line", now)
	pushLogsToLoki(t, port, container, "error", "ERROR critical failure", now+1)

	stdout, _, _ := runEPWithTimeout(t, tempHome(t), 10*time.Second, "logs", container, "--errors-only")
	assert.Contains(t, stdout, "ERROR critical failure",
		"error line must appear with --errors-only")
	assert.NotContains(t, stdout, "routine-info-line",
		"info line must be excluded by --errors-only")
}

// ---------------------------------------------------------------------------
// TestStack_Logs_Since
// ---------------------------------------------------------------------------

// TestStack_Logs_Since verifies that `ep logs --since 30s` omits log lines
// that were emitted significantly earlier.
func TestStack_Logs_Since(t *testing.T) {
	cfg := loadDefaultConfig(t)
	ensureStackDown(cfg)
	t.Cleanup(func() { ensureStackDown(cfg) })

	upCtx, upCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer upCancel()
	require.NoError(t, stack.Up(upCtx, cfg, nil))

	port := lokiPort(t)
	waitForLokiReady(t, port, 30*time.Second)

	container := uniqueName("logtest-since")
	// Old line: 5 minutes ago (outside 30 s window).
	old := time.Now().Add(-5 * time.Minute).UnixNano()
	pushLogsToLoki(t, port, container, "info", "old-line-must-be-excluded", old)
	// Recent line: now (inside 30 s window).
	pushLogsToLoki(t, port, container, "info", "recent-line-must-appear", 0)

	stdout, _, _ := runEPWithTimeout(t, tempHome(t), 10*time.Second, "logs", container, "--since", "30s")
	assert.Contains(t, stdout, "recent-line-must-appear")
	assert.NotContains(t, stdout, "old-line-must-be-excluded")
}

// ---------------------------------------------------------------------------
// TestStack_Logs_JSON
// ---------------------------------------------------------------------------

// TestStack_Logs_JSON verifies that `ep logs --json` emits JSONL where each
// line is a JSON object with at least "time", "container", and "line" fields.
func TestStack_Logs_JSON(t *testing.T) {
	cfg := loadDefaultConfig(t)
	ensureStackDown(cfg)
	t.Cleanup(func() { ensureStackDown(cfg) })

	upCtx, upCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer upCancel()
	require.NoError(t, stack.Up(upCtx, cfg, nil))

	port := lokiPort(t)
	waitForLokiReady(t, port, 30*time.Second)

	container := uniqueName("logtest-json")
	pushLogsToLoki(t, port, container, "info", "json-log-output-test", 0)

	stdout, _, _ := runEPWithTimeout(t, tempHome(t), 10*time.Second, "logs", container, "--json")

	lines := nonEmptyLines([]byte(stdout))
	require.NotEmpty(t, lines, "ep logs --json must emit at least one JSONL line")

	// Verify the first JSONL line has the expected shape.
	var entry struct {
		Time      string `json:"time"`
		Container string `json:"container"`
		Line      string `json:"line"`
	}
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry),
		"each JSONL line must be a valid JSON object with time/container/line")
	assert.NotEmpty(t, entry.Time)
	assert.Equal(t, container, entry.Container)
	assert.Contains(t, entry.Line, "json-log-output-test")
}
