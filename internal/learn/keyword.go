package learn

import "strings"

// keywordEntry associates a keyword/phrase with a severity tier and a score
// multiplier used by the classifier.
type keywordEntry struct {
	phrase     string
	tier       int
	multiplier float64
}

// highKeywords are Tier-3 (critical) indicators. A single match is sufficient
// to reach the confidence threshold.
var highKeywords = []keywordEntry{
	{"panic", 3, 1.0},
	{"fatal", 3, 1.0},
	{"exception", 3, 0.9},
	{"oomkilled", 3, 1.0},
	{"stack trace", 3, 0.9},
	{"traceback", 3, 0.9},
	{"sigsegv", 3, 1.0},
	{"sigabrt", 3, 1.0},
	{"core dumped", 3, 1.0},
}

// mediumKeywords are Tier-2 indicators. Multiple windows of evidence typically
// push the score above the confidence threshold.
var mediumKeywords = []keywordEntry{
	{"error:", 2, 0.8},
	{"err:", 2, 0.7},
	{"failed", 2, 0.7},
	{"refused", 2, 0.75},
	{"denied", 2, 0.7},
	{"unreachable", 2, 0.8},
	{"http 5", 2, 0.8},
	{"ora-", 2, 0.85},
	{"sqlstate", 2, 0.8},
	{"deadlock", 2, 0.9},
	{"econnrefused", 2, 0.85},
	{"enospc", 2, 0.9},
	{"enomem", 2, 0.9},
	{"out of memory", 2, 0.9},
	{"no space left", 2, 0.9},
	{"certificate expired", 2, 0.85},
	{"ssl handshake", 2, 0.75},
}

// lowKeywords are Tier-1 indicators. They contribute only when corroborated
// by multiple observation windows.
var lowKeywords = []keywordEntry{
	{"timeout", 1, 0.5},
	{"retry exhausted", 1, 0.6},
	{"circuit breaker", 1, 0.6},
	{"slow", 1, 0.3},
	{"degraded", 1, 0.5},
	{"unexpected", 1, 0.4},
	{"http 4", 1, 0.4},
}

// blocklistPhrases are benign messages that contain error-related words but do
// NOT indicate a real problem. A line that matches any phrase here is skipped
// entirely by the learning pipeline.
var blocklistPhrases = []string{
	"no error",
	"0 errors",
	"error count: 0",
	"error: none",
	"error: <nil>",
	"suppressed error",
	"ignoring error",
	"expected error",
	"error: ok",
	"no errors found",
}

// blocklistWords are single words whose sole presence (without additional
// signal) is too common to be meaningful. They must appear as the ONLY keyword
// hit; if a higher-tier keyword is also present the word is not a blocker.
var blocklistWords = []string{"error", "fail"}

// IsBlocklisted reports whether msg matches a suppression phrase.
// Comparison is case-insensitive and uses substring matching.
func IsBlocklisted(msg string) bool {
	lower := strings.ToLower(msg)
	for _, phrase := range blocklistPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// ScoreKeywords scans msg for known error keywords and returns:
//   - tier: highest tier matched (3=high, 2=medium, 1=low, 0=none)
//   - multiplier: score multiplier corresponding to the strongest match
//
// When only blocklist words match and no higher-tier keyword is present,
// tier=0 and multiplier=0 are returned (suppress learning).
func ScoreKeywords(msg string) (tier int, multiplier float64) {
	lower := strings.ToLower(msg)

	// Check high-tier first for early exit.
	for _, kw := range highKeywords {
		if strings.Contains(lower, kw.phrase) {
			return kw.tier, kw.multiplier
		}
	}

	// Accumulate medium-tier — return the best hit.
	for _, kw := range mediumKeywords {
		if strings.Contains(lower, kw.phrase) {
			if kw.tier > tier || (kw.tier == tier && kw.multiplier > multiplier) {
				tier = kw.tier
				multiplier = kw.multiplier
			}
		}
	}
	if tier > 0 {
		return
	}

	// Low-tier pass.
	for _, kw := range lowKeywords {
		if strings.Contains(lower, kw.phrase) {
			if kw.multiplier > multiplier {
				tier = kw.tier
				multiplier = kw.multiplier
			}
		}
	}
	if tier > 0 {
		return
	}

	// Check whether any blocklist word matched (would give a spurious hit).
	for _, bw := range blocklistWords {
		if strings.Contains(lower, bw) {
			// Blocklist word only — suppress.
			return 0, 0
		}
	}

	return 0, 0
}
