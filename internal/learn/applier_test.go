package learn

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestApplier_Apply_OnReloadCalled(t *testing.T) {
	dir := t.TempDir()
	overlayPath := filepath.Join(dir, "learned.yaml")
	pendingPath := filepath.Join(dir, "pending.yaml")
	suppPath := filepath.Join(dir, "suppressed.yaml")

	reloadCalled := false
	a := NewApplier(overlayPath, pendingPath, suppPath, func() {
		reloadCalled = true
	})
	r := sampleRule("reload-test")
	require.NoError(t, a.Apply(r))
	require.True(t, reloadCalled, "onReload callback must be called after Apply")
}

func TestApplier_ConfirmRule_FromOverlay(t *testing.T) {
	// Rule is in the overlay (not pending) — ConfirmRule should still promote it.
	a, overlayPath, pendingPath, _ := newTestApplier(t)
	r := sampleRule("overlay-rule")
	_ = a.Apply(r) // puts it in overlay, not pending

	if err := a.ConfirmRule("overlay-rule"); err != nil {
		t.Fatalf("ConfirmRule from overlay: %v", err)
	}
	overlay, _ := LoadOverlay(overlayPath)
	var found bool
	for _, rule := range overlay {
		if rule.Name == "overlay-rule" && rule.Source == SourceConfirmed {
			found = true
		}
	}
	if !found {
		t.Errorf("rule not confirmed in overlay: %+v", overlay)
	}
	// Pending should remain empty.
	pending, _ := LoadPending(pendingPath)
	for _, rule := range pending {
		if rule.Name == "overlay-rule" {
			t.Error("confirmed rule should not be in pending")
		}
	}
}

func TestApplier_RejectRule_AlreadySuppressed_Idempotent(t *testing.T) {
	a, _, _, suppPath := newTestApplier(t)
	r := sampleRule("reject-idem")
	_ = a.Apply(r)
	// Reject twice — second call must not error (suppression is idempotent).
	require.NoError(t, a.RejectRule("reject-idem", `regex:(?i)panic`))
	require.NoError(t, a.RejectRule("reject-idem", `regex:(?i)panic`))
	sl, err := LoadSuppressionList(suppPath)
	require.NoError(t, err)
	assert.True(t, sl.Contains(`regex:(?i)panic`))
}

func TestApplier_ConfirmRule_OnReloadCalled(t *testing.T) {
	dir := t.TempDir()
	reloadCalled := false
	a := NewApplier(
		filepath.Join(dir, "learned.yaml"),
		filepath.Join(dir, "pending.yaml"),
		filepath.Join(dir, "suppressed.yaml"),
		func() { reloadCalled = true },
	)
	r := sampleRule("confirm-reload")
	_ = a.Pending(r)
	require.NoError(t, a.ConfirmRule("confirm-reload"))
	assert.True(t, reloadCalled, "onReload must fire after ConfirmRule")
}

func TestApplier_Apply_CorruptOverlay_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	overlayPath := filepath.Join(dir, "learned.yaml")
	// Write invalid YAML to trigger LoadOverlay error.
	require.NoError(t, os.WriteFile(overlayPath, []byte("!!invalid: {unclosed"), 0o644))
	a := NewApplier(overlayPath, filepath.Join(dir, "pending.yaml"), filepath.Join(dir, "supp.yaml"), nil)
	err := a.Apply(sampleRule("bad"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "apply")
}

func TestApplier_Pending_CorruptPendingFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	pendingPath := filepath.Join(dir, "pending.yaml")
	require.NoError(t, os.WriteFile(pendingPath, []byte("!!bad"), 0o644))
	a := NewApplier(filepath.Join(dir, "learned.yaml"), pendingPath, filepath.Join(dir, "supp.yaml"), nil)
	err := a.Pending(sampleRule("bad"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pending")
}

func TestApplier_RejectRule_CorruptSuppFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	suppPath := filepath.Join(dir, "supp.yaml")
	require.NoError(t, os.WriteFile(suppPath, []byte("!!bad"), 0o644))
	a := NewApplier(filepath.Join(dir, "learned.yaml"), filepath.Join(dir, "pending.yaml"), suppPath, nil)
	// First put a rule in the overlay so RejectRule finds it.
	_ = os.WriteFile(filepath.Join(dir, "learned.yaml"), []byte("[]"), 0o644)
	r := sampleRule("to-reject")
	_ = os.WriteFile(filepath.Join(dir, "learned.yaml"), marshalRules(t, []LearnedRule{r}), 0o644)
	err := a.RejectRule("to-reject", `literal:error`)
	require.Error(t, err)
}

// marshalRules serialises a slice of LearnedRule to YAML bytes for test setup.
func marshalRules(t *testing.T, rules []LearnedRule) []byte {
	t.Helper()
	data, err := yaml.Marshal(rules)
	require.NoError(t, err)
	return data
}

func TestSaveOverlay_NonexistentDir_ReturnsError(t *testing.T) {
	err := SaveOverlay("/nonexistent/deeply/nested/overlay.yaml", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "writing overlay file")
}

func TestSaveSuppressionList_NonexistentDir_ReturnsError(t *testing.T) {
	err := SaveSuppressionList("/nonexistent/deeply/nested/supp.yaml", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "writing suppression file")
}

func TestRemoveRule_MultipleRules_RetainsNonMatchingRules(t *testing.T) {
	// Exercises the append branch: rules that don't match name are kept.
	rules := []LearnedRule{
		{Name: "keep-me", Match: "log"},
		{Name: "remove-me", Match: "log"},
		{Name: "also-keep", Match: "infra"},
	}
	result := removeRule(rules, "remove-me")
	require.Len(t, result, 2)
	assert.Equal(t, "keep-me", result[0].Name)
	assert.Equal(t, "also-keep", result[1].Name)
}
