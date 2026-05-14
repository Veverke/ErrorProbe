//go:build integration

package e2e_test

// cmd_stack_ext_test.go covers `ep restart`, `ep check`, and `ep logs`.
//
// Stack consumers (check, logs) run as subtests of TestWithSharedStack so the
// stack lifecycle is controlled by a single t.Cleanup — no reliance on test
// execution order or naming conventions.
//
// Stack destructors (restart) are independent top-level tests that manage their
// own lifecycle with ensureStackDown + t.Cleanup.

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

	"github.com/errorprobe/errorprobe/internal/health"
	"github.com/errorprobe/errorprobe/internal/stack"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func lokiPort(t *testing.T) int {
	t.Helper()
	return loadDefaultConfig(t).Stack.Loki.Port
}

// pushLogsToLoki sends one log line directly to Loki's push API.
// Pass ts=0 to use time.Now().
func pushLogsToLoki(t *testing.T, port int, container, level, message string, ts int64) {
	t.Helper()
	if ts == 0 {
		ts = time.Now().UnixNano()
	}
	body := map[string]any{
		"streams": []map[string]any{
			{
				"stream": map[string]string{"container": container, "level": level},
				"values": [][]string{{strconv.FormatInt(ts, 10), message}},
			},
		},
	}
	payload, err := json.Marshal(body)
	require.NoError(t, err)
	rawURL := fmt.Sprintf("http://127.0.0.1:%d/loki/api/v1/push", port)
	req, err := http.NewRequest(http.MethodPost, rawURL, bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Less(t, resp.StatusCode, 300, "Loki push must succeed")
}

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

// ---------------------------------------------------------------------------
// TestWithSharedStack — ep check + ep logs
//
// The stack is brought up once for the entire parent test and torn down by
// t.Cleanup. Subtests run sequentially in declaration order. No test naming
// convention or alphabetical sorting is required.
// ---------------------------------------------------------------------------

func TestWithSharedStack(t *testing.T) {
	cfg := loadDefaultConfig(t)
	ensureStackDown(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	require.NoError(t, stack.Up(ctx, cfg, nil), "shared stack must start")
	t.Cleanup(func() { ensureStackDown(cfg) })

	port := lokiPort(t)
	waitForLokiReady(t, port, 30*time.Second)

	// -- ep check -------------------------------------------------------

	t.Run("CheckAllOK", func(t *testing.T) {
		home := tempHome(t)
		writeSnapshot(t, home, snapOK("healthy-svc"))
		stdout, _, exitCode := runEP(t, home, "check")
		t.Logf("stdout: %s", stdout)
		assert.Equal(t, 0, exitCode)
		assert.Contains(t, stdout, "healthy")
	})

	t.Run("CheckWithErrors", func(t *testing.T) {
		home := tempHome(t)
		writeSnapshot(t, home, snapErrors("broken-svc", "connection refused", 5))
		_, _, exitCode := runEP(t, home, "check")
		assert.Equal(t, 1, exitCode)
	})

	t.Run("CheckJSON", func(t *testing.T) {
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
		require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &result))
		assert.False(t, result.OK)
		require.NotEmpty(t, result.Failing)
		assert.Equal(t, "api-svc", result.Failing[0].Name)
		assert.Equal(t, string(health.StateHasErrors), result.Failing[0].State)
	})

	// -- ep logs --------------------------------------------------------

	t.Run("LogsStream", func(t *testing.T) {
		container := uniqueName("logtest")
		pushLogsToLoki(t, port, container, "info", "hello-from-loki-push", 0)
		stdout, _, _ := runEPWithTimeout(t, tempHome(t), 10*time.Second, "logs", container)
		assert.Contains(t, stdout, "hello-from-loki-push")
	})

	t.Run("LogsErrorsOnly", func(t *testing.T) {
		container := uniqueName("logtest-erronly")
		now := time.Now().UnixNano()
		pushLogsToLoki(t, port, container, "info", "routine-info-line", now)
		pushLogsToLoki(t, port, container, "error", "ERROR critical failure", now+1)
		stdout, _, _ := runEPWithTimeout(t, tempHome(t), 10*time.Second, "logs", container, "--errors-only")
		assert.Contains(t, stdout, "ERROR critical failure")
		assert.NotContains(t, stdout, "routine-info-line")
	})

	t.Run("LogsSince", func(t *testing.T) {
		container := uniqueName("logtest-since")
		old := time.Now().Add(-5 * time.Minute).UnixNano()
		pushLogsToLoki(t, port, container, "info", "old-line-must-be-excluded", old)
		pushLogsToLoki(t, port, container, "info", "recent-line-must-appear", 0)
		stdout, _, _ := runEPWithTimeout(t, tempHome(t), 10*time.Second, "logs", container, "--since", "30s")
		assert.Contains(t, stdout, "recent-line-must-appear")
		assert.NotContains(t, stdout, "old-line-must-be-excluded")
	})

	t.Run("LogsJSON", func(t *testing.T) {
		container := uniqueName("logtest-json")
		pushLogsToLoki(t, port, container, "info", "json-log-output-test", 0)
		stdout, _, _ := runEPWithTimeout(t, tempHome(t), 10*time.Second, "logs", container, "--json")
		lines := nonEmptyLines([]byte(stdout))
		require.NotEmpty(t, lines)
		var entry struct {
			Time      string `json:"time"`
			Container string `json:"container"`
			Line      string `json:"line"`
		}
		require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry))
		assert.NotEmpty(t, entry.Time)
		assert.Equal(t, container, entry.Container)
		assert.Contains(t, entry.Line, "json-log-output-test")
	})
}

// ---------------------------------------------------------------------------
// TestRestart_* — ep restart
//
// These tests manage their own stack lifecycle independently. They do NOT
// call stack.Up() before running ep restart because restart = down+up.
// ---------------------------------------------------------------------------

func TestRestart_CyclesStack(t *testing.T) {
	cfg := loadDefaultConfig(t)
	ensureStackDown(cfg)
	t.Cleanup(func() { ensureStackDown(cfg) })
	// restart stays in the foreground (identical to 'ep up'); use a timeout so
	// the test can verify the stack is running once services are healthy.
	stdout, stderr, _ := runEPWithTimeout(t, tempHome(t), 90*time.Second, "restart")
	t.Logf("stdout: %s", stdout)
	t.Logf("stderr: %s", stderr)
	cli := newDockerClient(t)
	defer cli.Close()
	running, err := stack.IsStackRunning(t.Context(), cfg, cli)
	require.NoError(t, err)
	assert.True(t, running)
}

func TestRestart_Purge(t *testing.T) {
	cfg := loadDefaultConfig(t)
	ensureStackDown(cfg)
	t.Cleanup(func() { ensureStackDown(cfg) })
	// restart stays in the foreground (identical to 'ep up'); use a timeout so
	// the test can verify the stack is running once services are healthy.
	stdout, stderr, _ := runEPWithTimeout(t, tempHome(t), 90*time.Second, "restart", "--purge")
	t.Logf("stdout: %s", stdout)
	t.Logf("stderr: %s", stderr)
	cli := newDockerClient(t)
	defer cli.Close()
	running, err := stack.IsStackRunning(t.Context(), cfg, cli)
	require.NoError(t, err)
	assert.True(t, running)
}
