package discovery

import (
	"context"
	"fmt"
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

	current := WatchSet{
		Containers:  approved,
		GeneratedAt: time.Now(),
	}

	// 4. Load previous watch set.
	previous, err := LoadWatchSet(r.statePath)
	if err != nil {
		return fmt.Errorf("loading previous watch set: %w", err)
	}

	// 5. Diff — skip if unchanged.
	added, removed := current.Diff(previous)
	if len(added) == 0 && len(removed) == 0 {
		return nil
	}
	for _, c := range added {
		logger.Info("container added to watch set", "container", c.Name, "runtime", c.Runtime)
	}
	for _, c := range removed {
		logger.Info("container removed from watch set", "container", c.Name, "runtime", c.Runtime)
	}

	// 6. Regenerate Vector config.
	dockerNames := make([]string, 0, len(approved))
	k8sRefs := make([]K8sContainerRef, 0)
	for _, c := range approved {
		if c.Runtime == "docker" {
			dockerNames = append(dockerNames, c.Name)
		} else if c.Runtime == "k8s" {
			k8sRefs = append(k8sRefs, K8sContainerRef{
				PodName:   c.Pod,
				Namespace: c.Namespace,
			})
		}
	}
	if err := r.configgen.GenerateVector(r.cfg, r.cfg.ConfigsDir(), dockerNames, k8sRefs); err != nil {
		return fmt.Errorf("generating vector config: %w", err)
	}

	// 7. Persist new watch set before signalling Vector.
	if err := SaveWatchSet(r.statePath, current); err != nil {
		return fmt.Errorf("saving watch set: %w", err)
	}

	// 8. Send SIGHUP to Vector container.
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

	// 9. Notify caller whenever the watch set changed.
	if r.onReload != nil {
		r.onReload()
	}

	return nil
}