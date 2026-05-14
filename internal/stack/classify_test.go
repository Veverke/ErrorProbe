package stack_test

import (
	"testing"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/stack"
)

func baseCfg() *config.Config {
	return &config.Config{
		Version: 1,
		Stack: config.Stack{
			Loki:    config.LokiConfig{Image: "grafana/loki:3.0.0", Port: 3100},
			Grafana: config.GrafanaConfig{Image: "grafana/grafana:11.0.0", Port: 3000},
			Vector:  config.VectorConfig{Image: "timberio/vector:0.38.0-alpine"},
			Ingest:  config.IngestConfig{Transport: "http", Port: 9099, Bind: "127.0.0.1"},
		},
		Detection: config.Detection{
			SeverityPatterns: config.SeverityPatterns{
				Error: []string{"error", "ERROR"},
				Warn:  []string{"warn", "WARN"},
			},
		},
		Containers: config.Containers{Exclude: []string{}},
		Check:      config.Check{FailOn: "HAS_ERRORS", Exclude: []string{}},
	}
}

func TestClassifyChanges_NoChange(t *testing.T) {
	a := baseCfg()
	b := baseCfg()
	cs := stack.ClassifyChanges(a, b)
	if cs.HasSoft || cs.HasHard {
		t.Errorf("expected no changes; got soft=%v hard=%v", cs.SoftChanges, cs.HardChanges)
	}
}

func TestClassifyChanges_SeverityPattern_IsSoft(t *testing.T) {
	a := baseCfg()
	b := baseCfg()
	b.Detection.SeverityPatterns.Error = append(b.Detection.SeverityPatterns.Error, "CRITICAL")
	cs := stack.ClassifyChanges(a, b)
	if !cs.HasSoft {
		t.Error("expected HasSoft true")
	}
	if cs.HasHard {
		t.Error("expected HasHard false")
	}
	if len(cs.SoftChanges) == 0 {
		t.Error("expected SoftChanges to be non-empty")
	}
}

func TestClassifyChanges_WarnPattern_IsSoft(t *testing.T) {
	a := baseCfg()
	b := baseCfg()
	b.Detection.SeverityPatterns.Warn = []string{"warning"}
	cs := stack.ClassifyChanges(a, b)
	if !cs.HasSoft {
		t.Error("expected HasSoft true for warn pattern change")
	}
}

func TestClassifyChanges_ImageVersion_IsHard(t *testing.T) {
	a := baseCfg()
	b := baseCfg()
	b.Stack.Loki.Image = "grafana/loki:3.1.0"
	cs := stack.ClassifyChanges(a, b)
	if !cs.HasHard {
		t.Error("expected HasHard true")
	}
	if cs.HasSoft {
		t.Error("expected HasSoft false")
	}
	if len(cs.HardChanges) == 0 {
		t.Error("expected HardChanges to be non-empty")
	}
}

func TestClassifyChanges_GrafanaImage_IsHard(t *testing.T) {
	a := baseCfg()
	b := baseCfg()
	b.Stack.Grafana.Image = "grafana/grafana:12.0.0"
	cs := stack.ClassifyChanges(a, b)
	if !cs.HasHard {
		t.Error("expected HasHard true for grafana image change")
	}
}

func TestClassifyChanges_VectorImage_IsHard(t *testing.T) {
	a := baseCfg()
	b := baseCfg()
	b.Stack.Vector.Image = "timberio/vector:0.39.0-alpine"
	cs := stack.ClassifyChanges(a, b)
	if !cs.HasHard {
		t.Error("expected HasHard true for vector image change")
	}
}

func TestClassifyChanges_Port_IsHard(t *testing.T) {
	a := baseCfg()
	b := baseCfg()
	b.Stack.Grafana.Port = 3001
	cs := stack.ClassifyChanges(a, b)
	if !cs.HasHard {
		t.Error("expected HasHard true for grafana port change")
	}
	if cs.HasSoft {
		t.Error("expected HasSoft false")
	}
}

func TestClassifyChanges_LokiPort_IsHard(t *testing.T) {
	a := baseCfg()
	b := baseCfg()
	b.Stack.Loki.Port = 3101
	cs := stack.ClassifyChanges(a, b)
	if !cs.HasHard {
		t.Error("expected HasHard true for loki port change")
	}
}

