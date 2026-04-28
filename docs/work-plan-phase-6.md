# Phase 6 — Tier 2 Detection (V2 Start)

**Goal:** Distinguish confirmed failures from noise. Introduce the `FAILING` state.  
**Prerequisite:** Phase 5 complete.

**UT coverage requirement: ≥ 90% on all new packages and functions.**

---

## Atomic Tasks

Tasks are grouped by dependency tier. All tasks within a tier can be implemented independently and in parallel.

---

### Tier 1 — Independent implementations (no Phase 6 dependencies beyond Phase 5)

#### T6.1 — Implement Loki query client
- `internal/loki` package (new)
- `Client` struct with base URL from config
- `Client.QueryRange(ctx, logql string, start, end time.Time, limit int) ([]LogLine, error)` — calls `GET /loki/api/v1/query_range`
- `Client.CountErrors(ctx, containerName string, since time.Duration) (int, error)` — queries `count_over_time({container="<name>",level="error"}[<window>])`
- `LogLine` struct: `Timestamp time.Time`, `Container string`, `Level string`, `Message string`
- HTTP client with configurable timeout (default 10s); respects context cancellation

#### T6.2 — Implement error fingerprinter
- `Fingerprint(message string) string` in `internal/health` package
- Strips volatile parts of a log line to produce a stable fingerprint:
  - Remove timestamps (ISO8601, Unix epoch, common date formats)
  - Remove memory addresses (`0x[0-9a-f]+`)
  - Remove line numbers (`line \d+`, `:\d+`)
  - Remove UUIDs
  - Remove numeric IDs that look like auto-incremented values
  - Normalise whitespace
- Returns SHA-256 (first 16 chars) of the normalised string
- Pure function — no I/O; deterministic; fully unit-testable

#### T6.3 — Implement history log writer
- `internal/health.HistoryLog` struct
- `HistoryLog.Append(entry StateTransition) error`
- `StateTransition` struct: `ContainerName`, `From FunctionalState`, `To FunctionalState`, `At time.Time`, `Reason string`
- Appends to `~/.errorprobe/state/history.jsonl` (newline-delimited JSON)
- Atomic append: write to temp file, append, rename (or use OS-level append semantics)
- `HistoryLog.Prune(retention time.Duration) error` — removes entries older than `retention` from config; rewrites file

---

### Tier 2 — Tier 2 trigger and state machine extension (depends on T6.1, T6.2, T6.3)

#### T6.4 — Extend health state machine for `FAILING`
- Add `StateFailing FunctionalState = "FAILING"` (already defined as reserved in Phase 3 T3.1)
- Add to `ContainerHealth`:
  ```go
  Fingerprints map[string]int  // fingerprint → occurrence count within window
  ```
- `HealthSnapshot.RecordFingerprint(name, fingerprint string)` — increment count for fingerprint on named container

#### T6.5 — Implement Tier 2 evaluator
- `Tier2Evaluator` struct in `internal/health`:
  ```go
  type Tier2Evaluator struct {
      loki      loki.Client
      cfg       *config.Config
      engine    *Engine
      history   *HistoryLog
  }
  ```
- `Tier2Evaluator.Run(ctx context.Context)` — goroutine; evaluates on a configurable tick (default: every 30s)
- On each tick, for every `HAS_ERRORS` container:
  1. Query Loki: count errors in `cfg.Detection.Tier2.Window`
  2. If count ≥ `cfg.Detection.Tier2.Threshold`:
     - Fingerprint recent error messages via `Fingerprint`
     - Find dominant fingerprint (most frequent)
     - If dominant fingerprint count ≥ threshold: transition container to `FAILING`
     - Call `engine.SetFailing(name, fingerprint, count)` — new method on Engine
     - Append to history log via `HistoryLog.Append`
  3. If container is `FAILING` and error rate drops below threshold for a full window: transition back to `HAS_ERRORS`
     - Append to history log

#### T6.6 — Extend `Engine` for FAILING state
- `Engine.SetFailing(name, fingerprint string, count int) error` — transitions container to FAILING; persists; calls onChange
- `Engine.SetRecovered(name string) error` — transitions FAILING → HAS_ERRORS (not back to OK — errors happened); persists; calls onChange
- Update `Engine.ProcessBatch` to call `RecordFingerprint` for error events

---

### Tier 3 — `check.fail_on: FAILING` now functional (depends on T6.5)

#### T6.7 — Activate `check.fail_on: FAILING`
- `cmd/check.go`: `fail_on: FAILING` now evaluates correctly — only containers in `FAILING` state trigger exit 1
- Remove the V1 documentation note that said FAILING is not reachable; update with accurate behaviour
- `fail_on: HAS_ERRORS` continues to cover both `HAS_ERRORS` and `FAILING` states (superset)

---

### Tier 4 — TUI and status extensions (depends on T6.5)

#### T6.8 — Extend `status` and `watch` for FAILING state
- `errorprobe status`: third state `✗ FAILING` rendered distinctly
- Show fingerprint summary: `"same pattern 47×"` under FAILING containers
- `errorprobe watch` TUI: `FAILING` row uses distinct styling (e.g., red vs yellow for HAS_ERRORS)
- New TUI column or expandable detail: dominant fingerprint excerpt

#### T6.9 — Implement history log retention enforcement
- On `errorprobe up` startup: call `HistoryLog.Prune(cfg.HistoryRetention)`
- Ensures `~/.errorprobe/state/history.jsonl` does not grow unbounded
- Default retention: 30 days (from `errorprobe.yaml` `history_retention: 30d`)

---

### Tier 5 — Unit tests (depends on T6.1–T6.6; independent of each other)

