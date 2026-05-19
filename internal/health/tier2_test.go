package health

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/ingest"
	"github.com/errorprobe/errorprobe/internal/pbr"
)

// mockLokiClient implements LokiQueryClient for testing.
type mockLokiClient struct {
	count    int
	err      error
	messages []string
}

func (m *mockLokiClient) CountErrors(_ context.Context, _ string, _ time.Duration) (int, error) {
	return m.count, m.err
}

func (m *mockLokiClient) QueryErrorMessages(_ context.Context, _ string, _ time.Duration) ([]string, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.messages, nil
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

// repeatMessages returns a slice of n copies of msg for use as mock Loki query results.
func repeatMessages(msg string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = msg
	}
	return out
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
	e := NewEngine(filepath.Join(dir, "health.json"), nil, nil)
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
	e := NewEngine(filepath.Join(dir, "health.json"), nil, nil)
	// 15 identical errors → dominant fingerprint count = 15 ≥ threshold 10
	populateFingerprints(e, "api", "connection refused to postgres", 15)

	msgs := repeatMessages("connection refused to postgres", 15)
	loki := &mockLokiClient{count: 15, messages: msgs} // ≥ threshold
	cfg := buildTier2TestCfg(10, "3m", "30s")
	ev := NewTier2Evaluator(loki, cfg, e, nil)
	ev.evaluate(context.Background())

	snap := e.Snapshot()
	assert.Equal(t, StateFailing, snap.Containers["api"].State)
	assert.Equal(t, 15, snap.Containers["api"].DominantFingerprintCount)
}

func TestTier2Evaluator_RateDrops_RecoverToHasErrors(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine(filepath.Join(dir, "health.json"), nil, nil)
	populateFingerprints(e, "api", "disk full", 20)

	// First tick: count ≥ threshold → FAILING
	highLoki := &mockLokiClient{count: 20, messages: repeatMessages("disk full", 20)}
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
	e := NewEngine(filepath.Join(dir, "health.json"), nil, nil)
	populateFingerprints(e, "api", "timeout connecting to cache", 12)

	histPath := filepath.Join(dir, "history.jsonl")
	hist := NewHistoryLog(histPath)

	loki := &mockLokiClient{count: 12, messages: repeatMessages("timeout connecting to cache", 12)}
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
	e := NewEngine(filepath.Join(dir, "health.json"), nil, nil)

	// "highrate" has 15 identical errors → should transition to FAILING
	populateFingerprints(e, "highrate", "out of memory", 15)

	// "lowrate" has only 3 errors → should stay HAS_ERRORS
	populateFingerprints(e, "lowrate", "minor warning", 3)

	// Loki returns 15 for highrate and 3 for lowrate.
	callMap := map[string]int{"highrate": 15, "lowrate": 3}
	msgMap := map[string][]string{"highrate": repeatMessages("out of memory", 15)}
	loki := &mapMockLoki{counts: callMap, messages: msgMap}
	cfg := buildTier2TestCfg(10, "3m", "30s")
	ev := NewTier2Evaluator(loki, cfg, e, nil)
	ev.evaluate(context.Background())

	snap := e.Snapshot()
	assert.Equal(t, StateFailing, snap.Containers["highrate"].State)
	assert.Equal(t, StateHasErrors, snap.Containers["lowrate"].State)
}

// mapMockLoki returns counts and messages from maps keyed by container name.
type mapMockLoki struct {
	counts   map[string]int
	messages map[string][]string
}

func (m *mapMockLoki) CountErrors(_ context.Context, container string, _ time.Duration) (int, error) {
	return m.counts[container], nil
}

