package learn

import (
	"context"
	"time"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/health"
	"github.com/errorprobe/errorprobe/internal/ingest"
	"github.com/errorprobe/errorprobe/internal/logger"
	"github.com/errorprobe/errorprobe/internal/pbr"
)

const (
	// scanWindow is the time range queried when a forward trigger fires.
	scanWindow = 5 * time.Minute
	// numScanWindows is the number of sub-windows used for window counting.
	numScanWindows = 5
	// preRestartWindow is the time range scanned before a restart event.
	preRestartWindow = 30 * time.Second
	// retroWindow is the time range scanned for backward (recovery) triggers.
	retroWindow = 10 * time.Minute
	// maxEventsPerScan is the per-window Loki query limit.
	maxEventsPerScan = 200
)

// RuleProvider is a function that returns the current compiled rule set.
// The learner calls it before each gap analysis so it always works against the
// latest rules (including those applied by a previous learning cycle).
type RuleProvider func() []pbr.Rule

// Learner is the adaptive rule-learning goroutine. It listens for
// StateTransitionEvents, analyses recent logs for uncovered error patterns, and
// writes new rules to the overlay via an Applier.
type Learner struct {
	transitions <-chan health.StateTransitionEvent
	sampler     *Sampler
	applier     *Applier
	rules       RuleProvider
	cfg         config.LearnConfig
	suppPath    string
}

// NewLearner creates a Learner.
//
//   - transitions: channel from which state-transition events are received.
//   - sampler: used to query Loki for log lines.
//   - applier: manages overlay/pending file writes.
//   - rules: returns the current compiled PBR rule set on demand.
//   - cfg: the [learn] configuration block.
//   - suppressionPath: path to the suppression list file.
func NewLearner(
	transitions <-chan health.StateTransitionEvent,
	sampler *Sampler,
	applier *Applier,
	rules RuleProvider,
	cfg config.LearnConfig,
	suppressionPath string,
) *Learner {
	return &Learner{
		transitions: transitions,
		sampler:     sampler,
		applier:     applier,
		rules:       rules,
		cfg:         cfg,
		suppPath:    suppressionPath,
	}
}

// Run starts the learner event loop. It blocks until ctx is cancelled.
func (l *Learner) Run(ctx context.Context) {
	bgTicker := l.backgroundTicker()

	for {
		select {
		case <-ctx.Done():
			return

		case ev, ok := <-l.transitions:
			if !ok {
				return
			}
			l.handleTransition(ctx, ev)

		case <-bgTicker:
			if l.cfg.BackgroundScan {
				l.runBackgroundScan(ctx)
			}
		}
	}
}

// backgroundTicker returns a channel that fires at the configured background
// scan interval. Returns a never-firing channel when background scan is disabled.
func (l *Learner) backgroundTicker() <-chan time.Time {
	if !l.cfg.BackgroundScan || l.cfg.BackgroundScanInterval == "" {
		return make(chan time.Time) // never fires
	}
	d, err := config.ParseDuration(l.cfg.BackgroundScanInterval)
	if err != nil || d <= 0 {
		return make(chan time.Time)
	}
	return time.NewTicker(d).C
}

// handleTransition dispatches a StateTransitionEvent to the appropriate scan
// strategy.
func (l *Learner) handleTransition(ctx context.Context, ev health.StateTransitionEvent) {
	key := ev.Container
	if ev.Namespace != "" {
		key = ev.Namespace + "/" + ev.Container
	}

	switch {
	case ev.NewState == "RESTARTED":
		// Pre-restart scan: look at logs just before the crash.
		l.runPreRestartScan(ctx, key, ev.At)

	case (ev.PrevState == "OK" || ev.PrevState == "") &&
		(ev.NewState == "HAS_ERRORS" || ev.NewState == "FAILING"):
		// Forward trigger: container just became unhealthy.
		l.runForwardScan(ctx, key, ev.At)

	case (ev.PrevState == "HAS_ERRORS" || ev.PrevState == "FAILING") &&
		ev.NewState == "OK":
		// Backward (retrospective) trigger: container recovered.
		l.runRetroScan(ctx, key, ev.At)

	default:
		// No learning action for this transition.
	}
}

