package health

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/ingest"
	"github.com/errorprobe/errorprobe/internal/pbr"
)

var baseTime = time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

func errEvent(container, msg string) ingest.LogEvent {
	return ingest.LogEvent{Timestamp: baseTime, Container: container, Level: "error", Message: msg}
}

func warnEvent(container, msg string) ingest.LogEvent {
	return ingest.LogEvent{Timestamp: baseTime, Container: container, Level: "warn", Message: msg}
}

func infoEvent(container, msg string) ingest.LogEvent {
	return ingest.LogEvent{Timestamp: baseTime, Container: container, Level: "info", Message: msg}
}

func TestEngine_ProcessBatch_ErrorEvent_FlipsState(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine(filepath.Join(dir, "health.json"), nil, nil)

	e.ProcessBatch([]ingest.LogEvent{errEvent("api", "null pointer")})

	snap := e.Snapshot()
	require.Contains(t, snap.Containers, "api")
	assert.Equal(t, StateHasErrors, snap.Containers["api"].State)
}

func TestEngine_ProcessBatch_InfoEvent_NoStateChange(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine(filepath.Join(dir, "health.json"), nil, nil)

	e.ProcessBatch([]ingest.LogEvent{infoEvent("api", "started")})

	snap := e.Snapshot()
	assert.Empty(t, snap.Containers)
}

func TestEngine_ProcessBatch_WarnEvent_FlipsState(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine(filepath.Join(dir, "health.json"), nil, nil)

	e.ProcessBatch([]ingest.LogEvent{warnEvent("api", "slow query")})

	snap := e.Snapshot()
	assert.Equal(t, StateHasErrors, snap.Containers["api"].State)
}

func TestEngine_ProcessBatch_MultipleContainers(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine(filepath.Join(dir, "health.json"), nil, nil)

	e.ProcessBatch([]ingest.LogEvent{
		errEvent("api", "error in api"),
		errEvent("db", "error in db"),
	})

	snap := e.Snapshot()
	assert.Equal(t, StateHasErrors, snap.Containers["api"].State)
	assert.Equal(t, StateHasErrors, snap.Containers["db"].State)
}

func TestEngine_ProcessBatch_PersistsOnChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "health.json")
	e := NewEngine(path, nil, nil)

	e.ProcessBatch([]ingest.LogEvent{errEvent("api", "boom")})

	// Snapshot file should now exist.
	loaded, err := LoadSnapshot(path)
	require.NoError(t, err)
	assert.Equal(t, StateHasErrors, loaded.Containers["api"].State)
}

func TestEngine_ProcessBatch_CallsOnChange(t *testing.T) {
	dir := t.TempDir()
	var callCount atomic.Int32
	e := NewEngine(filepath.Join(dir, "health.json"), nil, func(_ HealthSnapshot) {
		callCount.Add(1)
	})

	e.ProcessBatch([]ingest.LogEvent{errEvent("api", "boom")})

	assert.Equal(t, int32(1), callCount.Load())
}

func TestEngine_Reset_ClearsAndPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "health.json")
	e := NewEngine(path, nil, nil)

	e.ProcessBatch([]ingest.LogEvent{errEvent("api", "boom")})
	require.Equal(t, StateHasErrors, e.Snapshot().Containers["api"].State)

	err := e.Reset("api")
	require.NoError(t, err)
	assert.Equal(t, StateOK, e.Snapshot().Containers["api"].State)

	// Verify persisted file also reflects reset.
	loaded, err := LoadSnapshot(path)
	require.NoError(t, err)
	assert.Equal(t, StateOK, loaded.Containers["api"].State)
}

// T7.4 — SetRules hot-reload tests.

// TestEngine_SetRules_NewRulesApplied verifies that after SetRules, subsequent
// events are classified using the new rule set, not the original one.
func TestEngine_SetRules_NewRulesApplied(t *testing.T) {
	dir := t.TempDir()

	// Start with a rule that treats "warn" as OK (overrides the builtin).
	suppressWarn, err := pbr.Load([]config.RuleConfig{
		{Name: "suppress-warn", Priority: 200, Match: "log", When: map[string]string{"level": "warn"}, SetState: "OK"},
	}, nil, pbr.BuiltinRules())
	require.NoError(t, err)

	e := NewEngine(filepath.Join(dir, "health.json"), suppressWarn, nil)

	// Warn event → OK under the initial rule set.
	e.ProcessBatch([]ingest.LogEvent{warnEvent("svc", "slow query")})
	snap := e.Snapshot()
	if state := snap.Containers["svc"].State; state != StateOK && state != "" {
		t.Fatalf("expected OK or no entry before rule swap, got %q", state)
	}

	// Swap to built-in rules only (warn → HAS_ERRORS).
	newRules, err := pbr.Load(nil, nil, pbr.BuiltinRules())
	require.NoError(t, err)
	e.SetRules(newRules)

	// Reset state so the next event is the trigger.
	require.NoError(t, e.Reset("svc"))

	// Same warn event → HAS_ERRORS under the new rule set.
	e.ProcessBatch([]ingest.LogEvent{warnEvent("svc", "slow query")})
	assert.Equal(t, StateHasErrors, e.Snapshot().Containers["svc"].State)
}

// TestEngine_SetRules_InvalidRules_OldRulesRetained verifies the caller-side
// pattern: pbr.Load returns an error for invalid rules (duplicate priority),
// and SetRules is NOT called, so the engine retains its old rule set.
func TestEngine_SetRules_InvalidRules_OldRulesRetained(t *testing.T) {
	dir := t.TempDir()

	// Initial rules: suppress warn.
	suppressWarn, err := pbr.Load([]config.RuleConfig{
		{Name: "suppress-warn", Priority: 200, Match: "log", When: map[string]string{"level": "warn"}, SetState: "OK"},
	}, nil, pbr.BuiltinRules())
	require.NoError(t, err)

	e := NewEngine(filepath.Join(dir, "health.json"), suppressWarn, nil)

	// Attempt to load rules with duplicate priorities — this must fail.
	dupCfgs := []config.RuleConfig{
		{Name: "rule-a", Priority: 200, Match: "log", When: map[string]string{"level": "error"}, SetState: "HAS_ERRORS"},
		{Name: "rule-b", Priority: 200, Match: "log", When: map[string]string{"level": "warn"}, SetState: "HAS_ERRORS"},
	}
	_, loadErr := pbr.Load(dupCfgs, nil, pbr.BuiltinRules())
	require.Error(t, loadErr, "expected duplicate priority to be rejected")

	// Since Load failed, SetRules is never called — old rules still apply.
	require.NoError(t, e.Reset("svc"))
	e.ProcessBatch([]ingest.LogEvent{warnEvent("svc", "slow query")})
	snap := e.Snapshot()
	if state := snap.Containers["svc"].State; state != StateOK && state != "" {
		t.Fatalf("old suppress-warn rule should still apply, got %q", state)
	}
}
