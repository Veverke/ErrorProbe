package health

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/ingest"
)

func buildTestSnap() HealthSnapshot {
	snap := HealthSnapshot{SnapshotAt: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)}
	snap.SetError("api", "null pointer", time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC))
	return snap
}

func TestSaveLoadSnapshot_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "health.json")

	orig := buildTestSnap()
	require.NoError(t, SaveSnapshot(path, orig))

	loaded, err := LoadSnapshot(path)
	require.NoError(t, err)

	require.Len(t, loaded.Containers, 1)
	ch := loaded.Containers["api"]
	assert.Equal(t, StateHasErrors, ch.State)
	assert.Equal(t, 1, ch.ErrorCount)
	assert.Equal(t, "null pointer", ch.LastErrorMsg)
}

func TestLoadSnapshot_Missing_ReturnsEmpty(t *testing.T) {
	snap, err := LoadSnapshot("/tmp/nonexistent_health_test_xyz.json")
	require.NoError(t, err)
	assert.Nil(t, snap.Containers)
}

func TestSaveSnapshot_Atomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir_not_yet_created", "health.json")

	snap := buildTestSnap()
	// SaveSnapshot should create parent dirs.
	err := SaveSnapshot(path, snap)
	require.NoError(t, err)

	// Verify temp file is gone (rename succeeded).
	_, statErr := os.Stat(path + ".tmp")
	assert.True(t, os.IsNotExist(statErr), "temp file should be removed after rename")
}

func TestLoadSnapshot_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "health.json")
	require.NoError(t, os.WriteFile(path, []byte("{not json}"), 0o644))

	_, err := LoadSnapshot(path)
	assert.Error(t, err)
}

func TestEngine_LoadsExistingSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "health.json")

	// Pre-persist a snapshot.
	orig := buildTestSnap()
	require.NoError(t, SaveSnapshot(path, orig))

	engine := NewEngine(path, nil)
	snap := engine.Snapshot()
	require.Contains(t, snap.Containers, "api")
	assert.Equal(t, StateHasErrors, snap.Containers["api"].State)
}

func TestSaveSnapshot_MkdirError(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file where the parent dir would need to be.
	blockFile := filepath.Join(dir, "blockfile")
	require.NoError(t, os.WriteFile(blockFile, []byte("x"), 0o644))

	// health.json inside blockFile (which is a file, not a dir) — MkdirAll must fail.
	path := filepath.Join(blockFile, "health.json")
	err := SaveSnapshot(path, HealthSnapshot{})
	assert.Error(t, err)
}

func TestEngine_Reset_PersistError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "health.json")

	// Create engine with valid path, process a batch to build state.
	e := NewEngine(path, nil)
	e.ProcessBatch([]ingest.LogEvent{
		{Container: "api", Level: "error", Message: "boom", Timestamp: baseTime},
	})

	// Now replace the health.json with a directory so writes fail.
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.MkdirAll(path, 0o755))

	err := e.Reset("api")
	assert.Error(t, err)
}

func TestEngine_ProcessBatch_PersistError_DoesNotCrash(t *testing.T) {
	dir := t.TempDir()
	// Place a regular file where the parent dir for the snapshot would be.
	blockFile := filepath.Join(dir, "block")
	require.NoError(t, os.WriteFile(blockFile, []byte("x"), 0o644))

	path := filepath.Join(blockFile, "health.json")
	e := NewEngine(path, nil) // NewEngine: LoadSnapshot fails, that's fine.

	// ProcessBatch should not crash even when SaveSnapshot fails.
	e.ProcessBatch([]ingest.LogEvent{
		{Container: "api", Level: "error", Message: "boom", Timestamp: baseTime},
	})
	// State is still in memory.
	assert.Equal(t, StateHasErrors, e.Snapshot().Containers["api"].State)
}
