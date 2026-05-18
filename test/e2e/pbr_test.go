//go:build integration

package e2e_test

// Tests in this file exercise the Policy-Based Rules (PBR) engine end-to-end.
// All tests are fully in-process: no Docker containers, no external services.
// Coverage dimensions:
//   - All built-in rules and log levels (including case normalisation)
//   - builtin-failing count_in_window threshold boundaries
//   - Priority / first-match-wins ordering (boundary and multi-rule)
//   - All operators with exact boundary values (gt, gte, lt, lte, eq, regex, glob)
//   - Multi-condition AND semantics
//   - OK, DEGRADED, RESTARTING states produced by log rules
//   - Infra rule boundaries (restart_count, uptime, runtime, phase, namespace)
//   - Container overrides: scoping, multi-container, priority interaction
//   - Health-key formation (Docker bare name vs K8s namespace/container)
//   - Snapshot field persistence across batches and engine restarts
//   - Hot-reload (SetRules) atomicity and backward compat with nil
//   - Validation: every invalid Load input returns a named error
//   - ClassifyChanges: rules and overrides are soft changes
//   - onChange callback contract
//   - User rule replacing builtin by name
//   - Catch-all rule (no conditions)

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/health"
	"github.com/errorprobe/errorprobe/internal/ingest"
	"github.com/errorprobe/errorprobe/internal/pbr"
	"github.com/errorprobe/errorprobe/internal/stack"
)

// ─── local helpers ────────────────────────────────────────────────────────────

// k8sEvent builds a log event tagged as a Kubernetes workload.
func k8sEvent(container, namespace, level, msg string) ingest.LogEvent {
	return ingest.LogEvent{
		Timestamp: time.Now(),
		Container: container,
		Namespace: namespace,
		Level:     level,
		Message:   msg,
		Runtime:   "k8s",
	}
}

// mustLoad calls pbr.Load and fatals on error.
func mustLoad(t *testing.T, cfgs []config.RuleConfig, overrides map[string][]config.RuleConfig, builtins []pbr.Rule) []pbr.Rule {
	t.Helper()
	rules, err := pbr.Load(cfgs, overrides, builtins)
	require.NoError(t, err)
	return rules
}

// newEngine creates a health engine with a temp snapshot path and given rules.
func newEngine(t *testing.T, rules []pbr.Rule) *health.Engine {
	t.Helper()
	return health.NewEngine(filepath.Join(t.TempDir(), "health.json"), rules, nil)
}

// newEngineWithCallback creates a health engine and returns a channel that
// receives each onChange snapshot.
func newEngineWithCallback(t *testing.T, rules []pbr.Rule) (*health.Engine, <-chan health.HealthSnapshot) {
	t.Helper()
	ch := make(chan health.HealthSnapshot, 8)
	snapPath := filepath.Join(t.TempDir(), "health.json")
	e := health.NewEngine(snapPath, rules, func(s health.HealthSnapshot) { ch <- s })
	return e, ch
}

// infraCtxE2E builds an EvalContext for the infra plane.
func infraCtxE2E(name, namespace, runtime string, restartCount int, uptime time.Duration, phase string) pbr.EvalContext {
	return pbr.EvalContext{Infra: &pbr.InfraContainer{
		Name: name, Namespace: namespace, Runtime: runtime,
		RestartCount: restartCount, Uptime: uptime, Phase: phase,
	}}
}

// logCtxE2E builds an EvalContext for the log plane.
func logCtxE2E(level, message, container, namespace, runtime string, count int, window time.Duration) pbr.EvalContext {
	return pbr.EvalContext{Log: &pbr.LogEvalContext{
		Event: ingest.LogEvent{
			Level: level, Message: message, Container: container,
			Namespace: namespace, Runtime: runtime,
		},
		CountInWindow: count,
		Window:        window,
	}}
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 1 — Built-in rules: all log levels
// ═══════════════════════════════════════════════════════════════════════════

func TestPBR_Builtin_ErrorLevel_SetsHasErrors(t *testing.T) {
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "boom")})
	ch := e.Snapshot().Containers["svc"]
	assert.Equal(t, health.StateHasErrors, ch.State)
	assert.Equal(t, "builtin-log-error", ch.MatchedRule)
}

func TestPBR_Builtin_WarnLevel_SetsHasWarnings(t *testing.T) {
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "warn", "deprecated")})
	ch := e.Snapshot().Containers["svc"]
	assert.Equal(t, health.StateHasWarnings, ch.State)
	assert.Equal(t, "builtin-log-warn", ch.MatchedRule)
}

func TestPBR_Builtin_InfoLevel_NoEntry(t *testing.T) {
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "info", "started")})
	assert.NotContains(t, e.Snapshot().Containers, "svc")
}

func TestPBR_Builtin_DebugLevel_NoEntry(t *testing.T) {
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "debug", "trace data")})
	assert.NotContains(t, e.Snapshot().Containers, "svc")
}

func TestPBR_Builtin_EmptyLevel_NoEntry(t *testing.T) {
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "", "no level set")})
	assert.NotContains(t, e.Snapshot().Containers, "svc")
}

// Level values are normalised to lowercase before comparison.
func TestPBR_Builtin_UppercaseERROR_NormalisedMatch(t *testing.T) {
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "ERROR", "uppercase level")})
	assert.Equal(t, health.StateHasErrors, e.Snapshot().Containers["svc"].State)
}

func TestPBR_Builtin_UppercaseWARN_NormalisedMatch(t *testing.T) {
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "WARN", "uppercase warn")})
	assert.Equal(t, health.StateHasWarnings, e.Snapshot().Containers["svc"].State)
}

func TestPBR_Builtin_MixedCaseError_NormalisedMatch(t *testing.T) {
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "Error", "mixed case")})
	assert.Equal(t, health.StateHasErrors, e.Snapshot().Containers["svc"].State)
}

// "warning" is not the same token as "warn" — must not match builtin-log-warn.
func TestPBR_Builtin_WarningNotWarn_NoMatch(t *testing.T) {
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "warning", "wrong token")})
	assert.NotContains(t, e.Snapshot().Containers, "svc")
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 2 — builtin-failing: count_in_window threshold boundaries
// ═══════════════════════════════════════════════════════════════════════════

// count=5 (exactly at threshold) → FAILING via builtin-failing.
func TestPBR_Builtin_Failing_CountAtThreshold(t *testing.T) {
	rules := mustLoad(t, nil, nil, pbr.BuiltinRules())
	ctx := logCtxE2E("error", "db down", "api", "", "", 5, time.Minute)
	result := pbr.Evaluate(rules, ctx)
	assert.Equal(t, "FAILING", result.State)
	assert.Equal(t, "builtin-failing", result.MatchedRule)
}

