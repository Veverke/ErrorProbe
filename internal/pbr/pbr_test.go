package pbr

import (
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/ingest"
)

// ─── Helpers ────────────────────────────────────────────────────────────────

func logCtx(level, message string) EvalContext {
	return EvalContext{Log: &LogEvalContext{Event: ingest.LogEvent{Level: level, Message: message}}}
}

func logCtxFull(level, message, container, namespace, runtime string, count int, window time.Duration) EvalContext {
	return EvalContext{Log: &LogEvalContext{
		Event: ingest.LogEvent{
			Level:     level,
			Message:   message,
			Container: container,
			Namespace: namespace,
			Runtime:   runtime,
		},
		CountInWindow: count,
		Window:        window,
	}}
}

func infraCtx(name, namespace, runtime string, restartCount int, uptime time.Duration, phase string) EvalContext {
	return EvalContext{Infra: &InfraContainer{
		Name:         name,
		Namespace:    namespace,
		Runtime:      runtime,
		RestartCount: restartCount,
		Uptime:       uptime,
		Phase:        phase,
	}}
}

func cond(field string, op Operator, value string) Condition {
	return Condition{Field: field, Operator: op, Value: value}
}

func condNum(field string, op Operator, value string, num float64) Condition {
	return Condition{Field: field, Operator: op, Value: value, NumericValue: num}
}

// ─── EvalCondition ───────────────────────────────────────────────────────────

func TestEvalCondition_OpEq_String(t *testing.T) {
	c := cond("level", OpEq, "error")
	assert.True(t, EvalCondition(c, logCtx("error", "")))
	assert.False(t, EvalCondition(c, logCtx("warn", "")))
}

func TestEvalCondition_OpEq_Duration(t *testing.T) {
	// "5m" and "5m0s" are equal durations.
	c := cond("uptime", OpEq, "5m0s")
	ctx := infraCtx("web", "", "k8s", 0, 5*time.Minute, "Running")
	assert.True(t, EvalCondition(c, ctx))

	c2 := cond("uptime", OpEq, "10m")
	assert.False(t, EvalCondition(c2, ctx))
}

func TestEvalCondition_OpGt_Int(t *testing.T) {
	c := condNum("count_in_window", OpGt, "3", 3)
	assert.True(t, EvalCondition(c, logCtxFull("error", "", "", "", "", 4, time.Minute)))
	assert.False(t, EvalCondition(c, logCtxFull("error", "", "", "", "", 3, time.Minute)))
}

func TestEvalCondition_OpLt_Int(t *testing.T) {
	c := condNum("restart_count", OpLt, "5", 5)
	ctx := infraCtx("db", "", "k8s", 2, time.Minute, "Running")
	assert.True(t, EvalCondition(c, ctx))
	ctx2 := infraCtx("db", "", "k8s", 5, time.Minute, "Running")
	assert.False(t, EvalCondition(c, ctx2))
}

func TestEvalCondition_OpGte_Int(t *testing.T) {
	c := condNum("restart_count", OpGte, "1", 1)
	ctx := infraCtx("db", "", "k8s", 1, time.Minute, "Running")
	assert.True(t, EvalCondition(c, ctx))
	ctx0 := infraCtx("db", "", "k8s", 0, time.Minute, "Running")
	assert.False(t, EvalCondition(c, ctx0))
}

func TestEvalCondition_OpLte_Int(t *testing.T) {
	c := condNum("restart_count", OpLte, "2", 2)
	ctx := infraCtx("db", "", "k8s", 2, time.Minute, "Running")
	assert.True(t, EvalCondition(c, ctx))
	ctx3 := infraCtx("db", "", "k8s", 3, time.Minute, "Running")
	assert.False(t, EvalCondition(c, ctx3))
}

func TestEvalCondition_OpGt_Duration(t *testing.T) {
	// uptime > 2m (120s = 120.0)
	c := condNum("uptime", OpGt, "2m", 120)
	ctx := infraCtx("web", "", "k8s", 0, 3*time.Minute, "Running")
	assert.True(t, EvalCondition(c, ctx))
	ctx2 := infraCtx("web", "", "k8s", 0, 90*time.Second, "Running")
	assert.False(t, EvalCondition(c, ctx2))
}

