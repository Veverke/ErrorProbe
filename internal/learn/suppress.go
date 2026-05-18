package learn

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// SuppressionList is the in-memory view of the suppression file.
type SuppressionList struct {
	Entries []SuppressionEntry
	// path is the file location used by Save.
	path string
}

// suppressionFile is the top-level YAML structure for the on-disk format.
type suppressionFile struct {
	Suppressed []SuppressionEntry `yaml:"suppressed"`
}

// LoadSuppressionList loads the suppression list from path.
// If the file does not exist an empty list is returned without error.
func LoadSuppressionList(path string) (SuppressionList, error) {
	sl := SuppressionList{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sl, nil
		}
		return sl, fmt.Errorf("reading suppression file %s: %w", path, err)
	}
	var f suppressionFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return sl, fmt.Errorf("parsing suppression file %s: %w", path, err)
	}
	sl.Entries = f.Suppressed
	return sl, nil
}

// Contains reports whether pattern is already suppressed.
func (sl *SuppressionList) Contains(pattern string) bool {
	for _, e := range sl.Entries {
		if e.Pattern == pattern {
			return true
		}
	}
	return false
}

// Add appends entry to the list in memory; call Save to persist.
func (sl *SuppressionList) Add(entry SuppressionEntry) {
	sl.Entries = append(sl.Entries, entry)
}

// Save atomically writes the suppression list to the configured path.
func (sl *SuppressionList) Save() error {
	return SaveSuppressionList(sl.path, sl.Entries)
}

// SaveSuppressionList atomically writes entries to path.
func SaveSuppressionList(path string, entries []SuppressionEntry) error {
	f := suppressionFile{Suppressed: entries}
	data, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("marshalling suppression list: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing suppression file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replacing suppression file: %w", err)
	}
	return nil
}