// count=4 (one below) → HAS_ERRORS via builtin-log-error, NOT FAILING.
func TestPBR_Builtin_Failing_CountOneBelowThreshold(t *testing.T) {
	rules := mustLoad(t, nil, nil, pbr.BuiltinRules())
	ctx := logCtxE2E("error", "db down", "api", "", "", 4, time.Minute)
	result := pbr.Evaluate(rules, ctx)
	assert.Equal(t, "HAS_ERRORS", result.State)
	assert.Equal(t, "builtin-log-error", result.MatchedRule)
}

// count=0 → HAS_ERRORS (builtin-failing requires count>=5).
func TestPBR_Builtin_Failing_CountZero_HitLogError(t *testing.T) {
	rules := mustLoad(t, nil, nil, pbr.BuiltinRules())
	ctx := logCtxE2E("error", "single error", "", "", "", 0, 0)
	result := pbr.Evaluate(rules, ctx)
	assert.Equal(t, "HAS_ERRORS", result.State)
	assert.Equal(t, "builtin-log-error", result.MatchedRule)
}

// count=100 with warn level → HAS_WARNINGS; builtin-failing only triggers on error.
func TestPBR_Builtin_Failing_WarnHighCount_NotEscalated(t *testing.T) {
	rules := mustLoad(t, nil, nil, pbr.BuiltinRules())
	ctx := logCtxE2E("warn", "flood", "", "", "", 100, time.Minute)
	result := pbr.Evaluate(rules, ctx)
	assert.Equal(t, "HAS_WARNINGS", result.State)
	assert.Equal(t, "builtin-log-warn", result.MatchedRule)
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 3 — Priority: first-match-wins ordering
// ═══════════════════════════════════════════════════════════════════════════

// User rule at priority 200 beats builtin-log-error at 100.
func TestPBR_Priority_HigherUserRuleBeatsBuiltin(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "my-rule", Priority: 200, Match: "log", When: map[string]string{"level": "error"}, SetState: "FAILING"},
	}, nil, pbr.BuiltinRules())
	e := newEngine(t, rules)
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "crash")})
	ch := e.Snapshot().Containers["svc"]
	assert.Equal(t, health.StateFailing, ch.State)
	assert.Equal(t, "my-rule", ch.MatchedRule)
}

// User rule at priority 95 loses to builtin-log-error at 100.
func TestPBR_Priority_LowerUserRuleLosesToBuiltin(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "my-rule", Priority: 95, Match: "log", When: map[string]string{"level": "error"}, SetState: "FAILING"},
	}, nil, pbr.BuiltinRules())
	result := pbr.Evaluate(rules, logCtxE2E("error", "msg", "", "", "", 0, 0))
	assert.Equal(t, "HAS_ERRORS", result.State)
	assert.Equal(t, "builtin-log-error", result.MatchedRule)
}

// User rule at 111 beats builtin-failing (110) for the same event.
func TestPBR_Priority_BoundaryJustAboveBuiltinFailing(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "custom-111", Priority: 111, Match: "log",
			When:     map[string]string{"level": "error"},
			SetState: "DEGRADED"},
	}, nil, pbr.BuiltinRules())
	ctx := logCtxE2E("error", "msg", "", "", "", 5, time.Minute)
	result := pbr.Evaluate(rules, ctx)
	assert.Equal(t, "DEGRADED", result.State)
	assert.Equal(t, "custom-111", result.MatchedRule)
}

// User rule at 109 loses to builtin-failing (110) when count_in_window >= 5.
func TestPBR_Priority_BoundaryJustBelowBuiltinFailing(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "custom-109", Priority: 109, Match: "log",
			When:     map[string]string{"level": "error"},
			SetState: "DEGRADED"},
	}, nil, pbr.BuiltinRules())
	ctx := logCtxE2E("error", "msg", "", "", "", 5, time.Minute)
	result := pbr.Evaluate(rules, ctx)
	assert.Equal(t, "FAILING", result.State)
	assert.Equal(t, "builtin-failing", result.MatchedRule)
}

// When two user rules match the same event, only the highest priority fires.
func TestPBR_Priority_TwoMatchingUserRules_HighestWins(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "low-rule", Priority: 50, Match: "log", When: map[string]string{"level": "error"}, SetState: "HAS_ERRORS"},
		{Name: "high-rule", Priority: 300, Match: "log", When: map[string]string{"level": "error"}, SetState: "FAILING"},
	}, nil, nil)
	result := pbr.Evaluate(rules, logCtxE2E("error", "msg", "", "", "", 0, 0))
	assert.Equal(t, "FAILING", result.State)
	assert.Equal(t, "high-rule", result.MatchedRule)
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 4 — Operators: numeric boundaries (gt / gte / lt / lte)
// ═══════════════════════════════════════════════════════════════════════════

func TestPBR_Operator_Gt_ExactValue_NoMatch(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "log", When: map[string]string{"count_in_window": "> 5"}, SetState: "FAILING"},
	}, nil, nil)
	assert.Equal(t, "", pbr.Evaluate(rules, logCtxE2E("error", "", "", "", "", 5, time.Minute)).State)
}

func TestPBR_Operator_Gt_OneAbove_Match(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "log", When: map[string]string{"count_in_window": "> 5"}, SetState: "FAILING"},
	}, nil, nil)
	assert.Equal(t, "FAILING", pbr.Evaluate(rules, logCtxE2E("error", "", "", "", "", 6, time.Minute)).State)
}

func TestPBR_Operator_Gte_ExactValue_Match(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "log", When: map[string]string{"count_in_window": ">= 5"}, SetState: "FAILING"},
	}, nil, nil)
	assert.Equal(t, "FAILING", pbr.Evaluate(rules, logCtxE2E("error", "", "", "", "", 5, time.Minute)).State)
}

func TestPBR_Operator_Gte_OneBelowValue_NoMatch(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "log", When: map[string]string{"count_in_window": ">= 5"}, SetState: "FAILING"},
	}, nil, nil)
	assert.Equal(t, "", pbr.Evaluate(rules, logCtxE2E("error", "", "", "", "", 4, time.Minute)).State)
}

func TestPBR_Operator_Lt_ExactValue_NoMatch(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "infra", When: map[string]string{"restart_count": "< 3"}, SetState: "RESTARTING"},
	}, nil, nil)
	assert.Equal(t, "", pbr.Evaluate(rules, infraCtxE2E("svc", "", "k8s", 3, time.Minute, "")).State)
}

func TestPBR_Operator_Lt_OneBelowValue_Match(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "infra", When: map[string]string{"restart_count": "< 3"}, SetState: "RESTARTING"},
	}, nil, nil)
	assert.Equal(t, "RESTARTING", pbr.Evaluate(rules, infraCtxE2E("svc", "", "k8s", 2, time.Minute, "")).State)
}

func TestPBR_Operator_Lte_ExactValue_Match(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "infra", When: map[string]string{"restart_count": "<= 3"}, SetState: "RESTARTING"},
	}, nil, nil)
	assert.Equal(t, "RESTARTING", pbr.Evaluate(rules, infraCtxE2E("svc", "", "k8s", 3, time.Minute, "")).State)
}

