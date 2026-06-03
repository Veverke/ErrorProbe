package health

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/errorprobe/errorprobe/internal/ingest"
	"github.com/errorprobe/errorprobe/internal/logger"
	"github.com/errorprobe/errorprobe/internal/pbr"
)

// Engine maintains the current HealthSnapshot, processes incoming log batches,
// and persists state changes to disk.
type Engine struct {
	snapshot         HealthSnapshot
	mu               sync.RWMutex
	snapshotPath     string
	onChange         func(HealthSnapshot)
	rules            []pbr.Rule                 // compiled PBR rule set; guarded by mu
	transitionEvents chan<- StateTransitionEvent // nil when not wired; writes are non-blocking
	watchedKeys      map[string]struct{}        // nil = accept all; guarded by mu
}

// NewEngine creates an Engine that persists state to snapshotPath and calls
// onChange (if non-nil) whenever the snapshot changes.
// rules is the compiled PBR rule set returned by pbr.Load; pass nil to use
// the built-in rule set automatically (BuiltinRules are loaded as the default).
// Note: nil has different semantics in SetRules — see SetRules for details.
// On startup it loads any existing snapshot from disk so state survives
// ErrorProbe restarts.
func NewEngine(snapshotPath string, rules []pbr.Rule, onChange func(HealthSnapshot)) *Engine {
	if rules == nil {
		rules = pbr.BuiltinRules()
	}
	e := &Engine{
		snapshotPath: snapshotPath,
		rules:        rules,
		onChange:     onChange,
	}

	if snap, err := LoadSnapshot(snapshotPath); err == nil {
		e.snapshot = snap
	}

	if e.snapshot.Containers == nil {
		e.snapshot.Containers = make(map[string]ContainerHealth)
	}

	return e
}

// SetTransitionEvents wires ch as the destination for StateTransitionEvents
// emitted by ProcessBatch. Pass a buffered channel to avoid blocking the
// engine; dropped events are silently skipped when the channel is full.
// Safe to call before the first ProcessBatch invocation.
func (e *Engine) SetTransitionEvents(ch chan<- StateTransitionEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.transitionEvents = ch
}

// SetWatchedKeys restricts ProcessBatch to events whose health key is present
// in keys. Pass nil to accept events from all containers (the default).
// This should be called whenever the approved watch set changes so that K8s
// system containers (e.g. storage-provisioner) whose logs Vector forwards but
// which are hidden from ep watch / ep list do not pollute the health state or
// log file.
// Safe to call concurrently with ProcessBatch.
func (e *Engine) SetWatchedKeys(keys map[string]struct{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.watchedKeys = keys
}

// SetRules atomically replaces the engine's compiled rule set.
// Pass nil or an empty slice to clear all rules so that no events produce
// health state changes until new rules are loaded.
// Note: unlike NewEngine, nil here does NOT fall back to built-in rules — it
// clears the rule set entirely. Callers that want built-ins must pass
// pbr.BuiltinRules() or the result of pbr.Load.
// Safe to call concurrently with ProcessBatch.
func (e *Engine) SetRules(rules []pbr.Rule) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules = rules
}

// Rules returns a snapshot of the current compiled rule set.
// Safe to call concurrently with SetRules and ProcessBatch.
func (e *Engine) Rules() []pbr.Rule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.rules
}

