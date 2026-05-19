package learn

import (
	"strings"
	"testing"
	"time"

	"github.com/errorprobe/errorprobe/internal/ingest"
)

func TestGenerateRule_NameStability(t *testing.T) {
	pattern := "connection refused to database"
	sc := ScoredCandidate{
		Candidate: Candidate{
			Event:         ingest.LogEvent{Message: "error: connection refused to database"},
			Pattern:       pattern,
			KeywordTier:   2,
			Windows:       4,
			MatchFraction: 0.1,
		},
		Score:          0.72,
		GeneratedState: "HAS_ERRORS",
	}
	r1 := GenerateRule(sc, 0)
	r2 := GenerateRule(sc, 0)
	if r1.Name != r2.Name {
		t.Errorf("rule name not stable: %q vs %q", r1.Name, r2.Name)
	}
	if !strings.HasPrefix(r1.Name, "learned-") {
		t.Errorf("rule name missing prefix: %q", r1.Name)
	}
}

func TestGenerateRule_Priority(t *testing.T) {
	sc := ScoredCandidate{
		Candidate:      Candidate{Pattern: "disk full"},
		Score:          0.8,
		GeneratedState: "HAS_ERRORS",
	}
	r0 := GenerateRule(sc, 0)
	r1 := GenerateRule(sc, 1)
	if r0.Priority <= r1.Priority {
		t.Errorf("expected priority to decrease with existingCount: %d vs %d", r0.Priority, r1.Priority)
	}
	if r0.Priority > learnedPriorityMax {
		t.Errorf("priority %d exceeds max %d", r0.Priority, learnedPriorityMax)
	}
}

func TestGenerateRule_PriorityClamp(t *testing.T) {
	sc := ScoredCandidate{
		Candidate:      Candidate{Pattern: "out of memory"},
		Score:          0.9,
		GeneratedState: "HAS_ERRORS",
	}
	// existingCount so large that raw priority would go below min.
	r := GenerateRule(sc, 9999)
	if r.Priority < learnedPriorityMin {
		t.Errorf("priority %d below min %d", r.Priority, learnedPriorityMin)
	}
}

func TestGenerateRule_WhenContainsPattern(t *testing.T) {
	pattern := "timeout waiting for reply"
	sc := ScoredCandidate{
		Candidate:      Candidate{Pattern: pattern},
		Score:          0.6,
		GeneratedState: "DEGRADED",
	}
	r := GenerateRule(sc, 0)
	msg, ok := r.When["message"]
	if !ok {
		t.Fatal("expected 'message' key in When")
	}
	if !strings.Contains(msg, "regex:") {
		t.Errorf("expected regex prefix in When[message]: %q", msg)
	}
}

func TestGenerateRule_DiscoveredAt(t *testing.T) {
	before := time.Now().Add(-time.Second)
	sc := ScoredCandidate{Candidate: Candidate{Pattern: "fatal error"}, Score: 0.9, GeneratedState: "HAS_ERRORS"}
	r := GenerateRule(sc, 0)
	if r.DiscoveredAt.Before(before) {
		t.Errorf("DiscoveredAt %v is before test start %v", r.DiscoveredAt, before)
	}
}
