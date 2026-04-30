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
			prevCount := 0
			if ch, ok := e.snapshot.Containers[ev.Container]; ok {
				prevCount = ch.ErrorCount
			}
			e.snapshot.SetError(ev.Container, ev.Message, ev.Timestamp)
			if ch, ok := e.snapshot.Containers[ev.Container]; ok && ch.ErrorCount != prevCount {
				changed = true
			}
		}
	}

	if changed {
		e.snapshot.SnapshotAt = time.Now()
		snap := e.snapshot
		if err := SaveSnapshot(e.snapshotPath, snap); err != nil {
			// Log but do not crash; state is still in memory.
			logger.Error("health engine: persist snapshot", "err", err)
		}
		if e.onChange != nil {
			e.onChange(snap)
		}
	}
}

// Snapshot returns a thread-safe copy of the current health snapshot.
func (e *Engine) Snapshot() HealthSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.snapshot
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