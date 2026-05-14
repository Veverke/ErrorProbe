package health

import (
	"context"
	"fmt"
	"time"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/ingest"
	"github.com/errorprobe/errorprobe/internal/logger"
	"github.com/errorprobe/errorprobe/internal/pbr"
)

// LokiQueryClient is the minimal Loki interface required by Tier2Evaluator.
// *loki.Client satisfies this interface automatically.
type LokiQueryClient interface {
	CountErrors(ctx context.Context, container string, since time.Duration) (int, error)
	QueryErrorMessages(ctx context.Context, containerKey string, since time.Duration) ([]string, error)
}

// Tier2Evaluator periodically queries Loki for error rates and transitions
// containers between HAS_ERRORS and FAILING states based on PBR rules.
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
			e.evalHasErrors(ctx, name, window, threshold)
		case StateFailing:
			e.evalFailing(ctx, name, window, threshold)
		}
	}
}

// evalHasErrors checks whether a HAS_ERRORS container should transition to FAILING.
// It queries Loki for the error count in the configured window, then runs the PBR
// evaluator with a synthetic log event carrying that count. If a rule matches with
// state FAILING the container is escalated.
func (e *Tier2Evaluator) evalHasErrors(ctx context.Context, name string, window time.Duration, threshold int) {
	count, err := e.loki.CountErrors(ctx, name, window)
	if err != nil {
		logger.Warn("tier2: count errors", "container", name, "err", err)
		return
	}

	// Build a synthetic LogEvalContext so PBR rules can test count_in_window.
	syntheticEvent := ingest.LogEvent{Level: "error"}
	evalCtx := pbr.EvalContext{
		Log: &pbr.LogEvalContext{
			Event:         syntheticEvent,
			CountInWindow: count,
			Window:        window,
		},
	}
	result := pbr.Evaluate(e.engine.Rules(), evalCtx)
	if result.State != "FAILING" {
		// PBR evaluated but did not escalate to FAILING — respect the result.
		// Only apply the legacy count-threshold fallback when PBR matched no
		// rule at all (empty State), which preserves existing behaviour for
		// installs that have not configured any rules.  When a user rule did
		// match (result.State != ""), the rule is authoritative and the legacy
		// fallback is skipped so it cannot silently override the user's intent.
		if result.State != "" || count < threshold {
			return
		}
	}

	// Query Loki for the actual error messages within the window to derive
	// window-scoped fingerprint counts.
	msgs, err := e.loki.QueryErrorMessages(ctx, name, window)
	if err != nil {
		logger.Warn("tier2: query error messages", "container", name, "err", err)
		return
	}
	fpCounts := make(map[string]int, len(msgs))
	for _, m := range msgs {
		fpCounts[Fingerprint(m)]++
	}
	dominant, domCount := dominantFingerprint(fpCounts)
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
		logger.Warn("tier2: count errors (failing)", "container", name, "err", err)
		return
	}

	// Use PBR to decide whether the container should still be FAILING.
	syntheticEvent := ingest.LogEvent{Level: "error"}
	evalCtx := pbr.EvalContext{
		Log: &pbr.LogEvalContext{
			Event:         syntheticEvent,
			CountInWindow: count,
			Window:        window,
		},
	}
	result := pbr.Evaluate(e.engine.Rules(), evalCtx)
	// Apply the same rule-authority semantics as evalHasErrors: if a rule
	// matched, honour it. Only fall back to the legacy count threshold when no
	// rule matched at all (result.State == "").
	var stillFailing bool
	if result.State != "" {
		stillFailing = result.State == "FAILING"
	} else {
		stillFailing = count >= threshold
	}
	if stillFailing {
		return
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

