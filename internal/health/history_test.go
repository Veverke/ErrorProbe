package health

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeTransition(container string, from, to FunctionalState, at time.Time) StateTransition {
	return StateTransition{
		ContainerName: container,
		From:          from,
		To:            to,
		At:            at,
		Reason:        "test",
	}
}

func readAllTransitions(t *testing.T, path string) []StateTransition {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	var entries []StateTransition
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var tr StateTransition
		require.NoError(t, json.Unmarshal([]byte(line), &tr))
		entries = append(entries, tr)
	}
	return entries
}

func TestHistoryLog_Append_WritesEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	h := NewHistoryLog(path)

	entry := makeTransition("api", StateOK, StateHasErrors, time.Now())
	require.NoError(t, h.Append(entry))

	entries := readAllTransitions(t, path)
	require.Len(t, entries, 1)
	assert.Equal(t, "api", entries[0].ContainerName)
	assert.Equal(t, StateOK, entries[0].From)
	assert.Equal(t, StateHasErrors, entries[0].To)
}

func TestHistoryLog_Append_MultipleEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	h := NewHistoryLog(path)

	now := time.Now()
	require.NoError(t, h.Append(makeTransition("svc1", StateOK, StateHasErrors, now)))
	require.NoError(t, h.Append(makeTransition("svc2", StateHasErrors, StateFailing, now.Add(time.Second))))
	require.NoError(t, h.Append(makeTransition("svc1", StateFailing, StateHasErrors, now.Add(2*time.Second))))

	entries := readAllTransitions(t, path)
	assert.Len(t, entries, 3)
}

func TestHistoryLog_Prune_RemovesOldEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	h := NewHistoryLog(path)

	old := time.Now().Add(-2 * time.Hour)
	recent := time.Now().Add(-1 * time.Minute)

	require.NoError(t, h.Append(makeTransition("old-svc", StateOK, StateHasErrors, old)))
	require.NoError(t, h.Append(makeTransition("new-svc", StateOK, StateHasErrors, recent)))

	require.NoError(t, h.Prune(30*time.Minute))

	entries := readAllTransitions(t, path)
	require.Len(t, entries, 1)
	assert.Equal(t, "new-svc", entries[0].ContainerName)
}

func TestHistoryLog_Prune_RetainsRecentEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	h := NewHistoryLog(path)

	recent := time.Now().Add(-5 * time.Minute)
	require.NoError(t, h.Append(makeTransition("svc", StateOK, StateHasErrors, recent)))

	require.NoError(t, h.Prune(30*time.Minute))

	entries := readAllTransitions(t, path)
	assert.Len(t, entries, 1)
}

func TestHistoryLog_Prune_EmptyFile_NoError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	h := NewHistoryLog(path)
	assert.NoError(t, h.Prune(24*time.Hour))
}

func TestHistoryLog_Prune_NonexistentFile_NoError(t *testing.T) {
	h := NewHistoryLog("/tmp/nonexistent_history_test_xyz.jsonl")
	assert.NoError(t, h.Prune(24*time.Hour))
}
