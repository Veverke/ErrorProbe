package learn

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/errorprobe/errorprobe/internal/logger"
)

// Applier manages the lifecycle of learned rules: writing them to the overlay
// file, confirming them as operator-validated, and recording false-positive
// suppressions.
type Applier struct {
	overlayPath     string
	pendingPath     string
	suppressionPath string
	onReload        func() // called after the overlay or suppression list changes
	mu              sync.Mutex
}

// NewApplier creates an Applier.
// overlayPath is the file for confirmed/learned rules.
// pendingPath is the scratch file for rules awaiting user review.
// suppressionPath is the file for false-positive patterns.
// onReload is called (without the lock held) whenever the overlay changes so
// the caller can hot-swap the compiled rule set; pass nil to disable.
func NewApplier(overlayPath, pendingPath, suppressionPath string, onReload func()) *Applier {
	return &Applier{
		overlayPath:     overlayPath,
		pendingPath:     pendingPath,
		suppressionPath: suppressionPath,
		onReload:        onReload,
	}
}

// Apply writes rule to the overlay file and triggers a reload.
// If a rule with the same name already exists in the overlay, it is replaced.
// Apply is safe to call concurrently.
func (a *Applier) Apply(rule LearnedRule) error {
	a.mu.Lock()
	rules, err := LoadOverlay(a.overlayPath)
	if err != nil {
		a.mu.Unlock()
		return fmt.Errorf("apply: loading overlay: %w", err)
	}

	rules = upsertRule(rules, rule)

	if err := SaveOverlay(a.overlayPath, rules); err != nil {
		a.mu.Unlock()
		return fmt.Errorf("apply: saving overlay: %w", err)
	}
	a.mu.Unlock()

	if a.onReload != nil {
		a.onReload()
	}
	return nil
}

// Pending writes rule to the pending scratch file without triggering a reload.
// Rules in the pending file are shown to the operator via the ⚑ ? indicator
// but are not yet part of the active rule set.
func (a *Applier) Pending(rule LearnedRule) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	rules, err := LoadPending(a.pendingPath)
	if err != nil {
		return fmt.Errorf("pending: loading pending file: %w", err)
	}
	rules = upsertRule(rules, rule)
	if err := SavePending(a.pendingPath, rules); err != nil {
		return fmt.Errorf("pending: saving pending file: %w", err)
	}
	return nil
}

// ConfirmRule moves rule name from pending (if present) to the overlay with
// source=confirmed and triggers a reload.
func (a *Applier) ConfirmRule(name string) error {
	a.mu.Lock()

	// Find the rule in pending or overlay.
	var rule *LearnedRule
	pending, _ := LoadPending(a.pendingPath)
	for i := range pending {
		if pending[i].Name == name {
			r := pending[i]
			rule = &r
			break
		}
	}
	if rule == nil {
		overlay, _ := LoadOverlay(a.overlayPath)
		for i := range overlay {
			if overlay[i].Name == name {
				r := overlay[i]
				rule = &r
				break
			}
		}
	}
	if rule == nil {
		a.mu.Unlock()
		return fmt.Errorf("confirm: rule %q not found", name)
	}

	// Promote to confirmed.
	now := time.Now().UTC()
	rule.Source = SourceConfirmed
	rule.ConfirmedAt = &now

	// Remove from pending.
	newPending := removeRule(pending, name)
	if err := SavePending(a.pendingPath, newPending); err != nil {
		logger.Warn("applier: could not save pending after confirm", "err", err)
	}

	// Upsert into overlay.
	overlay, _ := LoadOverlay(a.overlayPath)
	overlay = upsertRule(overlay, *rule)
	if err := SaveOverlay(a.overlayPath, overlay); err != nil {
		a.mu.Unlock()
		return fmt.Errorf("confirm: saving overlay: %w", err)
	}
	a.mu.Unlock()

	if a.onReload != nil {
		a.onReload()
	}
	return nil
}

// RejectRule removes rule name from the overlay and pending files, and adds
// pattern to the suppression list so it is never re-learned.
// Returns a non-nil error if any on-disk write fails so the caller can detect
// an inconsistent state (e.g. rule still active, suppression not recorded).
func (a *Applier) RejectRule(name, pattern string) error {
	a.mu.Lock()

	var errs []error

	// Remove from overlay.
	overlay, loadErr := LoadOverlay(a.overlayPath)
	if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("reject: loading overlay: %w", loadErr))
	} else {
		overlay = removeRule(overlay, name)
		if err := SaveOverlay(a.overlayPath, overlay); err != nil {
			errs = append(errs, fmt.Errorf("reject: saving overlay: %w", err))
		}
	}

	// Remove from pending.
	pending, loadErr := LoadPending(a.pendingPath)
	if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("reject: loading pending: %w", loadErr))
	} else {
		pending = removeRule(pending, name)
		if err := SavePending(a.pendingPath, pending); err != nil {
			errs = append(errs, fmt.Errorf("reject: saving pending: %w", err))
		}
	}

	// Add to suppression.
	sl, loadErr := LoadSuppressionList(a.suppressionPath)
	if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("reject: loading suppression list: %w", loadErr))
	} else if !sl.Contains(pattern) {
		sl.Add(SuppressionEntry{
			Pattern: pattern,
			AddedAt: time.Now().UTC(),
			Reason:  "operator rejected",
		})
		if err := sl.Save(); err != nil {
			errs = append(errs, fmt.Errorf("reject: saving suppression list: %w", err))
		}
	}
	a.mu.Unlock()

	if a.onReload != nil {
		a.onReload()
	}
	return errors.Join(errs...)
}

// OverlayRules returns the current contents of the overlay file.
// Safe to call concurrently with Apply/ConfirmRule/RejectRule.
func (a *Applier) OverlayRules() []LearnedRule {
	a.mu.Lock()
	defer a.mu.Unlock()
	rules, _ := LoadOverlay(a.overlayPath)
	return rules
}

// upsertRule returns rules with r upserted by name: if a rule with r.Name
// already exists it is replaced in-place; otherwise r is appended.
func upsertRule(rules []LearnedRule, r LearnedRule) []LearnedRule {
	for i, existing := range rules {
		if existing.Name == r.Name {
			rules[i] = r
			return rules
		}
	}
	return append(rules, r)
}

// removeRule returns rules with all entries named name removed.
func removeRule(rules []LearnedRule, name string) []LearnedRule {
	out := rules[:0:0]
	for _, r := range rules {
		if r.Name != name {
			out = append(out, r)
		}
	}
	return out
}
