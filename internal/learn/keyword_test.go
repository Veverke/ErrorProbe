package learn

import (
	"testing"
)

func TestScoreKeywords_Tiers(t *testing.T) {
	tests := []struct {
		msg        string
		wantTier   int
		wantMinMul float64
	}{
		{"panic: runtime error", 3, 0.9},
		{"fatal: out of memory", 3, 0.9},
		{"goroutine 1 [running]: traceback", 3, 0.8},
		{"sigsegv received", 3, 0.9},
		{"oomkilled by kernel", 3, 0.9},
		{"error: connection refused", 2, 0.6},
		{"failed to connect to database", 2, 0.6},
		{"econnrefused localhost:5432", 2, 0.7},
		{"deadlock detected", 2, 0.8},
		{"ssl handshake failure", 2, 0.6},
		{"timeout waiting for response", 1, 0.3},
		{"circuit breaker open", 1, 0.5},
		{"degraded service", 1, 0.4},
		{"hello world", 0, 0},
		{"", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			tier, mul := ScoreKeywords(tt.msg)
			if tier != tt.wantTier {
				t.Errorf("tier=%d, want %d", tier, tt.wantTier)
			}
			if tt.wantTier > 0 && mul < tt.wantMinMul {
				t.Errorf("multiplier=%.2f, want >=%.2f", mul, tt.wantMinMul)
			}
		})
	}
}

func TestIsBlocklisted(t *testing.T) {
	blocked := []string{
		"no error occurred",
		"0 errors found",
		"error: <nil>",
		"ignoring error",
		"error count: 0",
		"no errors found here",
	}
	for _, msg := range blocked {
		if !IsBlocklisted(msg) {
			t.Errorf("expected %q to be blocklisted", msg)
		}
	}

	notBlocked := []string{
		"panic: runtime error",
		"fatal: disk full",
		"connection refused",
	}
	for _, msg := range notBlocked {
		if IsBlocklisted(msg) {
			t.Errorf("expected %q to NOT be blocklisted", msg)
		}
	}
}

func TestScoreKeywords_HighestTierWins(t *testing.T) {
	// When a message has both tier-1 and tier-3 keywords, tier-3 should win.
	tier, _ := ScoreKeywords("timeout: panic: unexpected")
	if tier != 3 {
		t.Errorf("expected tier 3 for mixed message, got %d", tier)
	}
}
