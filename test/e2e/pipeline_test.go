//go:build integration

package e2e_test

// Tests in this file exercise the ingest → health engine pipeline end-to-end.
// They are fully in-process: no Docker containers, no external services.
// An ingest.HTTPTransport is started on a random port; log events are POSTed
// to it; assertions are made against the health snapshot on disk.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/health"
	"github.com/errorprobe/errorprobe/internal/ingest"
)

// ---------------------------------------------------------------------------
// Tier 1: ingest → health engine
// ---------------------------------------------------------------------------

func TestIngest_ErrorLog_SetsHasErrors(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "health.json")
	engine := health.NewEngine(snapPath, nil)
	addr := startTransport(t, engine.ProcessBatch)

	status := postEvents(t, addr, []ingest.LogEvent{logEvent("myapp", "error", "connection refused")})

	assert.Equal(t, http.StatusNoContent, status)
	snap, err := health.LoadSnapshot(snapPath)
	require.NoError(t, err)
	require.Contains(t, snap.Containers, "myapp")
	assert.Equal(t, health.StateHasErrors, snap.Containers["myapp"].State)
	assert.Equal(t, 1, snap.Containers["myapp"].ErrorCount)
	assert.Equal(t, "connection refused", snap.Containers["myapp"].LastErrorMsg)
}

func TestIngest_WarnLog_SetsHasErrors(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "health.json")
	engine := health.NewEngine(snapPath, nil)
	addr := startTransport(t, engine.ProcessBatch)

	status := postEvents(t, addr, []ingest.LogEvent{logEvent("svc", "warn", "high latency 500ms")})

	assert.Equal(t, http.StatusNoContent, status)
	snap, err := health.LoadSnapshot(snapPath)
	require.NoError(t, err)
	assert.Equal(t, health.StateHasErrors, snap.Containers["svc"].State)
}

func TestIngest_InfoLog_DoesNotChangeState(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "health.json")
	engine := health.NewEngine(snapPath, nil)
	addr := startTransport(t, engine.ProcessBatch)

	postEvents(t, addr, []ingest.LogEvent{logEvent("svc", "info", "application started")})

	snap, err := health.LoadSnapshot(snapPath)
	require.NoError(t, err)
	assert.NotContains(t, snap.Containers, "svc",
		"info-level log must not create a health entry")
}

func TestIngest_MultiLineError_Concatenated(t *testing.T) {
	// The engine performs a "continuation pass": when a stored error ends with ":"
	// it searches the same batch for a follow-on line and appends it.
	snapPath := filepath.Join(t.TempDir(), "health.json")
	engine := health.NewEngine(snapPath, nil)
	addr := startTransport(t, engine.ProcessBatch)

	events := []ingest.LogEvent{
		{Timestamp: time.Now(), Container: "db", Level: "error",
			Message: "Error in process with exit value:", Runtime: "docker"},
		{Timestamp: time.Now(), Container: "db", Level: "info",
			Message: "{database_does_not_exist, reason}", Runtime: "docker"},
	}
	postEvents(t, addr, events)

	snap, err := health.LoadSnapshot(snapPath)
	require.NoError(t, err)
	msg := snap.Containers["db"].LastErrorMsg
	assert.Contains(t, msg, "Error in process with exit value:")
	assert.Contains(t, msg, "database_does_not_exist",
		"follow-on context line must be appended to the error message")
}

func TestIngest_K8sNamespacedKey(t *testing.T) {
	// K8s events carry a Namespace field; the health key must be "namespace/container".
	snapPath := filepath.Join(t.TempDir(), "health.json")
	engine := health.NewEngine(snapPath, nil)
	addr := startTransport(t, engine.ProcessBatch)

	ev := ingest.LogEvent{
		Timestamp: time.Now(),
		Container: "api",
		Namespace: "production",
		Level:     "error",
		Message:   "panic: nil pointer dereference",
		Runtime:   "k8s",
	}
	postEvents(t, addr, []ingest.LogEvent{ev})

	snap, err := health.LoadSnapshot(snapPath)
	require.NoError(t, err)
	assert.Contains(t, snap.Containers, "production/api",
		"K8s health key must be namespace/container")
	assert.Equal(t, health.StateHasErrors, snap.Containers["production/api"].State)
}

