package health

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StateTransition records a single health state change for a container.
type StateTransition struct {
	ContainerName string          `json:"container"`
	From          FunctionalState `json:"from"`
	To            FunctionalState `json:"to"`
	At            time.Time       `json:"at"`
	Reason        string          `json:"reason"`
}

// HistoryLog appends StateTransition entries to a newline-delimited JSON file
// (~/.errorprobe/state/history.jsonl) and prunes entries older than a retention window.
type HistoryLog struct {
	path string
}

// NewHistoryLog creates a HistoryLog that writes to path.
// The directory is created lazily on first Append.
func NewHistoryLog(path string) *HistoryLog {
	return &HistoryLog{path: path}
}

// Append serialises entry as a JSON object, appends it as a single line to the
// history file, and flushes.  The parent directory is created if absent.
func (h *HistoryLog) Append(entry StateTransition) error {
	if err := os.MkdirAll(filepath.Dir(h.path), 0o755); err != nil {
		return fmt.Errorf("creating history directory: %w", err)
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshalling transition: %w", err)
	}
	data = append(data, '\n')

	f, err := os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening history log: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("writing history entry: %w", err)
	}
	return nil
}

// Prune removes all entries whose At timestamp is older than retention from
// the history file, rewriting it atomically.  If the file does not exist,
// Prune is a no-op.
func (h *HistoryLog) Prune(retention time.Duration) error {
	raw, err := os.ReadFile(h.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading history log: %w", err)
	}

	cutoff := time.Now().Add(-retention)
	var kept [][]byte
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var t StateTransition
		if err := json.Unmarshal(line, &t); err != nil {
			// Keep lines we cannot parse to avoid data loss.
			kept = append(kept, line)
			continue
		}
		if t.At.After(cutoff) {
			kept = append(kept, line)
		}
	}

	// Build the replacement content: each kept entry on its own line.
	var out []byte
	if len(kept) > 0 {
		out = append(bytes.Join(kept, []byte("\n")), '\n')
	}

	tmp := h.path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("writing pruned history: %w", err)
	}
	if err := atomicReplace(h.path, tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replacing history file: %w", err)
	}
	return nil
}

// atomicReplace replaces dst with src. It removes dst first (ignoring
// ErrNotExist) before renaming src into place, which is necessary on Windows
// where os.Rename fails if the destination file already exists.
func atomicReplace(dst, src string) error {
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(src, dst)
}
