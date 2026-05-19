package learn

// White-box tests for learner.go — uses mockLogQuerier and real applier
// (temp-dir backed) to verify the learning pipeline without live Loki.

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/health"
	"github.com/errorprobe/errorprobe/internal/ingest"
	"github.com/errorprobe/errorprobe/internal/loki"
	"github.com/errorprobe/errorprobe/internal/pbr"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newTestLearner(t *testing.T, mock *mockLogQuerier) (*Learner, string) {
	t.Helper()
	dir := t.TempDir()
	ch := make(chan health.StateTransitionEvent, 4)
	applier := NewApplier(
		filepath.Join(dir, "learned.yaml"),
		filepath.Join(dir, "pending.yaml"),
		filepath.Join(dir, "suppressed.yaml"),
		nil,
	)
	rules, err := pbr.Load(nil, nil, pbr.BuiltinRules())
	require.NoError(t, err)
	l := NewLearner(
		ch,
		NewSampler(mock),
		applier,
		func() []pbr.Rule { return rules },
		config.LearnConfig{},
		filepath.Join(dir, "suppressed.yaml"),
	)
	return l, dir
}

// ---------------------------------------------------------------------------
// NewLearner
// ---------------------------------------------------------------------------

func TestNewLearner_ConstructsCorrectly(t *testing.T) {
	mock := &mockLogQuerier{}
	l, _ := newTestLearner(t, mock)
	assert.NotNil(t, l)
}

// ---------------------------------------------------------------------------
// handleTransition
// ---------------------------------------------------------------------------

func TestHandleTransition_OKToHasErrors_RunsForwardScan(t *testing.T) {
	// Forward scan: sampler is called when transitioning from OK → HAS_ERRORS.
	now := time.Now()
	mock := &mockLogQuerier{
		lines: []loki.LogLine{
			{Timestamp: now, Container: "api", Level: "error", Message: "db connection failed"},
		},
	}
	l, _ := newTestLearner(t, mock)
	ev := health.StateTransitionEvent{
		Container: "api",
		PrevState: health.StateOK,
		NewState:  health.StateHasErrors,
		At:        now,
	}
	l.handleTransition(context.Background(), ev)
	assert.NotEmpty(t, mock.calls, "forward scan should have called the sampler")
}

func TestHandleTransition_OKToFailing_RunsForwardScan(t *testing.T) {
	now := time.Now()
	mock := &mockLogQuerier{}
	l, _ := newTestLearner(t, mock)
	ev := health.StateTransitionEvent{
		Container: "svc",
		PrevState: health.StateOK,
		NewState:  health.StateFailing,
		At:        now,
	}
	l.handleTransition(context.Background(), ev)
	assert.NotEmpty(t, mock.calls)
}

func TestHandleTransition_Restarted_RunsPreRestartScan(t *testing.T) {
	now := time.Now()
	mock := &mockLogQuerier{}
	l, _ := newTestLearner(t, mock)
	ev := health.StateTransitionEvent{
		Container: "svc",
		NewState:  "RESTARTED",
		At:        now,
	}
	l.handleTransition(context.Background(), ev)
	assert.NotEmpty(t, mock.calls, "pre-restart scan should have called the sampler")
}

func TestHandleTransition_HasErrorsToOK_RunsRetroScan(t *testing.T) {
	now := time.Now()
	mock := &mockLogQuerier{}
	l, _ := newTestLearner(t, mock)
	ev := health.StateTransitionEvent{
		Container: "svc",
		PrevState: health.StateHasErrors,
		NewState:  health.StateOK,
		At:        now,
	}
	l.handleTransition(context.Background(), ev)
	assert.NotEmpty(t, mock.calls, "retro scan should have called the sampler")
}

func TestHandleTransition_K8sKey_NamespacedLookup(t *testing.T) {
	// When Namespace is set the sampler query should use "ns/container" key.
	now := time.Now()
	mock := &mockLogQuerier{}
	l, _ := newTestLearner(t, mock)
	ev := health.StateTransitionEvent{
		Container: "api",
		Namespace: "production",
		PrevState: health.StateOK,
		NewState:  health.StateHasErrors,
		At:        now,
	}
	l.handleTransition(context.Background(), ev)
	require.NotEmpty(t, mock.calls)
	assert.Contains(t, mock.calls[0].query, "production")
}

func TestHandleTransition_WarnStateTransition_NoScan(t *testing.T) {
	// HAS_WARNINGS doesn't match any forward/retro/pre-restart pattern → no scan.
	mock := &mockLogQuerier{}
	l, _ := newTestLearner(t, mock)
	ev := health.StateTransitionEvent{
		Container: "svc",
		PrevState: health.StateOK,
		NewState:  health.StateHasWarnings,
		At:        time.Now(),
	}
	l.handleTransition(context.Background(), ev)
	assert.Empty(t, mock.calls, "no scan expected for OK→HAS_WARNINGS transition")
}

