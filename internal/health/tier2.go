package health

import (
	"context"
	"fmt"
	"time"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/logger"
)

// LokiQueryClient is the minimal Loki interface required by Tier2Evaluator.
// *loki.Client satisfies this interface automatically.
type LokiQueryClient interface {
	CountErrors(ctx context.Context, container string, since time.Duration) (int, error)
}

// Tier2Evaluator periodically queries Loki for error rates and transitions
// containers between HAS_ERRORS and FAILING states based on configured thresholds.
type Tier2Evaluator struct {
	loki    LokiQueryClient
	cfg     *config.Config
	engine  *Engine
	history *HistoryLog
}

// NewTier2Evaluator creates a Tier2Evaluator.  history may be nil (history
// logging is skipped if it is nil).
func NewTier2Evaluator(loki LokiQueryClient, cfg *config.Config, engine *Engine, history *HistoryLog) *Tier2Evaluator {
	return &Tier2Evaluator{
		loki:    loki,
		cfg:     cfg,
		engine:  engine,
		history: history,
	}
}

// Run starts the evaluation loop.  It returns when ctx is cancelled.
func (e *Tier2Evaluator) Run(ctx context.Context) {
	tick, err := time.ParseDuration(e.cfg.Detection.Tier2.Tick)
	if err != nil || tick <= 0 {
		tick = 30 * time.Second
	}

	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.evaluate(ctx)
		}
	}
}

// evaluate runs one assessment cycle over all tracked containers.
func (e *Tier2Evaluator) evaluate(ctx context.Context) {
	window, err := time.ParseDuration(e.cfg.Detection.Tier2.Window)
	if err != nil || window <= 0 {
		window = 3 * time.Minute
	}

	threshold := e.cfg.Detection.Tier2.Threshold
	if threshold <= 0 {
		threshold = 10
	}

	snap := e.engine.Snapshot()
	for name, ch := range snap.Containers {
		switch ch.State {
		case StateHasErrors:
			e.evalHasErrors(ctx, name, ch, window, threshold)
		case StateFailing:
			e.evalFailing(ctx, name, window, threshold)
		}
	}
}

// evalHasErrors checks whether a HAS_ERRORS container should transition to FAILING.
func (e *Tier2Evaluator) evalHasErrors(ctx context.Context, name string, ch ContainerHealth, window time.Duration, threshold int) {
	count, err := e.loki.CountErrors(ctx, name, window)
	if err != nil {
		logger.Error("tier2: count errors", "container", name, "err", err)
		return
	}
	if count < threshold {
		return
	}

	// Find the dominant fingerprint from the in-memory accumulation.
	dominant, domCount := dominantFingerprint(ch.Fingerprints)
	if domCount < threshold {
		return
	}

	if err := e.engine.SetFailing(name, dominant, domCount); err != nil {
		logger.Error("tier2: set failing", "container", name, "err", err)
		return
	}

	if e.history != nil {
		_ = e.history.Append(StateTransition{
			ContainerName: name,
			From:          StateHasErrors,
			To:            StateFailing,
			At:            time.Now(),
			Reason:        fmt.Sprintf("dominant fingerprint %s seen %d×", dominant, domCount),
		})
	}
}

// evalFailing checks whether a FAILING container's error rate has dropped below
// threshold, and if so transitions it back to HAS_ERRORS.
func (e *Tier2Evaluator) evalFailing(ctx context.Context, name string, window time.Duration, threshold int) {
	count, err := e.loki.CountErrors(ctx, name, window)
	if err != nil {
		logger.Error("tier2: count errors (failing)", "container", name, "err", err)
		return
	}
	if count >= threshold {
		return // still failing
	}

	if err := e.engine.SetRecovered(name); err != nil {
		logger.Error("tier2: set recovered", "container", name, "err", err)
		return
	}

	if e.history != nil {
		_ = e.history.Append(StateTransition{
			ContainerName: name,
			From:          StateFailing,
			To:            StateHasErrors,
			At:            time.Now(),
			Reason:        fmt.Sprintf("error rate dropped below threshold (%d)", threshold),
		})
	}
}

// dominantFingerprint returns the most-frequent fingerprint and its count from
// a fingerprint→count map.  Returns ("", 0) for an empty or nil map.
func dominantFingerprint(fps map[string]int) (string, int) {
	best, bestCount := "", 0
	for fp, c := range fps {
		if c > bestCount {
			best = fp
			bestCount = c
		}
	}
	return best, bestCount
}
