# Phase 4 — Developer UX & CI Integration

**Goal:** Complete the V1 success criterion — hours of debugging → 5 seconds. Composable with test scripts.  
**Prerequisite:** Phase 3 complete.

**UT coverage requirement: ≥ 90% on all new packages and functions.**

---

## Atomic Tasks

Tasks are grouped by dependency tier. All tasks within a tier can be implemented independently and in parallel.

---

### Tier 1 — Independent utilities (no Phase 4 dependencies beyond Phase 3)

#### T4.1 — Implement Grafana deep-link builder
- `BuildExploreURL(grafanaBaseURL string, containerName string, from time.Time, to time.Time) string` in `internal/configgen` or a new `internal/links` package
- Produces a Grafana Explore URL pre-filtered to `{container="<name>"}` with the given time range
- Time range encoded as Unix milliseconds in Grafana URL format (`?left=[...]`)
- Default time range: last 15 minutes relative to now
- Pure function — no I/O; fully unit-testable

#### T4.2 — Implement stack running check
- `IsStackRunning(cfg *config.Config, dockerClient docker.DockerAPI) (bool, error)` in `internal/stack`
- Returns true if all three managed containers (Loki, Grafana, Vector) are in running state
- Used by `check` and `logs` commands to gate execution with a clear message

#### T4.3 — Implement reload change classifier
- `ClassifyChanges(previous *config.Config, current *config.Config) ChangeSet` in `internal/stack`
- `ChangeSet` struct:
  ```go
  type ChangeSet struct {
      SoftChanges []string  // human-readable descriptions
      HardChanges []string  // human-readable descriptions
      HasSoft     bool
      HasHard     bool
  }
  ```
- Soft changes: `detection.severity_patterns`, `detection.tier2` thresholds, `containers.exclude`, `check.*`
- Hard changes: any `stack.*.image`, any `stack.*.port`, `stack.ingest.bind`, `stack.ingest.transport`
- Mixed: both sets populated if both classes changed
- Pure function — no I/O; fully unit-testable

---

### Tier 2 — Commands (depends on T4.1, T4.2, T4.3 and Phase 3 outputs; independent of each other)

#### T4.4 — Implement `errorprobe check` command
- Replace stub in `cmd/check.go`
- Requires stack running: call `IsStackRunning`; exit 1 with `"errorprobe stack is not running — run 'errorprobe up' first"` if false
- Load `HealthSnapshot` from `~/.errorprobe/state/health.json`
- Load `WatchSet` from `~/.errorprobe/state/containers.json`
- Apply `cfg.Check.Exclude`: remove exempt containers from evaluation
- Apply `cfg.Check.FailOn`:
  - `HAS_ERRORS`: any container with state `HAS_ERRORS` or `FAILING` → exit 1
  - `FAILING`: any container with state `FAILING` only → exit 1 (note: `FAILING` state not reachable in V1; `HAS_ERRORS` containers pass under this setting)
- Exit 0: print `"All containers healthy"` to stdout
- Exit 1: print list of failing containers with their state and last error message
- `--json` flag: output result as JSON (`{ "ok": bool, "failing": [...] }`)
- Document clearly in command `Long` description that `FAILING` state requires V2 Tier 2 detection

#### T4.5 — Implement `errorprobe logs` command
- Replace stub in `cmd/logs.go`
- Requires stack running: call `IsStackRunning`; exit 1 with clear message if not
- Queries Loki HTTP API: `GET /loki/api/v1/tail?query={container="<name>"}&limit=100`
- Streams log lines to stdout in real-time (Loki tail API uses WebSocket / chunked transfer)
- `--errors-only` flag: appends ` |= "error"` or uses label filter `{container="<name>",level="error"}` to limit to error-level entries
- `--since` flag: time offset (e.g., `--since 30m`); defaults to last 15 minutes
- Ctrl+C exits cleanly

#### T4.6 — Implement `errorprobe reload` command
- Replace stub in `cmd/reload.go`
- Load previous config from `~/.errorprobe/state/` or compare with currently running stack state
- Load new config via `config.Load()`
- Call `ClassifyChanges(previous, current)`
- If only soft changes:
  1. Regenerate Vector config (`GenerateVector`)
  2. Send SIGHUP to Vector container
  3. Print summary: `"Soft changes applied (no restart required): [list]"`
