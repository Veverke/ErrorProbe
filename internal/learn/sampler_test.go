package learn

// White-box tests for sampler.go helpers and the Sampler methods.
// Uses a mockLogQuerier to avoid any network or Loki dependency.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/loki"
)

// ---------------------------------------------------------------------------
// mockLogQuerier
// ---------------------------------------------------------------------------

type queryCall struct {
	query     string
	start     time.Time
	end       time.Time
	limit     int
	direction string
}

type mockLogQuerier struct {
	lines []loki.LogLine
	err   error
	calls []queryCall
}

func (m *mockLogQuerier) QueryRange(_ context.Context, query string, start, end time.Time, limit int, direction string) ([]loki.LogLine, error) {
	m.calls = append(m.calls, queryCall{query: query, start: start, end: end, limit: limit, direction: direction})
	return m.lines, m.err
}

// ---------------------------------------------------------------------------
// buildContainerQuery
// ---------------------------------------------------------------------------

func TestBuildContainerQuery_DockerBareKey(t *testing.T) {
	q := buildContainerQuery("myapp")
	assert.Equal(t, `{container="myapp"}`, q)
}

func TestBuildContainerQuery_K8sNamespacedKey(t *testing.T) {
	q := buildContainerQuery("production/payment-svc")
	assert.Equal(t, `{container="payment-svc",namespace="production"}`, q)
}

func TestBuildContainerQuery_SpecialCharsPreserved(t *testing.T) {
	// Ensure names with hyphens are quoted correctly.
	q := buildContainerQuery("my-ns/my-container")
	assert.Contains(t, q, `container="my-container"`)
	assert.Contains(t, q, `namespace="my-ns"`)
}

// ---------------------------------------------------------------------------
// linesToEvents
// ---------------------------------------------------------------------------

func TestLinesToEvents_DockerKey_NoNamespace(t *testing.T) {
	now := time.Now()
	lines := []loki.LogLine{
		{Timestamp: now, Container: "api", Level: "info", Message: "started"},
	}
	events := linesToEvents(lines, "api")
	require.Len(t, events, 1)
	assert.Equal(t, "api", events[0].Container)
	assert.Equal(t, "", events[0].Namespace)
	assert.Equal(t, "started", events[0].Message)
	assert.Equal(t, "info", events[0].Level)
}

func TestLinesToEvents_K8sKey_SetsNamespace(t *testing.T) {
	now := time.Now()
	lines := []loki.LogLine{
		{Timestamp: now, Container: "worker", Level: "error", Message: "crash"},
	}
	events := linesToEvents(lines, "staging/worker")
	require.Len(t, events, 1)
	assert.Equal(t, "staging", events[0].Namespace)
	assert.Equal(t, "worker", events[0].Container)
}

func TestLinesToEvents_FallsBackToKeyContainer_WhenLineLackName(t *testing.T) {
	now := time.Now()
	// LogLine with empty Container falls back to the name parsed from containerKey.
	lines := []loki.LogLine{
		{Timestamp: now, Container: "", Level: "info", Message: "hello"},
	}
	events := linesToEvents(lines, "svc-without-label")
	require.Len(t, events, 1)
	assert.Equal(t, "svc-without-label", events[0].Container)
}

func TestLinesToEvents_PreservesTimestamp(t *testing.T) {
	ts := time.Date(2024, 3, 10, 12, 0, 0, 0, time.UTC)
	lines := []loki.LogLine{{Timestamp: ts, Container: "c", Level: "warn", Message: "slow"}}
	events := linesToEvents(lines, "c")
	require.Len(t, events, 1)
	assert.Equal(t, ts, events[0].Timestamp)
}

func TestLinesToEvents_EmptySlice_ReturnsEmpty(t *testing.T) {
	events := linesToEvents(nil, "any")
	assert.Empty(t, events)
}

// ---------------------------------------------------------------------------
// Sampler.QueryWindow
// ---------------------------------------------------------------------------

func TestSampler_QueryWindow_ReturnsConvertedEvents(t *testing.T) {
	now := time.Now()
	mock := &mockLogQuerier{
		lines: []loki.LogLine{
			{Timestamp: now, Container: "api", Level: "info", Message: "boot"},
		},
	}
	s := NewSampler(mock)
	start := now.Add(-10 * time.Minute)
	end := now

	events, err := s.QueryWindow(context.Background(), "api", start, end, 100)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "boot", events[0].Message)
}

