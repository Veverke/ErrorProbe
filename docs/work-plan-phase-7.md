# Phase 7 — Rule-Based Detection Policy

**Goal:** Give developers per-container control over what constitutes `FAILING`. The global Tier 2 threshold from Phase 6 becomes the fallback; named rules in `errorprobe.yaml` override it per container or glob pattern.  
**Prerequisite:** Phase 6 complete (Tier 2 evaluator, `FAILING` state, fingerprinter, and history log all exist).

**UT coverage requirement: ≥ 90% on all new packages and functions.**

---

## Background & Motivation

Phase 6 introduces a single global promotion policy: N errors with the same fingerprint within a window → `FAILING`. This works well as a default but is too coarse for real developer workloads, where different containers have very different error-signal expectations:

- A **payments service** should fail immediately on 3 errors in 1 minute — it has no tolerance for faults.
- A **background worker** emitting occasional retries may need 50+ errors before the signal is meaningful.
- A **database container** should go to `FAILING` the instant it logs `"connection refused"` — no count required.
- An **auth service** logging `"OOMKilled"` twice is a hard escalation regardless of rate.

The global threshold cannot express any of these distinctions. Rule-based policies close that gap entirely in `errorprobe.yaml` — no new tooling, no UI, no secondary config file. The developer describes their intent next to the rest of their ErrorProbe configuration.

---

## Config Schema Extension

Rules are defined under `detection.rules` in `errorprobe.yaml`, keyed by container name or glob. The global `detection.tier2` settings remain as the fallback for any container not matched by a rule.

```yaml
detection:
  tier2:
    window: 3m
    threshold: 10       # global default — applies when no rule matches

  rules:
    payments-api:
      threshold: 3      # stricter: 3 errors in window → FAILING
      window: 1m

    worker-*:
      threshold: 50     # more tolerant

    db-*:
      pattern: "connection refused"   # zero-tolerance: 1 match → FAILING immediately

    auth-service:
      pattern: "OOMKilled"
      threshold: 2      # pattern must appear ≥ 2 times before promoting
```

**Rule resolution order:**
1. Walk `detection.rules` in config declaration order.
2. First key that matches the container name (exact match first, then `filepath.Match` glob) wins.
3. If no rule matches, fall back to `detection.tier2` global defaults.

A rule may specify `threshold`, `window`, `pattern`, or any combination:
- `threshold` alone — override the error count required to promote; use global `window` if not set.
- `window` alone — override the evaluation window; use global `threshold` if not set.
- `pattern` alone — zero-tolerance: one matching error message → `FAILING`; no count or window required.
- `pattern` + `threshold` — pattern must match at least `threshold` times before promoting.
- `threshold` + `window` — standard rate-based override with both axes explicit.

---

## Atomic Tasks

Tasks are grouped by dependency tier. All tasks within a tier can be implemented independently and in parallel.

---

### Tier 1 — Config schema and rule resolution (no Phase 7 dependencies beyond Phase 6)

#### T7.1 — Extend config schema: `detection.rules`
- Add `DetectionRule` struct to `internal/config`:
  ```go
  type DetectionRule struct {
      Threshold *int          // nil = use global default
      Window    *Duration     // nil = use global default
      Pattern   string        // empty = no pattern rule
  }
  ```
- Rules stored as `[]NamedRule` (slice of `{Key string, Rule DetectionRule}`) to preserve declaration order — **not** a map, which would lose ordering
- Add `Rules []NamedRule` field to `Detection` struct
- Validate on load:
  - Rule key must be non-empty
  - If `Threshold` set: must be ≥ 1
  - If `Window` set: must be a valid duration ≥ 1s
  - If `Pattern` + `Threshold` both set: `Threshold` must be ≥ 1
- Update `config_test.go` with valid and invalid rule cases

#### T7.2 — Implement rule resolver: `RuleFor`
- `RuleFor(containerName string, cfg *config.Config) config.ResolvedRule` in `internal/health` package
- `ResolvedRule` always has concrete `Threshold int`, `Window time.Duration`, `Pattern string`, and `Key string` (the rule key that matched, or `"<global>"` for fallback)
- Resolution logic:
  1. Walk `cfg.Detection.Rules` in slice order
  2. If key equals `containerName` exactly → match
  3. Else if `filepath.Match(key, containerName)` → match
  4. First match wins; return resolved rule with nil fields filled from global `detection.tier2` defaults
  5. No match → return rule built entirely from global defaults with `Key = "<global>"`
- Pure function — no I/O; deterministic; fully unit-testable

