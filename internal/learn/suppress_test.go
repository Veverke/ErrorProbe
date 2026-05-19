package learn

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSuppressionList_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "suppressed.yaml")

	entries := []SuppressionEntry{
		{Pattern: `connection refused`, AddedAt: time.Now().UTC().Truncate(time.Second), Reason: "test"},
		{Pattern: `disk full`, AddedAt: time.Now().UTC().Truncate(time.Second), Reason: "test2"},
	}
	if err := SaveSuppressionList(path, entries); err != nil {
		t.Fatalf("save: %v", err)
	}

	sl, err := LoadSuppressionList(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(sl.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(sl.Entries))
	}
	if sl.Entries[0].Pattern != "connection refused" {
		t.Errorf("wrong pattern: %q", sl.Entries[0].Pattern)
	}
}

func TestLoadSuppressionList_Missing(t *testing.T) {
	sl, err := LoadSuppressionList("/nonexistent/path/suppressed.yaml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if len(sl.Entries) != 0 {
		t.Errorf("expected empty list, got %d entries", len(sl.Entries))
	}
}

func TestSuppressionList_Contains(t *testing.T) {
	sl := &SuppressionList{
		Entries: []SuppressionEntry{
			{Pattern: "panic at runtime"},
		},
	}
	if !sl.Contains("panic at runtime") {
		t.Error("expected Contains to return true")
	}
	if sl.Contains("other pattern") {
		t.Error("expected Contains to return false for non-existent pattern")
	}
}

func TestSuppressionList_Add_Save(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "suppressed.yaml")

	sl, _ := LoadSuppressionList(path)
	sl.path = path
	sl.Add(SuppressionEntry{Pattern: "error: nil", AddedAt: time.Now().UTC()})
	if err := sl.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty file after save")
	}
}
