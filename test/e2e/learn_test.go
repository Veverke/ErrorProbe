//go:build integration

package e2e_test

// Tests in this file exercise the learning module in isolation.
// No Docker or external services are required — all I/O is to temp directories.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/health"
	"github.com/errorprobe/errorprobe/internal/learn"
	"github.com/errorprobe/errorprobe/internal/loki"
	"github.com/errorprobe/errorprobe/internal/pbr"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// mockSampler is a learn.LogQuerier that returns a pre-canned slice of lines.
type mockSampler struct {
	lines []loki.LogLine
}

func (m *mockSampler) QueryRange(
	_ context.Context,
	_ string,
	_, _ time.Time,
	_ int,
	_ string,
) ([]loki.LogLine, error) {
	return m.lines, nil
}

// makeLines creates a slice of Loki log lines with the given messages.
func makeLines(container, level string, msgs ...string) []loki.LogLine {
	lines := make([]loki.LogLine, len(msgs))
	for i, msg := range msgs {
		lines[i] = loki.LogLine{
			Timestamp: time.Now(),
			Container: container,
			Level:     level,
			Message:   msg,
		}
	}
	return lines
}

// ---------------------------------------------------------------------------
// Test: happy path — error events generate a rule that lands in the overlay
// ---------------------------------------------------------------------------

func TestLearn_HappyPath_GeneratesOverlay(t *testing.T) {
	dir := t.TempDir()
	overlayPath := filepath.Join(dir, "learned.yaml")
	pendingPath := filepath.Join(dir, "pending.yaml")
	suppPath := filepath.Join(dir, "suppressed.yaml")

	reloadCalled := 0
	applier := learn.NewApplier(overlayPath, pendingPath, suppPath, func() {
		reloadCalled++
	})

	// Sampler returns the same panic message across multiple windows.
	sampler := learn.NewSampler(&mockSampler{
		lines: makeLines("myapp", "error",
			"panic: nil pointer dereference in handler",
			"panic: nil pointer dereference in handler",
			"panic: nil pointer dereference in handler",
		),
	})

	cfg := config.LearnConfig{
		Enabled:             true,
		AutoApply:           true,
		ConfidenceThreshold: 0.50,
		ReviewThreshold:     0.25,
	}

	transitions := make(chan health.StateTransitionEvent, 10)
	learner := learn.NewLearner(transitions, sampler, applier, func() []pbr.Rule { return nil }, cfg, suppPath)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run learner in background; it listens to the transitions channel.
	done := make(chan struct{})
	go func() {
		learner.Run(ctx)
		close(done)
	}()

	// Send a transition: OK → HAS_ERRORS (should trigger forward scan).
	transitions <- health.StateTransitionEvent{
		Container: "myapp",
		PrevState: health.StateOK,
		NewState:  health.StateHasErrors,
		At:        time.Now(),
	}

	// Close the channel so the learner processes the buffered event and exits
	// deterministically, without relying on a wall-clock sleep.
	close(transitions)
	<-done

	// Overlay should have at least one rule.
	loaded, err := learn.LoadOverlay(overlayPath)
	require.NoError(t, err)
	assert.NotEmpty(t, loaded, "expected at least one learned rule in overlay")
	assert.Equal(t, learn.SourceLearned, loaded[0].Source)
}

// ---------------------------------------------------------------------------
// Test: blocklisted messages never generate rules
// ---------------------------------------------------------------------------

func TestLearn_Blocklisted_NoRule(t *testing.T) {
	dir := t.TempDir()
	overlayPath := filepath.Join(dir, "learned.yaml")
	pendingPath := filepath.Join(dir, "pending.yaml")
	suppPath := filepath.Join(dir, "suppressed.yaml")

	applier := learn.NewApplier(overlayPath, pendingPath, suppPath, nil)

	sampler := learn.NewSampler(&mockSampler{
		lines: makeLines("svc", "error",
			"no error found",
			"0 errors in batch",
			"error: <nil>",
		),
	})

	cfg := config.LearnConfig{
		Enabled:             true,
		AutoApply:           true,
		ConfidenceThreshold: 0.50,
		ReviewThreshold:     0.25,
	}
	transitions := make(chan health.StateTransitionEvent, 10)
	learner := learn.NewLearner(transitions, sampler, applier, func() []pbr.Rule { return nil }, cfg, suppPath)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		learner.Run(ctx)
		close(done)
	}()

	transitions <- health.StateTransitionEvent{
		Container: "svc",
		PrevState: health.StateOK,
		NewState:  health.StateHasErrors,
		At:        time.Now(),
	}

	close(transitions)
	<-done

	loaded, err := learn.LoadOverlay(overlayPath)
	require.NoError(t, err)
	assert.Empty(t, loaded, "blocklisted messages must not generate rules")
}

// ---------------------------------------------------------------------------
// Test: suppressed pattern is never re-learned
// ---------------------------------------------------------------------------