// ProcessBatch applies a batch of log events to the snapshot.
// Each event is evaluated through the PBR rule set; events that produce a
// health-degrading state (HAS_ERRORS or FAILING) update the snapshot.
// If the snapshot changes it is persisted and onChange is called.
func (e *Engine) ProcessBatch(events []ingest.LogEvent) {
	e.mu.Lock()

	// Snapshot per-container states BEFORE processing so we can detect
	// transitions and emit StateTransitionEvents after releasing the lock.
	prevStates := make(map[string]FunctionalState, len(e.snapshot.Containers))
	for k, ch := range e.snapshot.Containers {
		prevStates[k] = ch.State
	}

	changed := false
	for _, ev := range events {
		// Skip events for containers not in the approved watch set so that K8s
		// system containers (e.g. storage-provisioner) whose logs Vector
		// forwards to the ingest endpoint do not produce health state changes
		// or log entries.  nil means "accept all" (startup default).
		if e.watchedKeys != nil {
			if _, watched := e.watchedKeys[logEventKey(ev)]; !watched {
				continue
			}
		}
		result := pbr.Evaluate(e.rules, pbr.EvalContext{
			Log: &pbr.LogEvalContext{Event: ev},
		})
		state := result.State
		if state == "" {
			logger.Debug("pbr: no rule matched",
				"container", logEventKey(ev),
				"level", ev.Level,
				"msg", truncateMsg(ev.Message, 120),
			)
			continue
		}
		logger.Debug("pbr: rule matched",
			"container", logEventKey(ev),
			"rule", result.MatchedRule,
			"state", state,
			"msg", truncateMsg(ev.Message, 120),
		)
		if state == "HAS_WARNINGS" {
			key := logEventKey(ev)
			prevCount := 0
			if ch, ok := e.snapshot.Containers[key]; ok {
				prevCount = ch.ErrorCount
			}
			e.snapshot.SetWarn(key, extractNotableLines(ev.Message), ev.Timestamp)
			if ch, ok := e.snapshot.Containers[key]; ok && ch.ErrorCount != prevCount {
				changed = true
			}
			if ch, ok := e.snapshot.Containers[key]; ok {
				ch.MatchedRule = result.MatchedRule
				ch.MatchedPattern = result.MatchedPattern
				e.snapshot.Containers[key] = ch
			}
		} else if state == "HAS_ERRORS" || state == "FAILING" {
			key := logEventKey(ev)
			prevCount := 0
			if ch, ok := e.snapshot.Containers[key]; ok {
				prevCount = ch.ErrorCount
			}
			// Pass the raw message to SetError so that multi-line stack traces,
			// "Caused by:" chains, and Python/Java traceback frames are preserved
			// verbatim. extractNotableLines is only applied for HAS_WARNINGS, where
			// filtering noisy continuation lines is desirable.
			e.snapshot.SetError(key, ev.Message, ev.Timestamp)
			if ch, ok := e.snapshot.Containers[key]; ok && ch.ErrorCount != prevCount {
				changed = true
			}
			// Apply the PBR-determined state (may be FAILING even on first match).
			if ch, ok := e.snapshot.Containers[key]; ok {
				if state == "FAILING" {
					ch.State = StateFailing
				}
				ch.MatchedRule = result.MatchedRule
				ch.MatchedPattern = result.MatchedPattern
				e.snapshot.Containers[key] = ch
			}
			if strings.EqualFold(ev.Level, "error") {
				// Track fingerprints for Tier 2 detection (error-level only).
				e.snapshot.RecordFingerprint(key, Fingerprint(ev.Message))
			}
		}
	}

	// Continuation pass: many runtimes write multi-line errors where the header line
	// ends with a colon and the actual error detail arrives on the next line at a
	// different (often lower) severity level, so the main loop above misses it.
	//
	// Examples:
	//   Erlang/OTP   "Error in process … with exit value:"  → "{database_does_not_exist,…}"
	//   Python        "Traceback (most recent call last):"  → "ValueError: …"
	//   Java          "Exception in thread \"main\":"       → "java.lang.NullPointerException: …"
	//
	// For every container whose last stored error ends with ":", scan the current batch
	// for the first non-empty, non-header follow-on line from that same container and
	// append it. The search is bounded to one Vector batch (≤100 events, ≤1 s window),
	// which keeps the context tight enough to avoid false positives.
	for key, ch := range e.snapshot.Containers {
		if !strings.HasSuffix(strings.TrimSpace(ch.LastErrorMsg), ":") {
			continue
		}
		for _, ev := range events {
			if logEventKey(ev) != key {
				continue
			}
			msg := strings.TrimSpace(ev.Message)
			if msg != "" && !strings.HasSuffix(msg, ":") {
				ch.LastErrorMsg = strings.TrimSpace(ch.LastErrorMsg) + " " + msg
				e.snapshot.Containers[key] = ch
				changed = true
				break
			}
		}
	}

	// Collect state transitions before releasing the lock.
	type pendingTransition struct {
		container   string
		namespace   string
		prevState   FunctionalState
		newState    FunctionalState
		matchedRule string
	}
	var pending []pendingTransition
	for k, containerHealth := range e.snapshot.Containers {
		prev := prevStates[k]
		if containerHealth.State != prev {
			ns, name := splitHealthKey(k)
			pending = append(pending, pendingTransition{
				container:   name,
				namespace:   ns,
				prevState:   prev,
				newState:    containerHealth.State,
				matchedRule: containerHealth.MatchedRule,
			})
		}
	}

	if changed {
		e.snapshot.SnapshotAt = time.Now()
		snap := e.snapshot.DeepCopy()
		if err := SaveSnapshot(e.snapshotPath, snap); err != nil {
			// Log but do not crash; state is still in memory.
			logger.Error("health engine: persist snapshot", "err", err)
		}
		if e.onChange != nil {
			e.onChange(snap)
		}
	}

	evCh := e.transitionEvents
	e.mu.Unlock()

	// Log every state transition at Info so the log file captures the full
	// history for GitHub issue diagnostics, even without --debug.
	for _, t := range pending {
		logger.Info("health: state transition",
			"container", t.container,
			"from", string(t.prevState),
			"to", string(t.newState),
			"rule", t.matchedRule,
		)
	}

	// Emit transition events outside the lock to avoid deadlock when the
	// channel consumer also interacts with the engine.
	if evCh != nil {
		now := time.Now()
		for _, t := range pending {
			select {
			case evCh <- StateTransitionEvent{
				Container:   t.container,
				Namespace:   t.namespace,
				PrevState:   t.prevState,
				NewState:    t.newState,
				MatchedRule: t.matchedRule,
				At:          now,
			}:
			default:
				// Channel full — drop the event rather than block.
			}
		}
	}
}

