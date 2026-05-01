package health

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/ingest"
)

// mockLokiClient implements LokiQueryClient for testing.
type mockLokiClient struct {
	count int
	err   error
}

func (m *mockLokiClient) CountErrors(_ context.Context, _ string, _ time.Duration) (int, error) {
	return m.count, m.err
}

// buildTier2TestCfg returns a minimal config with the given threshold and window.
func buildTier2TestCfg(threshold int, window, tick string) *config.Config {
	return &config.Config{
		Detection: config.Detection{
			Tier2: config.Tier2Config{
				Window:    window,
				Threshold: threshold,
				Tick:      tick,
			},
		},
	}
}

// populateFingerprints drives the engine with n identical error events so the
// fingerprint map accumulates count n for the resulting fingerprint.
func populateFingerprints(e *Engine, container, msg string, n int) {
	events := make([]ingest.LogEvent, n)
	for i := range events {
		events[i] = ingest.LogEvent{
			Container: container,
			Level:     "error",
			Message:   msg,
			Timestamp: time.Now(),
		}
	}
	e.ProcessBatch(events)
}

func TestTier2Evaluator_BelowThreshold_NoTransition(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine(filepath.Join(dir, "health.json"), nil)
	populateFingerprints(e, "api", "connection refused", 5)

	loki := &mockLokiClient{count: 5} // below threshold of 10
	cfg := buildTier2TestCfg(10, "3m", "30s")
	ev := NewTier2Evaluator(loki, cfg, e, nil)
	ev.evaluate(context.Background())

	snap := e.Snapshot()
	assert.Equal(t, StateHasErrors, snap.Containers["api"].State)
}

func TestTier2Evaluator_ThresholdMet_TransitionsToFailing(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine(filepath.Join(dir, "health.json"), nil)
	// 15 identical errors → dominant fingerprint count = 15 ≥ threshold 10
	populateFingerprints(e, "api", "connection refused to postgres", 15)

	loki := &mockLokiClient{count: 15} // ≥ threshold
	cfg := buildTier2TestCfg(10, "3m", "30s")
	ev := NewTier2Evaluator(loki, cfg, e, nil)
	ev.evaluate(context.Background())

	snap := e.Snapshot()
	assert.Equal(t, StateFailing, snap.Containers["api"].State)
	assert.Equal(t, 15, snap.Containers["api"].DominantFingerprintCount)
}

func TestTier2Evaluator_RateDrops_RecoverToHasErrors(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine(filepath.Join(dir, "health.json"), nil)
	populateFingerprints(e, "api", "disk full", 20)

	// First tick: count ≥ threshold → FAILING
	highLoki := &mockLokiClient{count: 20}
	cfg := buildTier2TestCfg(10, "3m", "30s")
	ev := NewTier2Evaluator(highLoki, cfg, e, nil)
	ev.evaluate(context.Background())
	require.Equal(t, StateFailing, e.Snapshot().Containers["api"].State)

	// Second tick: count drops below threshold → back to HAS_ERRORS
	lowLoki := &mockLokiClient{count: 2}
	ev2 := NewTier2Evaluator(lowLoki, cfg, e, nil)
	ev2.evaluate(context.Background())

	assert.Equal(t, StateHasErrors, e.Snapshot().Containers["api"].State)
}

func TestTier2Evaluator_AppendHistoryOnTransition(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine(filepath.Join(dir, "health.json"), nil)
	populateFingerprints(e, "api", "timeout connecting to cache", 12)

	histPath := filepath.Join(dir, "history.jsonl")
	hist := NewHistoryLog(histPath)

	loki := &mockLokiClient{count: 12}
	cfg := buildTier2TestCfg(10, "3m", "30s")
	ev := NewTier2Evaluator(loki, cfg, e, hist)
	ev.evaluate(context.Background())

	require.Equal(t, StateFailing, e.Snapshot().Containers["api"].State)

	entries := readAllTransitions(t, histPath)
	require.Len(t, entries, 1)
	assert.Equal(t, StateHasErrors, entries[0].From)
	assert.Equal(t, StateFailing, entries[0].To)
}

func TestTier2Evaluator_MultipleContainers_IndependentEvaluation(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine(filepath.Join(dir, "health.json"), nil)

	// "highrate" has 15 identical errors → should transition to FAILING
	populateFingerprints(e, "highrate", "out of memory", 15)

	// "lowrate" has only 3 errors → should stay HAS_ERRORS
	populateFingerprints(e, "lowrate", "minor warning", 3)

	// Loki returns 15 for highrate and 3 for lowrate.
	callMap := map[string]int{"highrate": 15, "lowrate": 3}
	loki := &mapMockLoki{counts: callMap}
	cfg := buildTier2TestCfg(10, "3m", "30s")
	ev := NewTier2Evaluator(loki, cfg, e, nil)
	ev.evaluate(context.Background())

	snap := e.Snapshot()
	assert.Equal(t, StateFailing, snap.Containers["highrate"].State)
	assert.Equal(t, StateHasErrors, snap.Containers["lowrate"].State)
}

// mapMockLoki returns counts from a map keyed by container name.
type mapMockLoki struct {
	counts map[string]int
}

func (m *mapMockLoki) CountErrors(_ context.Context, container string, _ time.Duration) (int, error) {
	return m.counts[container], nil
}
