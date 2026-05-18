# Learning Module — Adaptive Rule Discovery

## Motivation

ErrorProbe ships four built-in PBR rules and a configurable `severity_patterns` block.
These cover the common case (applications that log at the `error` or `warn` level) but
miss a broad class of real-world error conditions:

- Applications that log errors at `info` level with keywords like `FAILED`, `Exception`, or `HTTP 503`
- Frameworks that use non-standard level names (`CRITICAL`, `SEVERE`, `NOTICE`)
- Language-specific exception formats (Java stack traces, Python tracebacks, Go panics in messages)
- OS and resource exhaustion messages (`OOMKilled`, `ENOSPC`, `ECONNREFUSED`)
- Database errors, circuit-breaker events, security failures — all commonly logged outside the standard `error` level

The Learning Module observes the live log stream, identifies error conditions that no
current rule covers, and generates new PBR rules to close those gaps — without user
intervention beyond initial consent and post-hoc validation.

---

## Design Decisions

### 1. Goroutine inside `ep up`, not a user command

The module is a goroutine started by `ep up`. It is not exposed as a CLI command.
It sleeps until a qualifying trigger fires, runs its analysis, then goes back to sleep.
When the system is healthy the goroutine consumes nothing. There is no polling interval
that wastes cycles returning empty results.

### 2. Trigger-based activation

The goroutine wakes only on qualifying health-state-transition events emitted by the
health engine. A low-frequency background scan is also available as an opt-in catch-all.

### 3. False-positive safety is the primary constraint

It is always better to miss a new error condition than to assert a false positive.
All candidate rules pass a multi-gate confidence model before being applied.
Rules derived by the module are permanently tagged `source: learned` and rendered with a
distinct visual marker in `ep watch` until the user explicitly validates them.

### 4. `learn.enabled` defaults to `true`

The module is part of EP's core value. Users who do not want it set `enabled: false`.
The module activates on the next `ep up` after the configuration is read.

### 5. No LLM dependency

Classification is purely heuristic. An LLM backend is noted as a future option
(configurable endpoint, no cost unless user opts in) and is out of scope for this phase.

### 6. Learned rules live in a sidecar overlay file

New rules are written to `errorprobe.learned.yaml` (sidecar to `errorprobe.yaml`).
EP loads and merges this file automatically — learned rules are fully active PBR rules.
The file is always human-readable YAML in the standard `rules:` format.
The user can inspect, edit, or delete it at any time to reset learned state.
A separate `errorprobe.learned.pending.yaml` holds candidates that passed keyword scoring
but fell below the auto-apply confidence threshold; these are never applied automatically.
A third file, `errorprobe.learned.suppressed.yaml`, holds patterns the user has marked as
false positives; the module never re-learns these patterns.

### 7. Rule changes are soft reloads — no container restarts

PBR rule changes are already classified as soft changes in `internal/stack/classify.go`.
The module triggers the existing reload path: regenerate Vector config, SIGHUP Vector,
signal the running `ep up` process to hot-swap the rule set. No containers restart.

### 8. Priority band reservation

Learned rules are assigned priorities in the band **500–999**.
This places them above built-ins (≤ 110) but below typical user rules (≥ 1000 by convention).
Within the band, priorities are assigned in descending order of confidence score so higher-
confidence rules evaluate first.

---

## Triggers

The learner goroutine subscribes to health-state-transition events published by the health
engine. It wakes on the following transitions:

| Trigger | Direction | Rationale |
|---|---|---|
| `OK → HAS_ERRORS`, `MatchedRule = ""` | Forward | Proven rule gap — no rule fired yet a container is now in error |
| `OK → HAS_ERRORS`, rule did fire | Forward | Rule covered one event; co-occurring uncovered patterns in the same window may also be error signals |
| `OK → FAILING` | Forward | Severe case; high confidence that surrounding log patterns are error-related |
| Container restarts unexpectedly | Forward | Pre-restart log lines are high-confidence error material |
| New container enters error state immediately | Forward | First exposure; novel patterns never seen before |
| `HAS_ERRORS → OK` | Backward | Retrospective: the recovery labels the preceding window as confirmed error ground truth |
| `FAILING → OK` | Backward | High-confidence retrospective; strongest labeling signal |
| Low-frequency background scan *(opt-in)* | Scheduled | Catch slow-burn patterns that never triggered a clean state transition |

