# Phase 2 — Container Discovery

**Goal:** ErrorProbe discovers all running user containers, applies watch policy, generates a Vector config scoped to the approved set, and reloads Vector when the set changes.  
**Prerequisite:** Phase 1 complete.

**UT coverage requirement: ≥ 90% on all new packages.**

---

## Atomic Tasks

Tasks are grouped by dependency tier. All tasks within a tier can be implemented independently and in parallel.

---

### Tier 1 — Data model (no Phase 2 dependencies)

#### T2.1 — Define `ContainerMeta` and `WatchSet` types
- In `internal/discovery` package, define:
  ```go
  type ContainerMeta struct {
      ID           string
      Name         string
      Image        string
      Labels       map[string]string
      StartedAt    time.Time
      RestartCount int
      InfraStatus  string  // "running" | "restarting" | "exited" | "paused"
      Runtime      string  // "docker" (K8s added in Phase 5)
  }

  type WatchSet struct {
      Containers []ContainerMeta
      GeneratedAt time.Time
  }
  ```
- `WatchSet.Diff(previous WatchSet) (added []ContainerMeta, removed []ContainerMeta)` — returns delta between two watch sets

---

### Tier 2 — Independent implementations (depends on T2.1 and T0.5; independent of each other)

#### T2.2 — Implement Docker container lister
- `ListRunning(ctx context.Context, dockerClient docker.DockerAPI) ([]ContainerMeta, error)` in `internal/discovery`
- Calls Docker `ContainerList` with `filters.Args{status: running}`
- Excludes ErrorProbe's own managed containers by label (`com.errorprobe.managed=true` — set on all stack containers at start)
- Maps Docker SDK `Container` struct to `ContainerMeta`
- Populates `RestartCount` and `InfraStatus` from container inspect

#### T2.3 — Implement watch policy filter
- `ApplyPolicy(containers []ContainerMeta, cfg *config.Config) []ContainerMeta` in `internal/discovery`
- Filters out containers whose name matches any glob pattern in `cfg.Containers.Exclude`
- Uses `path.Match` for glob evaluation
- Returns a new slice — does not mutate input
- Deterministic: sorts result by container name for stable config generation

#### T2.4 — Implement watch set persistence
- `SaveWatchSet(path string, ws WatchSet) error` in `internal/discovery`
- `LoadWatchSet(path string) (WatchSet, error)` in `internal/discovery`
- Serialises to JSON; writes atomically (write to `.tmp` then rename)
- `LoadWatchSet` returns empty `WatchSet{}` (not error) if file does not exist

---

### Tier 3 — Vector config generator full implementation (depends on T2.1, T0.5)

#### T2.5 — Implement Vector full config generator
- Replace Phase 1 stub in `internal/configgen.GenerateVector`
- Produces `vector.toml` with:
  - `[sources.docker_logs]` of type `docker_logs`; `include_containers` array set to approved container names
  - `[transforms.normalize]` VRL transform block:
    - Parse JSON if input is valid JSON; fall back to raw string for `message`
    - Set `timestamp` from log entry or current time
    - Set `container` from Vector metadata
    - Infer `level` by matching `detection.severity_patterns.error` patterns first, then `warn`, else `info`
    - Set `raw` to original unparsed line
    - Emit normalised event
  - `[sinks.loki]` — HTTP Loki push API (`http://loki:{port}/loki/api/v1/push`); labels: `container`, `level`
  - `[sinks.errorprobe_ingest]` — HTTP sink pointing to `http://{bind}:{ingest.port}/ingest`; encoding JSON; batch max 100 events or 1s timeout
- Severity pattern injection: VRL uses `if match(message, r'ERROR|FATAL...')` built from config patterns
- Template-driven: update `assets/templates/vector.toml.tmpl`

---

### Tier 4 — Reconciliation loop (depends on T2.2, T2.3, T2.4, T2.5)

