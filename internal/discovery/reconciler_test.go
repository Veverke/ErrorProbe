package discovery_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/discovery"
	"github.com/errorprobe/errorprobe/internal/docker"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

type fakeGenerator struct {
	err   error
	calls atomic.Int32
}

func (g *fakeGenerator) GenerateVector(_ *config.Config, _ string, _ []string, _ []discovery.K8sContainerRef) error {
	g.calls.Add(1)
	return g.err
}

// stubDockerForReconciler implements docker.DockerAPI for reconciler tests.
// Only ContainerList, ContainerInspect, and SendSignal are meaningful.
type stubDockerForReconciler struct {
	summaries []container.Summary
	listErr   error
	calls     atomic.Int32
}

func (s *stubDockerForReconciler) Close() error                 { return nil }
func (s *stubDockerForReconciler) Ping(_ context.Context) error { return nil }
func (s *stubDockerForReconciler) ImageExists(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (s *stubDockerForReconciler) PullImage(_ context.Context, _ string, _ func(string)) error {
	return nil
}
func (s *stubDockerForReconciler) ContainerRunning(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (s *stubDockerForReconciler) ContainerID(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (s *stubDockerForReconciler) NetworkExists(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (s *stubDockerForReconciler) CreateNetwork(_ context.Context, _ string) error { return nil }
func (s *stubDockerForReconciler) RemoveNetwork(_ context.Context, _ string) error { return nil }
func (s *stubDockerForReconciler) DisconnectFromNetwork(_ context.Context, _, _ string) error {
	return nil
}
func (s *stubDockerForReconciler) DisconnectNetworkEndpoints(_ context.Context, _ string) []string {
	return nil
}
func (s *stubDockerForReconciler) VolumeExists(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (s *stubDockerForReconciler) CreateVolume(_ context.Context, _ string) error         { return nil }
func (s *stubDockerForReconciler) RemoveVolume(_ context.Context, _ string) error         { return nil }
func (s *stubDockerForReconciler) SendSignal(_ context.Context, _ string, _ string) error { return nil }
func (s *stubDockerForReconciler) StartContainer(_ context.Context, _ docker.ContainerSpec) error {
	return nil
}
func (s *stubDockerForReconciler) StopContainer(_ context.Context, _ string, _ int) error { return nil }
func (s *stubDockerForReconciler) RemoveContainer(_ context.Context, _ string, _ bool) error {
	return nil
}

func (s *stubDockerForReconciler) ContainerList(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
	s.calls.Add(1)
	return s.summaries, s.listErr
}

func (s *stubDockerForReconciler) ContainerInspect(_ context.Context, _ string) (container.InspectResponse, error) {
	return container.InspectResponse{}, nil
}

func buildReconcilerCfg(dir string) *config.Config {
	return &config.Config{
		Stack: config.Stack{
			Loki:   config.LokiConfig{Port: 3100},
			Ingest: config.IngestConfig{Bind: "127.0.0.1", Port: 8080},
		},
	}
}

func summariesFromContainers(cs []discovery.ContainerMeta) []container.Summary {
	out := make([]container.Summary, len(cs))
	for i, c := range cs {
		out[i] = container.Summary{
			ID:    c.ID,
			Names: []string{"/" + c.Name},
			Image: c.Image,
			State: "running",
		}
	}
	return out
}

func newReconcilerForTest(t *testing.T, summaries []container.Summary, listErr error, gen *fakeGenerator, onReload func()) (*discovery.Reconciler, string) {
	t.Helper()
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	cfg := buildReconcilerCfg(dir)
	stub := &stubDockerForReconciler{summaries: summaries, listErr: listErr}
	r := discovery.NewReconciler(cfg, stub, nil, gen, onReload, nil)
	discovery.SetReconcilerInterval(r, 20*time.Millisecond)
	discovery.SetReconcilerStatePath(r, statePath)
	return r, statePath
}

// ---------------------------------------------------------------------------
// T2.13 — Reconciler tests
// ---------------------------------------------------------------------------

func TestReconciler_StopsOnContextCancel(t *testing.T) {
	gen := &fakeGenerator{}
	r, _ := newReconcilerForTest(t, nil, nil, gen, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("reconciler did not stop on context cancel")
	}
}

func TestReconciler_NoChange_NoReload(t *testing.T) {
	gen := &fakeGenerator{}
	c := makeContainer("abc", "my-app")
	summaries := summariesFromContainers([]discovery.ContainerMeta{c})

	var reloadCalls atomic.Int32
	r, statePath := newReconcilerForTest(t, summaries, nil, gen, func() { reloadCalls.Add(1) })

	// Pre-seed state with the same container.
	ws := discovery.WatchSet{Containers: []discovery.ContainerMeta{c}, GeneratedAt: time.Now()}
	require.NoError(t, discovery.SaveWatchSet(statePath, ws))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)

	assert.Zero(t, gen.calls.Load(), "GenerateVector should not be called when set is unchanged")
	assert.Zero(t, reloadCalls.Load())
}

func TestReconciler_ContainerAdded_TriggersReload(t *testing.T) {
	gen := &fakeGenerator{}
	c := makeContainer("abc", "my-app")
	summaries := summariesFromContainers([]discovery.ContainerMeta{c})

	var reloadCalls atomic.Int32
	r, _ := newReconcilerForTest(t, summaries, nil, gen, func() { reloadCalls.Add(1) })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)

	assert.Positive(t, gen.calls.Load())
	assert.Positive(t, reloadCalls.Load())
}

func TestReconciler_ContainerRemoved_TriggersReload(t *testing.T) {
	gen := &fakeGenerator{}
	c := makeContainer("abc", "my-app")

	var reloadCalls atomic.Int32
	// Docker returns nothing; pre-seed state with the container.
	r, statePath := newReconcilerForTest(t, nil, nil, gen, func() { reloadCalls.Add(1) })

	ws := discovery.WatchSet{Containers: []discovery.ContainerMeta{c}, GeneratedAt: time.Now()}
	require.NoError(t, discovery.SaveWatchSet(statePath, ws))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)

	assert.Positive(t, gen.calls.Load())
	assert.Positive(t, reloadCalls.Load())
}

func TestReconciler_ErrorOnList_ContinuesLoop(t *testing.T) {
	gen := &fakeGenerator{}
	stub := &stubDockerForReconciler{listErr: errors.New("daemon down")}
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	cfg := buildReconcilerCfg(dir)
	r := discovery.NewReconciler(cfg, stub, nil, gen, nil, nil)
	discovery.SetReconcilerInterval(r, 20*time.Millisecond)
	discovery.SetReconcilerStatePath(r, statePath)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	err := r.Run(ctx)
	require.NoError(t, err)
	assert.Greater(t, stub.calls.Load(), int32(1), "loop should have ticked multiple times despite errors")
}

func TestReconciler_SetOnApproved_CalledAfterTick(t *testing.T) {
	gen := &fakeGenerator{}
	r, _ := newReconcilerForTest(t, nil, nil, gen, nil)

	var called atomic.Bool
	r.SetOnApproved(func(_ discovery.WatchSet) {
		called.Store(true)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	go r.Run(ctx) //nolint:errcheck

	require.Eventually(t, called.Load, 500*time.Millisecond, 10*time.Millisecond,
		"SetOnApproved callback should be invoked after the first tick")
}
