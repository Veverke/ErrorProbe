package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	// Write global config to <home>/.errorprobe/config.yaml as Load() expects.
	epDir := filepath.Join(globalDir, ".errorprobe")
	if err := os.MkdirAll(epDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(epDir, "config.yaml"), []byte("version: 1\nstack:\n  loki:\n    port: 4000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Write project-local config with a different port.
	writeYAML(t, projectDir, "version: 1\nstack:\n  loki:\n    port: 5000\n")

	// Patch homeDir to return globalDir for this test.
	t.Setenv("HOME", globalDir)
	t.Setenv("USERPROFILE", globalDir)

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

// ---------------------------------------------------------------------------
// ConfigDir
// ---------------------------------------------------------------------------

func TestConfigDir_WithProjectDir_ReturnsAbs(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	require.NoError(t, err)

	abs, _ := filepath.Abs(dir)
	assert.Equal(t, abs, cfg.ConfigDir())
}

func TestConfigDir_NoProjectDir_ReturnsCwd(t *testing.T) {
	// Load with empty projectDir → configDir not set → falls back to cwd.
	cfg := &Config{}
	wd, _ := os.Getwd()
	assert.Equal(t, wd, cfg.ConfigDir())
}

// ---------------------------------------------------------------------------
// ParseDuration
// ---------------------------------------------------------------------------

func TestParseDuration_Days(t *testing.T) {
	d, err := ParseDuration("30d")
	require.NoError(t, err)
	assert.Equal(t, 30*24*time.Hour, d)
}

func TestParseDuration_OneDayExact(t *testing.T) {
	d, err := ParseDuration("1d")
	require.NoError(t, err)
	assert.Equal(t, 24*time.Hour, d)
}

func TestParseDuration_Standard_Hours(t *testing.T) {
	d, err := ParseDuration("72h")
	require.NoError(t, err)
	assert.Equal(t, 72*time.Hour, d)
}

func TestParseDuration_Standard_Minutes(t *testing.T) {
	d, err := ParseDuration("3m")
	require.NoError(t, err)
	assert.Equal(t, 3*time.Minute, d)
}

func TestParseDuration_InvalidDays(t *testing.T) {
	_, err := ParseDuration("xd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid duration")
}

func TestParseDuration_InvalidStandard(t *testing.T) {
	_, err := ParseDuration("5z")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// ConfigFilePath
// ---------------------------------------------------------------------------

func TestConfigFilePath_WithProjectDir(t *testing.T) {
	dir := t.TempDir()
	p := ConfigFilePath(dir)
	assert.Equal(t, filepath.Join(dir, "errorprobe.yaml"), p)
}

func TestConfigFilePath_LocalFileExists(t *testing.T) {
	// Create errorprobe.yaml in the working directory; ConfigFilePath should find it.
	dir := t.TempDir()
	local := filepath.Join(dir, "errorprobe.yaml")
	require.NoError(t, os.WriteFile(local, []byte("version: 1\n"), 0o644))

	// Change working directory to the temp dir for the duration of the test.
	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	p := ConfigFilePath("")
	assert.Equal(t, "errorprobe.yaml", p)
}

func TestConfigFilePath_NoLocalFile_FallsBackToGlobal(t *testing.T) {
	// No local file, no projectDir → should return the global path.
	dir := t.TempDir()
	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	p := ConfigFilePath("")
	home, _ := os.UserHomeDir()
	assert.Equal(t, filepath.Join(home, ".errorprobe", "config.yaml"), p)
}

// ---------------------------------------------------------------------------
// AppendExclude
// ---------------------------------------------------------------------------

func TestAppendExclude_NewFile_CreatesWithPattern(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "errorprobe.yaml")

	require.NoError(t, AppendExclude(cfgPath, "noisy-container"))

	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "noisy-container")
	assert.Contains(t, string(data), "containers:")
	assert.Contains(t, string(data), "exclude:")
}

func TestAppendExclude_ExistingFile_AppendsPattern(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "errorprobe.yaml")
	initial := "version: 1\ncontainers:\n  exclude:\n    - first-container\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(initial), 0o644))

	require.NoError(t, AppendExclude(cfgPath, "second-container"))

	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "first-container")
	assert.Contains(t, content, "second-container")
}

func TestAppendExclude_Idempotent(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "errorprobe.yaml")
	initial := "version: 1\ncontainers:\n  exclude:\n    - existing\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(initial), 0o644))

	require.NoError(t, AppendExclude(cfgPath, "existing"))

	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	// Pattern should appear exactly once.
	assert.Equal(t, 1, strings.Count(string(data), "- existing"))
}

func TestAppendExclude_NoExcludeSection_AppendsSection(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "errorprobe.yaml")
	// Config with a containers block but no exclude key.
	initial := "version: 1\nstack:\n  loki:\n    port: 3100\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(initial), 0o644))

	require.NoError(t, AppendExclude(cfgPath, "temp-job"))

	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "temp-job")
}

// ---------------------------------------------------------------------------
// Learn path helpers
// ---------------------------------------------------------------------------

func TestLearnOverlayFile_CustomPath(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "version: 1\nlearn:\n  overlay_file: /tmp/custom.yaml\n")
	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/custom.yaml", cfg.LearnOverlayFile())
}

func TestLearnOverlayFile_DefaultIsNextToConfig(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	require.NoError(t, err)
	abs, _ := filepath.Abs(dir)
	assert.Equal(t, filepath.Join(abs, "errorprobe.learned.yaml"), cfg.LearnOverlayFile())
}

func TestLearnSuppressionFile_DefaultIsNextToConfig(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	require.NoError(t, err)
	abs, _ := filepath.Abs(dir)
	assert.Equal(t, filepath.Join(abs, "errorprobe.suppressed.yaml"), cfg.LearnSuppressionFile())
}

func TestLearnPendingFile_IsInStateDir(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	require.NoError(t, err)
	// Pending file must live in StateDir, not next to the config.
	assert.True(t, strings.HasPrefix(cfg.LearnPendingFile(), cfg.StateDir()),
		"pending file %q should be inside state dir %q", cfg.LearnPendingFile(), cfg.StateDir())
	assert.True(t, strings.HasSuffix(cfg.LearnPendingFile(), "errorprobe.pending.yaml"))
}

func TestDataDir_ContainsErrorprobe(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.Contains(t, cfg.DataDir(), ".errorprobe")
}