---

### Tier 2 — Pattern rule evaluation (depends on T7.1, T7.2)

#### T7.3 — Implement pattern matcher
- `MatchesPattern(message, pattern string) bool` in `internal/health` package
- Case-insensitive substring match (simple, predictable for developers)
- If `pattern` is empty, always returns false (no-op — threshold-only rule)
- Pure function — no I/O

#### T7.4 — Integrate pattern rules into `Engine.ProcessBatch`
- After existing fingerprint recording, check the resolved rule for the container via `RuleFor`
- If rule has a non-empty `Pattern` and the event message matches via `MatchesPattern`:
  - Increment a per-container pattern match counter (new field `PatternMatchCount int` on `ContainerHealth`)
  - If `PatternMatchCount` ≥ resolved rule `Threshold` (default 1 if not set): call `engine.SetFailing` immediately
  - Record the triggered rule key in `ContainerHealth` (new field `TriggeredRule string`) for display
- Pattern-triggered `SetFailing` appends to the history log with `Reason: "pattern: <pattern>"` — reuse Phase 6's `HistoryLog.Append`

---

### Tier 3 — Tier 2 evaluator uses per-container rules (depends on T7.2)

#### T7.5 — Extend Tier 2 evaluator to use `RuleFor`
- In `Tier2Evaluator` (Phase 6 T6.5), replace direct reads of `cfg.Detection.Tier2.Window` and `cfg.Detection.Tier2.Threshold` with `RuleFor(containerName, cfg)` on each tick per container
- No structural change to the evaluator loop — only the threshold/window values change
- Containers with a matching rule use their rule values; unmatched containers continue using global defaults
- Record `TriggeredRule` on `ContainerHealth` when the evaluator promotes a container to `FAILING`

---

### Tier 4 — TUI and status surface the triggered rule (depends on T7.4, T7.5)

#### T7.6 — Surface triggered rule in `errorprobe status`
- FAILING containers show an additional annotation line: `triggered by: <rule-key> (<pattern or threshold>)`
- Example outputs:
  ```
  ✗ FAILING   db-postgres    triggered by: db-*  (pattern: "connection refused")
  ✗ FAILING   payments-api   triggered by: payments-api  (threshold: 3 in 1m)
  ✗ FAILING   worker-jobs    triggered by: <global>  (threshold: 10 in 3m)
  ```
- `--json` output: add `triggered_rule` and `triggered_reason` fields to FAILING container objects

#### T7.7 — Surface triggered rule in `errorprobe watch` TUI
- FAILING row expandable detail (key `[e]`) shows triggered rule key and reason
- Format mirrors `status` output; fits within existing expand panel without layout change

---

### Tier 5 — Unit tests (depends on T7.1–T7.5; independent of each other)

#### T7.8 — Unit tests: config schema and validation
- `TestDetectionRules_ValidConfig_LoadsCorrectly`: config with multiple rules including pattern and threshold rules; assert all fields parsed
- `TestDetectionRules_InvalidThreshold_RejectsZero`: threshold of 0; assert validation error
- `TestDetectionRules_InvalidWindow_RejectsBadDuration`: window `"xyz"`; assert validation error
- `TestDetectionRules_EmptyKey_Rejected`: rule with empty key; assert validation error
- `TestDetectionRules_MissingRules_UsesDefaults`: config with no `detection.rules`; assert no error; `RuleFor` returns global defaults

#### T7.9 — Unit tests: rule resolver
- `TestRuleFor_ExactMatch`: container name exactly matches a rule key; assert rule values returned
- `TestRuleFor_GlobMatch`: container name matches a glob key (e.g., `worker-jobs` matches `worker-*`); assert rule values returned
- `TestRuleFor_ExactMatchBeforeGlob`: both an exact rule and a glob rule would match; assert exact match wins
- `TestRuleFor_FirstGlobMatchWins`: two glob rules both match; assert first in declaration order wins
- `TestRuleFor_FallbackToGlobal`: no rule matches; assert global `tier2` values returned as concrete values with `Key = "<global>"`
- `TestRuleFor_PartialRule_InheritsGlobals`: rule sets only `threshold`; assert `window` inherited from global default

#### T7.10 — Unit tests: pattern matcher
- `TestMatchesPattern_SubstringMatch`: message contains pattern substring; assert true
- `TestMatchesPattern_CaseInsensitive`: pattern lowercase, message uppercase; assert true
- `TestMatchesPattern_NoMatch`: unrelated message; assert false
- `TestMatchesPattern_EmptyPattern_AlwaysFalse`: empty pattern; assert false regardless of message

