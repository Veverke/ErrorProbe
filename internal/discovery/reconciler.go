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
	for _, c := range added {
		logger.Info("container added to watch set", "container", c.Name)
	}
	for _, c := range removed {
		logger.Info("container removed from watch set", "container", c.Name)
	}

	// 4. Regenerate Vector config.
	names := make([]string, len(approved))
	for i, c := range approved {
		names[i] = c.Name
	}
	if err := r.configgen.GenerateVector(r.cfg, r.cfg.ConfigsDir(), names); err != nil {
		return fmt.Errorf("generating vector config: %w", err)
	}

	// 5. Persist new watch set before signalling Vector.
	// Decoupling persistence from the signal means the watch set stays up to
	// date even if Vector is temporarily unhealthy.
	if err := SaveWatchSet(r.statePath, current); err != nil {
		return fmt.Errorf("saving watch set: %w", err)
	}

	// 6. Send SIGHUP to Vector container. Log on failure but do not return an
	// error — the config has already been regenerated and persisted; Vector will
	// pick it up on its next restart.
	vectorRunning, err := r.docker.ContainerRunning(ctx, "errorprobe-vector")
	if err != nil {
		logger.Error("checking vector container state", "err", err)
	} else if !vectorRunning {
		logger.Info("vector container not running — config updated, reload deferred until restart")
	} else {
		if err := r.docker.SendSignal(ctx, "errorprobe-vector", "SIGHUP"); err != nil {
			logger.Error("sending SIGHUP to vector", "err", err)
		} else {
			// 7. Notify caller only when reload actually succeeded.
			logger.Info("vector config reloaded", "watching", len(approved))
			if r.onReload != nil {
				r.onReload()
			}
		}
		return nil
	}

	// Config updated but Vector wasn't reloaded — still notify so the user
	// knows the watch set changed.
	if r.onReload != nil {
		r.onReload()
	}

	return nil
}
