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
		_ = os.Remove(tmp)
		return fmt.Errorf("renaming temp file: %w", err)
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