func TestPBR_Operator_Lte_OneAboveValue_NoMatch(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "infra", When: map[string]string{"restart_count": "<= 3"}, SetState: "RESTARTING"},
	}, nil, nil)
	assert.Equal(t, "", pbr.Evaluate(rules, infraCtxE2E("svc", "", "k8s", 4, time.Minute, "")).State)
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 5 — Duration boundary conditions (uptime)
// ═══════════════════════════════════════════════════════════════════════════

// builtin-k8s-restarting uses uptime < 2m (120 s). Exact threshold = no match.
func TestPBR_Duration_UptimeExactly120s_NoMatch(t *testing.T) {
	rules := mustLoad(t, nil, nil, pbr.BuiltinRules())
	ctx := infraCtxE2E("svc", "", "k8s", 1, 120*time.Second, "")
	assert.Equal(t, "", pbr.Evaluate(rules, ctx).State)
}

func TestPBR_Duration_Uptime119s_Match(t *testing.T) {
	rules := mustLoad(t, nil, nil, pbr.BuiltinRules())
	ctx := infraCtxE2E("svc", "", "k8s", 1, 119*time.Second, "")
	assert.Equal(t, "RESTARTING", pbr.Evaluate(rules, ctx).State)
}

func TestPBR_Duration_Uptime121s_NoMatch(t *testing.T) {
	rules := mustLoad(t, nil, nil, pbr.BuiltinRules())
	ctx := infraCtxE2E("svc", "", "k8s", 1, 121*time.Second, "")
	assert.Equal(t, "", pbr.Evaluate(rules, ctx).State)
}

// Custom rule: uptime >= 5m → DEGRADED (long-running but never restarted).
func TestPBR_Duration_CustomGte_Match(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "long-running", Priority: 200, Match: "infra",
			When: map[string]string{"uptime": ">= 5m"}, SetState: "DEGRADED"},
	}, nil, nil)
	ctx := infraCtxE2E("svc", "", "k8s", 0, 5*time.Minute, "")
	result := pbr.Evaluate(rules, ctx)
	assert.Equal(t, "DEGRADED", result.State)
}

func TestPBR_Duration_CustomGte_ExactlyBelow_NoMatch(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "long-running", Priority: 200, Match: "infra",
			When: map[string]string{"uptime": ">= 5m"}, SetState: "DEGRADED"},
	}, nil, nil)
	ctx := infraCtxE2E("svc", "", "k8s", 0, 299*time.Second, "")
	assert.Equal(t, "", pbr.Evaluate(rules, ctx).State)
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 6 — Regex operator
// ═══════════════════════════════════════════════════════════════════════════

func TestPBR_Regex_SimplePattern_Match(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 300, Match: "log", When: map[string]string{"message": "~.*out of memory.*"}, SetState: "FAILING"},
	}, nil, pbr.BuiltinRules())
	e := newEngine(t, rules)
	e.ProcessBatch([]ingest.LogEvent{logEvent("worker", "warn", "process killed: out of memory")})
	assert.Equal(t, health.StateFailing, e.Snapshot().Containers["worker"].State)
}

func TestPBR_Regex_SimplePattern_NoMatch(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 300, Match: "log", When: map[string]string{"message": "~^panic"}, SetState: "FAILING"},
	}, nil, pbr.BuiltinRules())
	result := pbr.Evaluate(rules, logCtxE2E("error", "connection refused", "", "", "", 0, 0))
	assert.NotEqual(t, "FAILING", result.State)
}

func TestPBR_Regex_AnchoredStart_Match(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 300, Match: "log", When: map[string]string{"message": "~^FATAL:"}, SetState: "FAILING"},
	}, nil, nil)
	result := pbr.Evaluate(rules, logCtxE2E("info", "FATAL: disk full", "", "", "", 0, 0))
	assert.Equal(t, "FAILING", result.State)
}

func TestPBR_Regex_AnchoredStart_NoMatchMiddle(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 300, Match: "log", When: map[string]string{"message": "~^FATAL:"}, SetState: "FAILING"},
	}, nil, nil)
	result := pbr.Evaluate(rules, logCtxE2E("info", "something FATAL: disk full", "", "", "", 0, 0))
	assert.Equal(t, "", result.State)
}

func TestPBR_Regex_CaseInsensitiveFlag_Match(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 300, Match: "log", When: map[string]string{"message": "~(?i)panic"}, SetState: "FAILING"},
	}, nil, nil)
	result := pbr.Evaluate(rules, logCtxE2E("error", "PANIC: nil pointer", "", "", "", 0, 0))
	assert.Equal(t, "FAILING", result.State)
}

func TestPBR_Regex_InvalidPattern_LoadError(t *testing.T) {
	_, err := pbr.Load([]config.RuleConfig{
		{Name: "bad", Priority: 100, Match: "log", When: map[string]string{"message": "~[invalid"}, SetState: "FAILING"},
	}, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `rule "bad"`)
	assert.Contains(t, err.Error(), "invalid regex")
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 7 — Glob operator
// ═══════════════════════════════════════════════════════════════════════════

func TestPBR_Glob_WildcardSuffix_Match(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 300, Match: "log", When: map[string]string{"container": "web-*"}, SetState: "FAILING"},
	}, nil, nil)
	result := pbr.Evaluate(rules, logCtxE2E("error", "", "web-server", "", "", 0, 0))
	assert.Equal(t, "FAILING", result.State)
}

func TestPBR_Glob_WildcardSuffix_NoMatch(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 300, Match: "log", When: map[string]string{"container": "web-*"}, SetState: "FAILING"},
	}, nil, nil)
	result := pbr.Evaluate(rules, logCtxE2E("error", "", "api-server", "", "", 0, 0))
	assert.Equal(t, "", result.State)
}

func TestPBR_Glob_UniversalWildcard_MatchesAny(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "catch-all", Priority: 50, Match: "log", When: map[string]string{"container": "*"}, SetState: "HAS_ERRORS"},
	}, nil, nil)
	result := pbr.Evaluate(rules, logCtxE2E("info", "", "anything", "", "", 0, 0))
	assert.Equal(t, "HAS_ERRORS", result.State)
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 8 — Multi-condition AND semantics
// ═══════════════════════════════════════════════════════════════════════════

func TestPBR_MultiCondition_BothMatch_Fires(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "log",
			When:     map[string]string{"level": "error", "count_in_window": ">= 3"},
			SetState: "FAILING"},
	}, nil, nil)
	result := pbr.Evaluate(rules, logCtxE2E("error", "", "", "", "", 3, time.Minute))
	assert.Equal(t, "FAILING", result.State)
}

func TestPBR_MultiCondition_LevelMatchCountMiss_NoFire(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "log",
			When:     map[string]string{"level": "error", "count_in_window": ">= 3"},
			SetState: "FAILING"},
	}, nil, nil)
	result := pbr.Evaluate(rules, logCtxE2E("error", "", "", "", "", 2, time.Minute))
	assert.Equal(t, "", result.State)
}

