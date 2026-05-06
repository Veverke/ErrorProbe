//go:build integration

package e2e_test

// cmd_reload_test.go exercises stack.ClassifyChanges, which is the core logic
// of the `ep reload` command. These are pure-function tests: no Docker daemon,
// no running stack, and no subprocess invocation are required.

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/stack"
)

// baseCfg returns a config snapshot with default values so tests only need to
// override the fields they care about.
func baseCfg() *config.Config {
	return &config.Config{
		Stack: config.Stack{
			Loki:    config.LokiConfig{Image: "grafana/loki:3.0.0", Port: 3100},
			Grafana: config.GrafanaConfig{Image: "grafana/grafana:11.0.0", Port: 3000},
			Vector:  config.VectorConfig{Image: "timberio/vector:0.38.0-alpine"},
			Ingest:  config.IngestConfig{Port: 9099, Bind: "127.0.0.1", Transport: "http"},
		},
		Detection: config.Detection{
			SeverityPatterns: config.SeverityPatterns{
				Error: []string{"ERROR", "FATAL"},
				Warn:  []string{"WARN"},
			},
		},
		Containers: config.Containers{Exclude: []string{}},
		Check:      config.Check{FailOn: "HAS_ERRORS", Exclude: []string{}},
	}
}

// ---------------------------------------------------------------------------
// TestClassifyChanges_NoChanges (#41 — no-op case)
// ---------------------------------------------------------------------------

func TestClassifyChanges_NoChanges(t *testing.T) {
	prev := baseCfg()
	curr := baseCfg()

	cs := stack.ClassifyChanges(prev, curr)

	assert.False(t, cs.HasSoft, "identical configs must produce no soft changes")
	assert.False(t, cs.HasHard, "identical configs must produce no hard changes")
	assert.Empty(t, cs.SoftChanges)
	assert.Empty(t, cs.HardChanges)
}

// ---------------------------------------------------------------------------
// TestClassifyChanges_SoftOnly_SeverityPattern
// ---------------------------------------------------------------------------

// TestClassifyChanges_SoftOnly_SeverityPattern verifies that a change to
// detection.severity_patterns.error is classified as a soft change (Vector
// SIGHUP is sufficient; no container recreation needed).
func TestClassifyChanges_SoftOnly_SeverityPattern(t *testing.T) {
	prev := baseCfg()
	curr := baseCfg()
	curr.Detection.SeverityPatterns.Error = append(curr.Detection.SeverityPatterns.Error, "SEVERE")

	cs := stack.ClassifyChanges(prev, curr)

	assert.True(t, cs.HasSoft, "severity pattern change must be a soft change")
	assert.False(t, cs.HasHard, "severity pattern change must not be a hard change")
}

// ---------------------------------------------------------------------------
// TestClassifyChanges_SoftOnly_ExcludeList
// ---------------------------------------------------------------------------

// TestClassifyChanges_SoftOnly_ExcludeList verifies that adding a container to
// containers.exclude is a soft change.
func TestClassifyChanges_SoftOnly_ExcludeList(t *testing.T) {
	prev := baseCfg()
	curr := baseCfg()
	curr.Containers.Exclude = []string{"debug-sidecar"}

	cs := stack.ClassifyChanges(prev, curr)

	assert.True(t, cs.HasSoft)
	assert.False(t, cs.HasHard)
}

// ---------------------------------------------------------------------------
// TestClassifyChanges_HardOnly_LokiPort (#41 — port change)
// ---------------------------------------------------------------------------

// TestClassifyChanges_HardOnly_LokiPort verifies that changing the Loki port
// is classified as a hard change (Loki container must be recreated).
func TestClassifyChanges_HardOnly_LokiPort(t *testing.T) {
	prev := baseCfg()
	curr := baseCfg()
	curr.Stack.Loki.Port = 3200

	cs := stack.ClassifyChanges(prev, curr)

	assert.False(t, cs.HasSoft)
	assert.True(t, cs.HasHard, "Loki port change must be a hard change")
	assert.NotEmpty(t, cs.HardChanges)
}

// ---------------------------------------------------------------------------
// TestClassifyChanges_HardOnly_ImageChange
// ---------------------------------------------------------------------------

// TestClassifyChanges_HardOnly_ImageChange verifies that upgrading a container
// image is classified as a hard change.
func TestClassifyChanges_HardOnly_ImageChange(t *testing.T) {
	prev := baseCfg()
	curr := baseCfg()
	curr.Stack.Vector.Image = "timberio/vector:0.39.0-alpine"

	cs := stack.ClassifyChanges(prev, curr)

	assert.False(t, cs.HasSoft)
	assert.True(t, cs.HasHard, "image upgrade must be a hard change")
}

// ---------------------------------------------------------------------------
// TestClassifyChanges_Mixed_PortAndPattern
// ---------------------------------------------------------------------------

// TestClassifyChanges_Mixed_PortAndPattern verifies that a config with both a
// soft change (severity pattern) and a hard change (port) sets both flags.
func TestClassifyChanges_Mixed_PortAndPattern(t *testing.T) {
	prev := baseCfg()
	curr := baseCfg()
	curr.Stack.Grafana.Port = 3001                                                       // hard
	curr.Detection.SeverityPatterns.Warn = []string{"WARN", "WARNING", "warn", "CAUTION"} // soft

	cs := stack.ClassifyChanges(prev, curr)

	assert.True(t, cs.HasSoft, "severity pattern change must register as soft")
	assert.True(t, cs.HasHard, "port change must register as hard")
}

// ---------------------------------------------------------------------------
// TestClassifyChanges_CheckFailOn_IsSoft
// ---------------------------------------------------------------------------

// TestClassifyChanges_CheckFailOn_IsSoft verifies that changing check.fail_on
// is a soft change (no container restart needed).
func TestClassifyChanges_CheckFailOn_IsSoft(t *testing.T) {
	prev := baseCfg()
	curr := baseCfg()
	curr.Check.FailOn = "FAILING"

	cs := stack.ClassifyChanges(prev, curr)

	assert.True(t, cs.HasSoft)
	assert.False(t, cs.HasHard)
}