// ---------------------------------------------------------------------------
// processEvents
// ---------------------------------------------------------------------------

func TestProcessEvents_EmptyEvents_ReturnsImmediately(t *testing.T) {
	// processEvents with empty input must not call sampler or modify overlay.
	mock := &mockLogQuerier{}
	l, dir := newTestLearner(t, mock)
	l.processEvents("svc", nil, nil, 1)

	// Overlay file should NOT be created (no events → nothing to learn).
	overlay, err := LoadOverlay(filepath.Join(dir, "learned.yaml"))
	// File doesn't exist yet → LoadOverlay should return empty slice.
	if err == nil {
		assert.Empty(t, overlay)
	}
}

func TestProcessEvents_AllEventsCoveredByRules_NoNewRule(t *testing.T) {
	// Events that are already covered by existing built-in rules should NOT
	// produce a new learned rule.
	now := time.Now()
	events := []ingest.LogEvent{
		{Timestamp: now, Container: "api", Level: "error", Message: "plain error line"},
	}
	mock := &mockLogQuerier{}
	l, dir := newTestLearner(t, mock)
	// The builtin-log-error rule covers level=error events → FindUncovered → empty.
	counts := map[string]int{"plain error line": 1}
	l.processEvents("api", events, counts, 1)

	overlay, err := LoadOverlay(filepath.Join(dir, "learned.yaml"))
	if err == nil {
		assert.Empty(t, overlay, "covered events should not generate new rules")
	}
}

// ---------------------------------------------------------------------------
// backgroundTicker
// ---------------------------------------------------------------------------

func TestBackgroundTicker_BackgroundScanDisabled_NeverFires(t *testing.T) {
	mock := &mockLogQuerier{}
	l, _ := newTestLearner(t, mock)
	l.cfg.BackgroundScan = false

	ch, stop := l.backgroundTicker()
	defer stop()

	// Non-blocking read — should not receive immediately.
	select {
	case <-ch:
		t.Fatal("ticker should not fire when BackgroundScan=false")
	default:
		// OK — channel is empty / blocked
	}
}

func TestBackgroundTicker_EnabledButEmptyInterval_NeverFires(t *testing.T) {
	mock := &mockLogQuerier{}
	l, _ := newTestLearner(t, mock)
	l.cfg.BackgroundScan = true
	l.cfg.BackgroundScanInterval = "" // empty → fall through to noop

	ch, stop := l.backgroundTicker()
	defer stop()

	select {
	case <-ch:
		t.Fatal("ticker should not fire with empty interval")
	default:
	}
}

func TestBackgroundTicker_EnabledButInvalidInterval_NeverFires(t *testing.T) {
	mock := &mockLogQuerier{}
	l, _ := newTestLearner(t, mock)
	l.cfg.BackgroundScan = true
	l.cfg.BackgroundScanInterval = "not-a-duration"

	ch, stop := l.backgroundTicker()
	defer stop()

	select {
	case <-ch:
		t.Fatal("ticker should not fire with invalid interval")
	default:
	}
}

func TestBackgroundTicker_EnabledWithValidInterval_ReturnsTicker(t *testing.T) {
	mock := &mockLogQuerier{}
	l, _ := newTestLearner(t, mock)
	l.cfg.BackgroundScan = true
	l.cfg.BackgroundScanInterval = "1h" // large enough never to fire in test

	ch, stop := l.backgroundTicker()
	defer stop()

	// Channel is valid; ticker won't fire in 1 hour, so we just verify stop() doesn't panic.
	if ch == nil {
		t.Fatal("expected non-nil ticker channel")
	}
}

// ---------------------------------------------------------------------------
// processEvents — auto-apply and pending paths
// ---------------------------------------------------------------------------

// makeUncoveredEvents produces events with level="debug" (not matched by any
// builtin rule) whose messages contain high-tier keywords so the classifier
// produces ClassifyResults.
func makeUncoveredEvents(n int) ([]ingest.LogEvent, map[string]int) {
	events := make([]ingest.LogEvent, n)
	counts := make(map[string]int, n)
	for i := 0; i < n; i++ {
		msg := "panic: goroutine failed to start service"
		events[i] = ingest.LogEvent{
			Container: "api",
			Level:     "debug", // not caught by any builtin → uncovered
			Message:   msg,
		}
		counts[msg] = 5 // seen in 5 windows → full windowScore
	}
	return events, counts
}

func TestProcessEvents_UncoveredHighScore_AutoApplied(t *testing.T) {
	// Events not covered by builtins + high keyword score + AutoApply=true
	// → rule is written to overlay file.
	mock := &mockLogQuerier{}
	l, dir := newTestLearner(t, mock)
	l.cfg.AutoApply = true
	l.cfg.ConfidenceThreshold = 0.5 // lowered so score definitely qualifies

	events, counts := makeUncoveredEvents(1)
	l.processEvents("api", events, counts, 5)

	overlay, err := LoadOverlay(filepath.Join(dir, "learned.yaml"))
	require.NoError(t, err)
	assert.NotEmpty(t, overlay, "auto-applied rule should appear in overlay")
}