func TestIngest_BatchTooLarge_Returns413(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "health.json")
	engine := health.NewEngine(snapPath, nil)
	addr := startTransport(t, engine.ProcessBatch)

	// Build a body that exceeds the 10 MB server-side limit.
	oversized := bytes.Repeat([]byte("x"), 10*1024*1024+1)
	status := postRaw(t, addr, oversized)

	assert.Equal(t, http.StatusRequestEntityTooLarge, status)
}

func TestIngest_MultipleContainers_TrackedIndependently(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "health.json")
	engine := health.NewEngine(snapPath, nil)
	addr := startTransport(t, engine.ProcessBatch)

	postEvents(t, addr, []ingest.LogEvent{
		logEvent("alpha", "error", "alpha crashed"),
		logEvent("beta", "error", "beta crashed"),
		logEvent("gamma", "info", "gamma ok"),
	})

	snap, err := health.LoadSnapshot(snapPath)
	require.NoError(t, err)
	assert.Equal(t, health.StateHasErrors, snap.Containers["alpha"].State)
	assert.Equal(t, health.StateHasErrors, snap.Containers["beta"].State)
	assert.NotContains(t, snap.Containers, "gamma",
		"info-only container must not appear in the snapshot")
}

// ---------------------------------------------------------------------------
// Health state persistence across engine restart
// ---------------------------------------------------------------------------

func TestHealth_PersistsAcrossEngineRestart(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "health.json")

	// First engine instance accumulates an error.
	e1 := health.NewEngine(snapPath, nil)
	e1.ProcessBatch([]ingest.LogEvent{logEvent("worker", "error", "out of memory")})

	snap1, err := health.LoadSnapshot(snapPath)
	require.NoError(t, err)
	require.Equal(t, health.StateHasErrors, snap1.Containers["worker"].State)

	// Simulate a process restart: new engine, same snapshot path.
	e2 := health.NewEngine(snapPath, nil)
	snap2 := e2.Snapshot()

	assert.Equal(t, health.StateHasErrors, snap2.Containers["worker"].State,
		"health state must survive engine restart")
	assert.Equal(t, 1, snap2.Containers["worker"].ErrorCount)
	assert.Equal(t, "out of memory", snap2.Containers["worker"].LastErrorMsg)
}

// ---------------------------------------------------------------------------
// Status reset
// ---------------------------------------------------------------------------

func TestStatus_Reset_ClearsState(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "health.json")
	engine := health.NewEngine(snapPath, nil)
	engine.ProcessBatch([]ingest.LogEvent{logEvent("api", "error", "timeout connecting to database")})

	require.Equal(t, health.StateHasErrors, engine.Snapshot().Containers["api"].State)
	require.NoError(t, engine.Reset("api"))

	snap, err := health.LoadSnapshot(snapPath)
	require.NoError(t, err)
	assert.Equal(t, health.StateOK, snap.Containers["api"].State,
		"reset must transition HAS_ERRORS → OK and persist the change")
}

// ---------------------------------------------------------------------------
// Tier 2: escalation to FAILING
// ---------------------------------------------------------------------------

// mockLokiClient implements health.LokiQueryClient for Tier 2 tests without
// requiring a real Loki instance.
type mockLokiClient struct {
	count    int
	messages []string
	err      error
}

func (m *mockLokiClient) CountErrors(_ context.Context, _ string, _ time.Duration) (int, error) {
	return m.count, m.err
}

func (m *mockLokiClient) QueryErrorMessages(_ context.Context, _ string, _ time.Duration) ([]string, error) {
	return m.messages, m.err
}

// tier2Cfg builds a minimal Config with the given Tier 2 tick interval and
// threshold, leaving all other fields at their zero values.
func tier2Cfg(tickDur string, threshold int) *config.Config {
	return &config.Config{
		Detection: config.Detection{
			Tier2: config.Tier2Config{
				Tick:      tickDur,
				Window:    "3m",
				Threshold: threshold,
			},
		},
	}
}

