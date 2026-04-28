package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeYAML writes content to a file named errorprobe.yaml in dir.
func writeYAML(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "errorprobe.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("writing yaml: %v", err)
	}
}

func TestLoad_Defaults(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	require.NoError(t, err)

	assert.Equal(t, 1, cfg.Version)
	assert.Equal(t, "timberio/vector:0.38.0-alpine", cfg.Stack.Vector.Image)
	assert.Equal(t, "grafana/loki:3.0.0", cfg.Stack.Loki.Image)
	assert.Equal(t, 3100, cfg.Stack.Loki.Port)
	assert.Equal(t, "72h", cfg.Stack.Loki.Retention)
	assert.Equal(t, "grafana/grafana:11.0.0", cfg.Stack.Grafana.Image)
	assert.Equal(t, 3000, cfg.Stack.Grafana.Port)
	assert.Equal(t, "http", cfg.Stack.Ingest.Transport)
	assert.Equal(t, 9099, cfg.Stack.Ingest.Port)
	assert.Equal(t, "127.0.0.1", cfg.Stack.Ingest.Bind)
	assert.Equal(t, "HAS_ERRORS", cfg.Check.FailOn)
	assert.Contains(t, cfg.Detection.SeverityPatterns.Error, "ERROR")
	assert.Contains(t, cfg.Detection.SeverityPatterns.Warn, "WARN")
}

func TestLoad_ProjectLocal_Overrides_Global(t *testing.T) {
	globalDir := t.TempDir()
	projectDir := t.TempDir()

	// Write global config.
	if err := os.WriteFile(filepath.Join(globalDir, "config.yaml"), []byte("version: 1\nstack:\n  loki:\n    port: 4000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Write project-local config with a different port.
	writeYAML(t, projectDir, "version: 1\nstack:\n  loki:\n    port: 5000\n")

	// Patch homeDir to return globalDir for this test.
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", globalDir)
	t.Setenv("USERPROFILE", globalDir)
	defer func() {
		os.Setenv("HOME", origHome)
	}()

	cfg, err := Load(projectDir)
	require.NoError(t, err)
	assert.Equal(t, 5000, cfg.Stack.Loki.Port)
}

func TestLoad_Global_Overrides_Defaults(t *testing.T) {
	globalDir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(globalDir, ".errorprobe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, ".errorprobe", "config.yaml"), []byte("version: 1\nstack:\n  grafana:\n    port: 9999\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", globalDir)
	t.Setenv("USERPROFILE", globalDir)

	projectDir := t.TempDir() // no local yaml
	cfg, err := Load(projectDir)
	require.NoError(t, err)
	assert.Equal(t, 9999, cfg.Stack.Grafana.Port)
	// Other defaults intact.
	assert.Equal(t, 3100, cfg.Stack.Loki.Port)
}

func TestLoad_InvalidVersion(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "version: 2\n")
	_, err := Load(dir)
	require.Error(t, err)
	if !strings.Contains(err.Error(), "unsupported version") {
		t.Errorf("expected 'unsupported version' in error, got: %v", err)
	}
}

func TestLoad_PartialOverride(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "version: 1\nstack:\n  loki:\n    port: 4444\n")
	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, 4444, cfg.Stack.Loki.Port)
	// All other fields remain at defaults.
	assert.Equal(t, "grafana/grafana:11.0.0", cfg.Stack.Grafana.Image)
	assert.Equal(t, 3000, cfg.Stack.Grafana.Port)
	assert.Equal(t, "timberio/vector:0.38.0-alpine", cfg.Stack.Vector.Image)
}

func TestLoad_UnknownField(t *testing.T) {
	// Decision: lenient — unknown fields are ignored by viper/mapstructure.
	dir := t.TempDir()
	writeYAML(t, dir, "version: 1\nunknown_field: some_value\n")
	cfg, err := Load(dir)
	// Lenient: no error expected.
	require.NoError(t, err)
	assert.Equal(t, 1, cfg.Version)
}

func TestStateDirPaths(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	require.NoError(t, err)

	home, _ := os.UserHomeDir()

	assert.Equal(t, filepath.Join(home, ".errorprobe", "state")+string(filepath.Separator), cfg.StateDir())
	assert.Equal(t, filepath.Join(home, ".errorprobe", "configs")+string(filepath.Separator), cfg.ConfigsDir())
	assert.Equal(t, filepath.Join(home, ".errorprobe", "logs")+string(filepath.Separator), cfg.LogsDir())
}

func TestLoad_MalformedGlobalConfig(t *testing.T) {
	base := t.TempDir()
	epDir := filepath.Join(base, ".errorprobe")
	require.NoError(t, os.MkdirAll(epDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(epDir, "config.yaml"), []byte(":\tinvalid: yaml::\n"), 0o644))

	t.Setenv("HOME", base)
	t.Setenv("USERPROFILE", base)

	_, err := Load(t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading global config")
}

func TestLoad_MalformedProjectConfig(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "errorprobe.yaml"), []byte(":\tinvalid: yaml::\n"), 0o644))

	_, err := Load(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading project config")
}