func TestPBR_MultiCondition_CountMatchLevelMiss_NoFire(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "log",
			When:     map[string]string{"level": "error", "count_in_window": ">= 3"},
			SetState: "FAILING"},
	}, nil, nil)
	result := pbr.Evaluate(rules, logCtxE2E("warn", "", "", "", "", 5, time.Minute))
	assert.Equal(t, "", result.State)
}

func TestPBR_MultiCondition_ThreeConditions_AllRequired(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "infra",
			When: map[string]string{
				"runtime":       "k8s",
				"restart_count": ">= 2",
				"phase":         "CrashLoopBackOff",
			},
			SetState: "FAILING"},
	}, nil, nil)

	// All three match.
	full := infraCtxE2E("svc", "", "k8s", 2, 5*time.Minute, "CrashLoopBackOff")
	assert.Equal(t, "FAILING", pbr.Evaluate(rules, full).State)

	// phase missing.
	missing := infraCtxE2E("svc", "", "k8s", 2, 5*time.Minute, "Running")
	assert.Equal(t, "", pbr.Evaluate(rules, missing).State)

	// restart_count too low.
	lowRestart := infraCtxE2E("svc", "", "k8s", 1, 5*time.Minute, "CrashLoopBackOff")
	assert.Equal(t, "", pbr.Evaluate(rules, lowRestart).State)
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 9 — OK state: suppression semantics
// ═══════════════════════════════════════════════════════════════════════════

// A rule returning OK means the engine skips the event: no snapshot entry.
func TestPBR_OKState_NoSnapshotEntry(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "suppress", Priority: 200, Match: "log",
			When: map[string]string{"level": "error"}, SetState: "OK"},
	}, nil, nil)
	e := newEngine(t, rules)
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "expected")})
	assert.NotContains(t, e.Snapshot().Containers, "svc",
		"OK state from PBR must prevent any snapshot entry")
}

// User OK rule at priority 200 beats builtin-log-error at 100.
func TestPBR_OKState_BeatsBuiltinError(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "suppress-err", Priority: 200, Match: "log",
			When: map[string]string{"level": "error"}, SetState: "OK"},
	}, nil, pbr.BuiltinRules())
	e := newEngine(t, rules)
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "expected")})
	if ch, ok := e.Snapshot().Containers["svc"]; ok {
		assert.NotEqual(t, health.StateHasErrors, ch.State)
	}
}

func TestPBR_OKState_BeatsBuiltinWarn(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "suppress-warn", Priority: 200, Match: "log",
			When: map[string]string{"level": "warn"}, SetState: "OK"},
	}, nil, pbr.BuiltinRules())
	e := newEngine(t, rules)
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "warn", "noisy")})
	assert.NotContains(t, e.Snapshot().Containers, "svc")
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 10 — DEGRADED / RESTARTING from log rules (engine skips them)
// ═══════════════════════════════════════════════════════════════════════════

// A log rule returning DEGRADED is not HAS_ERRORS|FAILING — engine skips it.
func TestPBR_DEGRADEDFromLogRule_NotRecordedByEngine(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "degrade", Priority: 200, Match: "log",
			When: map[string]string{"level": "error"}, SetState: "DEGRADED"},
	}, nil, nil)
	e := newEngine(t, rules)
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "broken")})
	assert.NotContains(t, e.Snapshot().Containers, "svc",
		"DEGRADED from a log rule is not handled by the engine and must not create an entry")
}

// Same for RESTARTING from a log rule.
func TestPBR_RESTARTINGFromLogRule_NotRecordedByEngine(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "restart-log", Priority: 200, Match: "log",
			When: map[string]string{"level": "error"}, SetState: "RESTARTING"},
	}, nil, nil)
	e := newEngine(t, rules)
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "broken")})
	assert.NotContains(t, e.Snapshot().Containers, "svc")
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 11 — Infra rule evaluation: all boundary conditions
// ═══════════════════════════════════════════════════════════════════════════

func TestPBR_Infra_RestartCountZero_Builtin_NoMatch(t *testing.T) {
	rules := mustLoad(t, nil, nil, pbr.BuiltinRules())
	ctx := infraCtxE2E("svc", "", "k8s", 0, 90*time.Second, "")
	assert.Equal(t, "", pbr.Evaluate(rules, ctx).State)
}

func TestPBR_Infra_RestartCountOne_Builtin_Match(t *testing.T) {
	rules := mustLoad(t, nil, nil, pbr.BuiltinRules())
	ctx := infraCtxE2E("svc", "", "k8s", 1, 90*time.Second, "")
	assert.Equal(t, "RESTARTING", pbr.Evaluate(rules, ctx).State)
}

func TestPBR_Infra_DockerRuntime_BuiltinDoesNotFire(t *testing.T) {
	// builtin-k8s-restarting requires runtime=k8s; docker must not match.
	rules := mustLoad(t, nil, nil, pbr.BuiltinRules())
	ctx := infraCtxE2E("svc", "", "docker", 5, 30*time.Second, "")
	assert.Equal(t, "", pbr.Evaluate(rules, ctx).State)
}

func TestPBR_Infra_RuntimeCaseNormalised(t *testing.T) {
	// "K8S" must normalise to "k8s" and match the builtin runtime=k8s condition.
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "infra",
			When:     map[string]string{"runtime": "K8S", "restart_count": "> 0"},
			SetState: "RESTARTING"},
	}, nil, nil)
	ctx := infraCtxE2E("svc", "", "k8s", 1, 30*time.Second, "")
	assert.Equal(t, "RESTARTING", pbr.Evaluate(rules, ctx).State)
}

func TestPBR_Infra_Phase_CustomRule(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "crashloop", Priority: 200, Match: "infra",
			When: map[string]string{"phase": "CrashLoopBackOff"}, SetState: "FAILING"},
	}, nil, nil)
	match := infraCtxE2E("svc", "", "k8s", 0, 5*time.Minute, "CrashLoopBackOff")
	nomatch := infraCtxE2E("svc", "", "k8s", 0, 5*time.Minute, "Running")
	assert.Equal(t, "FAILING", pbr.Evaluate(rules, match).State)
	assert.Equal(t, "", pbr.Evaluate(rules, nomatch).State)
}

func TestPBR_Infra_Namespace_CustomRule(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "prod-strict", Priority: 200, Match: "infra",
			When: map[string]string{"namespace": "production", "restart_count": "> 0"},
			SetState: "FAILING"},
	}, nil, nil)
	prod := infraCtxE2E("svc", "production", "k8s", 1, 5*time.Minute, "")
	staging := infraCtxE2E("svc", "staging", "k8s", 1, 5*time.Minute, "")
	assert.Equal(t, "FAILING", pbr.Evaluate(rules, prod).State)
	assert.Equal(t, "", pbr.Evaluate(rules, staging).State)
}

