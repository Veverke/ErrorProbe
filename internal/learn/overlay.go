package learn

import (
	"errors"
	"fmt"
	"os"

	"github.com/errorprobe/errorprobe/internal/config"
	"gopkg.in/yaml.v3"
)

// overlayFile is the top-level YAML structure for the learned-rule overlay.
type overlayFile struct {
	Rules []LearnedRule `yaml:"rules"`
}

// LoadOverlay reads the overlay file at path and returns the slice of learned
// rules. Returns an empty slice (not an error) when the file does not exist.
func LoadOverlay(path string) ([]LearnedRule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading overlay file %s: %w", path, err)
	}
	var f overlayFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing overlay file %s: %w", path, err)
	}
	return f.Rules, nil
}

// SaveOverlay atomically writes rules to path, creating the file if needed.
func SaveOverlay(path string, rules []LearnedRule) error {
	f := overlayFile{Rules: rules}
	data, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("marshalling overlay: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing overlay file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replacing overlay file: %w", err)
	}
	return nil
}

// MergeOverlay combines user-defined config rules with learned overlay rules.
// For any rule whose name already appears in cfgRules, the overlay version is
// silently skipped (user-defined rules always win).
// The returned slice is safe to pass directly to pbr.Load.
func MergeOverlay(cfgRules []config.RuleConfig, overlay []LearnedRule) []config.RuleConfig {
	existing := make(map[string]struct{}, len(cfgRules))
	for _, r := range cfgRules {
		existing[r.Name] = struct{}{}
	}

	merged := make([]config.RuleConfig, len(cfgRules), len(cfgRules)+len(overlay))
	copy(merged, cfgRules)

	for _, lr := range overlay {
		if _, skip := existing[lr.Name]; skip {
			continue
		}
		merged = append(merged, lr.ToRuleConfig())
	}
	return merged
}

// ToRuleConfig converts a LearnedRule into the config.RuleConfig format
// expected by pbr.Load.
func (r LearnedRule) ToRuleConfig() config.RuleConfig {
	return config.RuleConfig{
		Name:     r.Name,
		Priority: r.Priority,
		Match:    r.Match,
		When:     r.When,
		SetState: r.SetState,
	}
}

// LoadPending reads the pending-rule scratch file at path.
// Returns an empty slice when the file does not exist.
func LoadPending(path string) ([]LearnedRule, error) {
	return LoadOverlay(path)
}

// SavePending atomically writes pending rules to path.
func SavePending(path string, rules []LearnedRule) error {
	return SaveOverlay(path, rules)
}
