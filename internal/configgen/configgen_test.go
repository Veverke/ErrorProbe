package configgen_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/configgen"
)

// buildConfig creates a minimal config with the given stack settings.
func buildConfig(lokiPort int, retention string, grafanaPort int) *config.Config {
	return &config.Config{
		Version: 1,
		Stack: config.Stack{
			Loki: config.LokiConfig{
				Image:     "grafana/loki:3.0.0",
				Port:      lokiPort,
				Retention: retention,
			},
			Grafana: config.GrafanaConfig{
				Image: "grafana/grafana:11.0.0",
				Port:  grafanaPort,
			},
			Vector: config.VectorConfig{
				Image: "timberio/vector:0.38.0-alpine",
			},
		},
	}
}

// ---------------------------------------------------------------------------
// wrapErr coverage
// ---------------------------------------------------------------------------

func TestWrapErr_WrapsContextAndError(t *testing.T) {
	inner := errors.New("inner error")
	wrapped := configgen.WrapErr("doing thing", inner)
	require.Error(t, wrapped)
	assert.Contains(t, wrapped.Error(), "doing thing")
	assert.Contains(t, wrapped.Error(), "inner error")
	assert.ErrorIs(t, wrapped, inner)
}

// ---------------------------------------------------------------------------
// T1.14 — Loki generator tests
// ---------------------------------------------------------------------------

