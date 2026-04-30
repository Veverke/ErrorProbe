package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds the full errorprobe.yaml configuration.
type Config struct {
	Version          int        `mapstructure:"version"`
	Stack            Stack      `mapstructure:"stack"`
	Detection        Detection  `mapstructure:"detection"`
	Containers       Containers `mapstructure:"containers"`
	Check            Check      `mapstructure:"check"`
	HistoryRetention string     `mapstructure:"history_retention"`
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
}

// SeverityPatterns maps level names to lists of matching strings.
type SeverityPatterns struct {
	Error []string `mapstructure:"error"`
	Warn  []string `mapstructure:"warn"`
}

// Containers holds container watch policy.
type Containers struct {
	Exclude []string `mapstructure:"exclude"`
}

// Check holds CI check settings.
type Check struct {
	FailOn  string   `mapstructure:"fail_on"`
	Exclude []string `mapstructure:"exclude"`
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
	v.SetDefault("check.fail_on", "HAS_ERRORS")
	v.SetDefault("check.exclude", []string{})
	v.SetDefault("containers.exclude", []string{})
	v.SetDefault("history_retention", "30d")
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}