# Work Plan — Scalability & Complexity Improvements

**Source:** Code-quality review, May 2026.  
**Scope:** Top 10 complexity and scalability issues identified across the codebase, ordered by severity.  
**UT coverage requirement: ≥ 85% on every touched package.**

---

## Issues Inventory

| # | Severity | Location | Summary |
|---|---|---|---|
| S1 | CRITICAL | `internal/health/engine.go` | Write lock held across disk I/O in `ProcessBatch` |
| S2 | HIGH | `internal/health/history.go` | `HistoryLog.Append` opens+writes+closes file per transition |
| S3 | HIGH | `internal/health/history.go` | `HistoryLog.Prune` loads entire file into memory |
| S4 | HIGH | `internal/health/tier2.go` | `Tier2Evaluator.evaluate` issues sequential O(n) Loki queries |
| S5 | HIGH | `internal/discovery/reconciler.go` | `fetchPrevExitMsg` fan-out: one K8s API call per restarting container per tick |
| S6 | MEDIUM | `internal/learn/extractor.go` | `ExtractPattern` applies every regex twice |
| S7 | MEDIUM | `cmd/up.go`, `internal/discovery/reconciler.go` | State paths built with string concatenation instead of `filepath.Join` |
| S8 | MEDIUM | `internal/discovery/policy.go` | Display-name regexes recompiled on every `ApplyPolicy` call |
| S9 | LOW | `internal/ingest/http.go` | `ProcessBatch` called inline in the HTTP handler, blocking ingest |
| S10 | LOW | `internal/discovery/types.go` | `WatchSet.Diff` is ID-only; container recreation triggers spurious regenerations |

---

## Atomic Tasks

Tasks are grouped by dependency tier. All tasks within a tier are independent and may be parallelised.

---

### Tier 1 — Independent (no cross-task dependencies)

#### S1 — Decouple snapshot persistence from the engine write lock

**File:** `internal/health/engine.go`

**Problem:** `ProcessBatch` holds `e.mu.Lock()` for the full batch loop *and* the `SaveSnapshot` call. Disk I/O under the write lock serialises all ingest throughput.

**Fix:**
- Accumulate state mutations under the lock as today.
- Copy the updated snapshot (value copy) before releasing the lock.
- Call `SaveSnapshot` **after** unlocking.
- Call `onChange` after unlocking (it currently runs under the lock too).

```go
// sketch
e.mu.Lock()
// ... apply mutations ...
snap := e.snapshot          // cheap value copy
e.mu.Unlock()               // release before I/O

if changed {
    _ = SaveSnapshot(e.snapshotPath, snap)
    if e.onChange != nil {
        e.onChange(snap)
    }
}
```

**Tests:** verify that concurrent calls to `ProcessBatch` and `Snapshot()` do not race (`go test -race`); verify persistence still happens after batch.

---

#### S2 — Buffer `HistoryLog.Append` writes

**File:** `internal/health/history.go`

**Problem:** Every state transition calls `os.OpenFile` + `Write` + `Close`. Under restart bursts (many containers flapping) this generates excessive syscall overhead.

**Fix:**
- Add a `bufio.Writer` or channel-based async appender inside `HistoryLog`.
- Buffer entries and flush on a configurable interval (default 1 s) or when the buffer exceeds N entries (default 32).
- Expose `Flush() error` for graceful shutdown.
- Keep `Append` synchronous for callers that need durability guarantees; add `AppendAsync` for the engine path.

**Tests:** benchmark `Append` before/after; verify no entries are lost on `Close`.

---

#### S3 — Stream-based `HistoryLog.Prune`

**File:** `internal/health/history.go`

**Problem:** `Prune` reads the entire `.jsonl` file into memory with `bytes.Split`, then rewrites it. For large histories this is O(file_size) memory.

**Fix:**
- Open the source file for reading and a temp file for writing.
- Stream line-by-line using `bufio.Scanner`, writing retained lines to the temp file.
- `atomicReplace` at the end.
- Remove the `bytes.Split` approach entirely.

```go
scanner := bufio.NewScanner(src)
for scanner.Scan() {
    line := scanner.Bytes()
    // unmarshal, check cutoff, write to tmp
}
```

**Tests:** test with synthetic files containing thousands of entries; assert final line count; assert memory allocations do not scale with file size (use `testing.AllocsPerRun`).

---

#### S6 — Single-pass `ExtractPattern`

**File:** `internal/learn/extractor.go`

**Problem:** The function applies all 7 regexes twice — once to produce the stripped string, once to measure removed characters. This is O(n × 14) per message.

**Fix:**
- Accumulate a `removedChars` counter during the single replacement pass by comparing `len([]rune(before))` vs `len([]rune(after))` at each step, or by using `regexp.ReplaceAllStringFunc` to count match widths inline.
- Delete the second pass entirely.

**Tests:** golden-file tests asserting `(pattern, matchFraction)` are unchanged after refactor; fuzz test on the extractor.

---

#### S7 — Replace path string concatenation with `filepath.Join`

