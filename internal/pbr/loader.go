package pbr

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/errorprobe/errorprobe/internal/config"
)

// validLogFields is the set of field names allowed in log-plane conditions.
var validLogFields = map[string]bool{
	"level":           true,
	"message":         true,
	"container":       true,
	"namespace":       true,
	"runtime":         true,
	"count_in_window": true,
	"window":          true,
}

// validInfraFields is the set of field names allowed in infra-plane conditions.
var validInfraFields = map[string]bool{
	"container":     true,
	"namespace":     true,
	"runtime":       true,
	"restart_count": true,
	"uptime":        true,
	"phase":         true,
}

// validStates is the set of recognised output states.
var validStates = map[string]bool{
	"OK":         true,
	"HAS_ERRORS": true,
	"FAILING":    true,
	"RESTARTING": true,
	"DEGRADED":   true,
}

// Load parses ruleCfgs into typed Rules, merges them with builtins, validates
// uniqueness of priority, and returns the merged slice sorted in descending
// priority order (highest priority evaluated first).
//
// containerOverrides maps container names to per-container rule configs. Each
// override rule has the container name automatically injected as an additional
// condition so it fires only for that specific container.
//
// On any validation error the function returns nil and an error that names the
// offending rule(s).
func Load(ruleCfgs []config.RuleConfig, containerOverrides map[string][]config.RuleConfig, builtins []Rule) ([]Rule, error) {
	rules := make([]Rule, 0, len(ruleCfgs)+len(builtins))

	// Parse user-supplied rules.
	for _, rc := range ruleCfgs {
		r, err := parseRule(rc)
		if err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}

	// Parse per-container overrides and inject container scope condition.
	for container, overrides := range containerOverrides {
		for _, rc := range overrides {
			r, err := parseRule(rc)
			if err != nil {
				return nil, fmt.Errorf("container_overrides[%s]: %w", container, err)
			}
			// Inject synthetic container equality condition.
			r.Conditions = append(r.Conditions, Condition{
				Field:    "container",
				Operator: OpEq,
				Value:    container,
			})
			rules = append(rules, r)
		}
	}

	// Validate uniqueness across user rules (not against builtins — the caller
	// controls builtins and they are designed to not collide with each other).
	if err := validateUniquePriority(rules); err != nil {
		return nil, err
	}

	// Merge with built-ins. Built-ins are only added when no user rule shares
	// their name (allowing full replacement). Priority uniqueness across builtins
	// vs user rules is intentionally not enforced — builtins sit at well-known
	// low priorities; if a user collides they shadow the builtin.
	userNames := make(map[string]bool, len(rules))
	for _, r := range rules {
		userNames[r.Name] = true
	}
	for _, b := range builtins {
		if !userNames[b.Name] {
			rules = append(rules, b)
		}
	}

	// Sort descending by priority (highest first).
	// SliceStable preserves the relative insertion order of rules with equal
	// priorities: user rules are appended before builtins, so a user rule that
	// shares a priority with a builtin will always evaluate first.
	sort.SliceStable(rules, func(i, j int) bool {
		return rules[i].Priority > rules[j].Priority
	})

	return rules, nil
}

// parseRule converts a config.RuleConfig into a typed Rule.
func parseRule(rc config.RuleConfig) (Rule, error) {
	if rc.Name == "" {
		return Rule{}, fmt.Errorf("rule has empty name")
	}
	match := MatchContext(strings.TrimSpace(rc.Match))
	if match != MatchLog && match != MatchInfra {
		return Rule{}, fmt.Errorf("rule %q: unknown match context %q (must be \"log\" or \"infra\")", rc.Name, rc.Match)
	}
	if rc.SetState == "" {
		return Rule{}, fmt.Errorf("rule %q: set_state is required", rc.Name)
	}
	if !validStates[rc.SetState] {
		return Rule{}, fmt.Errorf("rule %q: unknown set_state %q", rc.Name, rc.SetState)
	}

	conditions, err := parseConditions(rc.Name, match, rc.When)
	if err != nil {
		return Rule{}, err
	}

	return Rule{
		Name:       rc.Name,
		Priority:   rc.Priority,
		Match:      match,
		Conditions: conditions,
		SetState:   rc.SetState,
	}, nil
}

