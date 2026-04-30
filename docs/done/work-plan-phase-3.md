# Phase 3 — Semantic Health Engine (Tier 1)

**Goal:** Real-time error detection. `errorprobe status` shows per-container functional health. `errorprobe watch` provides a live TUI.  
**Prerequisite:** Phase 2 complete.

**UT coverage requirement: ≥ 90% on all new packages.**

---

## Atomic Tasks

Tasks are grouped by dependency tier. All tasks within a tier can be implemented independently and in parallel.

---

### Tier 1 — Data model (no Phase 3 dependencies)

#### T3.1 — Define health state types
- In `internal/health` package:
  ```go
  type FunctionalState string
  const (
      StateOK        FunctionalState = "OK"
      StateHasErrors FunctionalState = "HAS_ERRORS"
      StateFailing   FunctionalState = "FAILING"  // reserved for Phase 6
  )

  type ContainerHealth struct {
      Name            string
      State           FunctionalState
      ErrorCount      int
      FirstErrorAt    *time.Time
      LastErrorAt     *time.Time
      LastErrorMsg    string
      LastUpdated     time.Time
  }

  type HealthSnapshot struct {
      Containers  map[string]ContainerHealth  // keyed by container name
      SnapshotAt  time.Time
  }
  ```
- `HealthSnapshot.SetError(name, msg string, at time.Time)` — upsert container entry; flip state to `HAS_ERRORS`; increment count
- `HealthSnapshot.Reset(name string)` — set container state back to `OK`; clear counts and timestamps

#### T3.2 — Define `LogEvent` type
- In `internal/ingest` package:
  ```go
  type LogEvent struct {
      Timestamp  time.Time `json:"timestamp"`
      Container  string    `json:"container"`
      Level      string    `json:"level"`
      Message    string    `json:"message"`
      Raw        string    `json:"raw"`
  }
  ```
- This is the normalised schema coming from Vector's VRL transform
- `ParseBatch(data []byte) ([]LogEvent, error)` — parses a JSON array of `LogEvent`

---

### Tier 2 — Health persistence (depends on T3.1 only)

#### T3.3 — Implement health snapshot persistence
- `SaveSnapshot(path string, snap HealthSnapshot) error` in `internal/health`
- `LoadSnapshot(path string) (HealthSnapshot, error)` in `internal/health`
- Atomic write (temp file + rename)
- `LoadSnapshot` returns empty `HealthSnapshot{}` (not error) if file does not exist
- Default path: `~/.errorprobe/state/health.json`

---

### Tier 3 — Ingest transport interface and HTTP implementation (depends on T3.2 only)

#### T3.4 — Define ingest transport interface
- In `internal/ingest` package:
  ```go
  type Transport interface {
      Start(ctx context.Context) error
      Stop() error
      OnBatch(handler func([]LogEvent))
  }
  ```
- `OnBatch` registers the handler called for each received batch; called synchronously in Phase 3 (no queue yet)

#### T3.5 — Implement HTTP JSON ingest transport
- `HTTPTransport` struct implementing `Transport`
- Binds on `cfg.Stack.Ingest.Bind + ":" + cfg.Stack.Ingest.Port` (default `127.0.0.1:9099`)
- Single route: `POST /ingest`
- Request body: JSON array of `LogEvent`
- On valid body: call registered `OnBatch` handler
- On invalid body: return HTTP 400 with error message; log to ErrorProbe logger; do not crash
- Request size limit: 10 MB (prevent memory exhaustion)
- No auth in V1 (localhost only)
- Graceful shutdown via `Stop()`: drain in-flight requests with 5s timeout

---

### Tier 4 — Health engine (depends on T3.1, T3.2, T3.3)

#### T3.6 — Implement health engine
- `Engine` struct in `internal/health`:
  ```go
  type Engine struct {
      snapshot    HealthSnapshot
      mu          sync.RWMutex
      snapshotPath string
      onChange    func(HealthSnapshot)
  }
  ```
- `NewEngine(snapshotPath string, onChange func(HealthSnapshot)) *Engine`
- `Engine.ProcessBatch(events []ingest.LogEvent)`:
  - For each event where `level == "error"` or `level == "warn"`: call `snapshot.SetError`
  - If snapshot changed: persist via `SaveSnapshot`, call `onChange`
- `Engine.Snapshot() HealthSnapshot` — thread-safe read
- `Engine.Reset(containerName string) error` — clear state for named container; persist; call `onChange`
- On startup: load existing snapshot from disk if present (state survives ErrorProbe restarts)

---

### Tier 5 — Wire ingest to health engine (depends on T3.4, T3.5, T3.6)

