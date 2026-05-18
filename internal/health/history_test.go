package health

import (
	"bufio"
	"bytes"
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

func TestHistoryLog_Prune_MalformedLine_KeptVerbatim(t *testing.T) {
	// Lines that cannot be JSON-unmarshalled must be preserved (no data loss).
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	// Write one valid and one malformed line.
	valid := makeTransition("svc", StateOK, StateHasErrors, time.Now())
	h := NewHistoryLog(path)
	require.NoError(t, h.Append(valid))
	// Append a malformed line manually.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString("not valid json\n")
	require.NoError(t, f.Close())
	require.NoError(t, err)

	require.NoError(t, h.Prune(24*time.Hour))

	// Both lines should survive.
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "not valid json")
}

func TestHistoryLog_Prune_AllEntriesOld_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	h := NewHistoryLog(path)
	old := time.Now().Add(-48 * time.Hour)
	require.NoError(t, h.Append(makeTransition("svc", StateOK, StateHasErrors, old)))

	require.NoError(t, h.Prune(24*time.Hour))

	// File should exist but have no meaningful entries.
	raw, _ := os.ReadFile(path)
	assert.Empty(t, string(bytes.TrimSpace(raw)), "all old entries should be pruned")
}

func TestAtomicReplace_DestDoesNotExist_Succeeds(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "target.jsonl")
	src := filepath.Join(dir, "source.jsonl")
	require.NoError(t, os.WriteFile(src, []byte("content\n"), 0o644))

	// dst does not exist — atomicReplace must succeed (ErrNotExist is ignored).
	err := atomicReplace(dst, src)
	require.NoError(t, err)

	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "content\n", string(got))
}

func TestAtomicReplace_DestExists_Overwritten(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "target.jsonl")
	src := filepath.Join(dir, "source.jsonl")
	require.NoError(t, os.WriteFile(dst, []byte("old\n"), 0o644))
	require.NoError(t, os.WriteFile(src, []byte("new\n"), 0o644))

	require.NoError(t, atomicReplace(dst, src))

	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "new\n", string(got))
}

func TestHistoryLog_Append_PathIsAFile_MkdirAllError(t *testing.T) {
	// When the parent path component exists as a file, MkdirAll will fail.
	dir := t.TempDir()
	// Create a plain file at the path that should be a directory.
	blocker := filepath.Join(dir, "not-a-dir")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))
	// Use blocker as the directory part of the history path.
	h := NewHistoryLog(filepath.Join(blocker, "history.jsonl"))
	err := h.Append(makeTransition("svc", StateOK, StateHasErrors, time.Now()))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating history directory")
}

func TestHistoryLog_Append_ReadOnlyDir_OpenError(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("skipping read-only dir test in CI (may run as root)")
	}
	dir := t.TempDir()
	histDir := filepath.Join(dir, "hist")
	require.NoError(t, os.Mkdir(histDir, 0o555)) // no write permission
	h := NewHistoryLog(filepath.Join(histDir, "history.jsonl"))
	err := h.Append(makeTransition("svc", StateOK, StateHasErrors, time.Now()))
	// On Windows or when running as root, this might succeed — just verify no panic.
	if err != nil {
		assert.Contains(t, err.Error(), "opening history log")
	}
}

func TestAtomicReplace_DstIsDir_ReturnsError(t *testing.T) {
	// os.Remove of a non-empty directory fails, which exercises the error
	// branch in atomicReplace where err != nil && !errors.Is(err, os.ErrNotExist).
	dir := t.TempDir()
	// dst is a subdirectory (non-empty with a file inside).
	dst := filepath.Join(dir, "subdir")
	require.NoError(t, os.Mkdir(dst, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dst, "x"), []byte("y"), 0o644))
	src := filepath.Join(dir, "source.jsonl")
	require.NoError(t, os.WriteFile(src, []byte("data\n"), 0o644))

	err := atomicReplace(dst, src)
	require.Error(t, err)
}
