package discovery_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/discovery"
)

func cfgWithExcludes(patterns ...string) *config.Config {
	return &config.Config{
		Containers: config.Containers{Exclude: patterns},
	}
}

// T5.13 — extended ApplyPolicy tests

func TestApplyPolicy_NoExclusions_AllReturned(t *testing.T) {
	containers := []discovery.ContainerMeta{
		{Name: "app", Runtime: "docker"},
		{Name: "pod/svc", Pod: "pod", Namespace: "ns", Runtime: "k8s"},
	}
	result := discovery.ApplyPolicy(containers, cfgWithExcludes())
	assert.Len(t, result, 2)
}

func TestApplyPolicy_NameExclusion_Removed(t *testing.T) {
	containers := []discovery.ContainerMeta{
		{Name: "app", Runtime: "docker"},
		{Name: "debug", Runtime: "docker"},
	}
	result := discovery.ApplyPolicy(containers, cfgWithExcludes("debug"))
	require.Len(t, result, 1)
	assert.Equal(t, "app", result[0].Name)
}

func TestApplyPolicy_NameGlob_Removed(t *testing.T) {
	containers := []discovery.ContainerMeta{
		{Name: "sidecar-proxy", Runtime: "docker"},
		{Name: "app", Runtime: "docker"},
	}
	result := discovery.ApplyPolicy(containers, cfgWithExcludes("sidecar-*"))
	require.Len(t, result, 1)
	assert.Equal(t, "app", result[0].Name)
}

func TestApplyPolicy_PodExclusion(t *testing.T) {
	containers := []discovery.ContainerMeta{
		{Name: "worker-1/app", Pod: "worker-1", Namespace: "default", Runtime: "k8s"},
		{Name: "api-1/svc", Pod: "api-1", Namespace: "default", Runtime: "k8s"},
	}
	result := discovery.ApplyPolicy(containers, cfgWithExcludes("pod/worker-1"))
	require.Len(t, result, 1)
	assert.Equal(t, "api-1/svc", result[0].Name)
}

func TestApplyPolicy_NamespaceExclusion(t *testing.T) {
	containers := []discovery.ContainerMeta{
		{Name: "prod/app", Pod: "app", Namespace: "production", Runtime: "k8s"},
		{Name: "stage/app", Pod: "app", Namespace: "staging", Runtime: "k8s"},
	}
	result := discovery.ApplyPolicy(containers, cfgWithExcludes("namespace/staging"))
	require.Len(t, result, 1)
	assert.Equal(t, "prod/app", result[0].Name)
}

func TestApplyPolicy_DockerUnaffectedByK8sPattern(t *testing.T) {
	containers := []discovery.ContainerMeta{
		{Name: "app", Runtime: "docker"},
		{Name: "k8s-pod/app", Pod: "k8s-pod", Namespace: "default", Runtime: "k8s"},
	}
	// pod exclusion should only affect K8s; Docker name does not match pod pattern
	result := discovery.ApplyPolicy(containers, cfgWithExcludes("pod/k8s-pod"))
	require.Len(t, result, 1)
	assert.Equal(t, "docker", result[0].Runtime)
}

func TestApplyPolicy_MixedPatterns(t *testing.T) {
	containers := []discovery.ContainerMeta{
		{Name: "debug", Runtime: "docker"},
		{Name: "prod/api", Pod: "api-pod", Namespace: "production", Runtime: "k8s"},
		{Name: "test/svc", Pod: "svc-pod", Namespace: "testing", Runtime: "k8s"},
		{Name: "app", Runtime: "docker"},
	}
	result := discovery.ApplyPolicy(containers, cfgWithExcludes("debug", "namespace/testing"))
	require.Len(t, result, 2)
	// Sorted by runtime then name: docker "app" first, then k8s "prod/api"
	assert.Equal(t, "app", result[0].Name)
	assert.Equal(t, "prod/api", result[1].Name)
}

func TestApplyPolicy_SortedByRuntimeThenName(t *testing.T) {
	containers := []discovery.ContainerMeta{
		{Name: "z-app", Runtime: "docker"},
		{Name: "pod/z-svc", Runtime: "k8s"},
		{Name: "a-app", Runtime: "docker"},
		{Name: "pod/a-svc", Runtime: "k8s"},
	}
	result := discovery.ApplyPolicy(containers, cfgWithExcludes())
	require.Len(t, result, 4)
	assert.Equal(t, "docker", result[0].Runtime)
	assert.Equal(t, "a-app", result[0].Name)
	assert.Equal(t, "docker", result[1].Runtime)
	assert.Equal(t, "z-app", result[1].Name)
	assert.Equal(t, "k8s", result[2].Runtime)
	assert.Equal(t, "k8s", result[3].Runtime)
}
