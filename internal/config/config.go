package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds the full errorprobe.yaml configuration.
type Config struct {
	Version            int                      `mapstructure:"version"`
	Stack              Stack                    `mapstructure:"stack"`
	Detection          Detection                `mapstructure:"detection"`
	Containers         Containers               `mapstructure:"containers"`
	K8s                K8sConfig                `mapstructure:"k8s"`
	Check              Check                    `mapstructure:"check"`
	HistoryRetention   string                   `mapstructure:"history_retention"`
	Rules              []RuleConfig             `mapstructure:"rules"`
	ContainerOverrides map[string][]RuleConfig  `mapstructure:"container_overrides"`
}

// RuleConfig is the raw, unvalidated representation of a PBR rule as loaded
// from errorprobe.yaml. It is converted into a compiled pbr.Rule by pbr.Load.
type RuleConfig struct {
	Name     string            `mapstructure:"name"`
	Priority int               `mapstructure:"priority"`
	Match    string            `mapstructure:"match"`
	When     map[string]string `mapstructure:"when"`
	SetState string            `mapstructure:"set_state"`
}

// Stack groups all container image and port settings.
type Stack struct {
	Vector  VectorConfig  `mapstructure:"vector"`
	Loki    LokiConfig    `mapstructure:"loki"`
	Grafana GrafanaConfig `mapstructure:"grafana"`
	Ingest  IngestConfig  `mapstructure:"ingest"`
}

// VectorConfig holds Vector image settings.
type VectorConfig struct {
	Image string `mapstructure:"image"`
}

// LokiConfig holds Loki image and runtime settings.
type LokiConfig struct {
	Image     string `mapstructure:"image"`
	Port      int    `mapstructure:"port"`
	Retention string `mapstructure:"retention"`
}

// GrafanaConfig holds Grafana image and runtime settings.
type GrafanaConfig struct {
	Image string `mapstructure:"image"`
	Port  int    `mapstructure:"port"`
}

// IngestConfig holds the HTTP/gRPC ingest listener settings.
type IngestConfig struct {
	Transport string `mapstructure:"transport"`
	Port      int    `mapstructure:"port"`
	Bind      string `mapstructure:"bind"`
}

// Detection holds severity detection settings.
type Detection struct {
	SeverityPatterns SeverityPatterns `mapstructure:"severity_patterns"`
	Tier2            Tier2Config      `mapstructure:"tier2"`
}

// Tier2Config holds Tier 2 detection settings (FAILING state).
type Tier2Config struct {
	Window    string `mapstructure:"window"`    // LogQL duration window, e.g. "3m"
	Threshold int    `mapstructure:"threshold"` // minimum error count to trigger FAILING
	Tick      string `mapstructure:"tick"`      // evaluation interval, e.g. "30s"
}

// SeverityPatterns maps level names to lists of matching strings.
type SeverityPatterns struct {
	Error []string `mapstructure:"error"`
	Warn  []string `mapstructure:"warn"`
}

// Containers holds container watch policy.
type Containers struct {
	Exclude []string `mapstructure:"exclude"`
	// Include, when non-empty, acts as an allow-list: only containers that match
	// at least one pattern here are watched (after Exclude is applied).
	// Supports the same "pod/<glob>", "namespace/<glob>", and "<name glob>" syntax
	// as Exclude.  Useful for "infra-only" mode: set include to
	// ["namespace/kube-system"] to watch only Kubernetes infrastructure pods.
	Include []string `mapstructure:"include"`
	// DisplayNamePatterns is a list of regular expressions used to normalise
	// container names for display.  Each pattern must contain exactly one
	// capture group; when a container name matches, group 1 becomes its display
	// name.  Patterns are evaluated in order; the first match wins.
	// Leave empty (or omit from errorprobe.yaml) to use DefaultDisplayNamePatterns.
	DisplayNamePatterns []string `mapstructure:"display_name_patterns"`
}

// DefaultDisplayNamePatterns are the display-name normalisation regexes shipped
// with ErrorProbe.  They strip the noisy random suffixes that Kubernetes appends
// to container names.  Used whenever Containers.DisplayNamePatterns is empty.
var DefaultDisplayNamePatterns = []string{
	// K8s StatefulSet / Job / Deployment instance suffix: strip the trailing 5-char random suffix.
	// e.g.  selling-counter-couchdb-vx8fw  →  selling-counter-couchdb
	// e.g.  payments-api-7d9f6b8c4-vx8fw  →  payments-api-7d9f6b8c4
	// (For Deployment names the ReplicaSet hash remains; it is stable across restarts
	//  and far less noisy than the ever-changing instance suffix.)
	`^(.*)-[a-z0-9]{5}$`,
}