- If hard changes (with or without soft):
  1. Apply soft changes first (SIGHUP if any)
  2. For each affected container: stop → remove → regenerate config → start
  3. Re-run health poll for recreated containers
  4. Print summary: `"Hard changes applied (container recreated): [list]"`
- If no changes: print `"No configuration changes detected"` and exit 0
- Save new config state for future reload comparisons

---

### Tier 3 — `up` output polish (depends on Phase 3 wire-up)

#### T4.7 — Polish `up` command startup output
- After stack is ready, print:
  ```
  ErrorProbe ready
  ─────────────────────────────────────────────
  Watching N containers
  Grafana:   http://localhost:3000
  Loki:      http://localhost:3100
  Ingest:    http://127.0.0.1:9099
  ─────────────────────────────────────────────
  Run 'errorprobe watch' to monitor in real-time
  Run 'errorprobe check' to use in CI/scripts
  ```
- Container count sourced from first reconciliation tick result
- All URLs dynamically built from config (respect custom ports)

#### T4.8 — Add Grafana deep-link to `status` and `watch`
- `errorprobe status`: add `GRAFANA` column (or footer line) per container with short URL
- `errorprobe watch` TUI: pressing `[g]` on selected container opens Grafana Explore URL in default browser (`exec "cmd /c start <url>"` on Windows)
- URL is pre-filtered to selected container, last 15 minutes time range

---

### Tier 4 — Unit tests (depends on T4.1, T4.2, T4.3, T4.4; independent of each other)

#### T4.9 — Unit tests: Grafana deep-link builder
- `TestBuildExploreURL_ContainerNameEncoded`: container with special chars; assert URL-encoded correctly
- `TestBuildExploreURL_TimeRangeEncoded`: known from/to times; assert correct Unix ms in URL
- `TestBuildExploreURL_DefaultTimeRange`: no time passed; assert last 15 minutes used
- `TestBuildExploreURL_CustomPort`: Grafana on port 3001; assert URL uses 3001

#### T4.10 — Unit tests: reload change classifier
- `TestClassifyChanges_SeverityPattern_IsSoft`: change to severity patterns; assert in SoftChanges, HasSoft true
- `TestClassifyChanges_ImageVersion_IsHard`: change to loki image; assert in HardChanges, HasHard true
- `TestClassifyChanges_Port_IsHard`: change to Grafana port; assert HardChanges
- `TestClassifyChanges_Mixed`: both soft and hard changed; assert both populated
- `TestClassifyChanges_NoChange`: identical configs; assert both slices empty, HasSoft false, HasHard false
- `TestClassifyChanges_ExcludeList_IsSoft`: container exclusion change; assert SoftChanges

#### T4.11 — Unit tests: `check` command logic
- `TestCheck_AllOK_ExitsZero`: all containers OK; assert exit 0, output contains "All containers healthy"
- `TestCheck_HasErrors_FailOnHasErrors_ExitsOne`: one HAS_ERRORS; fail_on HAS_ERRORS; assert exit 1
- `TestCheck_HasErrors_FailOnFailing_ExitsZero`: one HAS_ERRORS; fail_on FAILING; assert exit 0
- `TestCheck_ExcludedContainer_NotEvaluated`: HAS_ERRORS container in exclude list; assert exit 0
- `TestCheck_StackNotRunning_ExitsOne`: IsStackRunning returns false; assert exit 1 with clear message
- `TestCheck_JSON_Output`: `--json` flag; assert valid JSON with correct `ok` field and `failing` array

#### T4.12 — Unit tests: `IsStackRunning`
- `TestIsStackRunning_AllRunning_True`: all three containers running; assert true
- `TestIsStackRunning_OneDown_False`: one container not running; assert false
- `TestIsStackRunning_NoneRunning_False`: no containers running; assert false

---

### Tier 5 — Integration test

#### T4.13 — Integration test: V1 end-to-end scenario
- **This is a manual test documented as a script for repeatability.**
- Script steps:
  1. `errorprobe down --purge` (clean state)
  2. Start a known-broken container: `docker run -d --name broken-app alpine sh -c "while true; do echo 'ERROR database connection failed'; sleep 1; done"`
  3. Start a healthy container: `docker run -d --name healthy-app alpine sh -c "while true; do echo 'INFO all good'; sleep 2; done"`
  4. `errorprobe up`
  5. Wait 10 seconds
  6. `errorprobe check` — assert exits 1
  7. `errorprobe status` — assert `broken-app` shows `⚠ HAS ERRORS`, `healthy-app` shows `✓ OK`
  8. `errorprobe check --fail-on FAILING` — assert exits 0 (FAILING not reachable in V1)
  9. `errorprobe status --reset broken-app` — assert exits 0
  10. `errorprobe status` — assert `broken-app` back to `✓ OK`
  11. Wait 5 seconds — assert `broken-app` flips back to `HAS ERRORS` (new errors still flowing)
  12. `errorprobe down`