func TestTier2_RepeatedErrors_SetsFailingState(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "health.json")
	engine := health.NewEngine(snapPath, nil)
	engine.ProcessBatch([]ingest.LogEvent{
		logEvent("payments-api", "error", "connection refused"),
	})

	mock := &mockLokiClient{
		count:    15, // above the threshold of 10
		messages: repeated("connection refused", 15),
	}
	tier2 := health.NewTier2Evaluator(mock, tier2Cfg("50ms", 10), engine, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tier2.Run(ctx)

	waitFor(t, 3*time.Second, func() bool {
		return engine.Snapshot().Containers["payments-api"].State == health.StateFailing
	})

	snap, err := health.LoadSnapshot(snapPath)
	require.NoError(t, err)
	assert.Equal(t, health.StateFailing, snap.Containers["payments-api"].State)
}

func TestTier2_DominantFingerprint_Recorded(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "health.json")
	engine := health.NewEngine(snapPath, nil)
	engine.ProcessBatch([]ingest.LogEvent{
		logEvent("svc", "error", "timeout waiting for db connection"),
	})

	mock := &mockLokiClient{
		count:    12,
		messages: repeated("timeout waiting for db connection", 12),
	}
	tier2 := health.NewTier2Evaluator(mock, tier2Cfg("50ms", 10), engine, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tier2.Run(ctx)

	waitFor(t, 3*time.Second, func() bool {
		return engine.Snapshot().Containers["svc"].State == health.StateFailing
	})

	ch := engine.Snapshot().Containers["svc"]
	assert.Equal(t, health.StateFailing, ch.State)
	assert.NotEmpty(t, ch.DominantFingerprint,
		"dominant fingerprint must be recorded when container enters FAILING")
	assert.GreaterOrEqual(t, ch.DominantFingerprintCount, 10)
}

func TestTier2_ErrorRateDrops_RecoverToHasErrors(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "health.json")
	engine := health.NewEngine(snapPath, nil)
	engine.ProcessBatch([]ingest.LogEvent{logEvent("db", "error", "crash")})

	// Force the container directly into FAILING to set up the recovery scenario.
	require.NoError(t, engine.SetFailing("db", "crash", 20))
	require.Equal(t, health.StateFailing, engine.Snapshot().Containers["db"].State)

	// Loki now reports the error rate has dropped below the threshold.
	mock := &mockLokiClient{count: 2, messages: []string{"crash", "crash"}}
	tier2 := health.NewTier2Evaluator(mock, tier2Cfg("50ms", 10), engine, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tier2.Run(ctx)

	waitFor(t, 3*time.Second, func() bool {
		return engine.Snapshot().Containers["db"].State == health.StateHasErrors
	})

	assert.Equal(t, health.StateHasErrors, engine.Snapshot().Containers["db"].State,
		"container must recover from FAILING to HAS_ERRORS when error rate drops below threshold")
}

func TestTier2_HistoryLog_AppendedOnTransition(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "health.json")
	histPath := filepath.Join(t.TempDir(), "history.jsonl")

	engine := health.NewEngine(snapPath, nil)
	engine.ProcessBatch([]ingest.LogEvent{logEvent("app", "error", "segmentation fault")})

	mock := &mockLokiClient{
		count:    11,
		messages: repeated("segmentation fault", 11),
	}
	histLog := health.NewHistoryLog(histPath)
	tier2 := health.NewTier2Evaluator(mock, tier2Cfg("50ms", 10), engine, histLog)

	ctx, cancel := context.WithCancel(context.Background())
	go tier2.Run(ctx)

	waitFor(t, 3*time.Second, func() bool {
		return engine.Snapshot().Containers["app"].State == health.StateFailing
	})
	cancel()
	// Allow the goroutine to complete its shutdown before reading the file.
	time.Sleep(100 * time.Millisecond)

	data, err := os.ReadFile(histPath)
	require.NoError(t, err)

	lines := nonEmptyLines(data)
	require.NotEmpty(t, lines, "history.jsonl must contain at least one entry after a state transition")

	var entry health.StateTransition
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry))
	assert.Equal(t, "app", entry.ContainerName)
	assert.Equal(t, health.StateHasErrors, entry.From)
	assert.Equal(t, health.StateFailing, entry.To)
	assert.NotEmpty(t, entry.Reason)
}

// ---------------------------------------------------------------------------
// local helpers
// ---------------------------------------------------------------------------

func nonEmptyLines(data []byte) []string {
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}