func TestClassifyChanges_IngestPort_IsHard(t *testing.T) {
	a := baseCfg()
	b := baseCfg()
	b.Stack.Ingest.Port = 9100
	cs := stack.ClassifyChanges(a, b)
	if !cs.HasHard {
		t.Error("expected HasHard true for ingest port change")
	}
}

func TestClassifyChanges_IngestBind_IsHard(t *testing.T) {
	a := baseCfg()
	b := baseCfg()
	b.Stack.Ingest.Bind = "0.0.0.0"
	cs := stack.ClassifyChanges(a, b)
	if !cs.HasHard {
		t.Error("expected HasHard true for ingest bind change")
	}
}

func TestClassifyChanges_IngestTransport_IsHard(t *testing.T) {
	a := baseCfg()
	b := baseCfg()
	b.Stack.Ingest.Transport = "grpc"
	cs := stack.ClassifyChanges(a, b)
	if !cs.HasHard {
		t.Error("expected HasHard true for ingest transport change")
	}
}

func TestClassifyChanges_Mixed(t *testing.T) {
	a := baseCfg()
	b := baseCfg()
	// Hard change: loki image
	b.Stack.Loki.Image = "grafana/loki:3.1.0"
	// Soft change: exclude list
	b.Containers.Exclude = []string{"noisy-app"}
	cs := stack.ClassifyChanges(a, b)
	if !cs.HasHard {
		t.Error("expected HasHard true")
	}
	if !cs.HasSoft {
		t.Error("expected HasSoft true")
	}
}

func TestClassifyChanges_ExcludeList_IsSoft(t *testing.T) {
	a := baseCfg()
	b := baseCfg()
	b.Containers.Exclude = []string{"skip-me"}
	cs := stack.ClassifyChanges(a, b)
	if !cs.HasSoft {
		t.Error("expected HasSoft true")
	}
	if cs.HasHard {
		t.Error("expected HasHard false")
	}
}

func TestClassifyChanges_CheckFailOn_IsSoft(t *testing.T) {
	a := baseCfg()
	b := baseCfg()
	b.Check.FailOn = "FAILING"
	cs := stack.ClassifyChanges(a, b)
	if !cs.HasSoft {
		t.Error("expected HasSoft true for check.fail_on change")
	}
}

func TestClassifyChanges_CheckExclude_IsSoft(t *testing.T) {
	a := baseCfg()
	b := baseCfg()
	b.Check.Exclude = []string{"excluded-app"}
	cs := stack.ClassifyChanges(a, b)
	if !cs.HasSoft {
		t.Error("expected HasSoft true for check.exclude change")
	}
}

// T7.4 — Rules-change classification.

func TestClassifyChanges_Rules_IsSoft(t *testing.T) {
	a := baseCfg()
	b := baseCfg()
	b.Rules = []config.RuleConfig{
		{Name: "custom-rule", Priority: 200, Match: "log", When: map[string]string{"level": "error"}, SetState: "HAS_ERRORS"},
	}
	cs := stack.ClassifyChanges(a, b)
	if !cs.HasSoft {
		t.Error("expected HasSoft true when rules slice changes")
	}
	if cs.HasHard {
		t.Error("expected HasHard false when only rules change")
	}
}

func TestClassifyChanges_Rules_Removed_IsSoft(t *testing.T) {
	a := baseCfg()
	a.Rules = []config.RuleConfig{
		{Name: "existing-rule", Priority: 200, Match: "log", When: map[string]string{"level": "error"}, SetState: "HAS_ERRORS"},
	}
	b := baseCfg()
	// b.Rules is nil — rules removed.
	cs := stack.ClassifyChanges(a, b)
	if !cs.HasSoft {
		t.Error("expected HasSoft true when rules are removed")
	}
}

func TestClassifyChanges_ContainerOverrides_IsSoft(t *testing.T) {
	a := baseCfg()
	b := baseCfg()
	b.ContainerOverrides = map[string][]config.RuleConfig{
		"my-svc": {{Name: "tolerate-restart", Priority: 300, Match: "infra", When: map[string]string{"restart_count": "> 0"}, SetState: "OK"}},
	}
	cs := stack.ClassifyChanges(a, b)
	if !cs.HasSoft {
		t.Error("expected HasSoft true when container_overrides change")
	}
}