For backward triggers the analysis window spans from `T_enter_error` to `T_recover`.
Patterns present in that labeled window but matched by no existing rule are validated
gap candidates with elevated confidence.

---

## Learning Strategies and State Mapping

Each strategy produces either `HAS_ERRORS` or `DEGRADED` on the resulting rule.
All learned-rule hits carry the `⚑ ?` indicator in `ep watch` regardless of state.

| Strategy | Trigger(s) | Generated state |
|---|---|---|
| **Exit/restart correlation** — scan last N lines before container exit | Container restart | `HAS_ERRORS` |
| **Retrospective gap analysis** — uncovered patterns in a confirmed error window | Backward triggers | `HAS_ERRORS` |
| **Pattern-gap: high-tier keyword** in message at any log level | Forward triggers | `HAS_ERRORS` |
| **Pattern-gap: medium-tier keyword** in message at any log level | Forward triggers | `HAS_ERRORS` |
| **Pattern-gap: low-tier keyword** in message at any log level | Forward triggers | `DEGRADED` |
| **Co-occurrence** — uncovered patterns present in window where a rule did fire | Forward triggers | `HAS_ERRORS` |
| **Spike/anomaly detection** — new message shape at unusual frequency | Background scan / forward | `DEGRADED` |
| **Cross-container cascade** — B spikes when A enters error | Forward triggers | `DEGRADED` |
| **Repeated pattern pressure** — same uncovered shape repeating silently | Background scan | `DEGRADED` |

### Keyword tiers

| Tier | Keywords | Confidence multiplier |
|---|---|---|
| **High** | `panic`, `fatal`, `exception`, `oomkilled`, `stack trace`, `traceback`, `sigsegv`, `sigabrt`, `core dumped` | 1.0× |
| **Medium** | `error:`, `err:`, `FAILED`, `refused`, `denied`, `unreachable`, `HTTP 5`, `ORA-`, `SQLSTATE`, `deadlock`, `ECONNREFUSED`, `ENOSPC`, `ENOMEM`, `out of memory`, `no space left`, `certificate expired`, `ssl handshake` | 0.8× |
| **Low** | `timeout`, `retry exhausted`, `circuit breaker`, `slow`, `degraded`, `unexpected`, `HTTP 4` | 0.6× |
| **Blocked** | `error` (standalone), `fail` (standalone) — matched as whole words | suppressed unless ≥ 2 other signals present |

### Blocklist — known false-positive phrases

These exact phrases are stripped before scoring regardless of keyword tier:
`"no error"`, `"0 errors"`, `"error count: 0"`, `"error: none"`, `"error: <nil>"`,
`"suppressed error"`, `"ignoring error"`, `"expected error"`, `"error: ok"`,
`"no errors found"`.

---

## Multi-Gate Confidence Model

A candidate pattern must pass every gate before a rule is generated:

| Gate | Logic | Disqualifies if... |
|---|---|---|
| **Keyword gate** | At least one keyword from tier High, Medium, or unblocked Low present | No qualifying keyword |
| **Blocklist gate** | Phrase does not match any blocklist entry | Phrase is a known false-positive |
| **Repetition gate** | Pattern appeared in ≥ 2 independent analysis windows | Single occurrence only → candidate file, not a rule |
| **Specificity gate** | After generalisation, regex is > 15 chars and matches < 5% of sampled messages | Regex too short or too generic |
| **Composite score** | `score = keyword_multiplier × repetition_factor × specificity_factor` | `score < review_threshold` → log only; `score < confidence_threshold` → pending file |

