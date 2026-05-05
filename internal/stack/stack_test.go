package stack_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/docker"
	"github.com/errorprobe/errorprobe/internal/stack"
)

// ---------------------------------------------------------------------------
// Mock DockerAPI
// ---------------------------------------------------------------------------

type mockDockerAPI struct {
	pingErr             error
	pullErr             error
	pullProgressMsg     string // if set, call onProgress with this message
	containerRunningErr error
	startErr            error
	startErrAfter       int // succeed this many times before returning startErr (0 = always fail)
	startCallN          int // counts StartContainer calls when startErrAfter > 0
	stopErr             error
	removeErr           error
	netErr              error
	volErr              error
	removeNetErr        error
	removeVolErr        error

	running map[string]bool
}

func newMockDocker() *mockDockerAPI {
	return &mockDockerAPI{running: map[string]bool{}}
}

func (m *mockDockerAPI) Close() error                 { return nil }
func (m *mockDockerAPI) Ping(_ context.Context) error { return m.pingErr }
func (m *mockDockerAPI) ImageExists(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (m *mockDockerAPI) PullImage(_ context.Context, _ string, onProgress func(string)) error {
	if m.pullErr != nil {
		return m.pullErr
	}
	if onProgress != nil && m.pullProgressMsg != "" {
		onProgress(m.pullProgressMsg)
	}
	return nil
}
func (m *mockDockerAPI) ContainerRunning(_ context.Context, name string) (bool, error) {
	if m.containerRunningErr != nil {
		return false, m.containerRunningErr
	}
	return m.running[name], nil
}
func (m *mockDockerAPI) ContainerID(_ context.Context, _ string) (string, error) { return "", nil }
func (m *mockDockerAPI) SendSignal(_ context.Context, _ string, _ string) error  { return nil }
func (m *mockDockerAPI) NetworkExists(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (m *mockDockerAPI) CreateNetwork(_ context.Context, _ string) error { return m.netErr }
func (m *mockDockerAPI) RemoveNetwork(_ context.Context, _ string) error {
	if m.removeNetErr != nil {
		return m.removeNetErr
	}
	return m.netErr
}
func (m *mockDockerAPI) DisconnectFromNetwork(_ context.Context, _, _ string) error { return nil }
func (m *mockDockerAPI) DisconnectNetworkEndpoints(_ context.Context, _ string) []string { return nil }
func (m *mockDockerAPI) StartContainer(_ context.Context, _ docker.ContainerSpec) error {
	if m.startErr == nil {
		return nil
	}
	if m.startErrAfter > 0 {
		m.startCallN++
		if m.startCallN <= m.startErrAfter {
			return nil
		}
	}
	return m.startErr
}
func (m *mockDockerAPI) StopContainer(_ context.Context, _ string, _ int) error { return m.stopErr }
func (m *mockDockerAPI) RemoveContainer(_ context.Context, _ string, _ bool) error {
	return m.removeErr
}
func (m *mockDockerAPI) VolumeExists(_ context.Context, _ string) (bool, error) { return false, nil }
func (m *mockDockerAPI) CreateVolume(_ context.Context, _ string) error         { return m.volErr }
func (m *mockDockerAPI) RemoveVolume(_ context.Context, _ string) error {
	if m.removeVolErr != nil {
		return m.removeVolErr
	}
	return m.volErr
}
func (m *mockDockerAPI) ContainerList(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
	return nil, nil
}
func (m *mockDockerAPI) ContainerInspect(_ context.Context, _ string) (container.InspectResponse, error) {
	return container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{State: &container.State{Status: "exited", Running: false}}}, nil
}

var _ docker.DockerAPI = (*mockDockerAPI)(nil)

// ---------------------------------------------------------------------------
// T1.13 — CheckPorts tests
// ---------------------------------------------------------------------------

func TestCheckPorts_AllFree(t *testing.T) {
	cfg := &config.Config{
		Stack: config.Stack{
			Loki:    config.LokiConfig{Port: 19301},
			Grafana: config.GrafanaConfig{Port: 19302},
			Ingest:  config.IngestConfig{Port: 19303},
		},
	}
	err := stack.CheckPorts(cfg)
	assert.NoError(t, err)
}

func TestCheckPorts_Conflict(t *testing.T) {
	// Pre-listen on port 19401 to simulate a conflict.
	ln, err := net.Listen("tcp", "127.0.0.1:19401")
	require.NoError(t, err)
	defer ln.Close()

	cfg := &config.Config{
		Stack: config.Stack{
			Loki:    config.LokiConfig{Port: 19401},
			Grafana: config.GrafanaConfig{Port: 19402},
			Ingest:  config.IngestConfig{Port: 19403},
		},
	}
	err = stack.CheckPorts(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "19401")
}

// ---------------------------------------------------------------------------
// T1.15 — PollUntilReady tests
// ---------------------------------------------------------------------------

func TestPollUntilReady_BothReady(t *testing.T) {
	lokiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer lokiSrv.Close()

	grafanaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer grafanaSrv.Close()

	cfg := configFromServers(t, lokiSrv.Listener.Addr().String(), grafanaSrv.Listener.Addr().String())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var statuses []string
	err := stack.PollUntilReady(ctx, cfg, func(msg string) {
		statuses = append(statuses, msg)
	})
	require.NoError(t, err)
	assert.Contains(t, statuses, "loki: ready")
	assert.Contains(t, statuses, "grafana: ready")
}

func TestPollUntilReady_Timeout(t *testing.T) {
	cfg := &config.Config{
		Stack: config.Stack{
			Loki:    config.LokiConfig{Port: 19501},
			Grafana: config.GrafanaConfig{Port: 19502},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := stack.PollUntilReady(ctx, cfg, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

func TestPollUntilReady_LokiSlower(t *testing.T) {
	callCount := 0
	lokiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer lokiSrv.Close()

	grafanaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer grafanaSrv.Close()

	cfg := configFromServers(t, lokiSrv.Listener.Addr().String(), grafanaSrv.Listener.Addr().String())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var statuses []string
	err := stack.PollUntilReady(ctx, cfg, func(msg string) {
		statuses = append(statuses, msg)
	})
	require.NoError(t, err)
	assert.Contains(t, statuses, "loki: ready")
	assert.Contains(t, statuses, "grafana: ready")
	assert.GreaterOrEqual(t, callCount, 3)
}

// ---------------------------------------------------------------------------
// T1.9 / T1.10 — UpCore / DownCore tests
// ---------------------------------------------------------------------------

// buildTestConfig creates a minimal Config for stack tests.
// The ports are high ephemeral ports very unlikely to be in use,
// and the configs dir points to a temporary directory.
func buildTestConfig(t *testing.T, lokiPort, grafanaPort, ingestPort int) *config.Config {
	t.Helper()
	return &config.Config{
		Version: 1,
		Stack: config.Stack{
			Loki: config.LokiConfig{
				Image:     "grafana/loki:3.0.0",
				Port:      lokiPort,
				Retention: "72h",
			},
			Grafana: config.GrafanaConfig{
				Image: "grafana/grafana:11.0.0",
				Port:  grafanaPort,
			},
			Vector: config.VectorConfig{
				Image: "timberio/vector:0.38.0-alpine",
			},
			Ingest: config.IngestConfig{Port: ingestPort},
		},
	}
}

// setTempHome redirects os.UserHomeDir() to a temporary directory for the
// duration of the test by overriding the HOME (and USERPROFILE on Windows)
// environment variables, ensuring no files are written to the real user home.
func setTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

// noopPoll is a poll function that always succeeds immediately.
func noopPoll(_ context.Context, _ *config.Config, _ func(string)) error {
	return nil
}

func TestUpCore_HappyPath(t *testing.T) {
	setTempHome(t)
	cfg := buildTestConfig(t, 19601, 19602, 19603)

	stack.SetPollFn(noopPoll)
	t.Cleanup(func() { stack.SetPollFn(stack.PollUntilReady) })

	cli := newMockDocker()
	var statuses []string
	err := stack.UpCore(context.Background(), cfg, cli, func(msg string) {
		statuses = append(statuses, msg)
	})
	require.NoError(t, err)
	assert.Contains(t, statuses, "checking docker daemon…")
	assert.Contains(t, statuses, "stack ready — Grafana: http://localhost:19602")
}

func TestUpCore_AlreadyRunning(t *testing.T) {
	setTempHome(t)
	cfg := buildTestConfig(t, 19701, 19702, 19703)

	stack.SetPollFn(noopPoll)
	t.Cleanup(func() { stack.SetPollFn(stack.PollUntilReady) })

	cli := newMockDocker()
	cli.running[stack.ContainerLoki] = true
	cli.running[stack.ContainerGrafana] = true
	cli.running[stack.ContainerVector] = true

	var statuses []string
	err := stack.UpCore(context.Background(), cfg, cli, func(msg string) {
		statuses = append(statuses, msg)
	})
	require.NoError(t, err)
	assert.Contains(t, statuses, "stack already running — use 'errorprobe down' to stop it")
}

func TestUpCore_PingFails(t *testing.T) {
	setTempHome(t)
	cfg := buildTestConfig(t, 19801, 19802, 19803)

	stack.SetPollFn(noopPoll)
	t.Cleanup(func() { stack.SetPollFn(stack.PollUntilReady) })

	cli := newMockDocker()
	cli.pingErr = errors.New("daemon unreachable")

	err := stack.UpCore(context.Background(), cfg, cli, func(string) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "daemon unreachable")
}

func TestUpCore_PullFails(t *testing.T) {
	setTempHome(t)
	cfg := buildTestConfig(t, 19901, 19902, 19903)

	stack.SetPollFn(noopPoll)
	t.Cleanup(func() { stack.SetPollFn(stack.PollUntilReady) })

	cli := newMockDocker()
	cli.pullErr = errors.New("registry error")

	err := stack.UpCore(context.Background(), cfg, cli, func(string) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pulling")
}

func TestDownCore_HappyPath(t *testing.T) {
	setTempHome(t)
	cfg := buildTestConfig(t, 19111, 19112, 19113)
	cli := newMockDocker()

	err := stack.DownCore(context.Background(), cfg, cli, false)
	assert.NoError(t, err)
}

func TestDownCore_WithPurge(t *testing.T) {
	setTempHome(t)
	cfg := buildTestConfig(t, 19121, 19122, 19123)
	cli := newMockDocker()

	err := stack.DownCore(context.Background(), cfg, cli, true)
	assert.NoError(t, err)
}

func TestDownCore_StopError(t *testing.T) {
	setTempHome(t)
	cfg := buildTestConfig(t, 19131, 19132, 19133)
	cli := newMockDocker()
	cli.stopErr = errors.New("cannot stop")

	// Stop errors are now best-effort: the force-remove still runs and the
	// overall Down succeeds when remove succeeds.
	err := stack.DownCore(context.Background(), cfg, cli, false)
	require.NoError(t, err)
}

func TestDownCore_RemoveContainerError(t *testing.T) {
	setTempHome(t)
	cfg := buildTestConfig(t, 19141, 19142, 19143)
	cli := newMockDocker()
	cli.removeErr = errors.New("cannot remove")

	// Container remove errors are non-fatal (best-effort); down succeeds overall.
	err := stack.DownCore(context.Background(), cfg, cli, false)
	require.NoError(t, err)
}

func TestDownCore_RemoveNetworkError(t *testing.T) {
	setTempHome(t)
	cfg := buildTestConfig(t, 19151, 19152, 19153)
	cli := newMockDocker()
	cli.removeNetErr = errors.New("network remove failed")

	err := stack.DownCore(context.Background(), cfg, cli, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "network remove failed")
}

func TestDownCore_PurgeVolumeError(t *testing.T) {
	cfg := buildTestConfig(t, 19161, 19162, 19163)
	cli := newMockDocker()
	cli.removeVolErr = errors.New("volume remove failed")

	err := stack.DownCore(context.Background(), cfg, cli, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "volume remove failed")
}

func TestUpCore_PortConflict(t *testing.T) {
	// Pre-bind one of the ports.
	ln, err := net.Listen("tcp", "127.0.0.1:19651")
	require.NoError(t, err)
	defer ln.Close()

	cfg := buildTestConfig(t, 19651, 19652, 19653)
	stack.SetPollFn(noopPoll)
	t.Cleanup(func() { stack.SetPollFn(stack.PollUntilReady) })

	cli := newMockDocker()
	err = stack.UpCore(context.Background(), cfg, cli, func(string) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "19651")
}

func TestUpCore_WithPullProgress(t *testing.T) {
	cfg := buildTestConfig(t, 19661, 19662, 19663)
	stack.SetPollFn(noopPoll)
	t.Cleanup(func() { stack.SetPollFn(stack.PollUntilReady) })

	cli := newMockDocker()
	cli.pullProgressMsg = "Pulling layer abc123"

	var statuses []string
	err := stack.UpCore(context.Background(), cfg, cli, func(msg string) {
		statuses = append(statuses, msg)
	})
	require.NoError(t, err)
	// The pull progress should have been forwarded.
	found := false
	for _, s := range statuses {
		if s == "  Pulling layer abc123" {
			found = true
		}
	}
	assert.True(t, found, "pull progress should appear in status messages")
}

func TestUpCore_ContainerRunningError(t *testing.T) {
	cfg := buildTestConfig(t, 19671, 19672, 19673)
	stack.SetPollFn(noopPoll)
	t.Cleanup(func() { stack.SetPollFn(stack.PollUntilReady) })

	cli := newMockDocker()
	cli.containerRunningErr = errors.New("inspect daemon error")

	err := stack.UpCore(context.Background(), cfg, cli, func(string) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspect daemon error")
}

func TestUpCore_CreateNetworkFails(t *testing.T) {
	cfg := buildTestConfig(t, 19681, 19682, 19683)
	stack.SetPollFn(noopPoll)
	t.Cleanup(func() { stack.SetPollFn(stack.PollUntilReady) })
	cli := newMockDocker()
	cli.netErr = errors.New("network create error")

	err := stack.UpCore(context.Background(), cfg, cli, func(string) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "network create error")
}

func TestUpCore_CreateVolumeFails(t *testing.T) {
	cfg := buildTestConfig(t, 19691, 19692, 19693)
	stack.SetPollFn(noopPoll)
	t.Cleanup(func() { stack.SetPollFn(stack.PollUntilReady) })

	cli := newMockDocker()
	cli.volErr = errors.New("volume create error")

	err := stack.UpCore(context.Background(), cfg, cli, func(string) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "volume create error")
}

func TestUpCore_StartContainerFails(t *testing.T) {
	cfg := buildTestConfig(t, 19711, 19712, 19713)
	stack.SetPollFn(noopPoll)
	t.Cleanup(func() { stack.SetPollFn(stack.PollUntilReady) })

	cli := newMockDocker()
	cli.startErr = errors.New("container start failed")

	err := stack.UpCore(context.Background(), cfg, cli, func(string) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting loki")
}

func TestUpCore_StartGrafanaFails(t *testing.T) {
	cfg := buildTestConfig(t, 19721, 19722, 19723)
	stack.SetPollFn(noopPoll)
	t.Cleanup(func() { stack.SetPollFn(stack.PollUntilReady) })

	cli := newMockDocker()
	cli.startErr = errors.New("grafana start failed")
	cli.startErrAfter = 1 // Loki starts OK; Grafana fails

	err := stack.UpCore(context.Background(), cfg, cli, func(string) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting grafana")
}

func TestUpCore_StartVectorFails(t *testing.T) {
	cfg := buildTestConfig(t, 19731, 19732, 19733)
	stack.SetPollFn(noopPoll)
	t.Cleanup(func() { stack.SetPollFn(stack.PollUntilReady) })

	cli := newMockDocker()
	cli.startErr = errors.New("vector start failed")
	cli.startErrAfter = 2 // Loki+Grafana start OK; Vector fails

	err := stack.UpCore(context.Background(), cfg, cli, func(string) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting vector")
}

func TestUpCore_PollFails(t *testing.T) {
	cfg := buildTestConfig(t, 19741, 19742, 19743)
	pollErr := errors.New("services never became ready")
	stack.SetPollFn(func(_ context.Context, _ *config.Config, _ func(string)) error {
		return pollErr
	})
	t.Cleanup(func() { stack.SetPollFn(stack.PollUntilReady) })

	cli := newMockDocker()
	err := stack.UpCore(context.Background(), cfg, cli, func(string) {})
	require.Error(t, err)
	assert.ErrorIs(t, err, pollErr)
}

func TestUpCore_GenerateLokiFails(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)
	// Block the entire .errorprobe directory by placing a file there.
	ep := filepath.Join(tmpHome, ".errorprobe")
	require.NoError(t, os.WriteFile(ep, []byte(""), 0o644))

	cfg := buildTestConfig(t, 19751, 19752, 19753)
	stack.SetPollFn(noopPoll)
	t.Cleanup(func() { stack.SetPollFn(stack.PollUntilReady) })

	cli := newMockDocker()
	err := stack.UpCore(context.Background(), cfg, cli, func(string) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generating loki config")
}

func TestUpCore_GenerateGrafanaFails(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)
	// Pre-create the configs directory so GenerateLoki can write to it.
	configsDir := filepath.Join(tmpHome, ".errorprobe", "configs")
	require.NoError(t, os.MkdirAll(configsDir, 0o755))
	// Block grafana subdirectory by placing a file where the dir should go.
	require.NoError(t, os.WriteFile(filepath.Join(configsDir, "grafana"), []byte(""), 0o644))

	cfg := buildTestConfig(t, 19761, 19762, 19763)
	stack.SetPollFn(noopPoll)
	t.Cleanup(func() { stack.SetPollFn(stack.PollUntilReady) })

	cli := newMockDocker()
	err := stack.UpCore(context.Background(), cfg, cli, func(string) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generating grafana datasource")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func configFromServers(t *testing.T, lokiAddr, grafanaAddr string) *config.Config {
	t.Helper()
	_, lokiPortStr, err := net.SplitHostPort(lokiAddr)
	require.NoError(t, err)
	_, grafanaPortStr, err := net.SplitHostPort(grafanaAddr)
	require.NoError(t, err)

	lokiPort := mustAtoi(t, lokiPortStr)
	grafanaPort := mustAtoi(t, grafanaPortStr)

	return &config.Config{
		Stack: config.Stack{
			Loki:    config.LokiConfig{Port: lokiPort},
			Grafana: config.GrafanaConfig{Port: grafanaPort},
		},
	}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	require.NoError(t, err)
	return n
}

// makeConfigDir returns a temp directory usable as the configs directory.
func makeConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "grafana", "provisioning", "datasources"), 0o755))
	return dir
}