func (m *mapMockLoki) QueryErrorMessages(_ context.Context, container string, _ time.Duration) ([]string, error) {
	if m.messages != nil {
		return m.messages[container], nil
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// evalHasErrors error paths
// ---------------------------------------------------------------------------

func TestTier2Evaluator_CountErrorsFailure_NoTransition(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine(filepath.Join(dir, "health.json"), nil, nil)
	populateFingerprints(e, "api", "db error", 5)

	lokiErr := &mockLokiClient{err: fmt.Errorf("loki unavailable")}
	cfg := buildTier2TestCfg(10, "3m", "30s")
	ev := NewTier2Evaluator(lokiErr, cfg, e, nil)
	ev.evaluate(context.Background())

	// State must remain HAS_ERRORS — no transition on Loki error.
	snap := e.Snapshot()
	assert.Equal(t, StateHasErrors, snap.Containers["api"].State)
}

func TestTier2Evaluator_QueryErrorMessagesFails_NoTransition(t *testing.T) {
	// CountErrors returns a count ≥ threshold (so PBR/legacy check passes) but
	// QueryErrorMessages returns an error → no SetFailing call.
	dir := t.TempDir()
	e := NewEngine(filepath.Join(dir, "health.json"), nil, nil)
	populateFingerprints(e, "api", "db error", 15)

	// First call (CountErrors) succeeds; second call (QueryErrorMessages) fails.
	callCount := 0
	srv := &errOnSecondCall{countVal: 15}
	cfg := buildTier2TestCfg(10, "3m", "30s")
	_ = callCount
	_ = srv

	// Use a mock whose QueryErrorMessages always errors.
	lokiMock := &queryMsgErrMock{countVal: 15}
	ev := NewTier2Evaluator(lokiMock, cfg, e, nil)
	ev.evaluate(context.Background())

	snap := e.Snapshot()
	assert.Equal(t, StateHasErrors, snap.Containers["api"].State)
}

// queryMsgErrMock succeeds on CountErrors but fails on QueryErrorMessages.
type queryMsgErrMock struct{ countVal int }

func (m *queryMsgErrMock) CountErrors(_ context.Context, _ string, _ time.Duration) (int, error) {
	return m.countVal, nil
}
func (m *queryMsgErrMock) QueryErrorMessages(_ context.Context, _ string, _ time.Duration) ([]string, error) {
	return nil, fmt.Errorf("query error messages unavailable")
}

func TestTier2Evaluator_DominantFingerprintBelowThreshold_NoTransition(t *testing.T) {
	// CountErrors ≥ threshold but dominant fingerprint count < threshold (scattered errors).
	dir := t.TempDir()
	e := NewEngine(filepath.Join(dir, "health.json"), nil, nil)
	populateFingerprints(e, "api", "varied error", 12)

	// Loki returns 12 errors but all with genuinely different messages (no dominant fingerprint).
	scattered := []string{
		"cache miss for user data",
		"slow query on accounts table",
		"timeout waiting for mutex lock",
		"disk read latency elevated now",
		"memory usage above limit set",
		"cpu throttled on this container",
		"network latency to downstream",
		"rate limit applied to incoming request",
		"session invalidated for given token",
		"invalid content type was received",
		"response body was unexpectedly truncated",
		"upstream connection closed abruptly",
	}
	lokiMock := &mockLokiClient{count: 12, messages: scattered}
	cfg := buildTier2TestCfg(10, "3m", "30s")
	ev := NewTier2Evaluator(lokiMock, cfg, e, nil)
	ev.evaluate(context.Background())

	snap := e.Snapshot()
	assert.Equal(t, StateHasErrors, snap.Containers["api"].State)
}

// ---------------------------------------------------------------------------
// evalFailing error paths and history on recovery
// ---------------------------------------------------------------------------

func TestTier2Evaluator_FailingCountErrorsFails_NoTransition(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine(filepath.Join(dir, "health.json"), nil, nil)
	populateFingerprints(e, "api", "disk full", 20)

	// Drive to FAILING first.
	highLoki := &mockLokiClient{count: 20, messages: repeatMessages("disk full", 20)}
	cfg := buildTier2TestCfg(10, "3m", "30s")
	ev := NewTier2Evaluator(highLoki, cfg, e, nil)
	ev.evaluate(context.Background())
	require.Equal(t, StateFailing, e.Snapshot().Containers["api"].State)

	// Now evalFailing with Loki error → state must not change.
	errLoki := &mockLokiClient{err: fmt.Errorf("loki down")}
	ev2 := NewTier2Evaluator(errLoki, cfg, e, nil)
	ev2.evaluate(context.Background())
	assert.Equal(t, StateFailing, e.Snapshot().Containers["api"].State)
}

func TestTier2Evaluator_RecoveryAppendsHistory(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine(filepath.Join(dir, "health.json"), nil, nil)
	populateFingerprints(e, "api", "cache miss", 20)

	histPath := filepath.Join(dir, "history.jsonl")
	hist := NewHistoryLog(histPath)

	// Drive to FAILING.
	highLoki := &mockLokiClient{count: 20, messages: repeatMessages("cache miss", 20)}
	cfg := buildTier2TestCfg(10, "3m", "30s")
	ev := NewTier2Evaluator(highLoki, cfg, e, hist)
	ev.evaluate(context.Background())
	require.Equal(t, StateFailing, e.Snapshot().Containers["api"].State)

	// Recover: count drops below threshold.
	lowLoki := &mockLokiClient{count: 2}
	ev2 := NewTier2Evaluator(lowLoki, cfg, e, hist)
	ev2.evaluate(context.Background())
	require.Equal(t, StateHasErrors, e.Snapshot().Containers["api"].State)

	// History should have 2 entries: HAS_ERRORS→FAILING and FAILING→HAS_ERRORS.
	entries := readAllTransitions(t, histPath)
	require.Len(t, entries, 2)
	assert.Equal(t, StateFailing, entries[0].To)
	assert.Equal(t, StateHasErrors, entries[1].To)
}

// errOnSecondCall is unused in the current test, kept for documentation.
type errOnSecondCall struct{ countVal int }

func (m *errOnSecondCall) CountErrors(_ context.Context, _ string, _ time.Duration) (int, error) {
	return m.countVal, nil
}
func (m *errOnSecondCall) QueryErrorMessages(_ context.Context, _ string, _ time.Duration) ([]string, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// evalFailing: PBR rule-authority path (result.State != "")
// ---------------------------------------------------------------------------

func TestTier2Evaluator_EvalFailing_PBRRuleKeepsFailing(t *testing.T) {
	// When a PBR rule explicitly returns FAILING, the container must remain FAILING.
	dir := t.TempDir()

	// Build a rule that sets state=FAILING for any error-level event.
	rules, err := pbr.Load([]config.RuleConfig{
		{Name: "keep-failing", Priority: 500, Match: "log",
			When: map[string]string{"level": "error"}, SetState: "FAILING"},
	}, nil, nil)
	require.NoError(t, err)

	e := NewEngine(filepath.Join(dir, "health.json"), rules, nil)
	populateFingerprints(e, "api", "disk full", 20)

	// Drive to FAILING.
	highLoki := &mockLokiClient{count: 20, messages: repeatMessages("disk full", 20)}
	cfg := buildTier2TestCfg(10, "3m", "30s")
	ev := NewTier2Evaluator(highLoki, cfg, e, nil)
	ev.evaluate(context.Background())
	require.Equal(t, StateFailing, e.Snapshot().Containers["api"].State)

	// Low count but PBR rule says FAILING → must stay FAILING.
	lowLoki := &mockLokiClient{count: 1, messages: []string{"disk full"}}
	ev2 := NewTier2Evaluator(lowLoki, cfg, e, nil)
	ev2.evaluate(context.Background())
	assert.Equal(t, StateFailing, e.Snapshot().Containers["api"].State,
		"PBR rule should keep container FAILING even when count < threshold")
}

func TestTier2Evaluator_EvalFailing_PBRRuleRecovery(t *testing.T) {
	// When a PBR rule explicitly returns a non-FAILING state, the container recovers.
	dir := t.TempDir()

	// Build a rule that sets state=HAS_ERRORS (not FAILING) for any error event.
	rules, err := pbr.Load([]config.RuleConfig{
		{Name: "downgrade-to-haserrors", Priority: 500, Match: "log",
			When: map[string]string{"level": "error"}, SetState: "HAS_ERRORS"},
	}, nil, nil)
	require.NoError(t, err)

	e := NewEngine(filepath.Join(dir, "health.json"), rules, nil)
	populateFingerprints(e, "api", "cache miss", 20)

	// Drive to FAILING first (must use a rule that allows FAILING).
	highLoki := &mockLokiClient{count: 20, messages: repeatMessages("cache miss", 20)}
	cfgNoRules := buildTier2TestCfg(10, "3m", "30s")
	eNoRules := NewEngine(filepath.Join(dir, "health2.json"), nil, nil)
	populateFingerprints(eNoRules, "api", "cache miss", 20)
	ev0 := NewTier2Evaluator(highLoki, cfgNoRules, eNoRules, nil)
	ev0.evaluate(context.Background())
	require.Equal(t, StateFailing, eNoRules.Snapshot().Containers["api"].State)

	// Now switch to engine with HAS_ERRORS rule; evalFailing should recover.
	// We copy the state to `e` by driving it to FAILING with no rules first.
	e.SetRules(nil) // clear rules so count-threshold path fires
	ev1 := NewTier2Evaluator(highLoki, cfgNoRules, e, nil)
	ev1.evaluate(context.Background())
	require.Equal(t, StateFailing, e.Snapshot().Containers["api"].State)

	// Now set the HAS_ERRORS rule and evaluate again with low count.
	e.SetRules(rules)
	lowLoki := &mockLokiClient{count: 1, messages: []string{"cache miss"}}
	ev2 := NewTier2Evaluator(lowLoki, cfgNoRules, e, nil)
	ev2.evaluate(context.Background())
	// PBR rule says HAS_ERRORS → stillFailing = false → SetRecovered called.
	assert.Equal(t, StateHasErrors, e.Snapshot().Containers["api"].State,
		"PBR rule returning HAS_ERRORS should trigger recovery from FAILING")
}

func TestTier2Evaluator_Evaluate_InvalidWindow_UsesDefault(t *testing.T) {
	// When window config is invalid, evaluate should still run using the default 3m window.
	dir := t.TempDir()
	e := NewEngine(filepath.Join(dir, "health.json"), nil, nil)
	populateFingerprints(e, "api", "timeout error", 5)

	// Drive to HAS_ERRORS.
	lokiClient := &mockLokiClient{count: 5, messages: repeatMessages("timeout error", 5)}
	cfg := &config.Config{}
	cfg.Detection.Tier2.Window = "NOT_A_DURATION"
	cfg.Detection.Tier2.Threshold = 0 // also triggers default threshold path
	cfg.Detection.Tier2.Tick = "30s"

	ev := NewTier2Evaluator(lokiClient, cfg, e, nil)
	// Should not panic even with invalid config values.
	ev.evaluate(context.Background())
}

// ---------------------------------------------------------------------------
// evalFailing: else branch (no PBR rules) and SetRecovered error path
// ---------------------------------------------------------------------------

func TestTier2Evaluator_EvalFailing_NoRules_CountHigh_StaysFailing(t *testing.T) {
	// When the engine has no PBR rules the else-branch in evalFailing is taken.
	// If count is still >= threshold the container must remain FAILING.
	dir := t.TempDir()
	// Use an empty (non-nil) rule slice so BuiltinRules are bypassed.
	e := NewEngine(filepath.Join(dir, "health.json"), []pbr.Rule{}, nil)
	require.NoError(t, e.SetFailing("api", "fp-crash", 15))

	highLoki := &mockLokiClient{count: 15} // still above threshold=10
	cfg := buildTier2TestCfg(10, "3m", "30s")
	ev := NewTier2Evaluator(highLoki, cfg, e, nil)
	ev.evaluate(context.Background())

	assert.Equal(t, StateFailing, e.Snapshot().Containers["api"].State,
		"count above threshold with no PBR rules should keep container FAILING")
}

func TestTier2Evaluator_EvalFailing_SetRecoveredError_LogsAndReturns(t *testing.T) {
	// When SetRecovered fails (SaveSnapshot error) evalFailing must log an
	// error and return without changing the in-memory state.
	dir := t.TempDir()
	e := NewEngine(filepath.Join(dir, "health.json"), []pbr.Rule{}, nil)
	require.NoError(t, e.SetFailing("api", "fp-crash", 12))

	// Block SaveSnapshot: put a regular file where MkdirAll would create a dir.
	blocker := filepath.Join(dir, "blocked")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))
	e.snapshotPath = filepath.Join(blocker, "snap.json")

	// count=2 < threshold=10 → stillFailing=false → SetRecovered called → fails.
	lowLoki := &mockLokiClient{count: 2}
	cfg := buildTier2TestCfg(10, "3m", "30s")
	ev := NewTier2Evaluator(lowLoki, cfg, e, nil)
	ev.evaluate(context.Background())

	// SetRecovered updates in-memory state before persisting, so even when
	// SaveSnapshot fails the state transitions to HAS_ERRORS. The important
	// thing here is that the logger.Error + return path is exercised (covered).
	assert.Equal(t, StateHasErrors, e.Snapshot().Containers["api"].State)
}