No healthy-window exclusion gate is applied. Startup errors that occur every run are real
errors and must not be silently discarded because they also appear in "healthy" windows.

### Scoring outcomes

| Score | Outcome |
|---|---|
| `≥ confidence_threshold` + `auto_apply: true` | Write to overlay file + trigger soft reload |
| `≥ confidence_threshold` + `auto_apply: false` | Write to overlay file; log: `new rules written — run 'ep reload' to apply` |
| `≥ review_threshold`, `< confidence_threshold` | Write to `errorprobe.learned.pending.yaml`; log notice |
| `< review_threshold` | Log only; no file written |

---

## Pattern Extraction

When a candidate event is identified, its message is generalised into a stable regex:

1. Strip dynamic segments: IPv4/IPv6 addresses, port numbers, UUIDs, hex identifiers,
   Unix timestamps, numeric IDs, file paths with version numbers
2. Collapse repeated whitespace
3. Escape remaining regex metacharacters in the static text
4. Verify specificity gate against the generalised form
5. Produce a case-insensitive `(?i)` regex suitable for a `message: "regex:..."` PBR condition

Example:
```
"Connection refused to 10.0.1.23:5432 after 3 retries"
  → (?i)connection refused.*after.*retr
```

---

## Rule Source Tagging and UI

Every rule written by the learning module carries a `source` field:

- `source: learned` — generated by the module, not yet validated by the user
- `source: confirmed` — user has validated the rule via `ep watch`

The `source` field is stored in `errorprobe.learned.yaml` alongside the standard rule
fields. It is not part of the PBR evaluation logic; it is metadata used by the TUI.

### `ep watch` changes

Containers whose current health state was set by a `source: learned` rule render with a
distinct indicator:

```
CONTAINER      FUNCTIONAL           INFRA    ERRORS  LAST ERROR
payments-api   ⚑ HAS ERRORS ?      running  2       INFO: FAILED to reach upstream…
user-service   ✓ OK                 running  0       —
db-primary     ⚠ HAS ERRORS         running  5       ERROR connection refused
```

Two new keyboard shortcuts are added to `ep watch`:

| Key | Action |
|---|---|
| `v` | **Validate** — confirm the selected container's current inferred state is a real error; the rule's `source` is updated from `learned` to `confirmed`; the `⚑ ?` marker is removed permanently |
| `f` | **False positive** — remove the rule from the overlay file; add its pattern to `errorprobe.learned.suppressed.yaml`; the module will never re-learn this pattern |

The `ep watch` help overlay and the user guide are updated to document both shortcuts.

### EKG waveform colour tiers

The EKG animation gains a cyan tier for the flagged state. Colours are evaluated in
precedence order — the highest-severity active state wins:

| EKG colour | Condition | Precedence |
|---|---|---|
| Red | ≥ 1 container is `FAILING` (confirmed) | 1 (highest) |
| Yellow | ≥ 1 container is `HAS_ERRORS` or `DEGRADED` (confirmed) | 2 |
| Cyan | No confirmed errors; ≥ 1 container has an active `⚑ ?` inferred condition | 3 |
| Green | All containers confirmed OK; no inferred conditions active | 4 (lowest) |

Cyan only shows when the worst active state across all containers is `source: learned`
and no confirmed error or failing state is present.

---

## Configuration

New top-level `learn:` block in `errorprobe.yaml`:

```yaml
learn:
  enabled: true                    # master switch; set false to disable entirely; default: true
  auto_apply: true                 # write overlay + reload on high-confidence findings; default: true
  confidence_threshold: 0.75       # composite score required for auto-apply; range 0.0–1.0
  review_threshold: 0.50           # minimum score to write to pending file
  overlay_file: ""                 # path to learned rules file; default: errorprobe.learned.yaml
  suppression_file: ""             # path to suppression list; default: errorprobe.learned.suppressed.yaml
  promote_to_config: false         # when true, user is offered to merge stable confirmed rules into errorprobe.yaml
  background_scan: false           # enable periodic catch-all scan; default: false
  background_scan_interval: 6h     # interval for background scan
```

