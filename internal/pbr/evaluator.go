package pbr

// Evaluate runs the rule set against ctx using first-match-wins semantics.
// Rules must be pre-sorted in descending priority order (as returned by Load).
// Returns an EvalResult with State and MatchedRule set on match;
// returns EvalResult{} (empty State) when no rule matches.
func Evaluate(rules []Rule, ctx EvalContext) EvalResult {
	for _, r := range rules {
		if !contextMatchesPlane(r.Match, ctx) {
			continue
		}
		if allConditionsMatch(r.Conditions, ctx) {
			return EvalResult{State: r.SetState, MatchedRule: r.Name}
		}
	}
	return EvalResult{}
}

// contextMatchesPlane returns true when the rule's match plane matches the
// evaluation context type.
func contextMatchesPlane(match MatchContext, ctx EvalContext) bool {
	switch match {
	case MatchLog:
		return ctx.Log != nil
	case MatchInfra:
		return ctx.Infra != nil
	}
	return false
}

// allConditionsMatch returns true only when every condition in cs evaluates
// to true against ctx.
func allConditionsMatch(cs []Condition, ctx EvalContext) bool {
	for _, c := range cs {
		if !EvalCondition(c, ctx) {
			return false
		}
	}
	return true
}