#### T3.7 — Wire ingest transport to health engine
- In `cmd/up.go` (or a new `cmd/runner.go`):
  - Instantiate `HTTPTransport` with config
  - Instantiate `Engine` with snapshot path and `onChange` callback
  - Register `engine.ProcessBatch` as `transport.OnBatch` handler
  - Start transport server: `transport.Start(ctx)`
  - Pass `onChange` callback that signals TUI to refresh (channel or broadcast)
- Update Vector config generator to add `[sinks.errorprobe_ingest]` pointing to configured address (already done in Phase 2 T2.5 — verify it matches)

---

### Tier 6 — `status` command (depends on T3.6)

#### T3.8 — Implement `errorprobe status` command
- Replace stub in `cmd/status.go`
- Reads `HealthSnapshot` from `~/.errorprobe/state/health.json` (does not require running engine — reads from disk)
- Merges with `WatchSet` from `~/.errorprobe/state/containers.json` to include infra state column
- Tabular output columns: `CONTAINER`, `FUNCTIONAL`, `INFRA`, `ERRORS`, `LAST ERROR`
- `✓ OK` (green), `⚠ HAS ERRORS [N]` (yellow), infra states shown as text
- `--json` flag: output full `HealthSnapshot` as JSON
- `--reset <container>` flag: call `Engine.Reset` for named container; requires engine to be running; exits 1 with clear message if engine not reachable
  - For `--reset`: engine must be reachable. Since V1 is foreground, use the persisted snapshot file directly: reload, call `Reset`, save. No IPC needed.

---

### Tier 7 — `watch` TUI (depends on T3.6, T3.8)

#### T3.9 — Implement Bubbletea TUI model
- `internal/tui` package
- `Model` struct implementing `tea.Model` (`Init`, `Update`, `View`)
- State: current `HealthSnapshot`, current `WatchSet`; updated via `tea.Cmd` channel messages
- `View` renders table:
  ```
  ┌─────────────────────────────────────────────────────────────┐
  │ ErrorProbe  watching N containers           [q] quit        │
  ├──────────────────┬────────────────┬──────────┬──────────────┤
  │ CONTAINER        │ FUNCTIONAL     │ INFRA    │ LAST ERROR   │
  ├──────────────────┼────────────────┼──────────┼──────────────┤
  │ payments-api     │ ⚠ HAS ERRORS 7 │ running  │ 14:32 NullPt…│
  │ auth-service     │ ✓ OK           │ running  │ —            │
  └──────────────────┴────────────────┴──────────┴──────────────┘
  ```
- Keyboard: `[q]` or `Ctrl+C` → quit; `[e]` → expand selected row to show last error message in full; `[r]` → reset selected container state
- Refresh: TUI subscribes to `onChange` channel; re-renders only on state changes (not on a timer)
- Terminal resize handled via `tea.WindowSizeMsg`

#### T3.10 — Wire `watch` command
- Replace stub in `cmd/watch.go`
- Requires stack to be running (check health snapshot exists and is fresh); exits 1 with clear message if not
- Instantiate TUI model with current snapshot and watch set
- Run `tea.NewProgram(model).Run()`
- On quit: clean terminal state; return to shell prompt

---

### Tier 8 — Unit tests (depends on T3.1–T3.6; independent of each other)

#### T3.11 — Unit tests: `internal/health` data model
- `TestSetError_FlipsState`: container at OK; call SetError; assert state is HAS_ERRORS
- `TestSetError_IncrementsCount`: call SetError twice; assert ErrorCount is 2
- `TestSetError_TracksFirstAndLast`: two SetError calls at different times; assert FirstErrorAt is first, LastErrorAt is last
- `TestSetError_PreservesFirstError`: already HAS_ERRORS; new SetError; assert FirstErrorAt unchanged
- `TestReset_ClearsState`: container at HAS_ERRORS; call Reset; assert state is OK, count 0, timestamps nil

#### T3.12 — Unit tests: `internal/health` persistence
- `TestSaveLoadSnapshot_RoundTrip`: save, load; assert identical
- `TestLoadSnapshot_Missing_ReturnsEmpty`: no file; assert empty snapshot, no error
- `TestSaveSnapshot_Atomic`: mock rename failure; assert original file unchanged
- `TestEngine_LoadsExistingSnapshot`: snapshot file exists on disk; `NewEngine`; assert snapshot loaded