`promote_to_config: true` causes EP to periodically offer (via log notice) to merge rules
that have been `source: confirmed` and active for ≥ 30 days into the main `errorprobe.yaml`.
It never merges automatically — it only prompts.

---

## New Package: `internal/learn`

| File | Responsibility |
|---|---|
| `types.go` | `Candidate`, `ScoredCandidate`, `LearnedRule`, `SuppressionEntry` types; source tag constants |
| `keyword.go` | Keyword tier definitions, blocklist, `ScoreKeywords(msg string) float64` |
| `extractor.go` | `ExtractPattern(msg string) (string, error)` — dynamic-segment stripping and regex generalisation |
| `classifier.go` | Multi-gate scoring pipeline; `Classify(events []ingest.LogEvent, rules []pbr.Rule) []ScoredCandidate` |
| `sampler.go` | Loki query helpers for the analysis window; wraps `loki.Client` |
| `gap.go` | `FindUncovered(events []ingest.LogEvent, rules []pbr.Rule) []ingest.LogEvent` — runs PBR and returns events where `MatchedRule = ""` |
| `generator.go` | `GenerateRules(candidates []ScoredCandidate, nextPriority int) []config.RuleConfig` |
| `overlay.go` | Load / merge / write `errorprobe.learned.yaml`; load / write `errorprobe.learned.pending.yaml` |
| `suppress.go` | Load / append / query `errorprobe.learned.suppressed.yaml` |
| `learner.go` | Main goroutine: subscribes to state-transition events, coordinates all components, applies findings |

---

## Atomic Tasks

Tasks are grouped by dependency tier. All tasks within a tier can be implemented independently.

**UT coverage requirement: ≥ 90% on all new packages and functions.**

---

### Tier 1 — Configuration and types (no dependencies)

#### TL1.1 — Add `LearnConfig` to `internal/config/config.go`
- Add `LearnConfig` struct with all fields listed in the Configuration section above
- Add `Learn LearnConfig` field to `Config`
- Add `mapstructure` tags matching the YAML key names
- `enabled` defaults to `true`; `auto_apply` defaults to `true`; `confidence_threshold` defaults to `0.75`; `review_threshold` defaults to `0.50`; `background_scan_interval` defaults to `6h`
- Add `LearnOverlayFile() string` and `LearnSuppressionFile() string` methods on `Config` that resolve the configured paths against the config file directory when relative, or return the default sidecar paths when the field is empty
- Add tests in `config_test.go` covering default values and path resolution

#### TL1.2 — Create `internal/learn/types.go`
- `SourceTag` string type with constants `SourceLearned = "learned"` and `SourceConfirmed = "confirmed"`
- `Candidate` struct: `Event ingest.LogEvent`, `Pattern string`, `KeywordTier int`, `Windows int` (number of independent windows the pattern appeared in), `MatchFraction float64` (fraction of sampled messages this regex matches)
- `ScoredCandidate` struct: embeds `Candidate`, adds `Score float64`, `GeneratedState string`
- `LearnedRule` struct: embeds `config.RuleConfig`, adds `Source SourceTag`, `DiscoveredAt time.Time`, `ConfirmedAt *time.Time`
- `SuppressionEntry` struct: `Pattern string`, `AddedAt time.Time`, `Reason string`
- `StateTransitionEvent` struct: `Container string`, `Namespace string`, `PrevState string`, `NewState string`, `MatchedRule string`, `At time.Time`

#### TL1.3 — Create `internal/learn/keyword.go`
- `KeywordTierHigh`, `KeywordTierMedium`, `KeywordTierLow`, `KeywordTierBlocked` int constants
- `highKeywords`, `mediumKeywords`, `lowKeywords`, `blockedKeywords` string slices — exact values from the Design Decisions section
- `falsePositiveBlocklist` string slice — exact values from the Blocklist section
- `ScoreKeywords(msg string) (tier int, multiplier float64)` — returns the highest-tier keyword found (case-insensitive whole-word match for blocked keywords; substring match for others) and its multiplier; returns `(0, 0)` when no keyword found or message matches blocklist
- Unit tests covering tier assignment, blocked-keyword whole-word matching, and blocklist rejection