#### T7.11 — Unit tests: pattern rule in engine
- `TestEngine_PatternRule_SingleMatch_PromotesToFailing`: container has pattern rule with default threshold 1; one matching error event → assert FAILING
- `TestEngine_PatternRule_BelowThreshold_NoPromotion`: pattern rule with threshold 2; one match → assert still HAS_ERRORS
- `TestEngine_PatternRule_ThresholdMet_PromotesToFailing`: threshold 2; two matching events → assert FAILING
- `TestEngine_PatternRule_NoPattern_NotTriggered`: rule has threshold only; matching message → assert no pattern promotion (threshold evaluator handles it)
- `TestEngine_PatternRule_SetsTriggeredRule`: promotion via pattern; assert `TriggeredRule` field set on `ContainerHealth`

#### T7.12 — Unit tests: evaluator uses container rule
- `TestEvaluator_UsesContainerRule_NotGlobal`: container has rule with threshold 3; global is 10; Loki mock returns count 5; assert FAILING (rule fires, global would not)
- `TestEvaluator_GlobalFallback_WhenNoRule`: container has no rule; Loki returns count above global threshold; assert FAILING
- `TestEvaluator_SetsTriggeredRule_OnPromotion`: evaluator promotes container; assert `TriggeredRule` set on health state

---

### Final Tasks

#### T7.13 — Mark phase complete in work-plan.md
- Open `docs/work-plan.md`
- Mark all Phase 7 tasks as `[x]`
- Add completion date next to phase heading

#### T7.14 — Update roadmap.html
- In the `PHASES` array, set Phase 7's `status` to `"completed"` and `actualEnd` to the actual finish date
- Compare actual duration against the planned estimate; if velocity differed, adjust `start` / `end` for subsequent phases accordingly
- Update the `TODAY` constant to the current date
- Recompute and document the revised projected completion date for remaining phases in a comment above the `PHASES` array

---

## Deliverables

| Deliverable | Description |
|---|---|
| `config.DetectionRule`, `config.NamedRule`, `config.ResolvedRule` | Rule schema with validation in `internal/config` |
| `health.RuleFor` | Per-container rule resolver with exact + glob matching and global fallback |
| `health.MatchesPattern` | Case-insensitive substring pattern matcher |
| `ContainerHealth.PatternMatchCount`, `.TriggeredRule` | New fields on health state |
| `Engine.ProcessBatch` (extended) | Pattern rule evaluation on every inbound error event |
| `Tier2Evaluator` (extended) | Uses `RuleFor` per container instead of global config directly |
| `errorprobe status` | FAILING containers annotated with triggered rule and reason |
| `errorprobe watch` TUI | Triggered rule visible in expandable detail row |
| Unit tests | ≥ 90% coverage on new functions in `internal/config` and `internal/health` |

---

## Manual Tests

Run after all tasks are complete:

1. **Global fallback** — no `detection.rules` in `errorprobe.yaml`; run a container emitting the same error > 10 times in 3 minutes; confirm it transitions from `⚠ HAS ERRORS` to `✗ FAILING` (global Tier 2 default fires as before Phase 7).
2. **Stricter threshold override** — add a rule for the container with `threshold: 3, window: 1m`; emit 3 matching errors in under 1 minute; confirm `FAILING` is reached before the global threshold would fire.
3. **More tolerant threshold override** — add a rule with `threshold: 50`; emit 15 errors (above global default of 10); confirm container stays at `⚠ HAS ERRORS` (rule threshold not yet met).
4. **Zero-tolerance pattern rule** — add a pattern rule (`pattern: "connection refused"`); emit a single log line containing that string; confirm immediate transition to `✗ FAILING` without waiting for a count window.
5. **Pattern with threshold** — add `pattern: "OOMKilled", threshold: 2`; emit one matching log line; confirm still `⚠ HAS ERRORS`; emit a second; confirm `✗ FAILING`.
6. **Triggered rule annotation in status** — `errorprobe status` on a FAILING container; confirm output shows `triggered by:` line with rule key and pattern/threshold details.
7. **Triggered rule in TUI** — `errorprobe watch`; navigate to a FAILING container; press `[e]`; confirm triggered rule and reason appear in the expanded detail row.
8. **`errorprobe check` unchanged** — rule-triggered `FAILING` exits 1 with `fail_on: FAILING`; same as Phase 6 behaviour.
9. **`go test ./... -cover`** — all tests pass; coverage ≥ 90% on updated `internal/config` and `internal/health`.
