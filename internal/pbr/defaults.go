package pbr

import "time"

// recentRestartWindow is the uptime threshold below which a K8s container with
// a non-zero restart count is considered to be actively restarting.  A container
// that has been running longer than this window is assumed to be stable even if
// it has restarted in the past.
const recentRestartWindow = 2 * time.Minute

// BuiltinRules returns the default rule set shipped with ErrorProbe.
// These rules are assigned the lowest standard priorities so any user rule
// with priority > 110 overrides all of them.
//
// builtin-failing    priority 110: any error-level event whose rolling count
//                    in the Tier-2 window is >= 5 escalates to FAILING.
// builtin-log-error  priority 100: any error-level log event → HAS_ERRORS.
// builtin-log-warn   priority  90: any warn-level log event  → HAS_ERRORS.
// builtin-k8s-restarting priority 100 (infra): K8s container with restart_count > 0
//                    within the first 2 minutes of its current lifetime → RESTARTING.
func BuiltinRules() []Rule {
	return []Rule{
		{
			Name:     "builtin-failing",
			Priority: 110,
			Match:    MatchLog,
			Conditions: []Condition{
				{Field: "level", Operator: OpEq, Value: "error"},
				{Field: "count_in_window", Operator: OpGte, NumericValue: 5},
			},
			SetState: "FAILING",
		},
		{
			Name:     "builtin-log-error",
			Priority: 100,
			Match:    MatchLog,
			Conditions: []Condition{
				{Field: "level", Operator: OpEq, Value: "error"},
			},
			SetState: "HAS_ERRORS",
		},
		{
			Name:     "builtin-log-warn",
			Priority: 90,
			Match:    MatchLog,
			Conditions: []Condition{
				{Field: "level", Operator: OpEq, Value: "warn"},
			},
			SetState: "HAS_ERRORS",
		},
		{
			// Infra plane: K8s container restarting within the recent-restart window.
			Name:     "builtin-k8s-restarting",
			Priority: 100,
			Match:    MatchInfra,
			Conditions: []Condition{
				{Field: "runtime", Operator: OpEq, Value: "k8s"},
				{Field: "restart_count", Operator: OpGt, NumericValue: 0},
				{Field: "uptime", Operator: OpLt, NumericValue: recentRestartWindow.Seconds()},
			},
			SetState: "RESTARTING",
		},
	}
}
