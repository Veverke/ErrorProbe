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
	"github.com/errorprobe/errorprobe/internal/discovery"
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

	err := configgen.GenerateVector(cfg, dir, []string{}, nil)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(dir, "vector.toml"))
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = toml.NewDecoder(strings.NewReader(string(content))).Decode(&parsed)
	require.NoError(t, err, "vector.toml must be valid TOML")

	sinks, ok := parsed["sinks"].(map[string]interface{})
	require.True(t, ok, "expected [sinks] table")
	_, ok = sinks["loki"]
	assert.True(t, ok, "expected [sinks.loki]")
}

// TestGenerateVector_FileCreateFails verifies error propagation.
func TestGenerateVector_FileCreateFails(t *testing.T) {
	dir := t.TempDir()
	err := os.Mkdir(filepath.Join(dir, "vector.toml"), 0o755)
	require.NoError(t, err)

	cfg := buildConfig(3100, "72h", 3000)
	err = configgen.GenerateVector(cfg, dir, []string{}, nil)
	require.Error(t, err)
}

// TestGenerateVector_MkdirFails verifies that GenerateVector propagates MkdirAll errors.
func TestGenerateVector_MkdirFails(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "cfg")
	require.NoError(t, os.WriteFile(blocker, []byte(""), 0o644))

	cfg := buildConfig(3100, "72h", 3000)
	err := configgen.GenerateVector(cfg, filepath.Join(blocker, "sub"), []string{}, nil)
	require.Error(t, err, "expected error when outputDir cannot be created")
}

// ---------------------------------------------------------------------------
// Template FS injection tests — covers parse-error and execute-error branches
// ---------------------------------------------------------------------------

// badFS is a valid fs.FS that contains templates with invalid syntax.
func badParseFS() fstest.MapFS {
	return fstest.MapFS{
		"templates/loki.yaml.tmpl":               {Data: []byte("{{")},
		"templates/grafana-datasource.yaml.tmpl": {Data: []byte("{{")},
		"templates/vector.toml.tmpl":             {Data: []byte("{{")},
	}
}

