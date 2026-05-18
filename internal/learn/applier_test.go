package learn

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestApplier(t *testing.T) (*Applier, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	overlayPath := filepath.Join(dir, "learned.yaml")
	pendingPath := filepath.Join(dir, "pending.yaml")
	suppPath := filepath.Join(dir, "suppressed.yaml")
	a := NewApplier(overlayPath, pendingPath, suppPath, nil)
	return a, overlayPath, pendingPath, suppPath
}

func sampleRule(name string) LearnedRule {
	return LearnedRule{
		Name:         name,
		Priority:     800,
		Match:        "log",
		When:         map[string]string{"message": `regex:(?i)panic`},
		SetState:     "HAS_ERRORS",
		Source:       SourceLearned,
		DiscoveredAt: time.Now().UTC(),
	}
}

func TestApplier_Apply(t *testing.T) {
	a, overlayPath, _, _ := newTestApplier(t)
	r := sampleRule("learned-abc1")
	if err := a.Apply(r); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	loaded, err := LoadOverlay(overlayPath)
	if err != nil {
		t.Fatalf("LoadOverlay: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "learned-abc1" {
		t.Errorf("unexpected overlay: %+v", loaded)
	}
}

func TestApplier_Apply_Upsert(t *testing.T) {
	a, overlayPath, _, _ := newTestApplier(t)
	r := sampleRule("learned-abc1")
	_ = a.Apply(r)
	// Apply again (upsert should replace, not duplicate).
	r.Priority = 750
	if err := a.Apply(r); err != nil {
		t.Fatalf("Apply upsert: %v", err)
	}
	loaded, _ := LoadOverlay(overlayPath)
	if len(loaded) != 1 {
		t.Errorf("expected 1 rule after upsert, got %d", len(loaded))
	}
	if loaded[0].Priority != 750 {
		t.Errorf("expected updated priority 750, got %d", loaded[0].Priority)
	}
}

func TestApplier_Pending(t *testing.T) {
	a, _, pendingPath, _ := newTestApplier(t)
	r := sampleRule("learned-pend1")
	if err := a.Pending(r); err != nil {
		t.Fatalf("Pending: %v", err)
	}
	loaded, _ := LoadPending(pendingPath)
	if len(loaded) != 1 || loaded[0].Name != "learned-pend1" {
		t.Errorf("pending not saved: %+v", loaded)
	}
}

func TestApplier_ConfirmRule(t *testing.T) {
	a, overlayPath, pendingPath, _ := newTestApplier(t)
	r := sampleRule("learned-pend1")
	_ = a.Pending(r)
	if err := a.ConfirmRule("learned-pend1"); err != nil {
		t.Fatalf("ConfirmRule: %v", err)
	}
	// Should be in overlay now.
	overlay, _ := LoadOverlay(overlayPath)
	if len(overlay) != 1 || overlay[0].Source != SourceConfirmed {
		t.Errorf("expected confirmed rule in overlay, got %+v", overlay)
	}
	// Should be removed from pending.
	pending, _ := LoadPending(pendingPath)
	if len(pending) != 0 {
		t.Errorf("expected pending to be empty after confirm, got %+v", pending)
	}
}

func TestApplier_RejectRule(t *testing.T) {
	a, overlayPath, pendingPath, suppPath := newTestApplier(t)
	r := sampleRule("learned-rej1")
	_ = a.Apply(r)
	if err := a.RejectRule("learned-rej1", `regex:(?i)panic`); err != nil {
		t.Fatalf("RejectRule: %v", err)
	}
	// Should be removed from overlay.
	overlay, _ := LoadOverlay(overlayPath)
	for _, rule := range overlay {
		if rule.Name == "learned-rej1" {
			t.Error("rejected rule still in overlay")
		}
	}
	// Should be removed from pending (not there, no error).
	pending, _ := LoadPending(pendingPath)
	for _, rule := range pending {
		if rule.Name == "learned-rej1" {
			t.Error("rejected rule still in pending")
		}
	}
	// Pattern should be suppressed.
	sl, err := LoadSuppressionList(suppPath)
	if err != nil {
		t.Fatalf("load suppression: %v", err)
	}
	if !sl.Contains(`regex:(?i)panic`) {
		t.Error("pattern not added to suppression list after rejection")
	}
}

func TestApplier_OverlayRules(t *testing.T) {
	a, _, _, _ := newTestApplier(t)
	_ = a.Apply(sampleRule("r1"))
	_ = a.Apply(sampleRule("r2"))
	rules := a.OverlayRules()
	if len(rules) != 2 {
		t.Errorf("expected 2 overlay rules, got %d", len(rules))
	}
}

func TestApplier_ConfirmRule_NotFound(t *testing.T) {
	a, _, _, _ := newTestApplier(t)
	err := a.ConfirmRule("nonexistent-rule")
	if err == nil {
		t.Error("expected error when confirming nonexistent rule")
	}
}
