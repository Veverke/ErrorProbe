package learn

import (
	"crypto/sha256"
	"fmt"
	"time"
)

const (
	// learnedPriorityMax is the highest priority assigned to a learned rule.
	// Learned rules occupy the band 500–999 so they sit below explicit user
	// rules (priority 0–499 by convention) but above builtins (priority < 0).
	learnedPriorityMax = 999
	// learnedPriorityMin is the lowest priority in the learned-rule band.
	learnedPriorityMin = 500
)

// GenerateRule builds a LearnedRule from a classified candidate.
//
// Rule naming: "learned-" + first 8 hex chars of the SHA-256 of the pattern.
// This ensures stable, collision-resistant names across sessions.
//
// Priority: candidates are assigned priorities in descending order within the
// 500–999 band. existingCount is the number of already-stored learned rules;
// it drives the priority offset so new rules get a unique slot.
func GenerateRule(sc ScoredCandidate, existingCount int) LearnedRule {
	name := rulenameForPattern(sc.Pattern)
	priority := learnedPriorityMax - existingCount
	if priority < learnedPriorityMin {
		priority = learnedPriorityMin
	}

	when := map[string]string{
		"message": fmt.Sprintf("regex:(?i)%s", EscapeForRegex(sc.Pattern)),
	}

	return LearnedRule{
		Name:         name,
		Priority:     priority,
		Match:        "log",
		When:         when,
		SetState:     sc.GeneratedState,
		Source:       SourceLearned,
		DiscoveredAt: time.Now().UTC(),
	}
}

// rulenameForPattern produces a stable rule name for the given pattern string.
func rulenameForPattern(pattern string) string {
	h := sha256.Sum256([]byte(pattern))
	return fmt.Sprintf("learned-%x", h[:4])
}
