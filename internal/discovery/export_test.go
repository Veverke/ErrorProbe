// export_test.go exposes internal fields for use in tests.
package discovery

import (
	"context"
	"time"

	"github.com/errorprobe/errorprobe/internal/health"
	"github.com/errorprobe/errorprobe/internal/pbr"
)

// SetReconcilerInterval replaces the ticker interval on a Reconciler for faster tests.
func SetReconcilerInterval(r *Reconciler, d time.Duration) {
	r.interval = d
}

// SetReconcilerStatePath replaces the state path used by the Reconciler.
func SetReconcilerStatePath(r *Reconciler, path string) {
	r.statePath = path
}

// InferInfraStatus exposes the unexported inferInfraStatus helper for testing.
func InferInfraStatus(rules []pbr.Rule, meta pbr.InfraContainer) string {
	return inferInfraStatus(rules, meta)
}

// PrevExitLine exposes the unexported prevExitLine helper for testing.
func PrevExitLine(logs string) string {
	return prevExitLine(logs)
}

// CurrentRules exposes the unexported currentRules method for testing.
func CurrentRules(r *Reconciler) []pbr.Rule {
	return r.currentRules()
}

// SetTransitionEvents wires a transition channel on a Reconciler (for testing).
func SetReconcilerTransitionEvents(r *Reconciler, ch chan<- health.StateTransitionEvent) {
	r.SetTransitionEvents(ch)
}

// FetchPrevExitMsg exposes fetchPrevExitMsg for white-box tests.
func FetchPrevExitMsg(r *Reconciler, namespace, pod, container string) string {
	return r.fetchPrevExitMsg(context.Background(), namespace, pod, container)
}

// ContainerNameForTest exposes containerName for testing the empty-names branch.
var ContainerNameForTest = containerName