#### TL1.4 — Create `internal/learn/suppress.go`
- `SuppressionList` type wrapping `[]SuppressionEntry`
- `LoadSuppressionList(path string) (SuppressionList, error)` — reads YAML; returns empty list when file does not exist
- `(sl SuppressionList) IsSuppressed(pattern string) bool`
- `(sl *SuppressionList) Add(pattern string, reason string)` — appends entry with current timestamp
- `SaveSuppressionList(path string, sl SuppressionList) error` — writes YAML atomically (write to `.tmp`, rename)
- Unit tests: load missing file returns empty list; add and query; save and reload round-trips

#### TL1.5 — Create `internal/learn/overlay.go`
- `LoadOverlay(path string) ([]LearnedRule, error)` — reads overlay YAML; returns empty slice when file does not exist
- `SaveOverlay(path string, rules []LearnedRule) error` — writes YAML atomically
- `MergeOverlay(base []config.RuleConfig, overlay []LearnedRule) []config.RuleConfig` — appends overlay rules to base after deduplication by name; overlay rules with the same name as a base rule are skipped (user rules always win)
- `LoadPending(path string) ([]LearnedRule, error)` and `SavePending(path string, rules []LearnedRule) error` — same semantics as overlay but for the pending file
- Unit tests: missing file returns empty; merge deduplication; atomic save verified by checking temp file absence after save

---

### Tier 2 — Analysis core (depends on TL1.2, TL1.3, TL1.4)

#### TL2.1 — Create `internal/learn/gap.go`
- `FindUncovered(events []ingest.LogEvent, rules []pbr.Rule, windowCounts map[string]int) []ingest.LogEvent`
  - For each event, construct a `pbr.LogEvalContext` and call `pbr.Evaluate`
  - Return only events where `EvalResult.MatchedRule == ""`
  - `windowCounts` supplies `count_in_window` per container (keyed by container name)
- Unit tests: events matched by existing rules are excluded; events with no matching rule are returned; both builtin and user rules are respected

#### TL2.2 — Create `internal/learn/extractor.go`
- `ExtractPattern(msg string) (string, error)` — applies dynamic-segment stripping in order:
  1. IPv4 addresses and ports (`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(?::\d+)?`)
  2. IPv6 addresses
  3. UUIDs (`[0-9a-f]{8}-[0-9a-f]{4}-...`)
  4. Hex identifiers of ≥ 8 chars (`0x[0-9a-f]+`)
  5. Unix timestamps (10–13 digit integers)
  6. Standalone integers ≥ 4 digits
  7. File paths containing version numbers
  - After stripping, collapse runs of whitespace and trim; escape regex metacharacters in static text; prepend `(?i)` prefix
  - Return error if resulting pattern is ≤ 15 characters
- `MatchFraction(pattern string, sample []string) (float64, error)` — compiles pattern and returns fraction of sample strings that match; returns error on invalid regex
- Unit tests: IP stripping, UUID stripping, specificity rejection, round-trip compile of produced patterns

#### TL2.3 — Create `internal/learn/classifier.go`
- `Classify(candidates []ingest.LogEvent, rules []pbr.Rule, windowID string, suppressList SuppressionList) []ScoredCandidate`
  - For each candidate event:
    1. Apply keyword gate — skip if `ScoreKeywords` returns 0
    2. Apply blocklist gate — already handled inside `ScoreKeywords`
    3. Apply suppression gate — skip if `suppressList.IsSuppressed(message)`
    4. Call `ExtractPattern` — skip on error (pattern too generic)
    5. Record candidate by pattern; increment `Windows` counter if pattern seen in prior window
    6. Compute `score = keyword_multiplier × clamp(windows/2, 0, 1) × (1 - matchFraction)`
    7. Assign `GeneratedState`: `"HAS_ERRORS"` for tier High or Medium; `"DEGRADED"` for tier Low
  - Return slice sorted by score descending