#### T2.6 — Implement reconciliation loop
- `Reconciler` struct in `internal/discovery`:
  ```go
  type Reconciler struct {
      cfg        *config.Config
      docker     docker.DockerAPI
      configgen  configgen.VectorGenerator  // interface
      onReload   func()
      interval   time.Duration
  }
  ```
- `NewReconciler(cfg, docker, configgen, onReload) *Reconciler`
- `Reconciler.Run(ctx context.Context) error` — runs until context cancelled
- Each tick:
  1. `ListRunning` → `ApplyPolicy` → current `WatchSet`
  2. Load previous `WatchSet` from `~/.errorprobe/state/containers.json`
  3. `Diff` — if no change, skip
  4. If changed: call `GenerateVector` with new container list
  5. Send SIGHUP to Vector container via `docker.SendSignal`
  6. Save new `WatchSet` to `~/.errorprobe/state/containers.json`
  7. Call `onReload()` (allows callers to react to set changes, e.g. update health engine)
- Default interval: 5 seconds (configurable, not exposed in yaml — internal constant)
- Error in a single tick is logged and retried; does not crash the loop

---

### Tier 5 — `list` command (depends on T2.2, T2.3)

#### T2.7 — Implement `errorprobe list` command
- Replace stub in `cmd/list.go`
- Calls `discovery.ListRunning` + `discovery.ApplyPolicy`
- Loads `WatchSet` from `~/.errorprobe/state/containers.json` to determine watch status
- Tabular output columns: `CONTAINER`, `IMAGE`, `INFRA STATUS`, `WATCHING`
- Uses `text/tabwriter` for alignment
- `--json` flag: output as JSON array of `ContainerMeta`
- Requires stack to be running (Docker client must be connectable); exits 1 with clear message if not

---

### Tier 6 — Integration into `up` command (depends on T2.6, T1.9)

#### T2.8 — Start reconciliation loop from `up` command
- After `stack.Up` completes, start `Reconciler.Run` as a goroutine
- Handle goroutine lifecycle: stop on SIGINT/SIGTERM
- `up` command stays running (blocking) while reconciliation is active — `errorprobe up` becomes a long-running foreground process
- Add `--detach` flag consideration: for V1, `up` is foreground only; document this

---

### Tier 7 — Unit tests (depends on T2.1–T2.6; independent of each other)

#### T2.9 — Unit tests: `internal/discovery` data model
- `TestWatchSet_Diff_AddedContainers`: previous empty, current has 2; assert both in added
- `TestWatchSet_Diff_RemovedContainers`: previous has 2, current has 0; assert both in removed
- `TestWatchSet_Diff_NoChange`: identical sets; assert both slices empty
- `TestWatchSet_Diff_Mixed`: one added, one removed; assert each in correct slice

#### T2.10 — Unit tests: `internal/discovery` policy filter
- `TestApplyPolicy_NoExclusions`: all containers pass through
- `TestApplyPolicy_ExactMatch`: exclude `"payments-api"`; assert absent from result
- `TestApplyPolicy_GlobMatch`: exclude `"sidecar-*"`; assert `"sidecar-logger"` excluded, `"payments-api"` not
- `TestApplyPolicy_ExcludesEPContainers`: managed EP containers not in input (excluded at list stage)
- `TestApplyPolicy_ResultSorted`: input unsorted; assert output sorted by name
- `TestApplyPolicy_EmptyInput`: empty input; assert empty output, no panic

#### T2.11 — Unit tests: `internal/discovery` persistence
- `TestSaveLoadWatchSet_RoundTrip`: save, load; assert identical
- `TestLoadWatchSet_FileMissing_ReturnsEmpty`: no file; assert empty WatchSet, no error
- `TestSaveWatchSet_Atomic`: simulate crash mid-write (mock rename failure); assert original file unchanged