// runForwardScan queries recent logs and runs the learning pipeline on
// uncovered events. Called when a container transitions from OK to unhealthy.
func (l *Learner) runForwardScan(ctx context.Context, containerKey string, at time.Time) {
	end := at
	start := end.Add(-scanWindow)

	events, windowCounts, err := l.sampler.SampleWindows(ctx, containerKey, start, end, numScanWindows, maxEventsPerScan)
	if err != nil {
		logger.Error("learner: forward scan loki query", "container", containerKey, "err", err)
		return
	}
	l.processEvents(containerKey, events, windowCounts, numScanWindows)
}

// runPreRestartScan queries logs immediately before a restart event and runs
// the learning pipeline on uncovered events.
func (l *Learner) runPreRestartScan(ctx context.Context, containerKey string, at time.Time) {
	events, err := l.sampler.QueryPreRestart(ctx, containerKey, at, preRestartWindow, maxEventsPerScan)
	if err != nil {
		logger.Error("learner: pre-restart scan loki query", "container", containerKey, "err", err)
		return
	}
	// Pre-restart scan uses a single window; set window count to 1 so the
	// score is driven primarily by keyword tier.
	counts := make(map[string]int, len(events))
	for _, ev := range events {
		counts[ev.Message] = 1
	}
	l.processEvents(containerKey, events, counts, 1)
}

// runRetroScan queries logs from the period before recovery and runs the
// learning pipeline to catch error patterns that resolved themselves.
func (l *Learner) runRetroScan(ctx context.Context, containerKey string, at time.Time) {
	end := at
	start := end.Add(-retroWindow)

	events, windowCounts, err := l.sampler.SampleWindows(ctx, containerKey, start, end, numScanWindows, maxEventsPerScan)
	if err != nil {
		logger.Error("learner: retro scan loki query", "container", containerKey, "err", err)
		return
	}
	l.processEvents(containerKey, events, windowCounts, numScanWindows)
}

// runBackgroundScan is a periodic catch-all scan that queries all containers
// tracked in Loki. Not yet implemented; placeholder for future work.
func (l *Learner) runBackgroundScan(_ context.Context) {
	logger.Debug("learner: background scan (not yet implemented)")
}

// processEvents runs the full learning pipeline on a set of log events.
func (l *Learner) processEvents(
	containerKey string,
	events []ingest.LogEvent,
	windowCounts map[string]int,
	totalWindows int,
) {
	if len(events) == 0 {
		return
	}

	// Load the current suppression list (may have changed since startup).
	sl, err := LoadSuppressionList(l.suppPath)
	if err != nil {
		logger.Error("learner: loading suppression list", "err", err)
	}

	// Filter to events not covered by any existing rule.
	uncovered := FindUncovered(events, l.rules())
	if len(uncovered) == 0 {
		return
	}

	opts := ClassifyOptions{
		ConfidenceThreshold: l.cfg.ConfidenceThreshold,
		ReviewThreshold:     l.cfg.ReviewThreshold,
		TotalWindows:        totalWindows,
	}
	results := ClassifyBatch(uncovered, windowCounts, totalWindows, &sl, opts)
	if len(results) == 0 {
		return
	}

	// Load the current overlay to determine how many learned rules already exist
	// (so newly generated rules get unique priority values).
	existingOverlay := l.applier.OverlayRules()
	existingCount := len(existingOverlay)

	for _, r := range results {
		rule := GenerateRule(r.Candidate, existingCount)

		switch {
		case r.AutoApply && l.cfg.AutoApply:
			if err := l.applier.Apply(rule); err != nil {
				logger.Error("learner: applying rule", "rule", rule.Name, "err", err)
			} else {
				logger.Info("learner: auto-applied rule",
					"container", containerKey,
					"rule", rule.Name,
					"score", r.Candidate.Score,
					"pattern", r.Candidate.Pattern,
				)
				existingCount++
			}

		default:
			// Score is above review threshold: add to pending for operator review.
			if err := l.applier.Pending(rule); err != nil {
				logger.Error("learner: queuing pending rule", "rule", rule.Name, "err", err)
			} else {
				logger.Info("learner: queued rule for review",
					"container", containerKey,
					"rule", rule.Name,
					"score", r.Candidate.Score,
				)
			}
		}
	}
}
