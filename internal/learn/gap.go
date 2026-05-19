package learn

import (
	"github.com/errorprobe/errorprobe/internal/ingest"
	"github.com/errorprobe/errorprobe/internal/pbr"
)

// FindUncovered returns the subset of events for which no existing PBR rule
// produces a health-state change. These are the candidates for learning.
//
// Events are evaluated through pbr.Evaluate the same way the health engine
// does; an empty MatchedRule in the result means no rule fired.
func FindUncovered(events []ingest.LogEvent, rules []pbr.Rule) []ingest.LogEvent {
	var uncovered []ingest.LogEvent
	for _, ev := range events {
		result := pbr.Evaluate(rules, pbr.EvalContext{
			Log: &pbr.LogEvalContext{Event: ev},
		})
		if result.MatchedRule == "" {
			uncovered = append(uncovered, ev)
		}
	}
	return uncovered
}