#### T6.10 — Unit tests: Loki query client
- Use `httptest.NewServer` to mock Loki HTTP API
- `TestQueryRange_ReturnsLogLines`: mock returns valid response; assert lines parsed correctly
- `TestQueryRange_EmptyResult_NoError`: mock returns empty streams; assert empty slice, no error
- `TestQueryRange_Timeout_ReturnsError`: mock delays beyond timeout; assert context error
- `TestCountErrors_ReturnsCount`: mock returns count; assert correct integer returned
- `TestCountErrors_ZeroCount`: no errors in window; assert 0, no error

#### T6.11 — Unit tests: error fingerprinter
- `TestFingerprint_TimestampStripped`: two lines identical except timestamp; assert same fingerprint
- `TestFingerprint_MemoryAddressStripped`: two lines identical except `0x7f3a...`; assert same fingerprint
- `TestFingerprint_LineNumberStripped`: `at line 42` vs `at line 99`; assert same fingerprint
- `TestFingerprint_UUIDStripped`: two lines with different UUIDs; assert same fingerprint
- `TestFingerprint_DifferentMessages_DifferentFingerprints`: two distinct errors; assert different fingerprints
- `TestFingerprint_Deterministic`: same input called twice; assert same output

#### T6.12 — Unit tests: history log
- `TestHistoryLog_Append_WritesEntry`: append one entry; read file; assert entry present as valid JSON line
- `TestHistoryLog_Append_MultipleEntries`: append 3 entries; assert 3 lines in file
- `TestHistoryLog_Prune_RemovesOldEntries`: entries older than retention; assert removed after prune
- `TestHistoryLog_Prune_RetainsRecentEntries`: recent entries; assert retained
- `TestHistoryLog_Prune_EmptyFile_NoError`: prune empty file; assert no error

#### T6.13 — Unit tests: Tier 2 evaluator
- `TestTier2Evaluator_BelowThreshold_NoTransition`: error count below threshold; assert state remains HAS_ERRORS
- `TestTier2Evaluator_ThresholdMet_TransitionsToFailing`: error count ≥ threshold with same fingerprint; assert FAILING
- `TestTier2Evaluator_RateDrops_RecoverToHasErrors`: FAILING container; error rate drops; assert back to HAS_ERRORS
- `TestTier2Evaluator_AppendHistoryOnTransition`: transition occurs; assert history log entry written
- `TestTier2Evaluator_MultipleContainers_IndependentEvaluation`: two containers at different rates; assert each evaluated independently

#### T6.14 — Unit tests: `check` with FAILING
- `TestCheck_FAILING_FailOnFailing_ExitsOne`: container at FAILING; fail_on FAILING; assert exit 1
- `TestCheck_HAS_ERRORS_FailOnFailing_ExitsZero`: container at HAS_ERRORS; fail_on FAILING; assert exit 0
- `TestCheck_FAILING_FailOnHasErrors_ExitsOne`: container at FAILING; fail_on HAS_ERRORS; assert exit 1 (FAILING is superset)

---

### Final Task

#### T6.15 — Mark phase complete in work-plan.md
- Open `docs/work-plan.md`
- Mark all Phase 6 tasks as `[x]`
- Add completion date next to phase heading

#### T6.16 — Update roadmap.html
- Open `docs/roadmap.html` in a browser and verify Phase 6 is reflected correctly
- In the `PHASES` array, set Phase 6's `status` to `"completed"` and `actualEnd` to the actual finish date
- Compare actual duration against the planned estimate; if velocity differed, adjust `start` / `end` for all subsequent phases accordingly
- Update the `TODAY` constant to the current date
- Recompute and document the revised total story-point burn rate and projected completion date for the remaining phases in a comment above the `PHASES` array

---

## Deliverables

| Deliverable | Description |
|---|---|
| `internal/loki` | Loki HTTP query client |
| `Fingerprint` | Error message fingerprinting in `internal/health` |
| `HistoryLog` | State transition history with retention |
| `Tier2Evaluator` | Periodic Loki query → FAILING state transition |
| `Engine.SetFailing`, `Engine.SetRecovered` | FAILING state management |
| `cmd/check.go` | `fail_on: FAILING` now fully functional |
| `errorprobe status` / `watch` | FAILING state rendered distinctly with fingerprint summary |
| `~/.errorprobe/state/history.jsonl` | State transition history file with retention |
| Unit tests | ≥ 90% coverage on `loki`, updated `health` |

---

## Manual Tests

Run after all tasks are complete:

1. **Tier 2 trigger** — run a container that emits the same ERROR message > 10 times in 3 minutes (default threshold); `errorprobe watch` should transition from `⚠ HAS ERRORS` to `✗ FAILING`.
2. **Fingerprint summary** — FAILING container shows `"same pattern N×"` in status and watch.
3. **Tier 1 only — noisy** — run a container that emits 1 error then stops; confirm it stays at `HAS ERRORS` and does not transition to FAILING.
4. **Recovery** — stop the error-emitting container; wait one full window (3 minutes); confirm container transitions from FAILING back to HAS_ERRORS.
5. **`errorprobe check --fail-on FAILING`** — FAILING container → exit 1; HAS_ERRORS-only container → exit 0.
6. **`errorprobe check --fail-on HAS_ERRORS`** — both FAILING and HAS_ERRORS → exit 1 (FAILING is superset).
7. **History log** — inspect `~/.errorprobe/state/history.jsonl`; confirm each state transition (OK→HAS_ERRORS, HAS_ERRORS→FAILING, FAILING→HAS_ERRORS) is recorded as a JSON line with correct timestamps.
8. **Retention** — set `history_retention: 1m` in yaml; restart errorprobe; confirm entries older than 1 minute are pruned from history file.
9. **`go test ./... -cover`** — all tests pass; coverage ≥ 90% on `internal/loki` and updated `internal/health`.