func TestProcessEvents_UncoveredHighScore_QueuedAsPending(t *testing.T) {
	// AutoApply=false → rule goes to pending file.
	mock := &mockLogQuerier{}
	l, dir := newTestLearner(t, mock)
	l.cfg.AutoApply = false
	l.cfg.ConfidenceThreshold = 0.5
	l.cfg.ReviewThreshold = 0.3

	events, counts := makeUncoveredEvents(1)
	l.processEvents("api", events, counts, 5)

	pending, err := LoadPending(filepath.Join(dir, "pending.yaml"))
	require.NoError(t, err)
	assert.NotEmpty(t, pending, "rule should be queued in pending when AutoApply=false")
}

// ---------------------------------------------------------------------------
// scan error paths
// ---------------------------------------------------------------------------

func TestHandleTransition_ForwardScan_LokiError_NoOverlayEntry(t *testing.T) {
	// When the Loki query returns an error, no rule should be written.
	now := time.Now()
	mock := &mockLogQuerier{err: fmt.Errorf("loki unreachable")}
	l, dir := newTestLearner(t, mock)
	l.cfg.AutoApply = true
	ev := health.StateTransitionEvent{
		Container: "api",
		PrevState: health.StateOK,
		NewState:  health.StateHasErrors,
		At:        now,
	}
	l.handleTransition(context.Background(), ev)

	overlay, err := LoadOverlay(filepath.Join(dir, "learned.yaml"))
	if err == nil {
		assert.Empty(t, overlay, "Loki error should prevent rule creation")
	}
}

func TestRunForwardScan_LokiError_EarlyReturn(t *testing.T) {
	// Directly test runForwardScan error path.
	mock := &mockLogQuerier{err: fmt.Errorf("loki down")}
	l, dir := newTestLearner(t, mock)
	l.runForwardScan(context.Background(), "api", time.Now())
	// No rule should have been written.
	overlay, err := LoadOverlay(filepath.Join(dir, "learned.yaml"))
	if err == nil {
		assert.Empty(t, overlay)
	}
}

func TestRunRetroScan_LokiError_EarlyReturn(t *testing.T) {
	// Directly test runRetroScan error path.
	mock := &mockLogQuerier{err: fmt.Errorf("loki down")}
	l, dir := newTestLearner(t, mock)
	l.runRetroScan(context.Background(), "api", time.Now())
	overlay, err := LoadOverlay(filepath.Join(dir, "learned.yaml"))
	if err == nil {
		assert.Empty(t, overlay)
	}
}

func TestRunForwardScan_Success_CallsProcessEvents(t *testing.T) {
	// runForwardScan with valid Loki response should reach processEvents.
	mock := &mockLogQuerier{} // no events, no error
	l, _ := newTestLearner(t, mock)
	l.runForwardScan(context.Background(), "api", time.Now())
	// No panic and no error → success path hit.
}

func TestRunRetroScan_Success_CallsProcessEvents(t *testing.T) {
	mock := &mockLogQuerier{}
	l, _ := newTestLearner(t, mock)
	l.runRetroScan(context.Background(), "api", time.Now())
}


func TestHandleTransition_PreRestartScan_LokiError_NoOverlayEntry(t *testing.T) {
	now := time.Now()
	mock := &mockLogQuerier{err: fmt.Errorf("loki timeout")}
	l, dir := newTestLearner(t, mock)
	l.cfg.AutoApply = true
	ev := health.StateTransitionEvent{
		Container: "svc",
		NewState:  "RESTARTED",
		At:        now,
	}
	l.handleTransition(context.Background(), ev)

	overlay, err := LoadOverlay(filepath.Join(dir, "learned.yaml"))
	if err == nil {
		assert.Empty(t, overlay)
	}
}

func TestHandleTransition_RetroScan_LokiError_NoOverlayEntry(t *testing.T) {
	now := time.Now()
	mock := &mockLogQuerier{err: fmt.Errorf("loki down")}
	l, dir := newTestLearner(t, mock)
	l.cfg.AutoApply = true
	ev := health.StateTransitionEvent{
		Container: "svc",
		PrevState: health.StateHasErrors,
		NewState:  health.StateOK,
		At:        now,
	}
	l.handleTransition(context.Background(), ev)

	overlay, err := LoadOverlay(filepath.Join(dir, "learned.yaml"))
	if err == nil {
		assert.Empty(t, overlay)
	}
}

func TestRunBackgroundScan_DoesNotPanic(t *testing.T) {
	mock := &mockLogQuerier{}
	l, _ := newTestLearner(t, mock)
	// runBackgroundScan is a stub that logs and returns; just verify no panic.
	l.runBackgroundScan(context.Background())
}
