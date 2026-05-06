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

func cfgWithIncludes(patterns ...string) *config.Config {
	return &config.Config{
		Containers: config.Containers{Include: patterns},
	}
}

func cfgWithExcludesAndIncludes(excludes, includes []string) *config.Config {
	return &config.Config{
		Containers: config.Containers{Exclude: excludes, Include: includes},
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

// Include-list ("allow-list") tests.

func TestApplyPolicy_Include_AllowsOnly(t *testing.T) {
	containers := []discovery.ContainerMeta{
		{Name: "app", Namespace: "default", Runtime: "k8s"},
		{Name: "coredns", Namespace: "kube-system", Runtime: "k8s"},
		{Name: "etcd", Namespace: "kube-system", Runtime: "k8s"},
	}
	result := discovery.ApplyPolicy(containers, cfgWithIncludes("namespace/kube-system"))
	require.Len(t, result, 2)
	for _, c := range result {
		assert.Equal(t, "kube-system", c.Namespace)
	}
}

func TestApplyPolicy_Include_EmptyMeansAll(t *testing.T) {
	containers := []discovery.ContainerMeta{
		{Name: "app", Runtime: "docker"},
		{Name: "svc", Namespace: "default", Runtime: "k8s"},
	}
	result := discovery.ApplyPolicy(containers, cfgWithIncludes()) // no include = no filter
	assert.Len(t, result, 2)
}

func TestApplyPolicy_Include_PodGlob(t *testing.T) {
	containers := []discovery.ContainerMeta{
		{Name: "app", Pod: "app-pod", Runtime: "k8s"},
		{Name: "debug", Pod: "debug-pod", Runtime: "k8s"},
	}
	result := discovery.ApplyPolicy(containers, cfgWithIncludes("pod/app-*"))
	require.Len(t, result, 1)
	assert.Equal(t, "app", result[0].Name)
}

func TestApplyPolicy_Include_NameGlob_DockerAndK8s(t *testing.T) {
	containers := []discovery.ContainerMeta{
		{Name: "payments-api", Runtime: "docker"},
		{Name: "payments-worker", Runtime: "docker"},
		{Name: "debug", Runtime: "docker"},
	}
	result := discovery.ApplyPolicy(containers, cfgWithIncludes("payments-*"))
	require.Len(t, result, 2)
	for _, c := range result {
		assert.Contains(t, c.Name, "payments")
	}
}

func TestApplyPolicy_ExcludeTakesPrecedenceOverInclude(t *testing.T) {
	// If a container matches both exclude and include, exclude wins.
	containers := []discovery.ContainerMeta{
		{Name: "svc", Namespace: "kube-system", Runtime: "k8s"},
	}
	result := discovery.ApplyPolicy(containers, cfgWithExcludesAndIncludes(
		[]string{"namespace/kube-system"},
		[]string{"namespace/kube-system"},
	))
	assert.Empty(t, result)
}

// ---------------------------------------------------------------------------
// Display-name pattern tests
// ---------------------------------------------------------------------------

func cfgWithDisplayPatterns(patterns ...string) *config.Config {
	return &config.Config{
		Containers: config.Containers{DisplayNamePatterns: patterns},
	}
}

func TestApplyPolicy_DisplayName_DefaultPatterns_K8sDeployment(t *testing.T) {
	// K8s Deployment name: only the 5-char instance suffix is stripped by default.
	// The ReplicaSet hash is stable across restarts and intentionally kept.
	containers := []discovery.ContainerMeta{
		{Name: "payments-api-7d9f6b8c4-vx8fw", Runtime: "k8s"},
	}
	result := discovery.ApplyPolicy(containers, &config.Config{})
	require.Len(t, result, 1)
	assert.Equal(t, "payments-api-7d9f6b8c4", result[0].DisplayName)
	assert.Equal(t, "payments-api-7d9f6b8c4-vx8fw", result[0].Name, "Name must be unchanged")
}

func TestApplyPolicy_DisplayName_DefaultPatterns_K8sStatefulSet(t *testing.T) {
	// K8s StatefulSet / Job name: strip pod-instance suffix only.
	containers := []discovery.ContainerMeta{
		{Name: "selling-counter-couchdb-vx8fw", Runtime: "k8s"},
		{Name: "selling-counter-pgsql-qhk9h", Runtime: "k8s"},
		{Name: "voucher-service-pgsql-8gzsm", Runtime: "k8s"},
	}
	result := discovery.ApplyPolicy(containers, &config.Config{})
	require.Len(t, result, 3)
	assert.Equal(t, "selling-counter-couchdb", result[0].DisplayName)
	assert.Equal(t, "selling-counter-pgsql", result[1].DisplayName)
	assert.Equal(t, "voucher-service-pgsql", result[2].DisplayName)
}

func TestApplyPolicy_DisplayName_NoMatchFallsBackToName(t *testing.T) {
	// A plain name with no suffix should display as-is.
	containers := []discovery.ContainerMeta{
		{Name: "payments-api", Runtime: "docker"},
	}
	result := discovery.ApplyPolicy(containers, &config.Config{})
	require.Len(t, result, 1)
	assert.Equal(t, "payments-api", result[0].DisplayName)
}

func TestApplyPolicy_DisplayName_UserPattern_OverridesDefault(t *testing.T) {
	// A user-supplied pattern can strip company-specific suffixes.
	containers := []discovery.ContainerMeta{
		{Name: "payments-api-prod", Runtime: "docker"},
		{Name: "payments-api-staging", Runtime: "docker"},
	}
	result := discovery.ApplyPolicy(containers, cfgWithDisplayPatterns(`^(.*)-(?:prod|staging)$`))
	require.Len(t, result, 2)
	assert.Equal(t, "payments-api", result[0].DisplayName)
	assert.Equal(t, "payments-api", result[1].DisplayName)
}

func TestApplyPolicy_DisplayName_FirstPatternWins(t *testing.T) {
	// User-supplied patterns: first match wins.
	containers := []discovery.ContainerMeta{
		{Name: "svc-abc1234567-xyzab", Runtime: "k8s"},
	}
	// Two patterns: the more-specific one (hash+suffix) is listed first.
	result := discovery.ApplyPolicy(containers, cfgWithDisplayPatterns(
		`^(.*)-[a-z0-9]{5,10}-[a-z0-9]{5}$`, // hash + suffix
		`^(.*)-[a-z0-9]{5}$`,                  // suffix only
	))
	require.Len(t, result, 1)
	// First pattern matches and strips both hash and suffix.
	assert.Equal(t, "svc", result[0].DisplayName)
}

func TestApplyPolicy_DisplayName_InvalidPatternSkipped(t *testing.T) {
	// A syntactically invalid regex should be silently skipped; the valid
	// pattern after it must still be applied.
	containers := []discovery.ContainerMeta{
		{Name: "app-vx8fw", Runtime: "docker"},
	}
	result := discovery.ApplyPolicy(containers, cfgWithDisplayPatterns(
		`[invalid`,          // bad regex — skipped
		`^(.*)-[a-z0-9]{5}$`, // valid — should match
	))
	require.Len(t, result, 1)
	assert.Equal(t, "app", result[0].DisplayName)
}

