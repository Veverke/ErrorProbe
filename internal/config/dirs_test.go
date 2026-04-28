package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tempConfig(t *testing.T, base string) *Config {
	t.Helper()
	return &Config{
		Version: 1,
		Stack: Stack{
			Loki:    LokiConfig{Port: 3100, Retention: "72h"},
			Grafana: GrafanaConfig{Port: 3000},
			Ingest:  IngestConfig{Transport: "http", Port: 9099, Bind: "127.0.0.1"},
		},
	}
}

// overrideHome redirects the home directory used by pathHelper funcs via env.
func overrideHome(t *testing.T, base string) {
	t.Helper()
	t.Setenv("HOME", base)
	t.Setenv("USERPROFILE", base)
}

func TestEnsureDirs_CreatesMissingDirs(t *testing.T) {
	base := t.TempDir()
	overrideHome(t, base)

	cfg, err := Load(t.TempDir())
	require.NoError(t, err)

	require.NoError(t, EnsureDirs(cfg))

	for _, d := range []string{cfg.ConfigsDir(), cfg.StateDir(), cfg.LogsDir()} {
		info, err := os.Stat(d)
		require.NoError(t, err, "expected dir %s to exist", d)
		assert.True(t, info.IsDir())
	}
}

func TestEnsureDirs_Idempotent(t *testing.T) {
	base := t.TempDir()
	overrideHome(t, base)

	cfg, err := Load(t.TempDir())
	require.NoError(t, err)

	require.NoError(t, EnsureDirs(cfg))
	require.NoError(t, EnsureDirs(cfg), "second call should not error")
}

func TestEnsureDirs_ExistingDirs(t *testing.T) {
	base := t.TempDir()
	overrideHome(t, base)

	cfg, err := Load(t.TempDir())
	require.NoError(t, err)

	// Pre-create all dirs.
	for _, d := range []string{cfg.ConfigsDir(), cfg.StateDir(), cfg.LogsDir()} {
		require.NoError(t, os.MkdirAll(d, 0o755))
		// Add a file to prove existing dirs are not wiped.
		require.NoError(t, os.WriteFile(filepath.Join(d, "probe.txt"), []byte("ok"), 0o644))
	}

	require.NoError(t, EnsureDirs(cfg))

	// Verify files still present.
	for _, d := range []string{cfg.ConfigsDir(), cfg.StateDir(), cfg.LogsDir()} {
		_, err := os.Stat(filepath.Join(d, "probe.txt"))
		assert.NoError(t, err, "pre-existing file should still exist in %s", d)
	}
}

func TestEnsureDirs_CannotCreate(t *testing.T) {
	base := t.TempDir()
	overrideHome(t, base)

	cfg, err := Load(t.TempDir())
	require.NoError(t, err)

	// Place a regular FILE where the configs dir should be,
	// so MkdirAll will fail trying to create it as a directory.
	epDir := filepath.Join(base, ".errorprobe")
	require.NoError(t, os.MkdirAll(epDir, 0o755))
	configsPath := filepath.Join(epDir, "configs")
	require.NoError(t, os.WriteFile(configsPath, []byte("block"), 0o644))

	err = EnsureDirs(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating directory")
}