- Unit tests: gate rejection at each stage; score ordering; HAS_ERRORS vs DEGRADED assignment; suppressed patterns excluded

#### TL2.4 — Create `internal/learn/sampler.go`
- `Sampler` struct holding a `*loki.Client`, a window duration, and a limit
- `(s *Sampler) QueryWindow(ctx context.Context, container string, start, end time.Time) ([]ingest.LogEvent, error)`
  - Issues `{container="<name>"} |~ "(?i)(error|exception|fatal|panic|fail|refused|denied|timeout|oom|killed|traceback|stack trace)"` to Loki
  - Decodes `loki.LogLine` results into `ingest.LogEvent` (sets `Level` from stream label if present, else `"info"`)
  - Returns at most `s.limit` results
- `(s *Sampler) QueryPreRestart(ctx context.Context, container string, restartAt time.Time, n int) ([]ingest.LogEvent, error)`
  - Issues a time-bounded query for the N seconds before `restartAt` with no keyword filter (all lines immediately preceding restart are candidates)
- Unit tests using a mock Loki HTTP server (httptest)

---

### Tier 3 — Rule generation and application (depends on TL1.5, TL2.3)

#### TL3.1 — Create `internal/learn/generator.go`
- `Generate(candidates []ScoredCandidate, existingOverlay []LearnedRule, nextPriority *int) (toApply []LearnedRule, toPending []LearnedRule, cfg *config.LearnConfig)`
  - Splits scored candidates by threshold: `≥ confidence_threshold` → `toApply`; `≥ review_threshold` → `toPending`; below → discarded
  - For each candidate in `toApply`:
    - Construct `config.RuleConfig{Name: "learned-<hash>", Priority: *nextPriority, Match: "log", When: map["message":"regex:<pattern>"], SetState: candidate.GeneratedState}`
    - `*nextPriority--` (within 500–999 band; error if band exhausted)
    - Wrap in `LearnedRule{Source: SourceLearned, DiscoveredAt: time.Now()}`
    - Skip if a rule with the same pattern already exists in `existingOverlay`
  - Name hash: first 8 characters of SHA-256 of the pattern string
- Unit tests: priority assignment; deduplication against existing overlay; threshold splitting; band exhaustion error

#### TL3.2 — Create `internal/learn/applier.go`
- `Applier` struct holding overlay/pending/suppression file paths, `*config.LearnConfig`, and a reload callback `func() error`
- `(a *Applier) Apply(ctx context.Context, toApply []LearnedRule, toPending []LearnedRule) error`
  - Load current overlay; merge `toApply` (dedup by name); save overlay
  - Load current pending; merge `toPending` (dedup by name); save pending
  - If `cfg.AutoApply && len(toApply) > 0`: call reload callback; log applied rule names
  - If `!cfg.AutoApply && len(toApply) > 0`: log notice with overlay file path and `ep reload` instruction
- `(a *Applier) ConfirmRule(name string) error` — set `Source = SourceConfirmed`, `ConfirmedAt = now()` in overlay; save
- `(a *Applier) RejectRule(name string, pattern string) error` — remove rule from overlay; add pattern to suppression list; save both
- Unit tests: apply writes overlay and calls reload callback; auto_apply=false skips callback; confirm/reject round-trips

---

### Tier 4 — Health engine event publication (depends on existing `internal/health`)