// K8sConfig holds Kubernetes discovery settings.
type K8sConfig struct {
	// ExcludeNamespaces lists namespaces to exclude from discovery.
	// Defaults to ["kube-system", "kube-public", "kube-node-lease"] when empty.
	ExcludeNamespaces []string `mapstructure:"exclude_namespaces"`
}

// Check holds CI check settings.
type Check struct {
	FailOn  string   `mapstructure:"fail_on"`
	Exclude []string `mapstructure:"exclude"`
}

// DataDir returns the root ~/.errorprobe/ directory that holds all
// errorprobe-managed data (configs, state, logs).
func (c *Config) DataDir() string {
	return filepath.Join(homeDir(), ".errorprobe") + string(filepath.Separator)
}

// StateDir returns the path to the state directory.
func (c *Config) StateDir() string {
	return filepath.Join(homeDir(), ".errorprobe", "state") + string(filepath.Separator)
}

// ConfigsDir returns the path to the generated configs directory.
func (c *Config) ConfigsDir() string {
	return filepath.Join(homeDir(), ".errorprobe", "configs") + string(filepath.Separator)
}

// LogsDir returns the path to the logs directory.
func (c *Config) LogsDir() string {
	return filepath.Join(homeDir(), ".errorprobe", "logs") + string(filepath.Separator)
}

// Load reads configuration from projectDir/errorprobe.yaml, then
// ~/.errorprobe/config.yaml, falling back to built-in defaults.
// If projectDir is empty, only the global file and defaults are used.
func Load(projectDir string) (*Config, error) {
	localPath := "errorprobe.yaml"
	if projectDir != "" {
		localPath = filepath.Join(projectDir, "errorprobe.yaml")
	}

	globalPath := filepath.Join(homeDir(), ".errorprobe", "config.yaml")

	// Merge: defaults → global → project-local (project-local wins).
	merged := viper.New()
	setDefaults(merged)
	merged.SetConfigType("yaml")

	// Load global if present.
	if _, err := os.Stat(globalPath); err == nil {
		merged.SetConfigFile(globalPath)
		if err := merged.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("reading global config %s: %w", globalPath, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("checking global config %s: %w", globalPath, err)
	}

	// Load project-local if present (overrides global).
	if _, err := os.Stat(localPath); err == nil {
		override := viper.New()
		override.SetConfigFile(localPath)
		if err := override.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("reading project config %s: %w", localPath, err)
		}
		for _, key := range override.AllKeys() {
			merged.Set(key, override.Get(key))
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("checking project config %s: %w", localPath, err)
	}

	var cfg Config
	if err := merged.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshalling config: %w", err)
	}

	if cfg.Version != 1 {
		return nil, fmt.Errorf("unsupported version %d: only version 1 is supported", cfg.Version)
	}

	if cfg.Check.FailOn != "" && cfg.Check.FailOn != "HAS_ERRORS" && cfg.Check.FailOn != "FAILING" {
		return nil, fmt.Errorf("invalid check.fail_on %q: must be HAS_ERRORS or FAILING", cfg.Check.FailOn)
	}

	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("version", 1)
	v.SetDefault("stack.vector.image", "timberio/vector:0.38.0-alpine")
	v.SetDefault("stack.loki.image", "grafana/loki:3.0.0")
	v.SetDefault("stack.loki.port", 3100)
	v.SetDefault("stack.loki.retention", "72h")
	v.SetDefault("stack.grafana.image", "grafana/grafana:11.0.0")
	v.SetDefault("stack.grafana.port", 3000)
	v.SetDefault("stack.ingest.transport", "http")
	v.SetDefault("stack.ingest.port", 9099)
	v.SetDefault("stack.ingest.bind", "127.0.0.1")
	v.SetDefault("detection.severity_patterns.error", []string{"ERROR", "FATAL", "panic", "Exception", "error"})
	v.SetDefault("detection.severity_patterns.warn", []string{"WARN", "WARNING", "warn"})
	v.SetDefault("detection.tier2.window", "3m")
	v.SetDefault("detection.tier2.threshold", 10)
	v.SetDefault("detection.tier2.tick", "30s")
	v.SetDefault("check.fail_on", "HAS_ERRORS")
	v.SetDefault("check.exclude", []string{})
	v.SetDefault("containers.exclude", []string{})
	v.SetDefault("history_retention", "30d")
}

