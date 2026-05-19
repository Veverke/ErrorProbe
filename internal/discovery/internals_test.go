package discovery_test

// Tests for unexported discovery helpers exposed via export_test.go.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/discovery"
	"github.com/errorprobe/errorprobe/internal/health"
	"github.com/errorprobe/errorprobe/internal/pbr"
)

// ---------------------------------------------------------------------------
// ContainerMeta.HealthKey
// ---------------------------------------------------------------------------

func TestHealthKey_DockerContainer_BareNameReturned(t *testing.T) {
	meta := discovery.ContainerMeta{Name: "my-api", Runtime: "docker"}
	assert.Equal(t, "my-api", meta.HealthKey())
}

func TestHealthKey_K8sContainer_NamespaceSlashName(t *testing.T) {
	meta := discovery.ContainerMeta{Name: "payment-svc", Namespace: "production", Runtime: "k8s"}
	assert.Equal(t, "production/payment-svc", meta.HealthKey())
}

func TestHealthKey_EmptyNamespace_BareNameReturned(t *testing.T) {
	meta := discovery.ContainerMeta{Name: "svc", Namespace: ""}
	assert.Equal(t, "svc", meta.HealthKey())
}

// ---------------------------------------------------------------------------
// WatchSet.Diff
// ---------------------------------------------------------------------------

func TestWatchSet_Diff_AddedAndRemoved(t *testing.T) {
	prev := discovery.WatchSet{Containers: []discovery.ContainerMeta{
		{ID: "aaa", Name: "old"},
		{ID: "bbb", Name: "keep"},
	}}
	curr := discovery.WatchSet{Containers: []discovery.ContainerMeta{
		{ID: "bbb", Name: "keep"},
		{ID: "ccc", Name: "new"},
	}}
	added, removed := curr.Diff(prev)
	assert.Len(t, added, 1)
	assert.Equal(t, "ccc", added[0].ID)
	assert.Len(t, removed, 1)
	assert.Equal(t, "aaa", removed[0].ID)
}

func TestWatchSet_Diff_NoDiff(t *testing.T) {
	ws := discovery.WatchSet{Containers: []discovery.ContainerMeta{
		{ID: "aaa", Name: "svc"},
	}}
	added, removed := ws.Diff(ws)
	assert.Empty(t, added)
	assert.Empty(t, removed)
}

// ---------------------------------------------------------------------------
// inferInfraStatus (via export_test.go)
// ---------------------------------------------------------------------------

func TestInferInfraStatus_NoRules_ReturnsRunning(t *testing.T) {
	meta := pbr.InfraContainer{Runtime: "k8s", RestartCount: 0}
	status := discovery.InferInfraStatus(nil, meta)
	assert.Equal(t, "running", status)
}

func TestInferInfraStatus_RestartingRule_ReturnsRestarting(t *testing.T) {
	rules, err := pbr.Load(nil, nil, pbr.BuiltinRules())
	if err != nil {
		t.Fatalf("loading rules: %v", err)
	}
	// RestartCount > 0 with short uptime triggers builtin-k8s-restarting.
	meta := pbr.InfraContainer{
		Runtime:      "k8s",
		RestartCount: 3,
		Uptime:       5 * time.Second, // well within the recentRestartWindow
	}
	status := discovery.InferInfraStatus(rules, meta)
	assert.Equal(t, "restarting", status)
}

func TestInferInfraStatus_OKRule_ReturnsRunning(t *testing.T) {
	// A rule that explicitly returns "OK" should normalise to "running".
	rules, err := pbr.Load([]config.RuleConfig{
		{Name: "force-ok", Priority: 200, Match: "infra",
			When: map[string]string{"runtime": "k8s"}, SetState: "OK"},
	}, nil, nil)
	if err != nil {
		t.Fatalf("loading rules: %v", err)
	}
	meta := pbr.InfraContainer{Runtime: "k8s"}
	status := discovery.InferInfraStatus(rules, meta)
	assert.Equal(t, "running", status)
}

func TestInferInfraStatus_NoRuleMatches_ReturnsRunning(t *testing.T) {
	// Rules exist but none match → fall back to "running".
	rules, err := pbr.Load([]config.RuleConfig{
		{Name: "docker-only", Priority: 100, Match: "infra",
			When: map[string]string{"runtime": "docker"}, SetState: "RESTARTING"},
	}, nil, nil)
	if err != nil {
		t.Fatalf("loading rules: %v", err)
	}
	meta := pbr.InfraContainer{Runtime: "k8s", RestartCount: 0}
	status := discovery.InferInfraStatus(rules, meta)
	assert.Equal(t, "running", status)
}

