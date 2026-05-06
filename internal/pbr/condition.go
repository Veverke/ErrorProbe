package pbr

import (
	"path/filepath"
	"regexp"
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
		return formatInt(ctx.CountInWindow), true
	case "window":
		return ctx.Window.String(), true
	}
	return "", false
}

func infraFieldValue(field string, ctx *InfraEvalContext) (string, bool) {
	switch field {
	case "container":
		return ctx.Container.Name, true
	case "namespace":
		return ctx.Container.Namespace, true
	case "runtime":
		return ctx.Container.Runtime, true
	case "restart_count":
		return formatInt(ctx.Container.RestartCount), true
	case "uptime":
		return ctx.Container.Uptime.String(), true
	case "phase":
		return ctx.Container.Phase, true
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
	var n int
	if _, err := parseIntStr(val, &n); err != nil {
		return 0, false
	}
	return float64(n), true
}

// isDurationField returns true for fields whose values are Go duration strings.
func isDurationField(field string) bool {
	return field == "uptime" || field == "window"
}

// formatInt renders an int as its decimal string.
func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := make([]byte, 0, 20)
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}
	// reverse
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	if neg {
		return "-" + string(buf)
	}
	return string(buf)
}

// parseIntStr parses a decimal integer string into *out. Returns the number of
// bytes consumed and any error.
func parseIntStr(s string, out *int) (int, error) {
	neg := false
	i := 0
	if i < len(s) && s[i] == '-' {
		neg = true
		i++
	}
	if i >= len(s) {
		return 0, errInvalidInt
	}
	n := 0
	for ; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, errInvalidInt
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		n = -n
	}
	*out = n
	return i, nil
}

// compileRegex compiles pattern as a regex.
func compileRegex(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(pattern)
}

// errInvalidInt is a sentinel for parseIntStr.
var errInvalidInt = &parseError{"invalid integer"}

type parseError struct{ msg string }

func (e *parseError) Error() string { return e.msg }