func TestPBR_Infra_LogPlaneContextIgnoredByInfraRule(t *testing.T) {
	// An infra-plane rule must not fire when evaluated against a log context.
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "infra",
			When: map[string]string{"restart_count": "> 0"}, SetState: "RESTARTING"},
	}, nil, nil)
	logCtxVal := logCtxE2E("error", "", "", "", "", 0, 0)
	assert.Equal(t, "", pbr.Evaluate(rules, logCtxVal).State)
}

func TestPBR_Log_InfraPlaneContextIgnoredByLogRule(t *testing.T) {
	// A log-plane rule must not fire when evaluated against an infra context.
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "log",
			When: map[string]string{"level": "error"}, SetState: "HAS_ERRORS"},
	}, nil, nil)
	ctx := infraCtxE2E("svc", "", "k8s", 0, time.Minute, "")
	assert.Equal(t, "", pbr.Evaluate(rules, ctx).State)
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 12 — Container overrides: scoping and interaction
// ═══════════════════════════════════════════════════════════════════════════

// Override fires only for the named container.
func TestPBR_Override_ScopedToNamedContainer(t *testing.T) {
	overrides := map[string][]config.RuleConfig{
		"noisy": {{Name: "noisy-ok", Priority: 500, Match: "log",
			When: map[string]string{"level": "error"}, SetState: "OK"}},
	}
	e := newEngine(t, mustLoad(t, nil, overrides, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{
		logEvent("noisy", "error", "expected"),
		logEvent("other", "error", "unexpected"),
	})
	snap := e.Snapshot()
	if ch, ok := snap.Containers["noisy"]; ok {
		assert.NotEqual(t, health.StateHasErrors, ch.State)
	}
	require.Contains(t, snap.Containers, "other")
	assert.Equal(t, health.StateHasErrors, snap.Containers["other"].State)
}

// Multiple containers each have their own independent override.
func TestPBR_Override_MultipleContainersScoped(t *testing.T) {
	overrides := map[string][]config.RuleConfig{
		"svc-a": {{Name: "a-escalate", Priority: 500, Match: "log",
			When: map[string]string{"level": "warn"}, SetState: "FAILING"}},
		"svc-b": {{Name: "b-suppress", Priority: 501, Match: "log",
			When: map[string]string{"level": "error"}, SetState: "OK"}},
	}
	e := newEngine(t, mustLoad(t, nil, overrides, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{
		logEvent("svc-a", "warn", "elevated warn"),
		logEvent("svc-b", "error", "suppressed error"),
		logEvent("svc-c", "error", "normal error"),
	})
	snap := e.Snapshot()

	assert.Equal(t, health.StateFailing, snap.Containers["svc-a"].State,
		"svc-a warn must be escalated to FAILING by its override")
	if ch, ok := snap.Containers["svc-b"]; ok {
		assert.NotEqual(t, health.StateHasErrors, ch.State,
			"svc-b error must be suppressed by its override")
	}
	assert.Equal(t, health.StateHasErrors, snap.Containers["svc-c"].State,
		"svc-c must follow builtin rules unmodified")
}

// Override priority beats a matching global rule for the named container.
func TestPBR_Override_PriorityBeatsGlobalRule(t *testing.T) {
	global := []config.RuleConfig{
		{Name: "global-warn-fail", Priority: 150, Match: "log",
			When: map[string]string{"level": "warn"}, SetState: "FAILING"},
	}
	overrides := map[string][]config.RuleConfig{
		"special": {{Name: "special-warn-ok", Priority: 600, Match: "log",
			When: map[string]string{"level": "warn"}, SetState: "OK"}},
	}
	e := newEngine(t, mustLoad(t, global, overrides, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{
		logEvent("special", "warn", "tolerated"),
		logEvent("other", "warn", "real warn"),
	})
	snap := e.Snapshot()

	if ch, ok := snap.Containers["special"]; ok {
		assert.NotEqual(t, health.StateFailing, ch.State,
			"special container override at 600 must beat global rule at 150")
	}
	assert.Equal(t, health.StateFailing, snap.Containers["other"].State,
		"other container must still hit the global rule at 150")
}

// Non-existent container name in override is harmless.
func TestPBR_Override_NonExistentContainerName_Harmless(t *testing.T) {
	overrides := map[string][]config.RuleConfig{
		"ghost": {{Name: "ghost-rule", Priority: 500, Match: "log",
			When: map[string]string{"level": "error"}, SetState: "OK"}},
	}
	rules, err := pbr.Load(nil, overrides, pbr.BuiltinRules())
	require.NoError(t, err, "override for non-existent container must not cause a load error")
	e := newEngine(t, rules)
	e.ProcessBatch([]ingest.LogEvent{logEvent("real", "error", "crash")})
	assert.Equal(t, health.StateHasErrors, e.Snapshot().Containers["real"].State)
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 13 — Health key formation
// ═══════════════════════════════════════════════════════════════════════════

func TestPBR_HealthKey_Docker_BareContainerName(t *testing.T) {
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{logEvent("myapp", "error", "crash")})
	require.Contains(t, e.Snapshot().Containers, "myapp")
	assert.NotContains(t, e.Snapshot().Containers, "/myapp")
}

func TestPBR_HealthKey_K8s_NamespacedKey(t *testing.T) {
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{k8sEvent("api", "production", "error", "crash")})
	require.Contains(t, e.Snapshot().Containers, "production/api",
		"K8s health key must be namespace/container")
	assert.NotContains(t, e.Snapshot().Containers, "api")
}

func TestPBR_HealthKey_K8sWithoutNamespace_BareKey(t *testing.T) {
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	// Runtime=k8s but empty namespace.
	ev := ingest.LogEvent{Timestamp: time.Now(), Container: "worker", Level: "error",
		Message: "crash", Runtime: "k8s"}
	e.ProcessBatch([]ingest.LogEvent{ev})
	require.Contains(t, e.Snapshot().Containers, "worker")
	assert.NotContains(t, e.Snapshot().Containers, "/worker")
}

// Container override keyed on a bare name fires for k8s containers with the
// same bare name regardless of namespace, because the override injects a
// container-name condition, not a health-key condition.
func TestPBR_HealthKey_OverrideMatchesContainerNameAcrossNamespaces(t *testing.T) {
	// Override is for "api" (bare name), but event carries namespace "prod" → key "prod/api".
	overrides := map[string][]config.RuleConfig{
		"api": {{Name: "suppress-api", Priority: 500, Match: "log",
			When: map[string]string{"level": "error"}, SetState: "OK"}},
	}
	e := newEngine(t, mustLoad(t, nil, overrides, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{k8sEvent("api", "prod", "error", "crash")})
	// The PBR container condition checks the event's Container field (bare name),
	// so "api" == "api" → override DOES fire regardless of namespace key.
	// This is the expected behaviour: the override targets the container name, not the full key.
	snap := e.Snapshot()
	if ch, ok := snap.Containers["prod/api"]; ok {
		assert.NotEqual(t, health.StateHasErrors, ch.State,
			"override on container name 'api' must suppress even k8s-namespaced containers named 'api'")
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 14 — Snapshot field persistence
// ═══════════════════════════════════════════════════════════════════════════

func TestPBR_Snapshot_ErrorCountAccumulates(t *testing.T) {
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "e1")})
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "e2")})
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "e3")})
	assert.Equal(t, 3, e.Snapshot().Containers["svc"].ErrorCount)
}