// badExecFS is a valid fs.FS whose templates reference a missing struct field
// to trigger a template.Execute error.
func badExecFS() fstest.MapFS {
	return fstest.MapFS{
		"templates/loki.yaml.tmpl":               {Data: []byte("{{.MissingField}}")},
		"templates/grafana-datasource.yaml.tmpl": {Data: []byte("{{.MissingField}}")},
		"templates/vector.toml.tmpl":             {Data: []byte("{{.MissingField}}")},
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
	err := configgen.GenerateVector(cfg, t.TempDir(), []string{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing vector template")
}

func TestGenerateVector_ExecuteTemplateFails(t *testing.T) {
	restore := configgen.SetTemplateFS(badExecFS())
	t.Cleanup(restore)
	cfg := buildConfig(3100, "72h", 3000)
	err := configgen.GenerateVector(cfg, t.TempDir(), []string{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rendering vector template")
}

// ---------------------------------------------------------------------------
// T2.12 — Vector full config generator tests
// ---------------------------------------------------------------------------

func buildConfigWithPatterns(lokiPort, ingestPort int, errorPatterns, warnPatterns []string) *config.Config {
	cfg := buildConfig(lokiPort, "72h", 3000)
	cfg.Stack.Ingest = config.IngestConfig{Bind: "127.0.0.1", Port: ingestPort}
	cfg.Detection = config.Detection{
		SeverityPatterns: config.SeverityPatterns{
			Error: errorPatterns,
			Warn:  warnPatterns,
		},
	}
	return cfg
}

func TestGenerateVector_ContainerListInjected(t *testing.T) {
	dir := t.TempDir()
	cfg := buildConfigWithPatterns(3100, 8080, nil, nil)
	containers := []string{"payments-api", "user-service"}

	require.NoError(t, configgen.GenerateVector(cfg, dir, containers, nil))

	content, err := os.ReadFile(filepath.Join(dir, "vector.toml"))
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, `"payments-api"`)
	assert.Contains(t, s, `"user-service"`)
}

func TestGenerateVector_SeverityPatternsFromConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := buildConfigWithPatterns(3100, 8080, []string{"FATAL|ERROR"}, []string{"WARN"})

	require.NoError(t, configgen.GenerateVector(cfg, dir, nil, nil))

	content, err := os.ReadFile(filepath.Join(dir, "vector.toml"))
	require.NoError(t, err)
	s := string(content)
	assert.Contains(t, s, "FATAL|ERROR")
	assert.Contains(t, s, "WARN")
}

func TestGenerateVector_LokiSinkURL(t *testing.T) {
	dir := t.TempDir()
	cfg := buildConfigWithPatterns(9999, 8080, nil, nil)

	require.NoError(t, configgen.GenerateVector(cfg, dir, nil, nil))

	content, err := os.ReadFile(filepath.Join(dir, "vector.toml"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "loki:9999")
}

func TestGenerateVector_IngestSinkURL(t *testing.T) {
	dir := t.TempDir()
	cfg := buildConfigWithPatterns(3100, 7777, nil, nil)

	require.NoError(t, configgen.GenerateVector(cfg, dir, nil, nil))

	content, err := os.ReadFile(filepath.Join(dir, "vector.toml"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "7777/ingest")
}

func TestGenerateVector_EmptyContainers_ValidToml(t *testing.T) {
	dir := t.TempDir()
	cfg := buildConfigWithPatterns(3100, 8080, nil, nil)

	require.NoError(t, configgen.GenerateVector(cfg, dir, []string{}, nil))

	content, err := os.ReadFile(filepath.Join(dir, "vector.toml"))
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = toml.NewDecoder(strings.NewReader(string(content))).Decode(&parsed)
	assert.NoError(t, err, "empty container list should still produce valid TOML")
}

func TestGenerateVector_OutputIsValidToml(t *testing.T) {
	dir := t.TempDir()
	cfg := buildConfigWithPatterns(3100, 8080, []string{"ERROR|FATAL"}, []string{"WARN"})
	containers := []string{"web", "worker"}

	require.NoError(t, configgen.GenerateVector(cfg, dir, containers, nil))

	content, err := os.ReadFile(filepath.Join(dir, "vector.toml"))
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = toml.NewDecoder(strings.NewReader(string(content))).Decode(&parsed)
	assert.NoError(t, err, "full vector config must be valid TOML")
}

// ---------------------------------------------------------------------------
// T5.14 — Vector K8s config tests
// ---------------------------------------------------------------------------

func TestGenerateVector_K8sSourceIncluded(t *testing.T) {
	dir := t.TempDir()
	cfg := buildConfig(3100, "72h", 3000)
	k8sRefs := []discovery.K8sContainerRef{
		{PodName: "api-pod", Namespace: "production"},
	}
	require.NoError(t, configgen.GenerateVector(cfg, dir, nil, k8sRefs))

	content, err := os.ReadFile(filepath.Join(dir, "vector.toml"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "kubernetes_logs")
}

func TestGenerateVector_K8sLabelsInLoki(t *testing.T) {
	dir := t.TempDir()
	cfg := buildConfig(3100, "72h", 3000)
	k8sRefs := []discovery.K8sContainerRef{
		{PodName: "worker", Namespace: "default"},
	}
	require.NoError(t, configgen.GenerateVector(cfg, dir, nil, k8sRefs))

	content, err := os.ReadFile(filepath.Join(dir, "vector.toml"))
	require.NoError(t, err)
	// Loki sink should include runtime label
	assert.Contains(t, string(content), "runtime")
}

func TestGenerateVector_DockerOnlyUnchanged(t *testing.T) {
	dir := t.TempDir()
	cfg := buildConfig(3100, "72h", 3000)

	require.NoError(t, configgen.GenerateVector(cfg, dir, []string{"web"}, nil))

	content, err := os.ReadFile(filepath.Join(dir, "vector.toml"))
	require.NoError(t, err)
	body := string(content)
	assert.Contains(t, body, "docker_logs")
	assert.NotContains(t, body, "kubernetes_logs")
}

func TestGenerateVector_BothSourcesPresent(t *testing.T) {
	dir := t.TempDir()
	cfg := buildConfig(3100, "72h", 3000)
	k8sRefs := []discovery.K8sContainerRef{
		{PodName: "pod-1", Namespace: "staging"},
	}
	require.NoError(t, configgen.GenerateVector(cfg, dir, []string{"app"}, k8sRefs))

	content, err := os.ReadFile(filepath.Join(dir, "vector.toml"))
	require.NoError(t, err)
	body := string(content)
	assert.Contains(t, body, "docker_logs")
	assert.Contains(t, body, "kubernetes_logs")

	var parsed map[string]interface{}
	err = toml.NewDecoder(strings.NewReader(body)).Decode(&parsed)
	assert.NoError(t, err, "combined docker+k8s config must be valid TOML")
}

// ---------------------------------------------------------------------------
// DefaultGenerator — satisfies discovery.VectorGenerator interface
// ---------------------------------------------------------------------------

func TestDefaultGenerator_GenerateVector_DelegatesToPackageFunc(t *testing.T) {
	dir := t.TempDir()
	cfg := buildConfig(3100, "72h", 3000)
	gen := configgen.DefaultGenerator{}

	err := gen.GenerateVector(cfg, dir, []string{"my-container"}, nil)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(dir, "vector.toml"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "my-container")
}

func TestDefaultGenerator_GenerateVector_K8sRefs(t *testing.T) {
	dir := t.TempDir()
	cfg := buildConfig(3100, "72h", 3000)
	gen := configgen.DefaultGenerator{}

	k8sRefs := []discovery.K8sContainerRef{{PodName: "mypod", Namespace: "prod"}}
	err := gen.GenerateVector(cfg, dir, nil, k8sRefs)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(dir, "vector.toml"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "kubernetes_logs")
}

// ---------------------------------------------------------------------------
// GenerateGrafanaDashboards
// ---------------------------------------------------------------------------

func TestGenerateGrafanaDashboards_CreatesProviderYAML(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, configgen.GenerateGrafanaDashboards(dir))

	providerPath := filepath.Join(dir, "grafana", "provisioning", "dashboards", "provider.yaml")
	data, err := os.ReadFile(providerPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "apiVersion: 1")
	assert.Contains(t, string(data), "ErrorProbe")
}

func TestGenerateGrafanaDashboards_CreatesDashboardJSONFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, configgen.GenerateGrafanaDashboards(dir))

	dashDir := filepath.Join(dir, "grafana", "provisioning", "dashboards")
	entries, err := os.ReadDir(dashDir)
	require.NoError(t, err)

	var jsonFiles int
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			jsonFiles++
		}
	}
	assert.Greater(t, jsonFiles, 0, "at least one dashboard JSON should be copied")
}

func TestGenerateGrafanaDashboards_IsIdempotent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, configgen.GenerateGrafanaDashboards(dir))
	// Second call must not error (overwrite is OK).
	require.NoError(t, configgen.GenerateGrafanaDashboards(dir))
}

// ---------------------------------------------------------------------------
// VectorK8sConfig
// ---------------------------------------------------------------------------

func TestVectorK8sConfig_ReturnsNonEmptyString(t *testing.T) {
	cfg := buildConfig(3100, "72h", 3000)
	out, err := configgen.VectorK8sConfig(cfg)
	require.NoError(t, err)
	assert.NotEmpty(t, out)
}

func TestVectorK8sConfig_ContainsLokiPort(t *testing.T) {
	cfg := buildConfig(4567, "72h", 3000)
	out, err := configgen.VectorK8sConfig(cfg)
	require.NoError(t, err)
	assert.Contains(t, out, "4567")
}

func TestVectorK8sConfig_ContainsIngestPort(t *testing.T) {
	cfg := &config.Config{
		Stack: config.Stack{
			Loki:   config.LokiConfig{Port: 3100},
			Ingest: config.IngestConfig{Port: 9999},
		},
	}
	out, err := configgen.VectorK8sConfig(cfg)
	require.NoError(t, err)
	assert.Contains(t, out, "9999")
}