#### TL4.1 — Add `StateTransitionEvent` channel to health engine
- Extend `internal/health/Engine` (or equivalent type) with an optional `TransitionEvents chan<- learn.StateTransitionEvent` field
- When a container's functional state changes (compare new state to previous persisted state), send a `StateTransitionEvent` on the channel if it is non-nil; send is non-blocking (use `select` with `default` to avoid blocking the health engine if the consumer is slow)
- `StateTransitionEvent` carries: `Container`, `Namespace`, `PrevState`, `NewState`, `MatchedRule`, `At`
- Unit tests: transition events emitted on state change; no event emitted when state unchanged; non-blocking send verified

#### TL4.2 — Add container-restart event to discovery reconciler
- Extend the reconciler's container-change handling in `internal/discovery` to emit a `StateTransitionEvent` with `NewState = "RESTARTED"` when a watched container's restart count increases
- Reuse the same `TransitionEvents` channel established in TL4.1
- Unit tests: restart increment triggers event; stable restart count does not

---

### Tier 5 — Learner goroutine (depends on TL2.4, TL3.2, TL4.1, TL4.2)

#### TL5.1 — Create `internal/learn/learner.go`
- `Learner` struct: holds `*config.LearnConfig`, `*Sampler`, `*Applier`, `SuppressionList`, `events <-chan StateTransitionEvent`, `lokiClient *loki.Client`
- `NewLearner(cfg *config.LearnConfig, loki *loki.Client, overlayPath, suppressionPath string, events <-chan StateTransitionEvent, reload func() error) *Learner`
- `(l *Learner) Run(ctx context.Context)` — main loop:
  - If `!cfg.Enabled`: return immediately
  - `select` on `ctx.Done()` and `events`
  - On event, determine trigger type (forward vs. backward, restart vs. state change)
  - Select analysis window: forward = last N minutes up to event time; backward = `T_enter_error` to `T_recover`
  - Call `Sampler.QueryWindow` or `Sampler.QueryPreRestart` as appropriate
  - Call `gap.FindUncovered` to discard already-covered events
  - Call `Classify` against uncovered events
  - Call `Generator.Generate`
  - Call `Applier.Apply`
  - If `cfg.BackgroundScan`: also run a background ticker at `cfg.BackgroundScanInterval` that performs a broad sweep across all watched containers using the sampler
- Trigger qualification logic (not all transitions are analysed):

  | Transition | Analysis type |
  |---|---|
  | `OK → HAS_ERRORS` | Forward gap analysis |
  | `OK → FAILING` | Forward gap analysis |
  | `RESTARTED` | Pre-restart scan (last 30s of logs) |
  | `HAS_ERRORS → OK` | Backward retrospective |
  | `FAILING → OK` | Backward retrospective |
  | `* → *` (any other) | Skip |

- Unit tests: event filtering (non-qualifying transitions skipped); context cancellation exits loop; background ticker fires at interval; disabled learner exits immediately

---

### Tier 6 — Integration in `ep up` (depends on TL4.1, TL4.2, TL5.1)

#### TL6.1 — Launch learner goroutine from `cmd/up.go`
- After the stack is running and the health engine is initialised:
  - Create a `chan StateTransitionEvent` (buffered, capacity 32)
  - Wire the channel into the health engine (`Engine.TransitionEvents`) and discovery reconciler
  - Construct a `Learner` using paths from `config.LearnOverlayFile()` and `config.LearnSuppressionFile()`
  - Pass a reload callback that calls the existing in-process reload path
  - Launch `learner.Run(ctx)` as a goroutine under the existing context
- Learner goroutine is cancelled automatically when the reconciliation loop's context is cancelled (i.e., on `Ctrl+C` or `ep down`)

#### TL6.2 — Wire overlay into `pbr.Load` call
- In the config load path (both `ep up` and `ep reload`), call `overlay.LoadOverlay` and pass the overlay's `[]config.RuleConfig` into `pbr.Load` alongside user rules
- Overlay rules participate in the full PBR validation (priority uniqueness, field validation) — a malformed overlay file causes a clear error before the rules are applied
- If overlay file does not exist: silently proceed with no additional rules

---

### Tier 7 — `ep watch` TUI changes (depends on TL1.2, TL3.2)