func TestGenerateLoki_PortInjected(t *testing.T) {
	dir := t.TempDir()
	cfg := buildConfig(3100, "72h", 3000)

	err := configgen.GenerateLoki(cfg, dir)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(dir, "loki-config.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "3100")
}

func TestGenerateLoki_RetentionInjected(t *testing.T) {
	dir := t.TempDir()
	cfg := buildConfig(3100, "168h", 3000)

	err := configgen.GenerateLoki(cfg, dir)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(dir, "loki-config.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "168h")
}

func TestGenerateLoki_OutputMatchesTemplate(t *testing.T) {
	dir := t.TempDir()
	cfg := buildConfig(3100, "72h", 3000)

	err := configgen.GenerateLoki(cfg, dir)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(dir, "loki-config.yaml"))
	require.NoError(t, err)

	s := string(content)
	assert.Contains(t, s, "auth_enabled: false")
	assert.Contains(t, s, "http_listen_port: 3100")
	assert.Contains(t, s, "schema_config:")
	assert.Contains(t, s, "retention_period: 72h")
}

func TestGenerateLoki_OverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()

	err := configgen.GenerateLoki(buildConfig(3100, "72h", 3000), dir)
	require.NoError(t, err)

	err = configgen.GenerateLoki(buildConfig(4100, "24h", 3000), dir)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(dir, "loki-config.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "4100")
	assert.Contains(t, string(content), "24h")
}

// TestGenerateLoki_FileCreateFails verifies that GenerateLoki returns an error
// when the output file cannot be created (a directory already occupies the path).
func TestGenerateLoki_FileCreateFails(t *testing.T) {
	dir := t.TempDir()
	// Create a directory where the output file should go.
	err := os.Mkdir(filepath.Join(dir, "loki-config.yaml"), 0o755)
	require.NoError(t, err)

	cfg := buildConfig(3100, "72h", 3000)
	err = configgen.GenerateLoki(cfg, dir)
	require.Error(t, err, "expected error when file path is occupied by a directory")
}

// TestGenerateLoki_MkdirFails verifies that GenerateLoki propagates MkdirAll errors.
func TestGenerateLoki_MkdirFails(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file at "cfg" path so a subdir cannot be created underneath it.
	blocker := filepath.Join(dir, "cfg")
	require.NoError(t, os.WriteFile(blocker, []byte(""), 0o644))

	cfg := buildConfig(3100, "72h", 3000)
	err := configgen.GenerateLoki(cfg, filepath.Join(blocker, "sub"))
	require.Error(t, err, "expected error when outputDir cannot be created")
}

// ---------------------------------------------------------------------------
// T1.14 — Grafana datasource generator tests
// ---------------------------------------------------------------------------

func TestGenerateGrafanaDatasource_OutputMatchesTemplate(t *testing.T) {
	dir := t.TempDir()
	cfg := buildConfig(3100, "72h", 3000)

	err := configgen.GenerateGrafanaDatasource(cfg, dir)
	require.NoError(t, err)

	dsPath := filepath.Join(dir, "grafana", "provisioning", "datasources", "loki.yaml")
	content, err := os.ReadFile(dsPath)
	require.NoError(t, err)

	s := string(content)
	assert.Contains(t, s, "name: ErrorProbe-Loki")
	assert.Contains(t, s, "type: loki")
	assert.Contains(t, s, "isDefault: true")
	assert.Contains(t, s, "3100")
}

func TestGenerateGrafanaDatasource_CreatesSubdirectory(t *testing.T) {
	dir := t.TempDir()
	cfg := buildConfig(3100, "72h", 3000)

	destDir := filepath.Join(dir, "grafana", "provisioning", "datasources")
	_, err := os.Stat(destDir)
	require.True(t, os.IsNotExist(err), "subdirectory should not exist before generation")

	err = configgen.GenerateGrafanaDatasource(cfg, dir)
	require.NoError(t, err)

	info, err := os.Stat(destDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

// TestGenerateGrafanaDatasource_DirCreateFails verifies error propagation
// when a file blocks directory creation.
func TestGenerateGrafanaDatasource_DirCreateFails(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file named "grafana" to block subdirectory creation.
	err := os.WriteFile(filepath.Join(dir, "grafana"), []byte("block"), 0o644)
	require.NoError(t, err)

	cfg := buildConfig(3100, "72h", 3000)
	err = configgen.GenerateGrafanaDatasource(cfg, dir)
	require.Error(t, err, "expected error when grafana directory cannot be created")
}

// TestGenerateGrafanaDatasource_FileCreateFails verifies error when the output
// file cannot be created (a directory is already at the file path).
func TestGenerateGrafanaDatasource_FileCreateFails(t *testing.T) {
	dir := t.TempDir()
	// Pre-create the datasources directory, then place a dir at "loki.yaml".
	destDir := filepath.Join(dir, "grafana", "provisioning", "datasources")
	require.NoError(t, os.MkdirAll(destDir, 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(destDir, "loki.yaml"), 0o755))

	cfg := buildConfig(3100, "72h", 3000)
	err := configgen.GenerateGrafanaDatasource(cfg, dir)
	require.Error(t, err, "expected error when output file cannot be created")
}

// ---------------------------------------------------------------------------
// T1.14 — Vector stub generator tests
// ---------------------------------------------------------------------------

func TestGenerateVector_Stub_ValidToml(t *testing.T) {
	dir := t.TempDir()
	cfg := buildConfig(3100, "72h", 3000)

	err := configgen.GenerateVector(cfg, dir, []string{})
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(dir, "vector.toml"))
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = toml.NewDecoder(strings.NewReader(string(content))).Decode(&parsed)
	require.NoError(t, err, "vector.toml must be valid TOML")

	sinks, ok := parsed["sinks"].(map[string]interface{})
	require.True(t, ok, "expected [sinks] table")
	_, ok = sinks["console"]
	assert.True(t, ok, "expected [sinks.console]")
}

// TestGenerateVector_FileCreateFails verifies error propagation.
func TestGenerateVector_FileCreateFails(t *testing.T) {
	dir := t.TempDir()
	err := os.Mkdir(filepath.Join(dir, "vector.toml"), 0o755)
	require.NoError(t, err)

	cfg := buildConfig(3100, "72h", 3000)
	err = configgen.GenerateVector(cfg, dir, []string{})
	require.Error(t, err)
}

// TestGenerateVector_MkdirFails verifies that GenerateVector propagates MkdirAll errors.
func TestGenerateVector_MkdirFails(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "cfg")
	require.NoError(t, os.WriteFile(blocker, []byte(""), 0o644))

	cfg := buildConfig(3100, "72h", 3000)
	err := configgen.GenerateVector(cfg, filepath.Join(blocker, "sub"), []string{})
	require.Error(t, err, "expected error when outputDir cannot be created")
}

// ---------------------------------------------------------------------------
// Template FS injection tests — covers parse-error and execute-error branches
// ---------------------------------------------------------------------------

// badFS is a valid fs.FS that contains templates with invalid syntax.
func badParseFS() fstest.MapFS {
	return fstest.MapFS{
		"templates/loki.yaml.tmpl":              {Data: []byte("{{")},
		"templates/grafana-datasource.yaml.tmpl": {Data: []byte("{{")},
		"templates/vector.toml.tmpl":            {Data: []byte("{{")},
	}
}

// badExecFS is a valid fs.FS whose templates reference a missing struct field
// to trigger a template.Execute error.
func badExecFS() fstest.MapFS {
	return fstest.MapFS{
		"templates/loki.yaml.tmpl":              {Data: []byte("{{.MissingField}}")},
		"templates/grafana-datasource.yaml.tmpl": {Data: []byte("{{.MissingField}}")},
		"templates/vector.toml.tmpl":            {Data: []byte("{{.MissingField}}")},
	}
}

func TestGenerateLoki_ParseTemplateFails(t *testing.T) {
	restore := configgen.SetTemplateFS(badParseFS())
	t.Cleanup(restore)
	cfg := buildConfig(3100, "72h", 3000)
	err := configgen.GenerateLoki(cfg, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing loki template")
}

func TestGenerateLoki_ExecuteTemplateFails(t *testing.T) {
	restore := configgen.SetTemplateFS(badExecFS())
	t.Cleanup(restore)
	cfg := buildConfig(3100, "72h", 3000)
	err := configgen.GenerateLoki(cfg, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rendering loki template")
}

func TestGenerateGrafanaDatasource_ParseTemplateFails(t *testing.T) {
	restore := configgen.SetTemplateFS(badParseFS())
	t.Cleanup(restore)
	cfg := buildConfig(3100, "72h", 3000)
	err := configgen.GenerateGrafanaDatasource(cfg, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing grafana-datasource template")
}

func TestGenerateGrafanaDatasource_ExecuteTemplateFails(t *testing.T) {
	restore := configgen.SetTemplateFS(badExecFS())
	t.Cleanup(restore)
	cfg := buildConfig(3100, "72h", 3000)
	err := configgen.GenerateGrafanaDatasource(cfg, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rendering grafana-datasource template")
}

func TestGenerateVector_ParseTemplateFails(t *testing.T) {
	restore := configgen.SetTemplateFS(badParseFS())
	t.Cleanup(restore)
	cfg := buildConfig(3100, "72h", 3000)
	err := configgen.GenerateVector(cfg, t.TempDir(), []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing vector template")
}

func TestGenerateVector_ExecuteTemplateFails(t *testing.T) {
	restore := configgen.SetTemplateFS(badExecFS())
	t.Cleanup(restore)
	cfg := buildConfig(3100, "72h", 3000)
	err := configgen.GenerateVector(cfg, t.TempDir(), []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rendering vector template")
}
