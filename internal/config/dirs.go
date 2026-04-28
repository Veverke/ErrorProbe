package config

import (
	"fmt"
	"os"
)

// EnsureDirs creates the ~/.errorprobe/{configs,state,logs}/ directories if
// they do not already exist. It is idempotent.
func EnsureDirs(cfg *Config) error {
	dirs := []string{
		cfg.ConfigsDir(),
		cfg.StateDir(),
		cfg.LogsDir(),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("creating directory %s: %w", d, err)
		}
	}
	return nil
}