// ParseDuration extends time.ParseDuration with support for the "d" (days) suffix.
// Examples: "30d" → 720h, "72h" → 72h, "3m" → 3m.
func ParseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// ConfigFilePath returns the path of the config file that EP would write to
// when making programmatic changes (e.g. adding a container to the exclude list).
//
// Resolution order (mirrors Load):
//  1. If projectDir is non-empty → projectDir/errorprobe.yaml
//  2. If ./errorprobe.yaml exists → ./errorprobe.yaml
//  3. Fall back to the global ~/.errorprobe/config.yaml
func ConfigFilePath(projectDir string) string {
	if projectDir != "" {
		return filepath.Join(projectDir, "errorprobe.yaml")
	}
	local := "errorprobe.yaml"
	if _, err := os.Stat(local); err == nil {
		return local
	}
	return filepath.Join(homeDir(), ".errorprobe", "config.yaml")
}

// AppendExclude adds pattern to the containers.exclude list in the config file
// at cfgPath, preserving all existing content including inline comments.
//
// If pattern is already present the file is left unchanged.
// If cfgPath does not exist it is created with a minimal containers.exclude section.
func AppendExclude(cfgPath, pattern string) error {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			content := "containers:\n  exclude:\n    - " + pattern + "\n"
			return os.WriteFile(cfgPath, []byte(content), 0o644)
		}
		return fmt.Errorf("reading config %s: %w", cfgPath, err)
	}

	lines := strings.Split(string(data), "\n")

	// Do not add a duplicate.
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "- "+pattern || strings.HasPrefix(t, "- "+pattern+" ") {
			return nil
		}
	}

	// Locate the insertion point by walking the lines with a small state machine.
	//
	// States:
	//   inContainers  — we are inside the containers: block (indent 0)
	//   inExclude     — we are inside the containers.exclude: block (indent 2)
	//
	// insertAfter   = index of last "    - " item in the exclude block (indent 4)
	// excludeHeader = index of the "  exclude:" line (fallback when list is empty)
	inContainers := false
	inExclude := false
	insertAfter := -1
	excludeHeader := -1

	for i, line := range lines {
		noTrail := strings.TrimRight(line, " \t\r")
		content := strings.TrimLeft(noTrail, " \t")
		indent := len(noTrail) - len(content)
		blank := content == "" || strings.HasPrefix(content, "#")
		if blank {
			continue
		}

		if indent == 0 {
			inContainers = (noTrail == "containers:")
			inExclude = false
		} else if inContainers && indent == 2 && strings.HasPrefix(content, "exclude:") {
			inExclude = true
			excludeHeader = i
		} else if inContainers && inExclude && indent == 4 && strings.HasPrefix(content, "- ") {
			insertAfter = i
		} else if inExclude && indent <= 2 && !strings.HasPrefix(content, "- ") {
			// Exited the exclude list (another key at containers level).
			inExclude = false
		}
	}

	newEntry := "    - " + pattern

	if insertAfter >= 0 {
		out := make([]string, 0, len(lines)+1)
		out = append(out, lines[:insertAfter+1]...)
		out = append(out, newEntry)
		out = append(out, lines[insertAfter+1:]...)
		return os.WriteFile(cfgPath, []byte(strings.Join(out, "\n")), 0o644)
	}
	if excludeHeader >= 0 {
		// exclude: exists but has no items yet — insert directly after the header.
		out := make([]string, 0, len(lines)+1)
		out = append(out, lines[:excludeHeader+1]...)
		out = append(out, newEntry)
		out = append(out, lines[excludeHeader+1:]...)
		return os.WriteFile(cfgPath, []byte(strings.Join(out, "\n")), 0o644)
	}

	// No containers.exclude section found anywhere — append to the file.
	content := strings.TrimRight(string(data), "\n") + "\ncontainers:\n  exclude:\n    - " + pattern + "\n"
	return os.WriteFile(cfgPath, []byte(content), 0o644)
}
