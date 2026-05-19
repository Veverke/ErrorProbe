package discovery

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/docker"
	"github.com/errorprobe/errorprobe/internal/health"
	"github.com/errorprobe/errorprobe/internal/k8s"
	"github.com/errorprobe/errorprobe/internal/logger"
	"github.com/errorprobe/errorprobe/internal/pbr"
)

const reconcileInterval = 5 * time.Second

// K8sContainerRef carries the fields the Vector config generator needs for K8s.
// It is defined here to avoid a circular import with configgen.
type K8sContainerRef struct {
	PodName   string
	Namespace string
}

// VectorGenerator is the interface used by the Reconciler to regenerate vector.toml.
type VectorGenerator interface {
	GenerateVector(cfg *config.Config, outputDir string, dockerContainers []string, k8sContainers []K8sContainerRef) error
}

// Reconciler discovers containers on a tick, compares to the previous watch set,
// regenerates Vector config on change, and signals Vector to reload.
type Reconciler struct {
	cfg              *config.Config
	docker           docker.DockerAPI
	k8s              k8s.K8sAPI // nil when K8s is not available
	configgen        VectorGenerator
	onReload         func()
	onApproved       func(WatchSet) // called every tick with the current approved set; nil = no-op
	interval         time.Duration
	statePath        string
	rulesMu          sync.RWMutex
	rules            []pbr.Rule                 // guarded by rulesMu
	transitionEvents chan<- health.StateTransitionEvent // nil when not wired
}

// SetOnApproved registers fn to be called after every reconciler tick with
// the current approved watch set, regardless of whether the set changed.
// Use this to keep the health engine's watched-key filter in sync with the
// policy — it is important that the filter is initialised on the first tick
// even when the container set is empty.
// Safe to call before Run.
func (r *Reconciler) SetOnApproved(fn func(WatchSet)) {
	r.onApproved = fn
}

// SetTransitionEvents wires ch as the destination for RESTARTED transition
// events emitted when a container's restart count increases.
// Safe to call before Run.
func (r *Reconciler) SetTransitionEvents(ch chan<- health.StateTransitionEvent) {
	r.transitionEvents = ch
}

// NewReconciler creates a Reconciler with the default interval.
// k8sClient may be nil if K8s discovery is not desired.
// rules is the compiled PBR rule set; pass nil to use built-in defaults.
func NewReconciler(cfg *config.Config, dockerClient docker.DockerAPI, k8sClient k8s.K8sAPI, gen VectorGenerator, onReload func(), rules []pbr.Rule) *Reconciler {
	return &Reconciler{
		cfg:       cfg,
		docker:    dockerClient,
		k8s:       k8sClient,
		configgen: gen,
		onReload:  onReload,
		interval:  reconcileInterval,
		statePath: cfg.StateDir() + "containers.json",
		rules:     rules,
	}
}

// SetRules atomically replaces the reconciler's compiled rule set.
// Safe to call concurrently with tick.
func (r *Reconciler) SetRules(rules []pbr.Rule) {
	r.rulesMu.Lock()
	defer r.rulesMu.Unlock()
	r.rules = rules
}

// currentRules returns a snapshot of the current rule set.
func (r *Reconciler) currentRules() []pbr.Rule {
	r.rulesMu.RLock()
	defer r.rulesMu.RUnlock()
	return r.rules
}

// Run runs the reconciliation loop until ctx is cancelled.
// An initial tick is performed immediately before entering the interval loop.
// An error in a single tick is logged and retried on the next tick.
func (r *Reconciler) Run(ctx context.Context) error {
	if err := r.tick(ctx); err != nil {
		logger.Error("reconciler tick failed", "err", err)
	}

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := r.tick(ctx); err != nil {
				logger.Error("reconciler tick failed", "err", err)
			}
		}
	}
}

