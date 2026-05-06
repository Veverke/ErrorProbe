package health

import "time"

// FunctionalState represents the derived health state for a container.
type FunctionalState string

const (
	StateOK        FunctionalState = "OK"
	StateHasErrors FunctionalState = "HAS_ERRORS"
	StateFailing   FunctionalState = "FAILING" // reserved for Phase 6
)

// ContainerHealth holds the current health state for a single container.
type ContainerHealth struct {
	Name                     string         `json:"name"`
	State                    FunctionalState `json:"state"`
	ErrorCount               int            `json:"error_count"`
	FirstErrorAt             *time.Time     `json:"first_error_at,omitempty"`
	LastErrorAt              *time.Time     `json:"last_error_at,omitempty"`
	LastErrorMsg             string         `json:"last_error_msg"`
	LastUpdated              time.Time      `json:"last_updated"`
	Fingerprints             map[string]int `json:"fingerprints,omitempty"`                // fingerprint → occurrence count
	DominantFingerprint      string         `json:"dominant_fingerprint,omitempty"`        // set when FAILING
	DominantFingerprintCount int            `json:"dominant_fingerprint_count,omitempty"`  // count when FAILING
	MatchedRule              string         `json:"matched_rule,omitempty"`                // PBR rule name that last set the state
}

// HealthSnapshot is an immutable-by-convention snapshot of all container health states.
type HealthSnapshot struct {
	Containers map[string]ContainerHealth `json:"containers"`
	SnapshotAt time.Time                  `json:"snapshot_at"`
}

// SetError upserts the container entry, flipping state to HAS_ERRORS and incrementing count.
// FirstErrorAt is only set on the first error; LastErrorAt is always updated.
// StateFailing is preserved — it can only be cleared by Tier2 recovery logic (Reset or an
// explicit state transition).
func (s *HealthSnapshot) SetError(name, msg string, at time.Time) {
	if s.Containers == nil {
		s.Containers = make(map[string]ContainerHealth)
	}
	ch := s.Containers[name]
	ch.Name = name
	if ch.State != StateFailing {
		ch.State = StateHasErrors
	}
	ch.ErrorCount++
	ch.LastErrorMsg = msg
	ch.LastUpdated = at
	if ch.FirstErrorAt == nil {
		t := at
		ch.FirstErrorAt = &t
	}
	t := at
	ch.LastErrorAt = &t
	s.Containers[name] = ch
}

// RecordFingerprint increments the occurrence count of fingerprint for the named container.
// It initialises the Fingerprints map if necessary.
func (s *HealthSnapshot) RecordFingerprint(name, fingerprint string) {
	if s.Containers == nil {
		s.Containers = make(map[string]ContainerHealth)
	}
	ch := s.Containers[name]
	if ch.Fingerprints == nil {
		ch.Fingerprints = make(map[string]int)
	}
	ch.Fingerprints[fingerprint]++
	s.Containers[name] = ch
}

// DeepCopy returns a new HealthSnapshot with an independent copy of the Containers map
// and each ContainerHealth's Fingerprints map, so callers cannot accidentally share
// mutable state with the engine.
func (s HealthSnapshot) DeepCopy() HealthSnapshot {
	cp := HealthSnapshot{
		SnapshotAt: s.SnapshotAt,
		Containers: make(map[string]ContainerHealth, len(s.Containers)),
	}
	for k, ch := range s.Containers {
		if ch.Fingerprints != nil {
			fps := make(map[string]int, len(ch.Fingerprints))
			for fp, count := range ch.Fingerprints {
				fps[fp] = count
			}
			ch.Fingerprints = fps
		}
		cp.Containers[k] = ch
	}
	return cp
}

// Reset sets the named container state back to OK and clears counts and timestamps.
func (s *HealthSnapshot) Reset(name string) {
	if s.Containers == nil {
		s.Containers = make(map[string]ContainerHealth)
	}
	ch := s.Containers[name]
	ch.Name = name
	ch.State = StateOK
	ch.ErrorCount = 0
	ch.FirstErrorAt = nil
	ch.LastErrorAt = nil
	ch.LastErrorMsg = ""
	ch.Fingerprints = nil
	ch.DominantFingerprint = ""
	ch.DominantFingerprintCount = 0
	ch.MatchedRule = ""
	ch.LastUpdated = time.Now()
	s.Containers[name] = ch
}