func TestPBR_Snapshot_FirstErrorAtSetOnce(t *testing.T) {
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	t1 := time.Now().Add(-5 * time.Second)
	t2 := time.Now()
	e.ProcessBatch([]ingest.LogEvent{{Timestamp: t1, Container: "svc", Level: "error", Message: "first", Runtime: "docker"}})
	e.ProcessBatch([]ingest.LogEvent{{Timestamp: t2, Container: "svc", Level: "error", Message: "second", Runtime: "docker"}})
	ch := e.Snapshot().Containers["svc"]
	require.NotNil(t, ch.FirstErrorAt)
	assert.WithinDuration(t, t1, *ch.FirstErrorAt, time.Second,
		"FirstErrorAt must be set on first error and not updated subsequently")
}

func TestPBR_Snapshot_LastErrorAtUpdatesEachBatch(t *testing.T) {
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	t1 := time.Now().Add(-5 * time.Second)
	t2 := time.Now()
	e.ProcessBatch([]ingest.LogEvent{{Timestamp: t1, Container: "svc", Level: "error", Message: "first", Runtime: "docker"}})
	before := e.Snapshot().Containers["svc"].LastErrorAt
	e.ProcessBatch([]ingest.LogEvent{{Timestamp: t2, Container: "svc", Level: "error", Message: "second", Runtime: "docker"}})
	after := e.Snapshot().Containers["svc"].LastErrorAt
	require.NotNil(t, before)
	require.NotNil(t, after)
	assert.True(t, after.After(*before), "LastErrorAt must advance on each error")
}

func TestPBR_Snapshot_MatchedRuleUpdatesOnNewMatch(t *testing.T) {
	rulesA := mustLoad(t, []config.RuleConfig{
		{Name: "rule-warn", Priority: 200, Match: "log", When: map[string]string{"level": "warn"}, SetState: "HAS_ERRORS"},
		{Name: "rule-error", Priority: 150, Match: "log", When: map[string]string{"level": "error"}, SetState: "FAILING"},
	}, nil, nil)
	e := newEngine(t, rulesA)
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "warn", "first")})
	assert.Equal(t, "rule-warn", e.Snapshot().Containers["svc"].MatchedRule)

	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "second")})
	assert.Equal(t, "rule-error", e.Snapshot().Containers["svc"].MatchedRule,
		"MatchedRule must update when a different rule fires")
}

func TestPBR_Snapshot_MatchedRuleSurvivesRestart(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "health.json")
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "my-rule", Priority: 200, Match: "log", When: map[string]string{"level": "error"}, SetState: "HAS_ERRORS"},
	}, nil, nil)
	e1 := health.NewEngine(snapPath, rules, nil)
	e1.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "boom")})
	assert.Equal(t, "my-rule", e1.Snapshot().Containers["svc"].MatchedRule)

	// New engine instance loads the persisted snapshot.
	e2 := health.NewEngine(snapPath, rules, nil)
	assert.Equal(t, "my-rule", e2.Snapshot().Containers["svc"].MatchedRule,
		"MatchedRule must survive engine restart via JSON persistence")
}

func TestPBR_Snapshot_MatchedRuleClearedAfterReset(t *testing.T) {
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "boom")})
	require.NotEmpty(t, e.Snapshot().Containers["svc"].MatchedRule)
	require.NoError(t, e.Reset("svc"))
	ch, ok := e.Snapshot().Containers["svc"]
	if ok {
		assert.Empty(t, ch.MatchedRule, "MatchedRule must be cleared after Reset")
	}
}

func TestPBR_Snapshot_InfoEventMixedInBatch_NoCount(t *testing.T) {
	// A batch of info + error events: only errors increment ErrorCount.
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{
		logEvent("svc", "info", "start"),
		logEvent("svc", "error", "crash"),
		logEvent("svc", "info", "end"),
	})
	assert.Equal(t, 1, e.Snapshot().Containers["svc"].ErrorCount,
		"info events in the same batch must not increment ErrorCount")
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 15 — Hot-reload: SetRules atomicity and nil semantics
// ═══════════════════════════════════════════════════════════════════════════

func TestPBR_HotReload_NewRulesAffectFutureBatches(t *testing.T) {
	initialRules := mustLoad(t, nil, nil, pbr.BuiltinRules())
	e := newEngine(t, initialRules)

	// Baseline: error → HAS_ERRORS.
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "crash")})
	assert.Equal(t, health.StateHasErrors, e.Snapshot().Containers["svc"].State)

	// Swap: error → FAILING.
	newRules := mustLoad(t, []config.RuleConfig{
		{Name: "escalate", Priority: 200, Match: "log",
			When: map[string]string{"level": "error"}, SetState: "FAILING"},
	}, nil, pbr.BuiltinRules())
	e.SetRules(newRules)
	require.NoError(t, e.Reset("svc"))

	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "crash again")})
	assert.Equal(t, health.StateFailing, e.Snapshot().Containers["svc"].State)
}

func TestPBR_HotReload_PastStateUnchanged(t *testing.T) {
	initialRules := mustLoad(t, nil, nil, pbr.BuiltinRules())
	e := newEngine(t, initialRules)
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "old error")})
	require.Equal(t, health.StateHasErrors, e.Snapshot().Containers["svc"].State)

	// Swap to rules that suppress errors.
	noRules := mustLoad(t, []config.RuleConfig{
		{Name: "suppress", Priority: 200, Match: "log",
			When: map[string]string{"level": "error"}, SetState: "OK"},
	}, nil, nil)
	e.SetRules(noRules)

	// Existing state must not change — SetRules does not retroactively re-evaluate.
	assert.Equal(t, health.StateHasErrors, e.Snapshot().Containers["svc"].State,
		"SetRules must not retroactively modify existing health state")
}

func TestPBR_HotReload_SetRulesNil_NoMatchGoingForward(t *testing.T) {
	// SetRules(nil) sets an empty rule set — subsequent events produce no matches.
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.SetRules(nil) // nil slice → empty; no rules
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "boom")})
	assert.NotContains(t, e.Snapshot().Containers, "svc",
		"after SetRules(nil), no rules fire and no entries are created")
}

func TestPBR_HotReload_SetRulesEmpty_NoMatch(t *testing.T) {
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.SetRules([]pbr.Rule{})
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "boom")})
	assert.NotContains(t, e.Snapshot().Containers, "svc")
}