func TestEvalCondition_OpLt_Duration(t *testing.T) {
	c := condNum("uptime", OpLt, "2m", 120)
	ctx := infraCtx("web", "", "k8s", 0, 90*time.Second, "Running")
	assert.True(t, EvalCondition(c, ctx))
	ctx2 := infraCtx("web", "", "k8s", 0, 3*time.Minute, "Running")
	assert.False(t, EvalCondition(c, ctx2))
}

func TestEvalCondition_OpRegex(t *testing.T) {
	re := regexp.MustCompile(`(?i)panic`)
	c := Condition{Field: "message", Operator: OpRegex, CompiledRegex: re}
	assert.True(t, EvalCondition(c, logCtx("error", "PANIC: nil pointer")))
	assert.False(t, EvalCondition(c, logCtx("info", "all good")))
}

func TestEvalCondition_OpRegex_NilPattern(t *testing.T) {
	c := Condition{Field: "message", Operator: OpRegex, CompiledRegex: nil}
	assert.False(t, EvalCondition(c, logCtx("error", "panic")))
}

func TestEvalCondition_OpGlob(t *testing.T) {
	c := cond("container", OpGlob, "web-*")
	assert.True(t, EvalCondition(c, logCtxFull("error", "", "web-server", "", "", 0, 0)))
	assert.False(t, EvalCondition(c, logCtxFull("error", "", "db-primary", "", "", 0, 0)))
}

func TestEvalCondition_UnknownField_ReturnsFalse(t *testing.T) {
	c := cond("nonexistent_field", OpEq, "value")
	assert.False(t, EvalCondition(c, logCtx("error", "")))
}

func TestEvalCondition_NilContext_ReturnsFalse(t *testing.T) {
	c := cond("level", OpEq, "error")
	assert.False(t, EvalCondition(c, EvalContext{}))
}

func TestEvalCondition_InfraFieldNotAvailableInLog(t *testing.T) {
	c := cond("restart_count", OpEq, "1")
	assert.False(t, EvalCondition(c, logCtx("error", "")))
}

func TestEvalCondition_LogFieldNotAvailableInInfra(t *testing.T) {
	c := cond("level", OpEq, "error")
	assert.False(t, EvalCondition(c, infraCtx("web", "", "k8s", 0, time.Minute, "Running")))
}

// ─── fieldValue ──────────────────────────────────────────────────────────────

func TestFieldValue_LogFields(t *testing.T) {
	ctx := logCtxFull("WARN", "hello world", "api", "prod", "docker", 7, 5*time.Minute)

	tests := []struct {
		field    string
		expected string
	}{
		{"level", "warn"},   // lowercased
		{"message", "hello world"},
		{"container", "api"},
		{"namespace", "prod"},
		{"runtime", "docker"},
		{"count_in_window", "7"},
		{"window", (5 * time.Minute).String()},
	}
	for _, tt := range tests {
		got, ok := FieldValueForTest(tt.field, ctx)
		assert.True(t, ok, "field %s should exist", tt.field)
		assert.Equal(t, tt.expected, got, "field %s", tt.field)
	}
}

func TestFieldValue_InfraFields(t *testing.T) {
	ctx := infraCtx("db", "staging", "k8s", 3, 2*time.Minute, "Running")

	tests := []struct {
		field    string
		expected string
	}{
		{"container", "db"},
		{"namespace", "staging"},
		{"runtime", "k8s"},
		{"restart_count", "3"},
		{"uptime", (2 * time.Minute).String()},
		{"phase", "Running"},
	}
	for _, tt := range tests {
		got, ok := FieldValueForTest(tt.field, ctx)
		assert.True(t, ok, "field %s should exist", tt.field)
		assert.Equal(t, tt.expected, got, "field %s", tt.field)
	}
}

// ─── Evaluate ────────────────────────────────────────────────────────────────

func TestEvaluate_FirstMatchWins(t *testing.T) {
	rules := []Rule{
		{Name: "high", Priority: 200, Match: MatchLog, SetState: "FAILING",
			Conditions: []Condition{cond("level", OpEq, "error")}},
		{Name: "low", Priority: 100, Match: MatchLog, SetState: "HAS_ERRORS",
			Conditions: []Condition{cond("level", OpEq, "error")}},
	}
	result := Evaluate(rules, logCtx("error", ""))
	assert.Equal(t, "FAILING", result.State)
	assert.Equal(t, "high", result.MatchedRule)
}

