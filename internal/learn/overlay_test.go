package learn

import (
	"path/filepath"
	"testing"

	"github.com/errorprobe/errorprobe/internal/config"
)

func TestOverlay_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "learned.yaml")

	rules := []LearnedRule{
		{
			Name:     "learned-abc1",
			Priority: 900,
			Match:    "*",
			When:     map[string]string{"message": `regex:(?i)connection refused`},
			SetState: "HAS_ERRORS",
			Source:   SourceLearned,
		},
	}
	if err := SaveOverlay(path, rules); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadOverlay(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(loaded))
	}
	if loaded[0].Name != "learned-abc1" {
		t.Errorf("name mismatch: %q", loaded[0].Name)
	}
	if loaded[0].Source != SourceLearned {
		t.Errorf("source mismatch: %v", loaded[0].Source)
	}
}

func TestLoadOverlay_Missing(t *testing.T) {
	rules, err := LoadOverlay("/nonexistent/learned.yaml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("expected empty slice, got %d rules", len(rules))
	}
}

func TestMergeOverlay_NoConflict(t *testing.T) {
	cfgRules := []config.RuleConfig{
		{Name: "existing-rule", Priority: 100},
	}
	overlay := []LearnedRule{
		{Name: "learned-new", Priority: 800, Match: "*", SetState: "HAS_ERRORS", Source: SourceLearned},
	}
	merged := MergeOverlay(cfgRules, overlay)
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged rules, got %d", len(merged))
	}
}

func TestMergeOverlay_ConflictSkipped(t *testing.T) {
	cfgRules := []config.RuleConfig{
		{Name: "learned-abc1", Priority: 100},
	}
	overlay := []LearnedRule{
		{Name: "learned-abc1", Priority: 800, SetState: "HAS_ERRORS", Source: SourceLearned},
	}
	merged := MergeOverlay(cfgRules, overlay)
	// Overlay entry with same name should be skipped (config wins).
	if len(merged) != 1 {
		t.Fatalf("expected 1 rule (no duplicate), got %d", len(merged))
	}
	if merged[0].Priority != 100 {
		t.Errorf("expected config priority 100, got %d", merged[0].Priority)
	}
}

func TestPending_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending.yaml")

	rules := []LearnedRule{
		{Name: "learned-pend1", Priority: 700, Source: SourceLearned},
	}
	if err := SavePending(path, rules); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadPending(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "learned-pend1" {
		t.Errorf("pending round trip failed: %+v", loaded)
	}
}