func TestPBR_HotReload_ConcurrentSetRulesAndProcessBatch_NoPanic(t *testing.T) {
	e := newEngine(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	altRules := mustLoad(t, []config.RuleConfig{
		{Name: "alt", Priority: 200, Match: "log",
			When: map[string]string{"level": "error"}, SetState: "FAILING"},
	}, nil, nil)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "concurrent")})
		}()
		go func() {
			defer wg.Done()
			e.SetRules(altRules)
		}()
	}
	wg.Wait()
	// No panic = pass. State is non-deterministic but must not be corrupted.
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 16 — Validation: every invalid Load input returns a named error
// ═══════════════════════════════════════════════════════════════════════════

func TestPBR_Load_EmptyName_Error(t *testing.T) {
	_, err := pbr.Load([]config.RuleConfig{
		{Name: "", Priority: 100, Match: "log", SetState: "HAS_ERRORS"},
	}, nil, nil)
	require.Error(t, err)
}

func TestPBR_Load_UnknownMatchContext_Error(t *testing.T) {
	for _, bad := range []string{"network", "metric", "LOG", ""} {
		_, err := pbr.Load([]config.RuleConfig{
			{Name: "r", Priority: 100, Match: bad, SetState: "HAS_ERRORS"},
		}, nil, nil)
		require.Errorf(t, err, "match context %q must be rejected", bad)
		assert.Contains(t, err.Error(), "r")
	}
}

func TestPBR_Load_UnknownSetState_Error(t *testing.T) {
	for _, bad := range []string{"BROKEN", "UNKNOWN", "has_errors", ""} {
		_, err := pbr.Load([]config.RuleConfig{
			{Name: "r", Priority: 100, Match: "log", SetState: bad},
		}, nil, nil)
		require.Errorf(t, err, "set_state %q must be rejected", bad)
	}
}

func TestPBR_Load_InvalidRegex_Error(t *testing.T) {
	_, err := pbr.Load([]config.RuleConfig{
		{Name: "r", Priority: 100, Match: "log",
			When: map[string]string{"message": "~[invalid regex"}, SetState: "FAILING"},
	}, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "r")
}

func TestPBR_Load_InfraFieldInLogRule_Error(t *testing.T) {
	// restart_count is an infra field; using it in a log rule must fail.
	_, err := pbr.Load([]config.RuleConfig{
		{Name: "r", Priority: 100, Match: "log",
			When: map[string]string{"restart_count": "> 0"}, SetState: "FAILING"},
	}, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "r")
}

func TestPBR_Load_LogFieldInInfraRule_Error(t *testing.T) {
	// level is a log field; using it in an infra rule must fail.
	_, err := pbr.Load([]config.RuleConfig{
		{Name: "r", Priority: 100, Match: "infra",
			When: map[string]string{"level": "error"}, SetState: "RESTARTING"},
	}, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "r")
}

func TestPBR_Load_DuplicatePriority_UserVsUser_Error(t *testing.T) {
	_, err := pbr.Load([]config.RuleConfig{
		{Name: "a", Priority: 100, Match: "log", SetState: "HAS_ERRORS",
			When: map[string]string{"level": "error"}},
		{Name: "b", Priority: 100, Match: "log", SetState: "HAS_ERRORS",
			When: map[string]string{"level": "warn"}},
	}, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a")
	assert.Contains(t, err.Error(), "b")
}

func TestPBR_Load_DuplicatePriority_UserVsOverride_Error(t *testing.T) {
	// User global rule at 100 + override rule at 100 → duplicate.
	_, err := pbr.Load([]config.RuleConfig{
		{Name: "global", Priority: 100, Match: "log", SetState: "HAS_ERRORS",
			When: map[string]string{"level": "error"}},
	}, map[string][]config.RuleConfig{
		"myapp": {{Name: "override", Priority: 100, Match: "log", SetState: "OK",
			When: map[string]string{"level": "error"}}},
	}, nil)
	require.Error(t, err)
}

func TestPBR_Load_ZeroPriority_ValidLoads(t *testing.T) {
	rules, err := pbr.Load([]config.RuleConfig{
		{Name: "r", Priority: 0, Match: "log", SetState: "HAS_ERRORS",
			When: map[string]string{"level": "error"}},
	}, nil, nil)
	require.NoError(t, err)
	require.Len(t, rules, 1)
}

func TestPBR_Load_NegativePriority_ValidLoads(t *testing.T) {
	rules, err := pbr.Load([]config.RuleConfig{
		{Name: "r", Priority: -10, Match: "log", SetState: "HAS_ERRORS",
			When: map[string]string{"level": "error"}},
	}, nil, nil)
	require.NoError(t, err)
	require.Len(t, rules, 1)
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 17 — ClassifyChanges: rules and overrides are soft changes
// ═══════════════════════════════════════════════════════════════════════════

func TestPBR_ClassifyChanges_RulesAdded_SoftChange(t *testing.T) {
	prev := baseCfg()
	curr := baseCfg()
	curr.Rules = []config.RuleConfig{
		{Name: "new-rule", Priority: 200, Match: "log", SetState: "FAILING",
			When: map[string]string{"level": "error"}},
	}
	cs := stack.ClassifyChanges(prev, curr)
	assert.True(t, cs.HasSoft)
	assert.False(t, cs.HasHard)
}

func TestPBR_ClassifyChanges_RulesRemoved_SoftChange(t *testing.T) {
	prev := baseCfg()
	prev.Rules = []config.RuleConfig{
		{Name: "old-rule", Priority: 200, Match: "log", SetState: "FAILING",
			When: map[string]string{"level": "error"}},
	}
	curr := baseCfg()
	cs := stack.ClassifyChanges(prev, curr)
	assert.True(t, cs.HasSoft)
	assert.False(t, cs.HasHard)
}

func TestPBR_ClassifyChanges_RuleModified_SoftChange(t *testing.T) {
	prev := baseCfg()
	prev.Rules = []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "log", SetState: "HAS_ERRORS",
			When: map[string]string{"level": "error"}},
	}
	curr := baseCfg()
	curr.Rules = []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "log", SetState: "FAILING", // SetState changed
			When: map[string]string{"level": "error"}},
	}
	cs := stack.ClassifyChanges(prev, curr)
	assert.True(t, cs.HasSoft)
	assert.False(t, cs.HasHard)
}

func TestPBR_ClassifyChanges_ContainerOverridesAdded_SoftChange(t *testing.T) {
	prev := baseCfg()
	curr := baseCfg()
	curr.ContainerOverrides = map[string][]config.RuleConfig{
		"svc": {{Name: "svc-ok", Priority: 500, Match: "log", SetState: "OK",
			When: map[string]string{"level": "error"}}},
	}
	cs := stack.ClassifyChanges(prev, curr)
	assert.True(t, cs.HasSoft)
	assert.False(t, cs.HasHard)
}