func TestEvaluate_NoMatch_ReturnsEmpty(t *testing.T) {
	rules := []Rule{
		{Name: "r1", Priority: 100, Match: MatchLog, SetState: "HAS_ERRORS",
			Conditions: []Condition{cond("level", OpEq, "error")}},
	}
	result := Evaluate(rules, logCtx("info", ""))
	assert.Equal(t, "", result.State)
	assert.Equal(t, "", result.MatchedRule)
}

func TestEvaluate_SkipsWrongPlane(t *testing.T) {
	// Infra rule should not fire on log context.
	rules := []Rule{
		{Name: "infra-rule", Priority: 100, Match: MatchInfra, SetState: "RESTARTING",
			Conditions: []Condition{condNum("restart_count", OpGt, "0", 0)}},
	}
	result := Evaluate(rules, logCtx("error", ""))
	assert.Equal(t, "", result.State)
}

func TestEvaluate_InfraRestarting(t *testing.T) {
	rules := []Rule{
		{Name: "restarting", Priority: 100, Match: MatchInfra, SetState: "RESTARTING",
			Conditions: []Condition{
				cond("runtime", OpEq, "k8s"),
				condNum("restart_count", OpGt, "0", 0),
				condNum("uptime", OpLt, "2m", 120),
			}},
	}
	ctx := infraCtx("svc", "", "k8s", 1, 90*time.Second, "Running")
	result := Evaluate(rules, ctx)
	assert.Equal(t, "RESTARTING", result.State)
	assert.Equal(t, "restarting", result.MatchedRule)
}

func TestEvaluate_InfraNotRestarting_UptimeTooLong(t *testing.T) {
	rules := []Rule{
		{Name: "restarting", Priority: 100, Match: MatchInfra, SetState: "RESTARTING",
			Conditions: []Condition{
				cond("runtime", OpEq, "k8s"),
				condNum("restart_count", OpGt, "0", 0),
				condNum("uptime", OpLt, "2m", 120),
			}},
	}
	ctx := infraCtx("svc", "", "k8s", 1, 5*time.Minute, "Running")
	result := Evaluate(rules, ctx)
	assert.Equal(t, "", result.State)
}

func TestEvaluate_EmptyRules_ReturnsEmpty(t *testing.T) {
	result := Evaluate(nil, logCtx("error", ""))
	assert.Equal(t, "", result.State)
}

func TestEvaluate_AllConditionsMustMatch(t *testing.T) {
	rules := []Rule{
		{Name: "r1", Priority: 100, Match: MatchLog, SetState: "FAILING",
			Conditions: []Condition{
				cond("level", OpEq, "error"),
				condNum("count_in_window", OpGte, "5", 5),
			}},
	}
	// Only level matches, count does not — no match.
	assert.Equal(t, "", Evaluate(rules, logCtxFull("error", "", "", "", "", 4, time.Minute)).State)
	// Both match.
	assert.Equal(t, "FAILING", Evaluate(rules, logCtxFull("error", "", "", "", "", 5, time.Minute)).State)
}

func TestEvaluate_MatchedPattern_LevelOnly(t *testing.T) {
	// Level-only rule has no message condition → MatchedPattern must be empty.
	rules := []Rule{
		{Name: "r", Priority: 100, Match: MatchLog, SetState: "HAS_ERRORS",
			Conditions: []Condition{cond("level", OpEq, "error")}},
	}
	result := Evaluate(rules, logCtx("error", "connection refused"))
	assert.Equal(t, "HAS_ERRORS", result.State)
	assert.Equal(t, "", result.MatchedPattern)
}

func TestEvaluate_MatchedPattern_Eq(t *testing.T) {
	// Rule with an exact-match message condition → MatchedPattern is the condition value.
	rules := []Rule{
		{Name: "r", Priority: 100, Match: MatchLog, SetState: "HAS_ERRORS",
			Conditions: []Condition{
				cond("level", OpEq, "error"),
				cond("message", OpEq, "connection refused"),
			}},
	}
	result := Evaluate(rules, logCtx("error", "connection refused"))
	assert.Equal(t, "HAS_ERRORS", result.State)
	assert.Equal(t, "connection refused", result.MatchedPattern)
}