func TestLearn_Suppressed_NotRelearned(t *testing.T) {
	dir := t.TempDir()
	overlayPath := filepath.Join(dir, "learned.yaml")
	pendingPath := filepath.Join(dir, "pending.yaml")
	suppPath := filepath.Join(dir, "suppressed.yaml")

	// Pre-populate the suppression list with the pattern that will appear.
	pat := "panic: nil pointer dereference in handler"
	extracted, _ := learn.ExtractPattern(pat)
	require.NoError(t, learn.SaveSuppressionList(suppPath, []learn.SuppressionEntry{
		{Pattern: extracted, AddedAt: time.Now(), Reason: "test"},
	}))

	applier := learn.NewApplier(overlayPath, pendingPath, suppPath, nil)

	sampler := learn.NewSampler(&mockSampler{
		lines: makeLines("myapp", "error",
			pat, pat, pat,
		),
	})

	cfg := config.LearnConfig{
		Enabled: true, AutoApply: true,
		ConfidenceThreshold: 0.50, ReviewThreshold: 0.25,
	}
	transitions := make(chan health.StateTransitionEvent, 10)
	learner := learn.NewLearner(transitions, sampler, applier, func() []pbr.Rule { return nil }, cfg, suppPath)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		learner.Run(ctx)
		close(done)
	}()

	transitions <- health.StateTransitionEvent{
		Container: "myapp",
		PrevState: health.StateOK,
		NewState:  health.StateHasErrors,
		At:        time.Now(),
	}

	close(transitions)
	<-done

	loaded, _ := learn.LoadOverlay(overlayPath)
	assert.Empty(t, loaded, "suppressed pattern must not generate a new rule")
}

// ---------------------------------------------------------------------------
// Test: generic pattern (high matchFraction) is discarded
// ---------------------------------------------------------------------------

func TestLearn_GenericPattern_Discarded(t *testing.T) {
	dir := t.TempDir()
	overlayPath := filepath.Join(dir, "learned.yaml")
	applier := learn.NewApplier(overlayPath, filepath.Join(dir, "pending.yaml"), filepath.Join(dir, "supp.yaml"), nil)

	// A message that is mostly an IP address list — should be too generic.
	sampler := learn.NewSampler(&mockSampler{
		lines: makeLines("net-svc", "error",
			"192.168.1.1 10.0.0.2 172.16.0.1 192.168.2.5 error",
			"192.168.1.1 10.0.0.2 172.16.0.1 192.168.2.5 error",
		),
	})

	cfg := config.LearnConfig{Enabled: true, AutoApply: true, ConfidenceThreshold: 0.50, ReviewThreshold: 0.25}
	transitions := make(chan health.StateTransitionEvent, 10)
	learner := learn.NewLearner(transitions, sampler, applier, func() []pbr.Rule { return nil }, cfg, filepath.Join(dir, "supp.yaml"))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		learner.Run(ctx)
		close(done)
	}()

	transitions <- health.StateTransitionEvent{
		Container: "net-svc", PrevState: health.StateOK, NewState: health.StateHasErrors, At: time.Now(),
	}

	close(transitions)
	<-done

	loaded, _ := learn.LoadOverlay(overlayPath)
	assert.Empty(t, loaded, "generic pattern must be discarded")
}

// ---------------------------------------------------------------------------
// Test: reject (false-positive) — rule removed, pattern suppressed, not re-learned
// ---------------------------------------------------------------------------

func TestLearn_FalsePositive_Suppressed(t *testing.T) {
	dir := t.TempDir()
	overlayPath := filepath.Join(dir, "learned.yaml")
	pendingPath := filepath.Join(dir, "pending.yaml")
	suppPath := filepath.Join(dir, "suppressed.yaml")

	applier := learn.NewApplier(overlayPath, pendingPath, suppPath, nil)

	// Seed a rule in the overlay.
	rule := learn.LearnedRule{
		Name:     "learned-test1",
		Priority: 800,
		Match:    "log",
		When:     map[string]string{"message": `regex:(?i)connection refused`},
		SetState: "HAS_ERRORS",
		Source:   learn.SourceLearned,
	}
	require.NoError(t, applier.Apply(rule))

	// Reject it as a false positive.
	err := applier.RejectRule("learned-test1", `regex:(?i)connection refused`)
	require.NoError(t, err)

	// Verify overlay is empty.
	overlay, _ := learn.LoadOverlay(overlayPath)
	assert.Empty(t, overlay)

	// Verify suppression was recorded.
	sl, _ := learn.LoadSuppressionList(suppPath)
	assert.True(t, sl.Contains(`regex:(?i)connection refused`), "rejected pattern must be suppressed")
}

// ---------------------------------------------------------------------------
// Test: confirm rule — source promoted to SourceConfirmed
// ---------------------------------------------------------------------------

func TestLearn_Confirm_PromotesSource(t *testing.T) {
	dir := t.TempDir()
	overlayPath := filepath.Join(dir, "learned.yaml")
	pendingPath := filepath.Join(dir, "pending.yaml")

	applier := learn.NewApplier(overlayPath, pendingPath, filepath.Join(dir, "supp.yaml"), nil)

	rule := learn.LearnedRule{
		Name: "learned-conf1", Priority: 750, Match: "log",
		When: map[string]string{"message": `regex:(?i)disk full`},
		SetState: "HAS_ERRORS", Source: learn.SourceLearned,
	}
	require.NoError(t, applier.Pending(rule))
	require.NoError(t, applier.ConfirmRule("learned-conf1"))

	overlay, err := learn.LoadOverlay(overlayPath)
	require.NoError(t, err)
	require.Len(t, overlay, 1)
	assert.Equal(t, learn.SourceConfirmed, overlay[0].Source)
}