#### T3.13 — Unit tests: `internal/ingest` HTTP transport
- `TestHTTPTransport_ValidBatch_CallsHandler`: POST valid JSON batch; assert handler called with correct events
- `TestHTTPTransport_InvalidJSON_Returns400`: POST malformed JSON; assert 400 response; handler not called
- `TestHTTPTransport_EmptyBatch_NoOp`: POST `[]`; assert handler called with empty slice; no error
- `TestHTTPTransport_OversizeRequest_Rejected`: POST body > 10 MB; assert 413 response
- `TestHTTPTransport_GracefulShutdown`: start server, call Stop; assert in-flight requests complete

#### T3.14 — Unit tests: `internal/ingest` ParseBatch
- `TestParseBatch_ValidArray`: valid JSON array; assert all events parsed correctly
- `TestParseBatch_SingleEvent`: JSON array with one event; assert correct
- `TestParseBatch_MalformedJSON`: invalid JSON; assert error returned
- `TestParseBatch_MissingFields`: event missing `level`; assert defaults applied (empty string, not panic)

#### T3.15 — Unit tests: `internal/health` engine
- `TestEngine_ProcessBatch_ErrorEvent_FlipsState`: batch with error event; assert container flips to HAS_ERRORS
- `TestEngine_ProcessBatch_InfoEvent_NoStateChange`: batch with info events only; assert state unchanged
- `TestEngine_ProcessBatch_WarnEvent_FlipsState`: batch with warn event; assert HAS_ERRORS
- `TestEngine_ProcessBatch_MultipleContainers`: batch with events from 2 containers; assert each tracked independently
- `TestEngine_ProcessBatch_PersistsOnChange`: state change; assert snapshot file written
- `TestEngine_ProcessBatch_CallsOnChange`: state change; assert onChange callback called
- `TestEngine_Reset_ClearsAndPersists`: reset container; assert state OK; file updated

---

### Final Task

#### T3.16 — Mark phase complete in work-plan.md
- Open `docs/work-plan.md`
- Mark all Phase 3 tasks as `[x]`
- Add completion date next to phase heading

#### T3.17 — Update roadmap.html
- Open `docs/roadmap.html` in a browser and verify Phase 3 is reflected correctly
- In the `PHASES` array, set Phase 3's `status` to `"completed"` and `actualEnd` to the actual finish date
- Compare actual duration against the planned estimate; if velocity differed, adjust `start` / `end` for all subsequent phases accordingly
- Update the `TODAY` constant to the current date
- Recompute and document the revised total story-point burn rate and projected completion date for the remaining phases in a comment above the `PHASES` array

---

## Deliverables

| Deliverable | Description |
|---|---|
| `HealthSnapshot`, `ContainerHealth`, `FunctionalState` | Health data model |
| `LogEvent`, `ParseBatch` | Ingest data model |
| `internal/ingest.HTTPTransport` | HTTP JSON ingest server |
| `internal/ingest.Transport` | Transport interface (gRPC slot reserved) |
| `internal/health.Engine` | State machine: processes batches, persists, notifies |
| `internal/health` persistence | `SaveSnapshot`, `LoadSnapshot` |
| `cmd/status.go` | Wired — tabular and JSON health output with infra state |
| `cmd/watch.go` | Wired — Bubbletea live TUI |
| `~/.errorprobe/state/health.json` | Persisted health snapshot |
| Unit tests | ≥ 90% coverage on `health`, `ingest` packages |

---

## Manual Tests

Run after all tasks are complete, with `errorprobe up` running and at least 2 user containers active:

1. **`errorprobe status`** — shows all watched containers with `✓ OK` initial state and correct infra state column.
2. **Error injection** — exec into a container and emit `echo 'ERROR something failed' to stdout`; within 2 seconds, run `errorprobe status`; confirm container shows `⚠ HAS ERRORS 1` with the message.
3. **`errorprobe status --json`** — valid JSON output; contains `containers` map with correct state for each.
4. **`errorprobe watch`** — TUI renders correctly; all containers visible with correct states.
5. **Live update in watch** — with TUI running, inject an error into a container; confirm TUI updates within 2 seconds without manual refresh.
6. **`[e]` key in TUI** — select a container with errors; press `e`; full error message displayed.
7. **`[q]` key in TUI** — press `q`; terminal returns to clean shell prompt with no visual artifacts.
8. **`errorprobe status --reset <container>`** — state clears to `✓ OK`; subsequent `errorprobe status` confirms reset; `health.json` updated.
9. **ErrorProbe restart** — stop and restart ErrorProbe (`Ctrl+C` then `errorprobe up`); run `errorprobe status`; confirm previous error state is restored from disk.
10. **`go test ./... -cover`** — all tests pass; coverage ≥ 90% on `internal/health` and `internal/ingest`.