func TestEvaluate_MatchedPattern_Regex(t *testing.T) {
	// Rule with a regex message condition → MatchedPattern is the matched substring.
	re := regexp.MustCompile(`timeout|refused`)
	rules := []Rule{
		{Name: "r", Priority: 100, Match: MatchLog, SetState: "HAS_ERRORS",
			Conditions: []Condition{
				cond("level", OpEq, "error"),
				{Field: "message", Operator: OpRegex, Value: `timeout|refused`, CompiledRegex: re},
			}},
	}
	result := Evaluate(rules, logCtx("error", "dial tcp: connection refused after 30s"))
	assert.Equal(t, "HAS_ERRORS", result.State)
	assert.Equal(t, "refused", result.MatchedPattern)
}

func TestEvaluate_MatchedPattern_Glob(t *testing.T) {
	// Rule with a glob message condition → MatchedPattern is the glob pattern itself.
	rules := []Rule{
		{Name: "r", Priority: 100, Match: MatchLog, SetState: "HAS_ERRORS",
			Conditions: []Condition{
				cond("level", OpEq, "error"),
				cond("message", OpGlob, "*refused*"),
			}},
	}
	result := Evaluate(rules, logCtx("error", "connection refused"))
	assert.Equal(t, "HAS_ERRORS", result.State)
	assert.Equal(t, "*refused*", result.MatchedPattern)
}

// ─── Load ────────────────────────────────────────────────────────────────────

func ruleConfig(name string, priority int, match, state string, when map[string]string) config.RuleConfig {
	return config.RuleConfig{Name: name, Priority: priority, Match: match, SetState: state, When: when}
}

func TestLoad_ValidRule_SortedDescending(t *testing.T) {
	cfgs := []config.RuleConfig{
		ruleConfig("low", 90, "log", "HAS_ERRORS", map[string]string{"level": "warn"}),
		ruleConfig("high", 150, "log", "FAILING", map[string]string{"level": "error"}),
	}
	rules, err := Load(cfgs, nil, nil)
	require.NoError(t, err)
	require.Len(t, rules, 2)
	assert.Equal(t, 150, rules[0].Priority)
	assert.Equal(t, 90, rules[1].Priority)
}

