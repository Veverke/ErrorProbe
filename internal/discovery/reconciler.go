package discovery

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/docker"
	"github.com/errorprobe/errorprobe/internal/k8s"
	"github.com/errorprobe/errorprobe/internal/logger"
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
	cfg       *config.Config
	docker    docker.DockerAPI
	k8s       k8s.K8sAPI // nil when K8s is not available
	configgen VectorGenerator
	onReload  func()
	interval  time.Duration
	statePath string
}

// NewReconciler creates a Reconciler with the default interval.
// k8sClient may be nil if K8s discovery is not desired.
func NewReconciler(cfg *config.Config, dockerClient docker.DockerAPI, k8sClient k8s.K8sAPI, gen VectorGenerator, onReload func()) *Reconciler {
	return &Reconciler{
		cfg:       cfg,
		docker:    dockerClient,
		k8s:       k8sClient,
		configgen: gen,
		onReload:  onReload,
		interval:  reconcileInterval,
		statePath: cfg.StateDir() + "containers.json",
	}
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
		k8sRunning, err = ListRunningK8s(ctx, r.k8s, r.cfg)
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
		} else if hasPrev && prev.PrevExitMsg != "" {
			// Carry forward the diagnostic from the previous tick.
			approved[i].PrevExitMsg = prev.PrevExitMsg
		}
	}

	current := WatchSet{
		Containers:  approved,
		GeneratedAt: time.Now(),
	}

	// 5. Diff — skip if nothing changed.
	added, removed := current.Diff(previous)
	structureChanged := len(added) > 0 || len(removed) > 0
	if !structureChanged && !restartDiagChanged {
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

	// 9. Notify caller whenever the watch set changed.
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
	// Prefer lines that contain an error keyword.
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") || strings.Contains(lower, "fatal") ||
			strings.Contains(lower, "panic") || strings.Contains(lower, "exception") ||
			strings.Contains(lower, "failed") {
			return truncate(line)
		}
	}
	// Fallback: last non-empty line.
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return truncate(line)
		}
	}
	return ""
}