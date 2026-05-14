package pbr

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// EvalCondition evaluates a single condition against the given context.
// All operator/type mismatches and unknown fields return false without panicking.
func EvalCondition(c Condition, ctx EvalContext) bool {
	val, ok := fieldValue(c.Field, ctx)
	if !ok {
		return false
	}

	switch c.Operator {
	case OpEq:
		if isDurationField(c.Field) {
			// Compare durations semantically: "5m" == "5m0s" must be true.
			d1, err1 := time.ParseDuration(val)
			d2, err2 := time.ParseDuration(c.Value)
			if err1 != nil || err2 != nil {
				return false
			}
			return d1 == d2
		}
		return val == c.Value
	case OpGt:
		n, ok := numericOrDuration(val, c.Field)
		if !ok {
			return false
		}
		return n > c.NumericValue
	case OpLt:
		n, ok := numericOrDuration(val, c.Field)
		if !ok {
			return false
		}
		return n < c.NumericValue
	case OpGte:
		n, ok := numericOrDuration(val, c.Field)
		if !ok {
			return false
		}
		return n >= c.NumericValue
	case OpLte:
		n, ok := numericOrDuration(val, c.Field)
		if !ok {
			return false
		}
		return n <= c.NumericValue
	case OpRegex:
		if c.CompiledRegex == nil {
			return false
		}
		return c.CompiledRegex.MatchString(val)
	case OpGlob:
		matched, err := filepath.Match(c.Value, val)
		if err != nil {
			return false
		}
		return matched
	}
	return false
}

// fieldValue retrieves the string representation of the named field from ctx.
// Returns ("", false) for unknown fields or when the context plane does not
// expose the field.
func fieldValue(field string, ctx EvalContext) (string, bool) {
	if ctx.Log != nil {
		return logFieldValue(field, ctx.Log)
	}
	if ctx.Infra != nil {
		return infraFieldValue(field, ctx.Infra)
	}
	return "", false
}

func logFieldValue(field string, ctx *LogEvalContext) (string, bool) {
	switch field {
	case "level":
		return strings.ToLower(ctx.Event.Level), true
	case "message":
		return ctx.Event.Message, true
	case "container":
		return ctx.Event.Container, true
	case "namespace":
		return ctx.Event.Namespace, true
	case "runtime":
		return ctx.Event.Runtime, true
	case "count_in_window":
		return strconv.Itoa(ctx.CountInWindow), true
	case "window":
		return ctx.Window.String(), true
	}
	return "", false
}

func infraFieldValue(field string, ctx *InfraContainer) (string, bool) {
	switch field {
	case "container":
		return ctx.Name, true
	case "namespace":
		return ctx.Namespace, true
	case "runtime":
		return ctx.Runtime, true
	case "restart_count":
		return strconv.Itoa(ctx.RestartCount), true
	case "uptime":
		return ctx.Uptime.String(), true
	case "phase":
		return ctx.Phase, true
	}
	return "", false
}

// numericOrDuration converts a string value to a float64 for numeric/duration
// comparison. Duration fields are converted to seconds. Returns (0, false) on
// any parse error.
func numericOrDuration(val, field string) (float64, bool) {
	if isDurationField(field) {
		d, err := time.ParseDuration(val)
		if err != nil {
			return 0, false
		}
		return d.Seconds(), true
	}
	// Integer fields (count_in_window, restart_count)
	n, err := strconv.Atoi(val)
	if err != nil {
		return 0, false
	}
	return float64(n), true
}

// isDurationField returns true for fields whose values are Go duration strings.
func isDurationField(field string) bool {
	return field == "uptime" || field == "window"
}

// compileRegex compiles pattern as a regex.
func compileRegex(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(pattern)
}
