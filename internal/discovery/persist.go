package discovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SaveWatchSet serialises ws to JSON at the given path, writing atomically.
// It writes to a temporary file alongside the destination and then renames it.
func SaveWatchSet(path string, ws WatchSet) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}

	data, err := json.Marshal(ws)
	if err != nil {
		return fmt.Errorf("marshalling watch set: %w", err)
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

// LoadWatchSet deserialises a WatchSet from path.
// If the file does not exist, an empty WatchSet is returned with no error.
func LoadWatchSet(path string) (WatchSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return WatchSet{}, nil
		}
		return WatchSet{}, fmt.Errorf("reading watch set: %w", err)
	}
	var ws WatchSet
	if err := json.Unmarshal(data, &ws); err != nil {
		return WatchSet{}, fmt.Errorf("parsing watch set: %w", err)
	}
	return ws, nil
}