func (r *Reconciler) tick(ctx context.Context) error {
	// 1. Discover running Docker containers.
	dockerRunning, err := ListRunning(ctx, r.docker)
	if err != nil {
		return fmt.Errorf("listing docker containers: %w", err)
	}

	// 2. Optionally discover K8s containers.
	var k8sRunning []ContainerMeta
	if r.k8s != nil && r.k8s.IsAvailable(ctx) {
		k8sRunning, err = ListRunningK8s(ctx, r.k8s, r.cfg, r.currentRules())
		if err != nil {
			logger.Error("listing k8s containers (skipped)", "err", err)
			k8sRunning = nil
		}
	}

	// 3. Merge and apply policy.
	merged := MergeContainers(dockerRunning, k8sRunning)
	approved := ApplyPolicy(merged, r.cfg)

	// 4. Load previous watch set.
	previous, err := LoadWatchSet(r.statePath)
	if err != nil {
		return fmt.Errorf("loading previous watch set: %w", err)
	}

	// 4.5 Enrich K8s containers with previous-exit diagnostics on new restarts.
	prevByName := make(map[string]ContainerMeta, len(previous.Containers))
	for _, c := range previous.Containers {
		prevByName[c.Name] = c
	}
	restartDiagChanged := false
	for i, c := range approved {
		if c.Runtime != "k8s" {
			continue
		}
		prev, hasPrev := prevByName[c.Name]
		if hasPrev && c.RestartCount > prev.RestartCount {
			// New restart detected: fetch the last error from the dying container.
			msg := r.fetchPrevExitMsg(ctx, c.Namespace, c.Pod, c.Name)
			if msg != "" {
				approved[i].PrevExitMsg = msg
				restartDiagChanged = true
			}
			// Emit a RESTARTED transition event for the learning module.
			if r.transitionEvents != nil {
				select {
				case r.transitionEvents <- health.StateTransitionEvent{
					Container: c.Name,
					Namespace: c.Namespace,
					PrevState: "OK",
					NewState:  "RESTARTED",
					At:        time.Now(),
				}:
				default:
				}
			}
		} else if hasPrev && prev.PrevExitMsg != "" {
			// Carry forward the diagnostic from the previous tick.
			approved[i].PrevExitMsg = prev.PrevExitMsg
		} else if !hasPrev && c.RestartCount > 0 {
			// First observation and the container has already restarted: fetch the
			// previous exit message now so operators see the crash reason immediately.
			msg := r.fetchPrevExitMsg(ctx, c.Namespace, c.Pod, c.Name)
			if msg != "" {
				approved[i].PrevExitMsg = msg
				restartDiagChanged = true
			}
		}
	}

	current := WatchSet{
		Containers:  approved,
		GeneratedAt: time.Now(),
	}

	// Always notify the caller with the up-to-date approved set so that
	// downstream filters (e.g. the health engine's watchedKeys) are
	// initialised on the first tick even when the container set is empty.
	if r.onApproved != nil {
		r.onApproved(current)
	}

	// Detect infra-status changes on containers whose ID is unchanged but whose
	// InfraStatus flipped (e.g. "restarting" → "running").  Diff only tracks
	// structural adds/removes by ID, so without this check a container that
	// stabilises after a restart loop would stay marked "restarting" in the saved
	// watch set forever — the TUI reads the file every second and would never see
	// the updated state.
	//
	// We only care about transitions *away from* a degraded state; ignoring the
	// empty→"running" case avoids spurious reloads when the watch set was seeded
	// without an InfraStatus (e.g. on first discovery or in tests).
	isDegraded := func(s string) bool {
		switch s {
		case "restarting", "failed", "error", "crashed", "terminating", "pending", "waiting":
			return true
		}
		return false
	}
	prevByID := make(map[string]ContainerMeta, len(previous.Containers))
	for _, c := range previous.Containers {
		prevByID[c.ID] = c
	}
	infraStatusChanged := false
	for _, c := range approved {
		if prev, ok := prevByID[c.ID]; ok && isDegraded(prev.InfraStatus) && c.InfraStatus != prev.InfraStatus {
			infraStatusChanged = true
			break
		}
	}

	// 5. Diff — skip if nothing changed.
	added, removed := current.Diff(previous)
	structureChanged := len(added) > 0 || len(removed) > 0
	if !structureChanged && !restartDiagChanged && !infraStatusChanged {
		return nil
	}
	for _, c := range added {
		logger.Debug("container added to watch set", "container", c.Name, "runtime", c.Runtime)
	}
	for _, c := range removed {
		logger.Debug("container removed from watch set", "container", c.Name, "runtime", c.Runtime)
	}

	// 6. Regenerate Docker-only Vector config (only when containers added/removed).
	// K8s container logs are collected by the Vector DaemonSet running inside the cluster.
	if structureChanged {
		dockerNames := make([]string, 0, len(approved))
		for _, c := range approved {
			if c.Runtime == "docker" {
				dockerNames = append(dockerNames, c.Name)
			}
		}
		if err := r.configgen.GenerateVector(r.cfg, r.cfg.ConfigsDir(), dockerNames, nil); err != nil {
			return fmt.Errorf("generating vector config: %w", err)
		}
	}

	// 7. Persist new watch set before signalling Vector.
	if err := SaveWatchSet(r.statePath, current); err != nil {
		return fmt.Errorf("saving watch set: %w", err)
	}

	// 8. Send SIGHUP to Vector container (only when structure changed).
	if structureChanged {
		vectorRunning, err := r.docker.ContainerRunning(ctx, "errorprobe-vector")
		if err != nil {
			logger.Error("checking vector container state", "err", err)
		} else if !vectorRunning {
			logger.Info("vector container not running — config updated, reload deferred until restart")
		} else {
			if err := r.docker.SendSignal(ctx, "errorprobe-vector", "SIGHUP"); err != nil {
				logger.Error("sending SIGHUP to vector", "err", err)
			} else {
				logger.Info("vector config reloaded", "watching", len(approved))
			}
		}
	}

	// 9. Notify callers whenever the watch set changed.
	if r.onReload != nil {
		r.onReload()
	}

	return nil
}

