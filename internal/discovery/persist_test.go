package discovery_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/discovery"
)

// ---------------------------------------------------------------------------
// T2.11 — Watch set persistence
// ---------------------------------------------------------------------------

func buildWatchSet() discovery.WatchSet {
	return discovery.WatchSet{
		Containers: []discovery.ContainerMeta{
			{ID: "abc", Name: "my-app", Image: "nginx:latest", Runtime: "docker", InfraStatus: "running"},
		},
		GeneratedAt: time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC),
	}
}

func TestSaveLoadWatchSet_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "containers.json")

	ws := buildWatchSet()
	require.NoError(t, discovery.SaveWatchSet(path, ws))

	loaded, err := discovery.LoadWatchSet(path)
	require.NoError(t, err)
	assert.Equal(t, ws.Containers[0].ID, loaded.Containers[0].ID)
	assert.Equal(t, ws.Containers[0].Name, loaded.Containers[0].Name)
	assert.True(t, ws.GeneratedAt.Equal(loaded.GeneratedAt))
}

func TestLoadWatchSet_FileMissing_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	ws, err := discovery.LoadWatchSet(path)
	require.NoError(t, err)
	assert.Empty(t, ws.Containers)
}

func TestSaveWatchSet_Atomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "containers.json")

	// Write original content
	original := []byte(`{"Containers":[],"GeneratedAt":"2026-01-01T00:00:00Z"}`)
	require.NoError(t, os.WriteFile(path, original, 0o644))

	// Simulate rename failure: make the .tmp path a directory so Rename will fail.
	tmpPath := path + ".tmp"
	require.NoError(t, os.Mkdir(tmpPath, 0o755))

	// WriteFile to a directory path should fail.
	err := discovery.SaveWatchSet(path, buildWatchSet())
	assert.Error(t, err)

	// Original file is unchanged.
	data, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, original, data)
}

func TestLoadWatchSet_CorruptFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "containers.json")
	require.NoError(t, os.WriteFile(path, []byte(`not valid json`), 0o644))

	_, err := discovery.LoadWatchSet(path)
	assert.Error(t, err)
}

func TestSaveWatchSet_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "containers.json")

	require.NoError(t, discovery.SaveWatchSet(path, buildWatchSet()))

	_, err := os.Stat(path)
	assert.NoError(t, err)
}