func TestLoad_DuplicatePriority_ReturnsError(t *testing.T) {
	cfgs := []config.RuleConfig{
		ruleConfig("rule-a", 100, "log", "HAS_ERRORS", map[string]string{"level": "error"}),
		ruleConfig("rule-b", 100, "log", "HAS_ERRORS", map[string]string{"level": "warn"}),
	}
	_, err := Load(cfgs, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rule-a")
	assert.Contains(t, err.Error(), "rule-b")
}

func TestLoad_EmptyName_ReturnsError(t *testing.T) {
	cfgs := []config.RuleConfig{
		ruleConfig("", 100, "log", "HAS_ERRORS", nil),
	}
	_, err := Load(cfgs, nil, nil)
	require.Error(t, err)
}

func TestLoad_InvalidMatchContext_ReturnsError(t *testing.T) {
	cfgs := []config.RuleConfig{
		ruleConfig("r1", 100, "network", "HAS_ERRORS", nil),
	}
	_, err := Load(cfgs, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "r1")
}

func TestLoad_InvalidSetState_ReturnsError(t *testing.T) {
	cfgs := []config.RuleConfig{
		ruleConfig("r1", 100, "log", "BROKEN", nil),
	}
	_, err := Load(cfgs, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "r1")
}

func TestLoad_UnknownFieldForPlane_ReturnsError(t *testing.T) {
	// restart_count is an infra field, not a log field
	cfgs := []config.RuleConfig{
		ruleConfig("r1", 100, "log", "HAS_ERRORS", map[string]string{"restart_count": "1"}),
	}
	_, err := Load(cfgs, nil, nil)
	require.Error(t, err)
}

func TestLoad_RegexOperator_CompiledCorrectly(t *testing.T) {
	cfgs := []config.RuleConfig{
		ruleConfig("regex-rule", 100, "log", "HAS_ERRORS", map[string]string{"message": "~panic.*"}),
	}
	rules, err := Load(cfgs, nil, nil)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	require.Len(t, rules[0].Conditions, 1)
	c := rules[0].Conditions[0]
	assert.Equal(t, OpRegex, c.Operator)
	assert.NotNil(t, c.CompiledRegex)
	assert.True(t, c.CompiledRegex.MatchString("panic: nil pointer"))
}

func TestLoad_NumericComparisonOperators(t *testing.T) {
	cfgs := []config.RuleConfig{
		ruleConfig("r1", 100, "log", "FAILING", map[string]string{"count_in_window": ">= 5"}),
	}
	rules, err := Load(cfgs, nil, nil)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	c := rules[0].Conditions[0]
	assert.Equal(t, OpGte, c.Operator)
	assert.InDelta(t, 5.0, c.NumericValue, 0.001)
}

func TestLoad_DurationComparison(t *testing.T) {
	cfgs := []config.RuleConfig{
		ruleConfig("r1", 100, "infra", "RESTARTING", map[string]string{"uptime": "< 2m"}),
	}
	rules, err := Load(cfgs, nil, nil)
	require.NoError(t, err)
	c := rules[0].Conditions[0]
	assert.Equal(t, OpLt, c.Operator)
	assert.InDelta(t, (2 * time.Minute).Seconds(), c.NumericValue, 0.001)
}

func TestLoad_LevelLowercased(t *testing.T) {
	cfgs := []config.RuleConfig{
		ruleConfig("r1", 100, "log", "HAS_ERRORS", map[string]string{"level": "ERROR"}),
	}
	rules, err := Load(cfgs, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "error", rules[0].Conditions[0].Value)
}

// ─── Load – Builtins ─────────────────────────────────────────────────────────

func TestLoad_MergesBuiltins(t *testing.T) {
	rules, err := Load(nil, nil, BuiltinRules())
	require.NoError(t, err)
	names := make(map[string]bool)
	for _, r := range rules {
		names[r.Name] = true
	}
	assert.True(t, names["builtin-failing"])
	assert.True(t, names["builtin-log-error"])
	assert.True(t, names["builtin-log-warn"])
	assert.True(t, names["builtin-k8s-restarting"])
}

func TestLoad_UserRuleWithSameNameSkipsBuiltin(t *testing.T) {
	cfgs := []config.RuleConfig{
		ruleConfig("builtin-log-error", 100, "log", "FAILING",
			map[string]string{"level": "error"}),
	}
	rules, err := Load(cfgs, nil, BuiltinRules())
	require.NoError(t, err)
	count := 0
	for _, r := range rules {
		if r.Name == "builtin-log-error" {
			count++
			// Should be the user's version with FAILING, not builtin's HAS_ERRORS.
			assert.Equal(t, "FAILING", r.SetState)
		}
	}
	assert.Equal(t, 1, count)
}

// ─── Load – Container Overrides ──────────────────────────────────────────────

func TestLoad_ContainerOverride_InjectsContainerCondition(t *testing.T) {
	overrides := map[string][]config.RuleConfig{
		"myapp": {
			ruleConfig("myapp-rule", 200, "log", "OK", map[string]string{"level": "error"}),
		},
	}
	rules, err := Load(nil, overrides, nil)
	require.NoError(t, err)
	require.Len(t, rules, 1)

	// Find the container condition.
	var containerCond *Condition
	for i := range rules[0].Conditions {
		if rules[0].Conditions[i].Field == "container" {
			containerCond = &rules[0].Conditions[i]
		}
	}
	require.NotNil(t, containerCond, "container condition must be injected")
	assert.Equal(t, OpEq, containerCond.Operator)
	assert.Equal(t, "myapp", containerCond.Value)
}

func TestLoad_ContainerOverride_OnlyFiresForNamedContainer(t *testing.T) {
	overrides := map[string][]config.RuleConfig{
		"web": {
			ruleConfig("web-suppress", 500, "log", "OK", map[string]string{"level": "error"}),
		},
	}
	rules, err := Load(nil, overrides, BuiltinRules())
	require.NoError(t, err)

	// Should return OK for "web" container even though level=error
	result := Evaluate(rules, logCtxFull("error", "oops", "web", "", "", 0, 0))
	assert.Equal(t, "OK", result.State)
	assert.Equal(t, "web-suppress", result.MatchedRule)

	// Should fire builtin for different container
	result2 := Evaluate(rules, logCtxFull("error", "oops", "api", "", "", 0, 0))
	assert.Equal(t, "HAS_ERRORS", result2.State)
}

func TestLoad_ContainerOverride_DuplicatePriorityError(t *testing.T) {
	// Two override rules for same container with same priority
	overrides := map[string][]config.RuleConfig{
		"web": {
			ruleConfig("web-rule-a", 100, "log", "HAS_ERRORS", map[string]string{"level": "error"}),
			ruleConfig("web-rule-b", 100, "log", "OK", map[string]string{"level": "warn"}),
		},
	}
	_, err := Load(nil, overrides, nil)
	require.Error(t, err)
}

// ─── BuiltinRules ────────────────────────────────────────────────────────────

func TestBuiltinRules_Count(t *testing.T) {
	rules := BuiltinRules()
	assert.Len(t, rules, 4)
}

func TestBuiltinRules_Priorities(t *testing.T) {
	rules := BuiltinRules()
	priorities := make(map[string]int)
	for _, r := range rules {
		priorities[r.Name] = r.Priority
	}
	assert.Equal(t, 110, priorities["builtin-failing"])
	assert.Equal(t, 100, priorities["builtin-log-error"])
	assert.Equal(t, 90, priorities["builtin-log-warn"])
	assert.Equal(t, 100, priorities["builtin-k8s-restarting"])
}

func TestBuiltinRules_MatchContexts(t *testing.T) {
	rules := BuiltinRules()
	for _, r := range rules {
		switch r.Name {
		case "builtin-failing", "builtin-log-error", "builtin-log-warn":
			assert.Equal(t, MatchLog, r.Match, "rule %s should be log plane", r.Name)
		case "builtin-k8s-restarting":
			assert.Equal(t, MatchInfra, r.Match, "rule %s should be infra plane", r.Name)
		}
	}
}

func TestBuiltinRules_LogError_FiresOnError(t *testing.T) {
	rules, err := Load(nil, nil, BuiltinRules())
	require.NoError(t, err)

	result := Evaluate(rules, logCtx("error", "something failed"))
	assert.Equal(t, "HAS_ERRORS", result.State)
	// builtin-failing requires count_in_window >= 5 too, so with count=0 should hit builtin-log-error
	assert.Equal(t, "builtin-log-error", result.MatchedRule)
}

func TestBuiltinRules_LogWarn_FiresOnWarn(t *testing.T) {
	rules, err := Load(nil, nil, BuiltinRules())
	require.NoError(t, err)

	result := Evaluate(rules, logCtx("warn", "deprecated api"))
	assert.Equal(t, "HAS_WARNINGS", result.State)
	assert.Equal(t, "builtin-log-warn", result.MatchedRule)
}

func TestBuiltinRules_Failing_FiresWhenCountHigh(t *testing.T) {
	rules, err := Load(nil, nil, BuiltinRules())
	require.NoError(t, err)

	result := Evaluate(rules, logCtxFull("error", "db down", "", "", "", 5, time.Minute))
	assert.Equal(t, "FAILING", result.State)
	assert.Equal(t, "builtin-failing", result.MatchedRule)
}

func TestBuiltinRules_K8sRestarting_WithinWindow(t *testing.T) {
	rules, err := Load(nil, nil, BuiltinRules())
	require.NoError(t, err)

	ctx := infraCtx("worker", "", "k8s", 1, 90*time.Second, "Running")
	result := Evaluate(rules, ctx)
	assert.Equal(t, "RESTARTING", result.State)
	assert.Equal(t, "builtin-k8s-restarting", result.MatchedRule)
}

func TestBuiltinRules_K8sRestarting_NotDockerRuntime(t *testing.T) {
	rules, err := Load(nil, nil, BuiltinRules())
	require.NoError(t, err)

	// Docker container with restart — should not trigger builtin-k8s-restarting
	ctx := infraCtx("worker", "", "docker", 1, 90*time.Second, "Running")
	result := Evaluate(rules, ctx)
	assert.Equal(t, "", result.State)
}

func TestBuiltinRules_InfoLevel_NoMatch(t *testing.T) {
	rules, err := Load(nil, nil, BuiltinRules())
	require.NoError(t, err)

	result := Evaluate(rules, logCtx("info", "started"))
	assert.Equal(t, "", result.State)
}

// ─── validateUniquePriority ──────────────────────────────────────────────────

func TestValidateUniquePriority_NoDuplicates_OK(t *testing.T) {
	rules := []Rule{
		{Name: "a", Priority: 100},
		{Name: "b", Priority: 200},
	}
	err := ValidateUniquePriorityForTest(rules)
	assert.NoError(t, err)
}

func TestValidateUniquePriority_Duplicate_Error(t *testing.T) {
	rules := []Rule{
		{Name: "a", Priority: 100},
		{Name: "b", Priority: 100},
	}
	err := ValidateUniquePriorityForTest(rules)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a")
	assert.Contains(t, err.Error(), "b")
}

// ─── ParseConditionForTest ───────────────────────────────────────────────────

func TestParseCondition_GtOperator(t *testing.T) {
	c, err := ParseConditionForTest("test-rule", "count_in_window", "> 10")
	require.NoError(t, err)
	assert.Equal(t, OpGt, c.Operator)
	assert.InDelta(t, 10.0, c.NumericValue, 0.001)
}

func TestParseCondition_LtOperator(t *testing.T) {
	c, err := ParseConditionForTest("test-rule", "uptime", "< 2m")
	require.NoError(t, err)
	assert.Equal(t, OpLt, c.Operator)
	assert.InDelta(t, (2 * time.Minute).Seconds(), c.NumericValue, 0.001)
}

func TestParseCondition_GteOperator(t *testing.T) {
	c, err := ParseConditionForTest("test-rule", "restart_count", ">= 3")
	require.NoError(t, err)
	assert.Equal(t, OpGte, c.Operator)
	assert.InDelta(t, 3.0, c.NumericValue, 0.001)
}

func TestParseCondition_LteOperator(t *testing.T) {
	c, err := ParseConditionForTest("test-rule", "restart_count", "<= 2")
	require.NoError(t, err)
	assert.Equal(t, OpLte, c.Operator)
	assert.InDelta(t, 2.0, c.NumericValue, 0.001)
}

func TestParseCondition_EqOperator_StringField(t *testing.T) {
	c, err := ParseConditionForTest("test-rule", "level", "error")
	require.NoError(t, err)
	assert.Equal(t, OpEq, c.Operator)
	assert.Equal(t, "error", c.Value)
}

func TestParseCondition_RegexOperator(t *testing.T) {
	c, err := ParseConditionForTest("test-rule", "message", "~timeout.*")
	require.NoError(t, err)
	assert.Equal(t, OpRegex, c.Operator)
	assert.NotNil(t, c.CompiledRegex)
}

func TestParseCondition_InvalidRegex_Error(t *testing.T) {
	_, err := ParseConditionForTest("test-rule", "message", "~[invalid")
	require.Error(t, err)
}

func TestParseCondition_GlobOperator_Wildcard(t *testing.T) {
	c, err := ParseConditionForTest("test-rule", "container", "web-*")
	require.NoError(t, err)
	assert.Equal(t, OpGlob, c.Operator)
	assert.Equal(t, "web-*", c.Value)
}

func TestParseCondition_GlobOperator_QuestionMark(t *testing.T) {
	c, err := ParseConditionForTest("test-rule", "container", "svc-?")
	require.NoError(t, err)
	assert.Equal(t, OpGlob, c.Operator)
}

func TestParseCondition_GlobOperator_CharClass(t *testing.T) {
	c, err := ParseConditionForTest("test-rule", "container", "[abc]-svc")
	require.NoError(t, err)
	assert.Equal(t, OpGlob, c.Operator)
}

func TestEvalCondition_DurationEq_ParseError_ReturnsFalse(t *testing.T) {
	// "uptime" is a duration field; passing a non-duration value makes both parses fail.
	c := Condition{Field: "uptime", Operator: OpEq, Value: "not-a-duration"}
	ctx := infraCtx("svc", "", "docker", 0, 5*time.Minute, "running")
	result := EvalCondition(c, ctx)
	assert.False(t, result)
}

func TestEvalCondition_GlobInvalidPattern_ReturnsFalse(t *testing.T) {
	// filepath.Match returns an error on an invalid glob pattern.
	c := Condition{Field: "container", Operator: OpGlob, Value: "[invalid"}
	result := EvalCondition(c, logCtx("error", "msg"))
	assert.False(t, result)
}

func TestContextMatchesPlane_UnknownPlane_ReturnsFalse(t *testing.T) {
	// Pass an unknown MatchContext value — hits the default return false branch.
	result := contextMatchesPlane(MatchContext("unknown"), logCtx("error", "msg"))
	assert.False(t, result)
}

func TestNumericOrDuration_DurationParseError_ReturnsFalse(t *testing.T) {
	// "uptime" is a duration field; an unparseable value hits the error branch.
	_, ok := numericOrDuration("not-a-duration", "uptime")
	assert.False(t, ok)
}

func TestNumericOrDuration_IntParseError_ReturnsFalse(t *testing.T) {
	// Non-duration field (restart_count) with non-integer value.
	_, ok := numericOrDuration("abc", "restart_count")
	assert.False(t, ok)
}