// ---------------------------------------------------------------------------
// prevExitLine (via export_test.go)
// ---------------------------------------------------------------------------

func TestPrevExitLine_EmptyLogs_ReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", discovery.PrevExitLine(""))
}

func TestPrevExitLine_PreferErrorLine(t *testing.T) {
	logs := "starting up\ndatabase error: connection refused\nclean shutdown"
	result := discovery.PrevExitLine(logs)
	assert.Equal(t, "database error: connection refused", result)
}

func TestPrevExitLine_LastErrorLinePreferred_OverEarlierOne(t *testing.T) {
	logs := "error: first problem\nnormal line\nerror: second problem"
	result := discovery.PrevExitLine(logs)
	// The last (most-recent) error line should be returned.
	assert.Equal(t, "error: second problem", result)
}

func TestPrevExitLine_NoErrorLine_FallsBackToLastNonEmpty(t *testing.T) {
	logs := "application started\nshutting down gracefully\n"
	result := discovery.PrevExitLine(logs)
	assert.Equal(t, "shutting down gracefully", result)
}

func TestPrevExitLine_KubeletNoise_Skipped(t *testing.T) {
	// Kubelet symlink messages must be filtered out.
	logs := "fatal: out of memory\nfailed to try resolving symlinks in path /var/log/pods/default_mypod_abc"
	result := discovery.PrevExitLine(logs)
	assert.Equal(t, "fatal: out of memory", result)
}

func TestPrevExitLine_TruncatesLongLine(t *testing.T) {
	// Lines longer than 120 runes should be truncated with an ellipsis.
	long := "error: " + string(make([]rune, 200))
	result := discovery.PrevExitLine(long)
	assert.LessOrEqual(t, len([]rune(result)), 120)
}

// ---------------------------------------------------------------------------
// Reconciler.SetRules / currentRules (via exported helpers)
// ---------------------------------------------------------------------------

func TestReconciler_SetRules_UpdatesRuleSet(t *testing.T) {
	cfg := &config.Config{}
	r := discovery.NewReconciler(cfg, nil, nil, nil, nil, nil)

	newRules, err := pbr.Load(nil, nil, pbr.BuiltinRules())
	if err != nil {
		t.Fatalf("loading rules: %v", err)
	}
	r.SetRules(newRules)
	got := discovery.CurrentRules(r)
	assert.Equal(t, len(newRules), len(got))
}

func TestReconciler_CurrentRules_ReturnsInitialRules(t *testing.T) {
	rules, err := pbr.Load(nil, nil, pbr.BuiltinRules())
	require.NoError(t, err)
	r := discovery.NewReconciler(&config.Config{}, nil, nil, nil, nil, rules)
	got := discovery.CurrentRules(r)
	assert.Equal(t, len(rules), len(got))
}

func TestReconciler_SetTransitionEvents_DoesNotPanic(t *testing.T) {
	r := discovery.NewReconciler(&config.Config{}, nil, nil, nil, nil, nil)
	ch := make(chan health.StateTransitionEvent, 4)
	// Should not panic.
	discovery.SetReconcilerTransitionEvents(r, ch)
}

// ---------------------------------------------------------------------------
// fetchPrevExitMsg (via export_test.go)
// ---------------------------------------------------------------------------

func TestFetchPrevExitMsg_NilK8sClient_ReturnsEmpty(t *testing.T) {
	// When the reconciler has no k8s client, fetchPrevExitMsg returns "".
	r := discovery.NewReconciler(&config.Config{}, nil, nil, nil, nil, nil)
	result := discovery.FetchPrevExitMsg(r, "default", "my-pod", "my-container")
	assert.Equal(t, "", result)
}

// ---------------------------------------------------------------------------
// containerName (via export_test.go)
// ---------------------------------------------------------------------------

func TestContainerName_EmptyNames_ReturnsEmpty(t *testing.T) {
	// The empty-names branch produces "" — the container has no Docker name assigned.
	assert.Equal(t, "", discovery.ContainerNameForTest(nil))
	assert.Equal(t, "", discovery.ContainerNameForTest([]string{}))
}

func TestContainerName_SingleName_StripsSlash(t *testing.T) {
	assert.Equal(t, "my-app", discovery.ContainerNameForTest([]string{"/my-app"}))
}