// splitHealthKey splits a health-snapshot key into (namespace, container).
// Docker keys are bare names; K8s keys are "namespace/container".
func splitHealthKey(key string) (namespace, container string) {
	for i, c := range key {
		if c == '/' {
			return key[:i], key[i+1:]
		}
	}
	return "", key
}

// logEventKey returns the canonical health-snapshot key for a log event.
// It mirrors ContainerMeta.HealthKey() on the ingest side:
//   - K8s events (Namespace non-empty): "namespace/container_name"
//   - Docker events: bare container name
func logEventKey(ev ingest.LogEvent) string {
	if ev.Namespace != "" {
		return ev.Namespace + "/" + ev.Container
	}
	return ev.Container
}

// Snapshot returns a thread-safe deep copy of the current health snapshot.
// The returned snapshot has no shared map references with the engine.
func (e *Engine) Snapshot() HealthSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.snapshot.DeepCopy()
}

// Reset clears the health state for the named container, persists and notifies.
func (e *Engine) Reset(containerName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.snapshot.Reset(containerName)
	e.snapshot.SnapshotAt = time.Now()
	snap := e.snapshot

	if err := SaveSnapshot(e.snapshotPath, snap); err != nil {
		return fmt.Errorf("health engine: persist after reset: %w", err)
	}

	if e.onChange != nil {
		e.onChange(snap)
	}
	return nil
}

// SetFailing transitions the named container to the FAILING state, recording the
// dominant fingerprint and its occurrence count.  Persists and notifies onChange.
func (e *Engine) SetFailing(name, fingerprint string, count int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	ch := e.snapshot.Containers[name]
	ch.Name = name
	ch.State = StateFailing
	ch.DominantFingerprint = fingerprint
	ch.DominantFingerprintCount = count
	ch.LastUpdated = time.Now()
	if e.snapshot.Containers == nil {
		e.snapshot.Containers = make(map[string]ContainerHealth)
	}
	e.snapshot.Containers[name] = ch
	e.snapshot.SnapshotAt = time.Now()
	snap := e.snapshot

	if err := SaveSnapshot(e.snapshotPath, snap); err != nil {
		return fmt.Errorf("health engine: persist after SetFailing: %w", err)
	}
	if e.onChange != nil {
		e.onChange(snap)
	}
	return nil
}

// SetRecovered transitions a FAILING container back to HAS_ERRORS (not OK —
// errors did occur).  Clears the dominant fingerprint fields.  Persists and notifies.
func (e *Engine) SetRecovered(name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	ch := e.snapshot.Containers[name]
	ch.State = StateHasErrors
	ch.DominantFingerprint = ""
	ch.DominantFingerprintCount = 0
	ch.LastUpdated = time.Now()
	e.snapshot.Containers[name] = ch
	e.snapshot.SnapshotAt = time.Now()
	snap := e.snapshot

	if err := SaveSnapshot(e.snapshotPath, snap); err != nil {
		return fmt.Errorf("health engine: persist after SetRecovered: %w", err)
	}
	if e.onChange != nil {
		e.onChange(snap)
	}
	return nil
}

// extractNotableLines filters a (potentially multi-line) log message down to
// only the lines that contain a warn or error keyword.
// When a message is a single line, or no line contains a keyword, the original
// message is returned unchanged so callers always get something useful.
//
// This addresses the case where Vector delivers a multi-line "block" (e.g. the
// entire initdb initialisation output).  Rather than storing the whole block or
// only the triggering line, we store the lines that carry the signal.
func extractNotableLines(msg string) string {
	lines := strings.Split(msg, "\n")
	if len(lines) <= 1 {
		return msg
	}
	var notable []string
	for _, l := range lines {
		if hasNotableKeyword(l) {
			notable = append(notable, strings.TrimSpace(l))
		}
	}
	if len(notable) == 0 {
		return msg
	}
	return strings.Join(notable, " | ")
}

// hasNotableKeyword reports whether a log line contains a word that marks it
// as an error or warning line (as opposed to a stack-frame or continuation line).
func hasNotableKeyword(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "error") ||
		strings.Contains(lower, "warn") ||
		strings.Contains(lower, "fatal") ||
		strings.Contains(lower, "panic") ||
		strings.Contains(lower, "exception")
}

// truncateMsg returns the first n runes of s, with an ellipsis when truncated.
// Used to keep log lines readable when messages contain stack traces.
func truncateMsg(s string, n int) string {
	// Use first line only to avoid multiline log values.
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}