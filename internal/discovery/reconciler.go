package discovery

import (
	"context"
	"fmt"
	"time"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/docker"
	"github.com/errorprobe/errorprobe/internal/logger"
)

const reconcileInterval = 5 * time.Second

// VectorGenerator is the interface used by the Reconciler to regenerate vector.toml.
type VectorGenerator interface {
	GenerateVector(cfg *config.Config, outputDir string, containers []string) error
}

// Reconciler discovers containers on a tick, compares to the previous watch set,
// regenerates Vector config on change, and signals Vector to reload.
type Reconciler struct {
	cfg       *config.Config
	docker    docker.DockerAPI
	configgen VectorGenerator
	onReload  func()
	interval  time.Duration
	statePath string
}

// NewReconciler creates a Reconciler with the default interval.
func NewReconciler(cfg *config.Config, dockerClient docker.DockerAPI, gen VectorGenerator, onReload func()) *Reconciler {
	return &Reconciler{
		cfg:       cfg,
		docker:    dockerClient,
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
	// 1. Discover running containers and apply policy.
	running, err := ListRunning(ctx, r.docker)
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}
	approved := ApplyPolicy(running, r.cfg)

	current := WatchSet{
		Containers:  approved,
		GeneratedAt: time.Now(),
	}

	// 2. Load previous watch set.
	previous, err := LoadWatchSet(r.statePath)
	if err != nil {
		return fmt.Errorf("loading previous watch set: %w", err)
	}

	// 3. Diff — skip if unchanged.
	added, removed := current.Diff(previous)
	if len(added) == 0 && len(removed) == 0 {
		return nil
	}

	// 4. Regenerate Vector config.
	names := make([]string, len(approved))
	for i, c := range approved {
		names[i] = c.Name
	}
	if err := r.configgen.GenerateVector(r.cfg, r.cfg.ConfigsDir(), names); err != nil {
		return fmt.Errorf("generating vector config: %w", err)
	}

	// 5. Send SIGHUP to Vector container. If this fails, return an error so the
	// tick is retried and the watch set is not persisted, preventing the diff
	// from being suppressed on subsequent ticks.
	if err := r.docker.SendSignal(ctx, "errorprobe-vector", "SIGHUP"); err != nil {
		return fmt.Errorf("sending SIGHUP to vector: %w", err)
	}

	// 6. Persist new watch set only after a successful reload signal.
	if err := SaveWatchSet(r.statePath, current); err != nil {
		return fmt.Errorf("saving watch set: %w", err)
	}

	// 7. Notify caller.
	if r.onReload != nil {
		r.onReload()
	}

	return nil
}