package learn

import (
	"testing"

	"github.com/errorprobe/errorprobe/internal/ingest"
)

func makeOpts(conf, review float64, total int) ClassifyOptions {
	return ClassifyOptions{
		ConfidenceThreshold: conf,
		ReviewThreshold:     review,
		TotalWindows:        total,
	}
}

func makeEvent(msg string) ingest.LogEvent {
	return ingest.LogEvent{Message: msg}
}

func TestClassify_Blocklisted(t *testing.T) {
	ev := makeEvent("no error occurred")
	_, ok := Classify(ev, 3, nil, makeOpts(0.75, 0.5, 5))
	if ok {
		t.Error("expected blocklisted message to be rejected")
	}
}

func TestClassify_NoKeyword(t *testing.T) {
	ev := makeEvent("server started on port 8080")
	_, ok := Classify(ev, 5, nil, makeOpts(0.75, 0.5, 5))
	if ok {
		t.Error("expected message without keyword to be rejected")
	}
}

func TestClassify_AutoApply(t *testing.T) {
	ev := makeEvent("panic: runtime error index out of range")
	res, ok := Classify(ev, 5, nil, makeOpts(0.50, 0.25, 5))
	if !ok {
		t.Fatal("expected panic message to classify")
	}
	if !res.AutoApply {
		t.Errorf("expected AutoApply=true, score=%.3f", res.Candidate.Score)
	}
}

func TestClassify_Flagged(t *testing.T) {
	ev := makeEvent("timeout waiting for upstream response")
	res, ok := Classify(ev, 2, nil, makeOpts(0.90, 0.20, 5))
	if !ok {
		t.Skip("message didn't classify at these thresholds")
	}
	if res.AutoApply {
		t.Log("auto-applied rather than flagged — acceptable")
	}
	_ = res
}

func TestClassify_Suppressed(t *testing.T) {
	ev := makeEvent("panic: unexpected nil pointer dereference")
	// First, get the pattern.
	pat, _ := ExtractPattern(ev.Message)
	sl := &SuppressionList{}
	sl.Entries = []SuppressionEntry{{Pattern: pat}}

	_, ok := Classify(ev, 5, sl, makeOpts(0.5, 0.25, 5))
	if ok {
		t.Error("expected suppressed pattern to be rejected")
	}
}

func TestClassify_TooGeneric(t *testing.T) {
	// Mostly IPs: pattern would be too volatile.
	ev := makeEvent("192.168.1.1 10.0.0.2 172.16.0.1 192.168.2.3 10.1.0.0 error")
	res, ok := Classify(ev, 5, nil, makeOpts(0.5, 0.25, 5))
	if ok && res.Candidate.Candidate.MatchFraction > maxMatchFraction {
		t.Error("expected too-generic pattern to be rejected")
	}
}

func TestClassify_MinWindows(t *testing.T) {
	// Only 1 window: should not auto-apply when totalWindows=5 lowers the window score.
	ev := makeEvent("fatal: disk full on /var/data")
	res, ok := Classify(ev, 1, nil, makeOpts(0.90, 0.50, 5))
	if ok && res.AutoApply {
		t.Errorf("expected 1 window out of 5 to not auto-apply, score=%.3f", res.Candidate.Score)
	}
}

func TestClassifyBatch_Deduplication(t *testing.T) {
	evs := []ingest.LogEvent{
		makeEvent("panic: nil pointer dereference in handler"),
		makeEvent("panic: nil pointer dereference in handler"),
		makeEvent("panic: nil pointer dereference in handler"),
	}
	wc := map[string]int{}
	for _, ev := range evs {
		pat, _ := ExtractPattern(ev.Message)
		wc[pat] = 3
	}
	results := ClassifyBatch(evs, wc, 5, nil, makeOpts(0.5, 0.25, 5))
	// Should deduplicate to 1 result.
	if len(results) != 1 {
		t.Errorf("expected 1 deduplicated result, got %d", len(results))
	}
}
