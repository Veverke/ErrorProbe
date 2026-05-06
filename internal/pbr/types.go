// Package pbr implements the Policy-Based Rules engine for ErrorProbe.
// Rules classify log events and infra metadata into health states using a
// first-match-wins, descending-priority evaluation model.
package pbr

import (
	"regexp"
	"time"

	"github.com/errorprobe/errorprobe/internal/ingest"
)

// MatchContext identifies which evaluation plane a rule operates on.
type MatchContext string

const (
	MatchLog   MatchContext = "log"
	MatchInfra MatchContext = "infra"
)

// Operator enumerates the comparison operators supported in Condition.
type Operator string

const (
	OpEq    Operator = "eq"
	OpGt    Operator = "gt"
	OpLt    Operator = "lt"
	OpGte   Operator = "gte"
	OpLte   Operator = "lte"
	OpRegex Operator = "regex"
	OpGlob  Operator = "glob"
)

// Condition is a single field comparison within a rule's when block.
// Regex patterns are compiled once at load time and stored in CompiledRegex.
type Condition struct {
	Field        string
	Operator     Operator
	Value        string        // raw string value for eq / gt / lt / gte / lte / glob
	NumericValue float64       // parsed numeric value for numeric operators
	DurValue     time.Duration // parsed duration value for duration fields
	CompiledRegex *regexp.Regexp
}

// Rule is a fully parsed, validated, and compiled PBR rule ready for evaluation.
type Rule struct {
	Name       string
	Priority   int
	Match      MatchContext
	Conditions []Condition
	SetState   string
}

// LogEvalContext carries the inputs available on the log evaluation plane.
type LogEvalContext struct {
	Event         ingest.LogEvent
	CountInWindow int
	Window        time.Duration
}

// InfraEvalContext carries the inputs available on the infra evaluation plane.
type InfraEvalContext struct {
	Container     InfraContainer
}

// InfraContainer holds the infra-plane fields exposed to rule conditions.
// It is a thin projection of discovery.ContainerMeta to keep the pbr package
// free of a direct import of the discovery package.
type InfraContainer struct {
	Name         string
	Namespace    string
	Runtime      string
	RestartCount int
	Uptime       time.Duration // time.Since(StartedAt)
	Phase        string        // K8s pod phase
}

// EvalContext is the union type that wraps either a LogEvalContext or an
// InfraEvalContext. Callers pass a pointer; only one field will be non-nil.
type EvalContext struct {
	Log   *LogEvalContext
	Infra *InfraEvalContext
}

// EvalResult is the output of one evaluation pass.
// When MatchedRule is empty no rule matched and the caller applies its default.
type EvalResult struct {
	State       string
	MatchedRule string
}