func TestSampler_QueryWindow_LokiError_PropagatesError(t *testing.T) {
	mock := &mockLogQuerier{err: errors.New("loki unavailable")}
	s := NewSampler(mock)

	_, err := s.QueryWindow(context.Background(), "svc", time.Now(), time.Now(), 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loki unavailable")
}

func TestSampler_QueryWindow_PassesForwardDirection(t *testing.T) {
	mock := &mockLogQuerier{}
	s := NewSampler(mock)
	start := time.Now().Add(-5 * time.Minute)
	end := time.Now()

	_, _ = s.QueryWindow(context.Background(), "svc", start, end, 50)

	require.Len(t, mock.calls, 1)
	assert.Equal(t, "forward", mock.calls[0].direction)
}

// ---------------------------------------------------------------------------
// Sampler.QueryPreRestart
// ---------------------------------------------------------------------------

func TestSampler_QueryPreRestart_CorrectTimeRange(t *testing.T) {
	at := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	window := 5 * time.Minute
	mock := &mockLogQuerier{}
	s := NewSampler(mock)

	_, _ = s.QueryPreRestart(context.Background(), "svc", at, window, 50)

	require.Len(t, mock.calls, 1)
	assert.Equal(t, at.Add(-window), mock.calls[0].start)
	assert.Equal(t, at, mock.calls[0].end)
}

// ---------------------------------------------------------------------------
// Sampler.SampleWindows
// ---------------------------------------------------------------------------

func TestSampler_SampleWindows_DividesIntoSubwindows(t *testing.T) {
	mock := &mockLogQuerier{
		lines: []loki.LogLine{
			{Container: "c", Level: "info", Message: "msg"},
		},
	}
	s := NewSampler(mock)
	start := time.Now()
	end := start.Add(3 * time.Hour)

	_, _, err := s.SampleWindows(context.Background(), "c", start, end, 3, 50)
	require.NoError(t, err)
	// Should have made exactly 3 QueryRange calls — one per sub-window.
	assert.Len(t, mock.calls, 3)
}

func TestSampler_SampleWindows_WindowHits_CountsPerWindow(t *testing.T) {
	// Same message returned in every sub-window should record 3 window hits.
	mock := &mockLogQuerier{
		lines: []loki.LogLine{
			{Container: "c", Level: "warn", Message: "slow query"},
		},
	}
	s := NewSampler(mock)
	start := time.Now()
	end := start.Add(3 * time.Hour)

	_, hits, err := s.SampleWindows(context.Background(), "c", start, end, 3, 50)
	require.NoError(t, err)
	assert.Equal(t, 3, hits["slow query"])
}

func TestSampler_SampleWindows_LokiErrorInOneWindow_Skipped(t *testing.T) {
	// Even if Loki errors on every call, SampleWindows should return nil error.
	mock := &mockLogQuerier{err: errors.New("timeout")}
	s := NewSampler(mock)
	start := time.Now()
	end := start.Add(time.Hour)

	events, hits, err := s.SampleWindows(context.Background(), "c", start, end, 2, 10)
	require.NoError(t, err)
	assert.Empty(t, events)
	assert.Empty(t, hits)
}

func TestSampler_SampleWindows_ZeroWindowCount_TreatedAsOne(t *testing.T) {
	mock := &mockLogQuerier{}
	s := NewSampler(mock)
	start := time.Now()
	end := start.Add(time.Hour)

	_, _, err := s.SampleWindows(context.Background(), "c", start, end, 0, 10)
	require.NoError(t, err)
	assert.Len(t, mock.calls, 1) // windowCount 0 normalised to 1
}

func TestSampler_SampleWindows_DeduplicatesWithinSameWindow(t *testing.T) {
	// Two lines with the same message in one window → windowHits["msg"]=1, not 2.
	mock := &mockLogQuerier{
		lines: []loki.LogLine{
			{Container: "c", Level: "warn", Message: "repeated"},
			{Container: "c", Level: "warn", Message: "repeated"},
		},
	}
	s := NewSampler(mock)
	start := time.Now()
	end := start.Add(time.Hour)

	_, hits, err := s.SampleWindows(context.Background(), "c", start, end, 1, 100)
	require.NoError(t, err)
	// Within the same window, a message is counted only once.
	assert.Equal(t, 1, hits["repeated"])
}