**Files:** `cmd/up.go`, `internal/discovery/reconciler.go`, any other callers of `cfg.StateDir()`

**Problem:** `cfg.StateDir() + "containers.json"` is fragile on Windows if `StateDir()` does not end with a separator.

**Fix:** Replace every occurrence with `filepath.Join(cfg.StateDir(), "containers.json")` etc.

**Grep pattern:** `cfg.StateDir() +`

**Tests:** existing tests sufficient; add a unit test in `internal/config` asserting `StateDir()` output is usable as a `filepath.Join` base on both Unix and Windows path styles.

---

#### S8 — Cache compiled display-name patterns on `Config`

**File:** `internal/discovery/policy.go`, `internal/config/config.go`

**Problem:** `compileDisplayPatterns` is called inside `ApplyPolicy`, which runs on every reconciler tick (every 5 s). The same patterns are recompiled repeatedly.

**Fix:**
- Add a `compiledDisplayPatterns []*regexp.Regexp` field to `Config` (unexported).
- Compile once in `config.Load` (or lazily on first access via a `sync.Once`).
- `ApplyPolicy` reads the pre-compiled slice.

**Tests:** assert `compileDisplayPatterns` is called exactly once per `Config` instance even when `ApplyPolicy` is called N times.

---

#### S10 — Name-aware `WatchSet.Diff`

**File:** `internal/discovery/types.go`

**Problem:** `Diff` keys containers by ID only. When Docker recreates a container with the same name but a new ID it appears as a remove+add, triggering unnecessary Vector config regenerations.

**Fix:**
- Add a secondary name-based diff: if a removed container's name matches an added container's name, treat it as an update (or stable), not a new container.
- Expose a `DiffResult` struct with `Added`, `Removed`, `Recreated []ContainerMeta` to let callers decide whether regeneration is needed.

**Tests:** table-driven tests covering: stable set, true add, true remove, ID-change-same-name (recreate), name-change.

---

### Tier 2 — Depends on S1 (engine lock refactor)

#### S9 — Async ingest handler

**File:** `internal/ingest/http.go`

**Problem:** `ProcessBatch` is called synchronously inside the HTTP handler. If snapshot persistence is slow, HTTP clients time out and backpressure accumulates.

**Prerequisite:** S1 (persistence decoupled from lock) to ensure async dispatch doesn't race on snapshot state.

**Fix:**
- Add a bounded channel (`batchCh chan []LogEvent`, capacity 256) to `HTTPTransport`.
- The HTTP handler sends the batch to the channel and immediately returns `204`.
- A dedicated worker goroutine drains the channel and calls the registered handler.
- `Stop()` drains the channel before returning.

**Tests:** verify the HTTP handler returns `204` without waiting for processing; verify no batch is lost on graceful shutdown; verify back-pressure behaviour when the channel is full (drop or block — document the choice).

---

### Tier 3 — Depends on S2, S3 (history refactor)

#### S4 — Batched Loki queries in `Tier2Evaluator`

**File:** `internal/health/tier2.go`

**Problem:** `evaluate` loops over all tracked containers and issues one `CountErrors` HTTP request per container per tick. With 100+ containers the tick duration expands proportionally.

**Fix:**
- Add `CountErrorsBatch(ctx, containers []string, since time.Duration) (map[string]int, error)` to `LokiQueryClient`.
- Implement it in `*loki.Client` using a single LogQL stream selector with `or` conditions.
- `evaluate` calls `CountErrorsBatch` once, then iterates the result map.

**Interface change:**
```go
type LokiQueryClient interface {
    CountErrors(ctx context.Context, container string, since time.Duration) (int, error)
    CountErrorsBatch(ctx context.Context, containers []string, since time.Duration) (map[string]int, error)
    QueryErrorMessages(ctx context.Context, containerKey string, since time.Duration) ([]string, error)
}
```

**Tests:** mock the new method; verify a single HTTP call is made regardless of container count; test the LogQL generation for N containers.

---

#### S5 — Rate-limited `fetchPrevExitMsg` in Reconciler

**File:** `internal/discovery/reconciler.go`

**Problem:** For each restarting K8s container a separate `GetPreviousContainerLogs` API call is made synchronously inside `tick`. With many simultaneously-restarting containers (e.g., rolling restart of a large deployment) this fans out to unbounded parallel API calls.

**Fix:**
- Collect all containers needing `fetchPrevExitMsg` in a slice.
- Dispatch with a semaphore-limited worker pool (`golang.org/x/sync/semaphore`, limit 8).
- Cap per-call timeout at 3 s independently of the reconcile context.
- Cache results in `approved[i].PrevExitMsg` before proceeding.

**Tests:** mock K8s client counting concurrent calls; assert concurrency never exceeds the semaphore limit.

---

## Acceptance Criteria

- All `go test -race ./...` pass with zero data races.
- `go test -cover ./...` shows ≥ 85% on every touched package.
- Benchmarks for S1 (`BenchmarkProcessBatch`) and S4 (`BenchmarkTier2Evaluate`) show ≥ 2× throughput improvement at 100-container scale.
- `golangci-lint run` reports no new issues.
