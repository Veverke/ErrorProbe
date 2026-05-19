// Package learn implements the adaptive rule-learning module for ErrorProbe.
// It observes live log events and health-state transitions, identifies error
// patterns not covered by existing PBR rules, and generates new rule YAML that
// can be applied automatically or flagged for user review.
package learn

import (
	"time"

	"github.com/errorprobe/errorprobe/internal/ingest"
)

// SourceTag indicates how a learned rule was produced.
type SourceTag string

const (
	// SourceLearned means the rule was generated automatically and has not yet
	// been confirmed by the operator.
	SourceLearned SourceTag = "learned"
	// SourceConfirmed means the operator has explicitly validated the rule via
	// the 'v' key in the watch TUI.
	SourceConfirmed SourceTag = "confirmed"
)

// Candidate is a single uncovered log event paired with the pattern the
// extractor derived from it.
type Candidate struct {
	Event        ingest.LogEvent
	Pattern      string
	KeywordTier  int     // 3 = high, 2 = medium, 1 = low
	Windows      int     // number of sampling windows in which the pattern appeared
	MatchFraction float64 // fraction of original message chars replaced by placeholders
}

// ScoredCandidate combines a Candidate with its computed confidence score and
// the health state the learning module proposes to assign.
type ScoredCandidate struct {
	Candidate
	Score          float64
	GeneratedState string // "HAS_ERRORS" | "FAILING" | "DEGRADED"
}

// LearnedRule is the YAML-serialisable form of a rule stored in the overlay
// file. It carries the full rule definition plus provenance metadata.
type LearnedRule struct {
	Name         string            `yaml:"name"`
	Priority     int               `yaml:"priority"`
	Match        string            `yaml:"match"`
	When         map[string]string `yaml:"when,omitempty"`
	SetState     string            `yaml:"set_state"`
	Source       SourceTag         `yaml:"source"`
	DiscoveredAt time.Time         `yaml:"discovered_at"`
	ConfirmedAt  *time.Time        `yaml:"confirmed_at,omitempty"`
}

// SuppressionEntry records a message pattern that must never be re-learned.
// Entries are written when the operator presses 'f' (false-positive) in the
// watch TUI.
type SuppressionEntry struct {
	Pattern string    `yaml:"pattern"`
	AddedAt time.Time `yaml:"added_at"`
	Reason  string    `yaml:"reason,omitempty"`
}