#### TL7.1 — Render `⚑ ?` indicator for inferred states
- In the `ep watch` row renderer, check the `MatchedRule` field of the health snapshot against the loaded overlay
- When the matched rule is a `source: learned` rule: prepend `⚑` to the state label and append `?`
  - `⚑ HAS ERRORS ?` instead of `⚠ HAS ERRORS`
  - `⚑ DEGRADED ?` instead of the standard degraded label
- Confirmed rules (`source: confirmed`) render without the `⚑ ?` marker
- The EKG waveform gains a **cyan** tier: when no confirmed error or failing state is active but ≥ 1 container has a `source: learned` rule firing, the waveform renders cyan. Yellow and red take precedence over cyan; green is shown only when no inferred conditions are active either. See the EKG waveform colour tiers table in the Rule Source Tagging and UI section.

#### TL7.2 — Add `v` (validate) keyboard shortcut
- On keypress `v` with a container selected:
  - If the container's matched rule is `source: learned`: call `Applier.ConfirmRule(matchedRuleName)`; refresh the row to remove the `⚑ ?` marker
  - If the matched rule is already confirmed or is a built-in: display a brief status message `"nothing to confirm"` and return
- No confirmation prompt — action is immediately reversible by deleting the overlay file

#### TL7.3 — Add `f` (false positive) keyboard shortcut
- On keypress `f` with a container selected:
  - If the container's matched rule is `source: learned`: call `Applier.RejectRule(matchedRuleName, pattern)`; refresh the row (container health reverts to whatever the next-priority rule or default gives it)
  - Display status message: `"rule removed — pattern suppressed"`
  - If the matched rule is not a learned rule: display `"only learned rules can be marked as false positive"` and return

#### TL7.4 — Update `ep watch` help overlay
- Add `v  Validate inferred rule` and `f  Mark as false positive` to the keyboard shortcut help displayed by the existing help toggle key

---

### Tier 8 — User guide updates (depends on TL6, TL7)

#### TL8.1 — Document the Learning Module in the user guide
- New section `## Learning Module` covering:
  - What it does and why (one paragraph)
  - How to enable/disable
  - The `⚑ ?` indicator and what it means
  - The `v` and `f` keyboard shortcuts in `ep watch`
  - The three files it manages (`errorprobe.learned.yaml`, `errorprobe.learned.pending.yaml`, `errorprobe.learned.suppressed.yaml`) and how to inspect/reset them
  - The full `learn:` config block with all keys and their defaults
- Update the `ep watch` keyboard shortcut table with `v` and `f`
- Update the config file full-schema block to include `learn:` with defaults

---

## File Summary

### New files
| Path | Description |
|---|---|
| `internal/learn/types.go` | Core types |
| `internal/learn/keyword.go` | Keyword tiers, blocklist, scoring |
| `internal/learn/extractor.go` | Pattern generalisation |
| `internal/learn/classifier.go` | Multi-gate scoring pipeline |
| `internal/learn/sampler.go` | Loki query helpers |
| `internal/learn/gap.go` | PBR gap analysis |
| `internal/learn/generator.go` | Rule generation |
| `internal/learn/overlay.go` | Overlay and pending file I/O |
| `internal/learn/suppress.go` | Suppression list I/O |
| `internal/learn/learner.go` | Main goroutine |

### Modified files
| Path | Change |
|---|---|
| `internal/config/config.go` | Add `LearnConfig`, `Learn` field, path-resolution helpers |
| `internal/health/engine.go` | Publish `StateTransitionEvent` on state change |
| `internal/discovery/reconciler.go` | Publish restart events on container restart count increase |
| `cmd/up.go` | Launch learner goroutine; wire transition event channel |
| `cmd/up.go` (config load path) | Load and merge overlay into `pbr.Load` |
| `internal/tui/*.go` | `⚑ ?` rendering; `v` and `f` keyboard handlers; help overlay |
| `docs/user-guide.md` | Learning Module section; updated shortcut table; updated config schema |