#### T2.12 — Unit tests: `internal/configgen` Vector full
- `TestGenerateVector_ContainerListInjected`: known container list; assert names appear in `include_containers`
- `TestGenerateVector_SeverityPatternsFromConfig`: custom patterns in config; assert patterns appear in VRL block
- `TestGenerateVector_LokiSinkURL`: custom Loki port; assert correct URL in `[sinks.loki]`
- `TestGenerateVector_IngestSinkURL`: custom ingest port; assert correct URL in `[sinks.errorprobe_ingest]`
- `TestGenerateVector_EmptyContainers_ValidToml`: empty list; assert output is valid TOML
- `TestGenerateVector_OutputIsValidToml`: full config; parse result as TOML; assert no error

#### T2.13 — Unit tests: `internal/discovery` reconciler
- `TestReconciler_NoChange_NoReload`: same WatchSet on two consecutive ticks; assert `onReload` not called
- `TestReconciler_ContainerAdded_TriggersReload`: new container appears; assert `onReload` called once
- `TestReconciler_ContainerRemoved_TriggersReload`: container disappears; assert `onReload` called once
- `TestReconciler_ErrorOnList_ContinuesLoop`: `ListRunning` returns error; assert loop continues on next tick
- `TestReconciler_StopsOnContextCancel`: cancel context; assert `Run` returns promptly

---

### Final Task

#### T2.14 — Mark phase complete in work-plan.md
- Open `docs/work-plan.md`
- Mark all Phase 2 tasks as `[x]`
- Add completion date next to phase heading

#### T2.15 — Update roadmap.html
- Open `docs/roadmap.html` in a browser and verify Phase 2 is reflected correctly
- In the `PHASES` array, set Phase 2's `status` to `"completed"` and `actualEnd` to the actual finish date
- Compare actual duration against the planned estimate; if velocity differed, adjust `start` / `end` for all subsequent phases accordingly
- Update the `TODAY` constant to the current date
- Recompute and document the revised total story-point burn rate and projected completion date for the remaining phases in a comment above the `PHASES` array

---

## Deliverables

| Deliverable | Description |
|---|---|
| `ContainerMeta`, `WatchSet` | Typed discovery data model with diff capability |
| `internal/discovery` | Container lister, policy filter, persistence, reconciler |
| `internal/configgen.GenerateVector` | Full Vector config with VRL transform, Loki sink, ingest sink |
| `assets/templates/vector.toml.tmpl` | Full Vector template |
| `cmd/list.go` | Wired — tabular and JSON output of all containers with watch status |
| Reconciliation loop | Running from `up`, reloads Vector on container set changes |
| `~/.errorprobe/state/containers.json` | Persisted watch set, updated on every reconciliation |
| Unit tests | ≥ 90% coverage on `discovery` and updated `configgen` |

---

## Manual Tests

Run after all tasks are complete, with Docker Desktop running and at least 2 user containers active:

1. **`errorprobe up`** — runs and stays running; reconciliation loop starts.
2. **`errorprobe list`** — shows all running user containers (not ErrorProbe's own stack); `WATCHING` column is `yes` for all (unless excluded).
3. **Exclusion** — add a container name to `containers.exclude` in `errorprobe.yaml`; run `errorprobe list`; confirm excluded container shows `WATCHING: no`.
4. **`errorprobe list --json`** — outputs valid JSON array; each entry has `name`, `image`, `infraStatus`, `watching` fields.
5. **New container mid-run** — with `errorprobe up` running, start a new Docker container; within 5 seconds, run `errorprobe list`; confirm new container appears.
6. **Vector log flow** — open Grafana Explore (`http://localhost:3000`); query `{container="<your-container-name>"}`; confirm log lines appear within 2 seconds of being emitted by the container.
7. **Label correctness** — in Grafana Explore, confirm `level` label is present and correctly classified (`error`, `warn`, `info`) for log lines of each type.
8. **`~/.errorprobe/state/containers.json`** — inspect file; confirm it lists watched containers with correct metadata.
9. **`go test ./... -cover`** — all tests pass; coverage ≥ 90% on `internal/discovery` and `internal/configgen`.
