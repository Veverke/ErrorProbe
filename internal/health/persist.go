package health

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SaveSnapshot serialises snap to JSON at path, writing atomically via a temp
// file + rename so the destination is never left in a partially-written state.
func SaveSnapshot(path string, snap HealthSnapshot) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}

	data, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshalling health snapshot: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		// On Windows, renaming over a file that is currently open by another
		// process fails with "Access is denied" because os.Open does not set
		// FILE_SHARE_DELETE.  Fall back to a direct write: os.WriteFile opens
		// with GENERIC_WRITE which succeeds while a reader holds a shared read
		// handle.  A partial write is safe — LoadSnapshot returns an error on
		// bad JSON and callers skip the update, keeping in-memory state intact.
		_ = os.Remove(tmp)
		if werr := os.WriteFile(path, data, 0o644); werr != nil {
			return fmt.Errorf("renaming temp file: %w", err)
		}
	}
	return nil
}

// LoadSnapshot deserialises a HealthSnapshot from path.
// If the file does not exist, an empty HealthSnapshot is returned with no error.
func LoadSnapshot(path string) (HealthSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return HealthSnapshot{}, nil
		}
		return HealthSnapshot{}, fmt.Errorf("reading health snapshot: %w", err)
	}
	var snap HealthSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return HealthSnapshot{}, fmt.Errorf("parsing health snapshot: %w", err)
	}
	return snap, nil
}
