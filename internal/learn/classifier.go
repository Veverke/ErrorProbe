package learn

import (
	"math"

	"github.com/errorprobe/errorprobe/internal/ingest"
)

const (
	// maxMatchFraction is the upper limit on the fraction of the original
	// message that may be replaced by placeholders. Patterns above this
	// threshold are considered too generic to be useful.
	maxMatchFraction = 0.60

	// minWindows is the minimum number of sampling windows a pattern must
	// appear in before it is eligible for auto-apply.
	minWindows = 2
)

// ClassifyResult holds the outcome of the Classify pipeline for a single event.
type ClassifyResult struct {
	Candidate ScoredCandidate
	// Flagged is true when the score is above the review threshold but below the
	// confidence threshold (pending user confirmation).
	Flagged bool
	// AutoApply is true when the score is at or above the confidence threshold.
	AutoApply bool
}

// ClassifyOptions controls the thresholds used during classification.
type ClassifyOptions struct {
	// ConfidenceThreshold is the minimum score to auto-apply a rule.
	ConfidenceThreshold float64
	// ReviewThreshold is the minimum score to flag a rule for user review.
	ReviewThreshold float64
	// TotalWindows is the denominator used when normalising the windows count.
	// Defaults to minWindows when zero.
	TotalWindows int
}

// Classify runs the full classification pipeline on a single uncovered event.
//
//  1. Blocklist check — returns zero ClassifyResult when the message contains a
//     known benign phrase.
//  2. Keyword scoring — aborts when no keyword tier matches.
//  3. Pattern extraction — aborts when matchFraction > maxMatchFraction.
//  4. Suppression check — aborts when the pattern is already suppressed.
//  5. Score computation: multiplier × clamp(windows/totalWindows, 0, 1) × (1 - matchFraction)
//  6. Threshold decisions: AutoApply | Flagged | discard.
func Classify(
	ev ingest.LogEvent,
	windows int,
	suppressed *SuppressionList,
	opts ClassifyOptions,
) (ClassifyResult, bool) {
	if IsBlocklisted(ev.Message) {
		return ClassifyResult{}, false
	}

	tier, multiplier := ScoreKeywords(ev.Message)
	if tier == 0 {
		return ClassifyResult{}, false
	}

	pattern, matchFraction := ExtractPattern(ev.Message)
	if pattern == "" || matchFraction > maxMatchFraction {
		return ClassifyResult{}, false
	}

	if suppressed != nil && suppressed.Contains(pattern) {
		return ClassifyResult{}, false
	}

	totalWindows := opts.TotalWindows
	if totalWindows <= 0 {
		totalWindows = minWindows
	}

	windowScore := math.Min(float64(windows)/float64(totalWindows), 1.0)
	score := multiplier * windowScore * (1.0 - matchFraction)

	// Map tier → health state.
	var generatedState string
	switch {
	case tier >= 2:
		generatedState = "HAS_ERRORS"
	default:
		generatedState = "DEGRADED"
	}

	sc := ScoredCandidate{
		Candidate: Candidate{
			Event:         ev,
			Pattern:       pattern,
			KeywordTier:   tier,
			Windows:       windows,
			MatchFraction: matchFraction,
		},
		Score:          score,
		GeneratedState: generatedState,
	}

	confidenceThreshold := opts.ConfidenceThreshold
	if confidenceThreshold <= 0 {
		confidenceThreshold = 0.75
	}
	reviewThreshold := opts.ReviewThreshold
	if reviewThreshold <= 0 {
		reviewThreshold = 0.50
	}

	switch {
	case score >= confidenceThreshold:
		return ClassifyResult{Candidate: sc, AutoApply: true}, true
	case score >= reviewThreshold:
		return ClassifyResult{Candidate: sc, Flagged: true}, true
	default:
		return ClassifyResult{}, false
	}
}

// ClassifyBatch runs Classify over a slice of uncovered events, deduplicating
// by extracted pattern. Each unique pattern is only returned once; the event
// with the highest keyword tier (and then the highest score) wins.
func ClassifyBatch(
	events []ingest.LogEvent,
	windowCounts map[string]int, // pattern → window count
	totalWindows int,
	suppressed *SuppressionList,
	opts ClassifyOptions,
) []ClassifyResult {
	opts.TotalWindows = totalWindows

	seen := make(map[string]ClassifyResult)
	for _, ev := range events {
		result, ok := Classify(ev, windowCounts[ev.Message], suppressed, opts)
		if !ok {
			continue
		}
		pattern := result.Candidate.Pattern
		if prev, exists := seen[pattern]; !exists ||
			result.Candidate.Score > prev.Candidate.Score {
			seen[pattern] = result
		}
	}

	out := make([]ClassifyResult, 0, len(seen))
	for _, r := range seen {
		out = append(out, r)
	}
	return out
}