// fetchPrevExitMsg calls the K8s API to get the last error line from the
// previous (terminated) container instance. Returns "" on error or no result.
func (r *Reconciler) fetchPrevExitMsg(ctx context.Context, namespace, pod, container string) string {
	if r.k8s == nil {
		return ""
	}
	logs, err := r.k8s.GetPreviousLogs(ctx, namespace, pod, container, 30)
	if err != nil {
		logger.Error("fetching previous container logs", "pod", pod, "container", container, "err", err)
		return ""
	}
	return prevExitLine(logs)
}

// prevExitLine scans logs (newest-last) in reverse and returns the last line
// that looks like an error, or the last non-empty line as a fallback.
// The result is truncated to 120 runes.
func prevExitLine(logs string) string {
	lines := strings.Split(strings.TrimSpace(logs), "\n")
	truncate := func(s string) string {
		r := []rune(s)
		if len(r) > 120 {
			return string(r[:119]) + "…"
		}
		return s
	}
	// isKubeletNoise returns true for container-runtime log-management messages
	// emitted by containerd/runc when cleaning up the pod log directory after exit
	// (e.g. "failed to try resolving symlinks in path /var/log/pods/…").
	// These are never written by the application and always contain both "symlink"
	// and the kubelet-specific path prefix "/var/log/pods/".
	isKubeletNoise := func(lower string) bool {
		return strings.Contains(lower, "symlink") && strings.Contains(lower, "/var/log/pods/")
	}
	// Prefer lines that contain an error keyword.
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if isKubeletNoise(lower) {
			continue
		}
		if strings.Contains(lower, "error") || strings.Contains(lower, "fatal") ||
			strings.Contains(lower, "panic") || strings.Contains(lower, "exception") ||
			strings.Contains(lower, "failed") {
			return truncate(line)
		}
	}
	// Fallback: last non-empty line that is not kubelet noise.
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if !isKubeletNoise(strings.ToLower(line)) {
			return truncate(line)
		}
	}
	return ""
}