- Document expected output at each step

---

### Final Task

#### T4.14 — Mark phase complete in work-plan.md
- Open `docs/work-plan.md`
- Mark all Phase 4 tasks as `[x]`
- Add completion date next to phase heading

#### T4.15 — Update roadmap.html
- Open `docs/roadmap.html` in a browser and verify Phase 4 is reflected correctly
- In the `PHASES` array, set Phase 4's `status` to `"completed"` and `actualEnd` to the actual finish date
- Compare actual duration against the planned estimate; if velocity differed, adjust `start` / `end` for all subsequent phases accordingly
- Update the `TODAY` constant to the current date
- Recompute and document the revised total story-point burn rate and projected completion date for the remaining phases in a comment above the `PHASES` array
- Verify the **V1 Core Complete** milestone date in `MILESTONES` still matches reality; correct it if the phase finished earlier or later than `2026-07-01`

---

## Deliverables

| Deliverable | Description |
|---|---|
| `cmd/check.go` | Wired — CI-composable exit codes with configurable fail threshold |
| `cmd/logs.go` | Wired — real-time log tail from Loki with errors-only filter |
| `cmd/reload.go` | Wired — soft/hard change classification and application |
| `internal/links.BuildExploreURL` | Grafana deep-link generator |
| `internal/stack.IsStackRunning` | Stack running guard used by check and logs |
| `internal/stack.ClassifyChanges` | Reload change classifier |
| `up` command polish | Clean startup summary with container count and URLs |
| `status` and `watch` Grafana links | Deep-link to Grafana Explore per container |
| Integration test script | Documented manual V1 end-to-end validation |
| Unit tests | ≥ 90% coverage on all new functions |

---

## Manual Tests

Run after all tasks are complete. This is the **V1 acceptance test**.

1. **Zero-config start** — clean machine (no `errorprobe.yaml`); run `errorprobe up`; confirm stack starts with defaults; no error.
2. **Startup summary** — confirm startup output shows container count, all three service URLs, and hints.
3. **`errorprobe check` — clean state** — all healthy containers; confirm exit 0 and "All containers healthy" message.
4. **`errorprobe check` — broken container** — `broken-app` running (from T4.13 script); confirm exit 1; output names the broken container and shows last error message.
5. **`errorprobe check --json`** — valid JSON; `ok: false`; `failing` array lists broken container.
6. **`fail_on: FAILING` in yaml** — set `check.fail_on: FAILING`; `errorprobe check` with broken container; confirm exit 0 (FAILING state not reachable in V1); confirm command output mentions this.
7. **`check.exclude`** — add broken container to `check.exclude`; `errorprobe check`; confirm exit 0.
8. **`errorprobe logs broken-app`** — real-time stream of all log lines; Ctrl+C exits cleanly.
9. **`errorprobe logs broken-app --errors-only`** — only ERROR lines appear; INFO lines absent.
10. **`errorprobe reload` — soft change** — change a severity pattern in `errorprobe.yaml`; `errorprobe reload`; confirm "Soft changes applied" message; no container restarts; new pattern takes effect (test by emitting a log line matching the new pattern).
11. **`errorprobe reload` — hard change** — change Loki image version in `errorprobe.yaml`; `errorprobe reload`; confirm Loki container is recreated; stack resumes; data retained (volume not deleted).
12. **`errorprobe reload` — no change** — reload with unchanged yaml; confirm "No configuration changes detected".
13. **Grafana deep-link** — `errorprobe status` shows Grafana URL per container; clicking it opens Grafana Explore pre-filtered to that container.
14. **`[g]` in TUI** — in `errorprobe watch`, press `g` on a container; Grafana Explore opens in browser pre-filtered.
15. **Total time test** — from `errorprobe up` to identifying a broken container via `errorprobe check`: should be under 10 seconds after stack is live.