// parseConditions converts the when map into typed Condition values.
func parseConditions(ruleName string, match MatchContext, when map[string]string) ([]Condition, error) {
	allowed := validLogFields
	if match == MatchInfra {
		allowed = validInfraFields
	}

	conditions := make([]Condition, 0, len(when))
	for field, rawValue := range when {
		if !allowed[field] {
			return nil, fmt.Errorf("rule %q: field %q is not valid for match context %q", ruleName, field, match)
		}

		c, err := parseCondition(ruleName, field, rawValue)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, c)
	}
	return conditions, nil
}

// parseCondition parses a single field/value pair into a Condition.
// Value syntax:
//   - plain string        → eq
//   - "> N" / "< N" / ">= N" / "<= N"  → numeric or duration comparison
//   - "~<regex>"          → regex match (value is the regex pattern)
func parseCondition(ruleName, field, rawValue string) (Condition, error) {
	// Regex: prefix "~"
	if strings.HasPrefix(rawValue, "~") {
		pattern := rawValue[1:]
		re, err := compileRegex(pattern)
		if err != nil {
			return Condition{}, fmt.Errorf("rule %q field %q: invalid regex %q: %w", ruleName, field, pattern, err)
		}
		return Condition{
			Field:         field,
			Operator:      OpRegex,
			Value:         rawValue,
			CompiledRegex: re,
		}, nil
	}

	// Comparison operators
	if op, rest, ok := splitComparisonOp(rawValue); ok {
		rest = strings.TrimSpace(rest)
		c := Condition{Field: field, Operator: op, Value: rest}
		if err := setNumericValue(ruleName, field, rest, &c); err != nil {
			return Condition{}, err
		}
		return c, nil
	}

	// Glob: plain value containing * or ? metacharacters.
	if strings.ContainsAny(rawValue, "*?[") {
		// Validate the pattern is legal so we don't fail silently at eval time.
		if _, err := filepath.Match(rawValue, ""); err != nil {
			return Condition{}, fmt.Errorf("rule %q field %q: invalid glob pattern %q: %w", ruleName, field, rawValue, err)
		}
		// Normalise the pattern case for fields whose values are lower-cased at
		// evaluation time (fieldValue calls strings.ToLower on level and runtime).
		// Without this, patterns like "ERR*" on level: would never match "error".
		patternValue := rawValue
		if field == "level" || field == "runtime" {
			patternValue = strings.ToLower(rawValue)
		}
		return Condition{
			Field:    field,
			Operator: OpGlob,
			Value:    patternValue,
		}, nil
	}

	// Equality (plain value). Normalise case-insensitive fields.
	value := rawValue
	if field == "level" || field == "runtime" {
		value = strings.ToLower(value)
	}

	return Condition{
		Field:    field,
		Operator: OpEq,
		Value:    value,
	}, nil
}

// splitComparisonOp detects a leading comparison operator and returns the
// operator, the remainder, and true. Returns ("", rawValue, false) if rawValue
// does not begin with a comparison operator.
func splitComparisonOp(rawValue string) (Operator, string, bool) {
	// Order matters: check two-char ops first.
	for _, pair := range []struct {
		prefix string
		op     Operator
	}{
		{">=", OpGte},
		{"<=", OpLte},
		{">", OpGt},
		{"<", OpLt},
	} {
		if strings.HasPrefix(rawValue, pair.prefix) {
			return pair.op, rawValue[len(pair.prefix):], true
		}
	}
	return "", rawValue, false
}

// setNumericValue parses the value for a numeric or duration comparison
// operator and stores the result in c.NumericValue.
func setNumericValue(ruleName, field, value string, c *Condition) error {
	if isDurationField(field) {
		d, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("rule %q field %q: invalid duration %q: %w", ruleName, field, value, err)
		}
		c.DurValue = d
		c.NumericValue = d.Seconds()
		return nil
	}
	// Integer field
	n, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("rule %q field %q: expected a number, got %q", ruleName, field, value)
	}
	c.NumericValue = n
	return nil
}

// validateUniquePriority checks that no two rules in the slice share a priority.
// Returns an error naming both conflicting rules.
func validateUniquePriority(rules []Rule) error {
	seen := make(map[int]string, len(rules))
	for _, r := range rules {
		if prev, exists := seen[r.Priority]; exists {
			return fmt.Errorf("duplicate priority %d: rules %q and %q both use this priority; priorities must be unique", r.Priority, prev, r.Name)
		}
		seen[r.Priority] = r.Name
	}
	return nil
}
