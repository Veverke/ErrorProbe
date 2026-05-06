package stack

import (
	"fmt"
	"reflect"

	"github.com/errorprobe/errorprobe/internal/config"
)

// ChangeSet is the result of classifying configuration differences between two
// Config snapshots.  Soft changes can be applied via a Vector SIGHUP; hard
// changes require one or more containers to be recreated.
type ChangeSet struct {
	SoftChanges []string // human-readable descriptions of soft changes
	HardChanges []string // human-readable descriptions of hard changes
	HasSoft     bool
	HasHard     bool
}

// ClassifyChanges inspects every relevant field of previous and current and
// sorts each difference into soft or hard.  It is a pure function with no
// side-effects.
//
// Soft changes: detection.severity_patterns, containers.exclude, check.*
// Hard changes: any stack.*.image, any stack.*.port, stack.ingest.bind,
//
//	stack.ingest.transport
func ClassifyChanges(previous *config.Config, current *config.Config) ChangeSet {
	var cs ChangeSet

	addSoft := func(msg string) {
		cs.SoftChanges = append(cs.SoftChanges, msg)
		cs.HasSoft = true
	}
	addHard := func(msg string) {
		cs.HardChanges = append(cs.HardChanges, msg)
		cs.HasHard = true
	}

	// --- Hard changes ---

	if previous.Stack.Loki.Image != current.Stack.Loki.Image {
		addHard(fmt.Sprintf("loki image: %s → %s", previous.Stack.Loki.Image, current.Stack.Loki.Image))
	}
	if previous.Stack.Grafana.Image != current.Stack.Grafana.Image {
		addHard(fmt.Sprintf("grafana image: %s → %s", previous.Stack.Grafana.Image, current.Stack.Grafana.Image))
	}
	if previous.Stack.Vector.Image != current.Stack.Vector.Image {
		addHard(fmt.Sprintf("vector image: %s → %s", previous.Stack.Vector.Image, current.Stack.Vector.Image))
	}
	if previous.Stack.Loki.Port != current.Stack.Loki.Port {
		addHard(fmt.Sprintf("loki port: %d → %d", previous.Stack.Loki.Port, current.Stack.Loki.Port))
	}
	if previous.Stack.Grafana.Port != current.Stack.Grafana.Port {
		addHard(fmt.Sprintf("grafana port: %d → %d", previous.Stack.Grafana.Port, current.Stack.Grafana.Port))
	}
	if previous.Stack.Ingest.Port != current.Stack.Ingest.Port {
		addHard(fmt.Sprintf("ingest port: %d → %d", previous.Stack.Ingest.Port, current.Stack.Ingest.Port))
	}
	if previous.Stack.Ingest.Bind != current.Stack.Ingest.Bind {
		addHard(fmt.Sprintf("ingest bind: %q → %q", previous.Stack.Ingest.Bind, current.Stack.Ingest.Bind))
	}
	if previous.Stack.Ingest.Transport != current.Stack.Ingest.Transport {
		addHard(fmt.Sprintf("ingest transport: %q → %q", previous.Stack.Ingest.Transport, current.Stack.Ingest.Transport))
	}

	// --- Soft changes ---

	if !stringSlicesEqual(previous.Detection.SeverityPatterns.Error, current.Detection.SeverityPatterns.Error) {
		addSoft("detection.severity_patterns.error changed")
	}
	if !stringSlicesEqual(previous.Detection.SeverityPatterns.Warn, current.Detection.SeverityPatterns.Warn) {
		addSoft("detection.severity_patterns.warn changed")
	}
	if !stringSlicesEqual(previous.Containers.Exclude, current.Containers.Exclude) {
		addSoft("containers.exclude changed")
	}
	if previous.Check.FailOn != current.Check.FailOn {
		addSoft(fmt.Sprintf("check.fail_on: %q → %q", previous.Check.FailOn, current.Check.FailOn))
	}
	if !stringSlicesEqual(previous.Check.Exclude, current.Check.Exclude) {
		addSoft("check.exclude changed")
	}
	if !ruleConfigSlicesEqual(previous.Rules, current.Rules) {
		addSoft("rules changed")
	}
	if !containerOverridesEqual(previous.ContainerOverrides, current.ContainerOverrides) {
		addSoft("container_overrides changed")
	}

	return cs
}

// stringSlicesEqual reports whether two string slices have identical contents
// (order-sensitive).
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ruleConfigSlicesEqual reports whether two RuleConfig slices have identical
// contents using deep equality (order-sensitive).
func ruleConfigSlicesEqual(a, b []config.RuleConfig) bool {
	return reflect.DeepEqual(a, b)
}

// containerOverridesEqual reports whether two container-overrides maps are equal.
func containerOverridesEqual(a, b map[string][]config.RuleConfig) bool {
	return reflect.DeepEqual(a, b)
}
