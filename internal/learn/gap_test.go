package learn

import (
	"testing"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/ingest"
	"github.com/errorprobe/errorprobe/internal/pbr"
)

func TestFindUncovered_NoRules(t *testing.T) {
	events := []ingest.LogEvent{
		{Message: "panic: out of memory"},
		{Message: "fatal: disk full"},
	}
	uncovered := FindUncovered(events, nil)
	if len(uncovered) != 2 {
		t.Errorf("expected all events uncovered with no rules, got %d", len(uncovered))
	}
}

func TestFindUncovered_AllCovered(t *testing.T) {
	rules, err := pbr.Load([]config.RuleConfig{
		{Name: "catch-all", Priority: 100, Match: "log", SetState: "HAS_ERRORS"},
	}, nil, nil)
	if err != nil {
		t.Fatalf("pbr.Load: %v", err)
	}

	events := []ingest.LogEvent{
		{Container: "myapp", Message: "error: something failed"},
	}
	uncovered := FindUncovered(events, rules)
	if len(uncovered) != 0 {
		t.Errorf("expected 0 uncovered events, got %d", len(uncovered))
	}
}

func TestFindUncovered_PartialCoverage(t *testing.T) {
	rules, err := pbr.Load([]config.RuleConfig{
		{
			Name:     "specific",
			Priority: 100,
			Match:    "log",
			SetState: "HAS_ERRORS",
			When:     map[string]string{"container": "known-container"},
		},
	}, nil, nil)
	if err != nil {
		t.Fatalf("pbr.Load: %v", err)
	}

	events := []ingest.LogEvent{
		{Container: "known-container", Message: "error: xyz"},
		{Container: "other-container", Message: "error: abc"},
	}
	uncovered := FindUncovered(events, rules)
	if len(uncovered) != 1 {
		t.Errorf("expected 1 uncovered event, got %d", len(uncovered))
	}
	if uncovered[0].Container != "other-container" {
		t.Errorf("unexpected container: %q", uncovered[0].Container)
	}
}
