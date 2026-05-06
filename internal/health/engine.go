package health

import (
	"fmt"
	"sync"
	"time"

	"github.com/errorprobe/errorprobe/internal/ingest"
	"github.com/errorprobe/errorprobe/internal/logger"
)

// Engine maintains the current HealthSnapshot, processes incoming log batches,
// and persists state changes to disk.
type Engine struct {
	snapshot     HealthSnapshot
	mu           sync.RWMutex
	snapshotPath string
	onChange     func(HealthSnapshot)
}

// NewEngine creates an Engine that persists state to snapshotPath and calls
// onChange (if non-nil) whenever the snapshot changes.
// On startup it loads any existing snapshot from disk so state survives
// ErrorProbe restarts.
func NewEngine(snapshotPath string, onChange func(HealthSnapshot)) *Engine {
	e := &Engine{
		snapshotPath: snapshotPath,
		onChange:     onChange,
	}

	if snap, err := LoadSnapshot(snapshotPath); err == nil {
		e.snapshot = snap
	}

	if e.snapshot.Containers == nil {
		e.snapshot.Containers = make(map[string]ContainerHealth)
	}

	return e
}

// ProcessBatch applies a batch of log events to the snapshot.
// Events whose level is "error" or "warn" are treated as health-degrading.
// If the snapshot changes it is persisted and onChange is called.
func (e *Engine) ProcessBatch(events []ingest.LogEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()

	changed := false
	for _, ev := range events {
		if ev.Level == "error" || ev.Level == "warn" {
			key := logEventKey(ev)
			prevCount := 0
			if ch, ok := e.snapshot.Containers[key]; ok {
				prevCount = ch.ErrorCount
			}
			e.snapshot.SetError(key, ev.Message, ev.Timestamp)
			if ch, ok := e.snapshot.Containers[key]; ok && ch.ErrorCount != prevCount {
				changed = true
			}
			if ev.Level == "error" {
				// Track fingerprints for Tier 2 detection (error-level only).
				e.snapshot.RecordFingerprint(key, Fingerprint(ev.Message))
			}
		}
	}

	if changed {
		e.snapshot.SnapshotAt = time.Now()
		snap := e.snapshot.DeepCopy()
		if err := SaveSnapshot(e.snapshotPath, snap); err != nil {
			// Log but do not crash; state is still in memory.
			logger.Error("health engine: persist snapshot", "err", err)
		}
		if e.onChange != nil {
			e.onChange(snap)
		}
	}
}

// logEventKey returns the canonical health-snapshot key for a log event.
// It mirrors ContainerMeta.HealthKey() on the ingest side:
//   - K8s events (Namespace non-empty): "namespace/container_name"
//   - Docker events: bare container name
func logEventKey(ev ingest.LogEvent) string {
	if ev.Namespace != "" {
		return ev.Namespace + "/" + ev.Container
	}
	return ev.Container
}

// Snapshot returns a thread-safe deep copy of the current health snapshot.
// The returned snapshot has no shared map references with the engine.
func (e *Engine) Snapshot() HealthSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.snapshot.DeepCopy()
}

// Reset clears the health state for the named container, persists and notifies.
func (e *Engine) Reset(containerName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.snapshot.Reset(containerName)
	e.snapshot.SnapshotAt = time.Now()
	snap := e.snapshot

	if err := SaveSnapshot(e.snapshotPath, snap); err != nil {
		return fmt.Errorf("health engine: persist after reset: %w", err)
	}

	if e.onChange != nil {
		e.onChange(snap)
	}
	return nil
}

// SetFailing transitions the named container to the FAILING state, recording the
// dominant fingerprint and its occurrence count.  Persists and notifies onChange.
func (e *Engine) SetFailing(name, fingerprint string, count int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	ch := e.snapshot.Containers[name]
	ch.Name = name
	ch.State = StateFailing
	ch.DominantFingerprint = fingerprint
	ch.DominantFingerprintCount = count
	ch.LastUpdated = time.Now()
	if e.snapshot.Containers == nil {
		e.snapshot.Containers = make(map[string]ContainerHealth)
	}
	e.snapshot.Containers[name] = ch
	e.snapshot.SnapshotAt = time.Now()
	snap := e.snapshot

	if err := SaveSnapshot(e.snapshotPath, snap); err != nil {
		return fmt.Errorf("health engine: persist after SetFailing: %w", err)
	}
	if e.onChange != nil {
		e.onChange(snap)
	}
	return nil
}

// SetRecovered transitions a FAILING container back to HAS_ERRORS (not OK —
// errors did occur).  Clears the dominant fingerprint fields.  Persists and notifies.
func (e *Engine) SetRecovered(name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	ch := e.snapshot.Containers[name]
	ch.State = StateHasErrors
	ch.DominantFingerprint = ""
	ch.DominantFingerprintCount = 0
	ch.LastUpdated = time.Now()
	e.snapshot.Containers[name] = ch
	e.snapshot.SnapshotAt = time.Now()
	snap := e.snapshot

	if err := SaveSnapshot(e.snapshotPath, snap); err != nil {
		return fmt.Errorf("health engine: persist after SetRecovered: %w", err)
	}
	if e.onChange != nil {
		e.onChange(snap)
	}
	return nil
}