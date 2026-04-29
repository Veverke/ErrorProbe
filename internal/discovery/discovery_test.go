package discovery_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/discovery"
)

// ---------------------------------------------------------------------------
// T2.9 — WatchSet.Diff
// ---------------------------------------------------------------------------

func makeContainer(id, name string) discovery.ContainerMeta {
	return discovery.ContainerMeta{ID: id, Name: name, Runtime: "docker"}
}

func TestWatchSet_Diff_AddedContainers(t *testing.T) {
	prev := discovery.WatchSet{}
	curr := discovery.WatchSet{
		Containers:  []discovery.ContainerMeta{makeContainer("a", "alpha"), makeContainer("b", "beta")},
		GeneratedAt: time.Now(),
	}
	added, removed := curr.Diff(prev)
	assert.Len(t, added, 2)
	assert.Empty(t, removed)
}

func TestWatchSet_Diff_RemovedContainers(t *testing.T) {
	prev := discovery.WatchSet{
		Containers: []discovery.ContainerMeta{makeContainer("a", "alpha"), makeContainer("b", "beta")},
	}
	curr := discovery.WatchSet{}
	added, removed := curr.Diff(prev)
	assert.Empty(t, added)
	assert.Len(t, removed, 2)
}

func TestWatchSet_Diff_NoChange(t *testing.T) {
	c := []discovery.ContainerMeta{makeContainer("a", "alpha"), makeContainer("b", "beta")}
	prev := discovery.WatchSet{Containers: c}
	curr := discovery.WatchSet{Containers: c}
	added, removed := curr.Diff(prev)
	assert.Empty(t, added)
	assert.Empty(t, removed)
}

func TestWatchSet_Diff_Mixed(t *testing.T) {
	prev := discovery.WatchSet{Containers: []discovery.ContainerMeta{makeContainer("a", "alpha"), makeContainer("b", "beta")}}
	curr := discovery.WatchSet{Containers: []discovery.ContainerMeta{makeContainer("b", "beta"), makeContainer("c", "gamma")}}
	added, removed := curr.Diff(prev)
	require.Len(t, added, 1)
	assert.Equal(t, "c", added[0].ID)
	require.Len(t, removed, 1)
	assert.Equal(t, "a", removed[0].ID)
}

// ---------------------------------------------------------------------------
// T2.10 — ApplyPolicy
// ---------------------------------------------------------------------------

func buildCfgExclude(patterns ...string) *config.Config {
	return &config.Config{
		Containers: config.Containers{Exclude: patterns},
	}
}

func TestApplyPolicy_NoExclusions(t *testing.T) {
	containers := []discovery.ContainerMeta{makeContainer("1", "alpha"), makeContainer("2", "beta")}
	result := discovery.ApplyPolicy(containers, buildCfgExclude())
	assert.Len(t, result, 2)
}

func TestApplyPolicy_ExactMatch(t *testing.T) {
	containers := []discovery.ContainerMeta{makeContainer("1", "payments-api"), makeContainer("2", "beta")}
	result := discovery.ApplyPolicy(containers, buildCfgExclude("payments-api"))
	require.Len(t, result, 1)
	assert.Equal(t, "beta", result[0].Name)
}

func TestApplyPolicy_GlobMatch(t *testing.T) {
	containers := []discovery.ContainerMeta{
		makeContainer("1", "sidecar-logger"),
		makeContainer("2", "payments-api"),
	}
	result := discovery.ApplyPolicy(containers, buildCfgExclude("sidecar-*"))
	require.Len(t, result, 1)
	assert.Equal(t, "payments-api", result[0].Name)
}

func TestApplyPolicy_ExcludesEPContainers(t *testing.T) {
	// Managed EP containers are excluded at list stage (not in input).
	containers := []discovery.ContainerMeta{makeContainer("1", "my-app")}
	result := discovery.ApplyPolicy(containers, buildCfgExclude())
	assert.Len(t, result, 1)
}

func TestApplyPolicy_ResultSorted(t *testing.T) {
	containers := []discovery.ContainerMeta{
		makeContainer("3", "zebra"),
		makeContainer("1", "alpha"),
		makeContainer("2", "mango"),
	}
	result := discovery.ApplyPolicy(containers, buildCfgExclude())
	require.Len(t, result, 3)
	assert.Equal(t, "alpha", result[0].Name)
	assert.Equal(t, "mango", result[1].Name)
	assert.Equal(t, "zebra", result[2].Name)
}

func TestApplyPolicy_EmptyInput(t *testing.T) {
	result := discovery.ApplyPolicy(nil, buildCfgExclude("*"))
	assert.Empty(t, result)
	assert.NotNil(t, result) // no panic
}