func TestPBR_ClassifyChanges_ContainerOverridesRemoved_SoftChange(t *testing.T) {
	prev := baseCfg()
	prev.ContainerOverrides = map[string][]config.RuleConfig{
		"svc": {{Name: "svc-ok", Priority: 500, Match: "log", SetState: "OK",
			When: map[string]string{"level": "error"}}},
	}
	curr := baseCfg()
	cs := stack.ClassifyChanges(prev, curr)
	assert.True(t, cs.HasSoft)
	assert.False(t, cs.HasHard)
}

func TestPBR_ClassifyChanges_RulesUnchanged_NoChange(t *testing.T) {
	prev := baseCfg()
	prev.Rules = []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "log", SetState: "FAILING",
			When: map[string]string{"level": "error"}},
	}
	curr := baseCfg()
	curr.Rules = []config.RuleConfig{
		{Name: "r", Priority: 200, Match: "log", SetState: "FAILING",
			When: map[string]string{"level": "error"}},
	}
	cs := stack.ClassifyChanges(prev, curr)
	assert.False(t, cs.HasSoft, "identical rules must not produce a soft change")
	assert.False(t, cs.HasHard)
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 18 — onChange callback contract
// ═══════════════════════════════════════════════════════════════════════════

func TestPBR_OnChange_CalledWhenStateChanges(t *testing.T) {
	e, ch := newEngineWithCallback(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "boom")})
	select {
	case snap := <-ch:
		assert.Contains(t, snap.Containers, "svc")
	case <-time.After(2 * time.Second):
		t.Fatal("onChange must be called when an error event creates a new snapshot entry")
	}
}

func TestPBR_OnChange_NotCalledForInfoOnlyBatch(t *testing.T) {
	e, ch := newEngineWithCallback(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "info", "heartbeat")})
	select {
	case <-ch:
		t.Fatal("onChange must NOT be called for an info-only batch")
	case <-time.After(50 * time.Millisecond):
		// expected — no notification
	}
}

func TestPBR_OnChange_NotCalledForOKRuleMatch(t *testing.T) {
	rules := mustLoad(t, []config.RuleConfig{
		{Name: "suppress", Priority: 200, Match: "log",
			When: map[string]string{"level": "error"}, SetState: "OK"},
	}, nil, nil)
	e, ch := newEngineWithCallback(t, rules)
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "suppressed")})
	select {
	case <-ch:
		t.Fatal("onChange must NOT be called when the PBR result is OK (no snapshot update)")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPBR_OnChange_CallbackReceivesDeepCopy(t *testing.T) {
	e, ch := newEngineWithCallback(t, mustLoad(t, nil, nil, pbr.BuiltinRules()))
	e.ProcessBatch([]ingest.LogEvent{logEvent("svc", "error", "boom")})
	snap := <-ch
	// Mutating the received snapshot must not affect the engine's internal state.
	snap.Containers["injected"] = health.ContainerHealth{Name: "injected"}
	assert.NotContains(t, e.Snapshot().Containers, "injected",
		"onChange snapshot must be a deep copy independent from the engine's state")
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 19 — User rule replacing builtin by name
// ═══════════════════════════════════════════════════════════════════════════

func TestPBR_UserRule_SameNameAsBuiltin_ReplacesBuiltin(t *testing.T) {
	// Override builtin-log-error by using its exact name at a different priority/state.
	rules, err := pbr.Load([]config.RuleConfig{
		{Name: "builtin-log-error", Priority: 100, Match: "log",
			When: map[string]string{"level": "error"}, SetState: "FAILING"},
	}, nil, pbr.BuiltinRules())
	require.NoError(t, err)

	count := 0
	for _, r := range rules {
		if r.Name == "builtin-log-error" {
			count++
			assert.Equal(t, "FAILING", r.SetState,
				"user rule with same name must replace the builtin")
		}
	}
	assert.Equal(t, 1, count, "builtin-log-error must appear exactly once")
}

func TestPBR_UserRule_SameNameAsBuiltin_AllOtherBuiltinsPreserved(t *testing.T) {
	rules, err := pbr.Load([]config.RuleConfig{
		{Name: "builtin-log-warn", Priority: 90, Match: "log",
			When: map[string]string{"level": "warn"}, SetState: "FAILING"},
	}, nil, pbr.BuiltinRules())
	require.NoError(t, err)

	names := make(map[string]bool)
	for _, r := range rules {
		names[r.Name] = true
	}
	assert.True(t, names["builtin-failing"], "builtin-failing must still be present")
	assert.True(t, names["builtin-log-error"], "builtin-log-error must still be present")
	assert.True(t, names["builtin-k8s-restarting"], "builtin-k8s-restarting must still be present")
}

// ═══════════════════════════════════════════════════════════════════════════
// Section 20 — Catch-all rule (no conditions on a plane)
// ═══════════════════════════════════════════════════════════════════════════

func TestPBR_EmptyConditions_MatchesAllLogEvents(t *testing.T) {
	// A rule with no when conditions must match every event on the correct plane.
	rules, err := pbr.Load([]config.RuleConfig{
		{Name: "catch-all-log", Priority: 1, Match: "log", SetState: "HAS_ERRORS"},
	}, nil, nil)
	require.NoError(t, err)

	for _, level := range []string{"info", "debug", "trace", "custom"} {
		result := pbr.Evaluate(rules, logCtxE2E(level, "any message", "", "", "", 0, 0))
		assert.Equalf(t, "HAS_ERRORS", result.State,
			"catch-all log rule must match level=%q", level)
	}
}

func TestPBR_EmptyConditions_MatchesAllInfraContainers(t *testing.T) {
	rules, err := pbr.Load([]config.RuleConfig{
		{Name: "catch-all-infra", Priority: 1, Match: "infra", SetState: "DEGRADED"},
	}, nil, nil)
	require.NoError(t, err)

	ctx := infraCtxE2E("any", "any", "docker", 0, time.Hour, "")
	result := pbr.Evaluate(rules, ctx)
	assert.Equal(t, "DEGRADED", result.State)
}

func TestPBR_EmptyConditions_NotTriggeredByWrongPlane(t *testing.T) {
	// Catch-all log rule must not fire on infra context and vice versa.
	logRule, _ := pbr.Load([]config.RuleConfig{
		{Name: "catch-all-log", Priority: 1, Match: "log", SetState: "HAS_ERRORS"},
	}, nil, nil)
	infraRule, _ := pbr.Load([]config.RuleConfig{
		{Name: "catch-all-infra", Priority: 1, Match: "infra", SetState: "DEGRADED"},
	}, nil, nil)

	infraCtxVal := infraCtxE2E("svc", "", "k8s", 0, time.Minute, "")
	logCtxVal := logCtxE2E("info", "msg", "", "", "", 0, 0)

	assert.Equal(t, "", pbr.Evaluate(logRule, infraCtxVal).State)
	assert.Equal(t, "", pbr.Evaluate(infraRule, logCtxVal).State)
}

