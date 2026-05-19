package learn

import (
	"strings"
	"testing"
)

func TestExtractPattern_StripVolatile(t *testing.T) {
	tests := []struct {
		msg         string
		contains    string // expected fragment in pattern
		notContains string // should be removed
	}{
		{
			msg:         "dial tcp 192.168.1.100:5432: connection refused",
			contains:    "connection refused",
			notContains: "192.168.1.100",
		},
		{
			msg:         "failed to connect: uuid=550e8400-e29b-41d4-a716-446655440000",
			contains:    "failed to connect",
			notContains: "550e8400",
		},
		{
			msg:         "OOM at 0xdeadbeef kernel panic",
			contains:    "kernel panic",
			notContains: "0xdeadbeef",
		},
		{
			msg:         "error at 2024-01-15T10:30:00Z timeout",
			contains:    "error",
			notContains: "2024-01-15",
		},
		{
			msg:         "retry 12345 times failed",
			contains:    "retry",
			notContains: "12345",
		},
	}
	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			pat, frac := ExtractPattern(tt.msg)
			if !strings.Contains(pat, tt.contains) {
				t.Errorf("pattern %q does not contain %q", pat, tt.contains)
			}
			if tt.notContains != "" && strings.Contains(pat, tt.notContains) {
				t.Errorf("pattern %q still contains volatile fragment %q", pat, tt.notContains)
			}
			if frac < 0 || frac > 1 {
				t.Errorf("matchFraction %v out of [0,1]", frac)
			}
		})
	}
}

func TestExtractPattern_Empty(t *testing.T) {
	pat, frac := ExtractPattern("")
	if pat != "" {
		t.Errorf("expected empty pattern, got %q", pat)
	}
	if frac != 1.0 {
		t.Errorf("expected frac=1.0 for empty msg, got %v", frac)
	}
}

func TestExtractPattern_TooGeneric(t *testing.T) {
	// A message that's entirely an IP should have frac > 0.60 and pattern returned empty by classifier,
	// but ExtractPattern itself still returns something — test frac > 0.
	_, frac := ExtractPattern("192.168.1.1 10.0.0.1 172.16.0.1")
	if frac <= 0 {
		t.Errorf("expected frac>0 for mostly-volatile message, got %v", frac)
	}
}

func TestEscapeForRegex(t *testing.T) {
	// Placeholders must not be escaped.
	pat := `connection to \S+ failed after \d+ retries`
	escaped := EscapeForRegex(pat)
	if !strings.Contains(escaped, `\S+`) {
		t.Errorf("EscapeForRegex stripped \\S+ placeholder: %q", escaped)
	}
	if !strings.Contains(escaped, `\d+`) {
		t.Errorf("EscapeForRegex stripped \\d+ placeholder: %q", escaped)
	}
	// Literal dots in the literal segment should be escaped.
	pat2 := `error.fatal`
	escaped2 := EscapeForRegex(pat2)
	if !strings.Contains(escaped2, `\.`) {
		t.Errorf("EscapeForRegex did not escape dot in %q: %q", pat2, escaped2)
	}
}
