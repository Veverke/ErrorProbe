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
			return EvalResult{
				State:          r.SetState,
				MatchedRule:    r.Name,
				MatchedPattern: messageMatchedText(r, ctx),
			}
		}
	}
	return EvalResult{}
}

// messageMatchedText returns the specific substring of the log message that
// satisfied the first "message" condition in the matched rule.
// For regex operators it returns the actual matched text via FindString;
// for eq operators it returns the condition value itself.
// Returns "" when no message condition is present (e.g. level-only rules).
func messageMatchedText(r Rule, ctx EvalContext) string {
	if ctx.Log == nil {
		return ""
	}
	msg := ctx.Log.Event.Message
	for _, c := range r.Conditions {
		if c.Field != "message" {
			continue
		}
		switch c.Operator {
		case OpEq:
			return c.Value
		case OpRegex:
			if c.CompiledRegex != nil {
				if m := c.CompiledRegex.FindString(msg); m != "" {
					return m
				}
			}
		case OpGlob:
			// Glob patterns can't easily yield a matched substring;
			// return the pattern value as a hint.
			return c.Value
		}
	}
	return ""
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
