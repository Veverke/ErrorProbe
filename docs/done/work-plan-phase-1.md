# Phase 1 — Stack Bootstrap Engine

**Goal:** `errorprobe up` pulls images and starts Vector, Loki, and Grafana as Docker containers. `errorprobe down` stops and removes them.  
**Prerequisite:** Phase 0 complete.

**UT coverage requirement: ≥ 90% on all new packages.**

---

## Atomic Tasks

Tasks are grouped by dependency tier. All tasks within a tier can be implemented independently and in parallel.

---

### Tier 1 — Docker client (no Phase 1 dependencies)

#### T1.1 — Implement `internal/docker` client wrapper
- Public API:
  - `NewClient() (*Client, error)` — wraps `client.NewClientWithOpts(client.FromEnv)`; validates Docker socket is reachable (ping)
  - `Client.Ping(ctx context.Context) error` — connectivity check
  - `Client.PullImage(ctx context.Context, image string, onProgress func(string)) error` — pulls image; calls `onProgress` with each status line; no-op if image already present locally
  - `Client.ImageExists(ctx context.Context, image string) (bool, error)`
  - `Client.ContainerRunning(ctx context.Context, name string) (bool, error)`
  - `Client.ContainerID(ctx context.Context, name string) (string, error)`
  - `Client.SendSignal(ctx context.Context, containerName string, signal string) error` — used later for SIGHUP
- All methods accept `context.Context` as first argument
- Wrap Docker SDK errors with context: `"pulling loki: <sdk error>"`

#### T1.2 — Implement `internal/docker` network management
- `Client.NetworkExists(ctx, name string) (bool, error)`
- `Client.CreateNetwork(ctx, name string) error` — idempotent; no error if already exists
- `Client.RemoveNetwork(ctx, name string) error`
- Network name constant: `errorprobe-net`

#### T1.3 — Implement `internal/docker` container lifecycle
- `Client.StartContainer(ctx, spec ContainerSpec) error`
- `Client.StopContainer(ctx, name string, timeoutSecs int) error`
- `Client.RemoveContainer(ctx, name string, force bool) error`
- `ContainerSpec` struct: `Name`, `Image`, `Env []string`, `Ports []PortBinding`, `Mounts []Mount`, `Volumes []VolumeMount`, `Networks []string`, `Labels map[string]string`
- `Client.VolumeExists(ctx, name string) (bool, error)`
- `Client.CreateVolume(ctx, name string) error` — idempotent
- `Client.RemoveVolume(ctx, name string) error`

---

### Tier 2 — Config generators (depends on T0.5 only; independent of each other)

#### T1.4 — Implement `internal/configgen` Loki generator
- `GenerateLoki(cfg *config.Config, outputDir string) error`
- Produces `loki-config.yaml` at `outputDir/loki-config.yaml`
- Config covers: HTTP listen address/port, storage path (`/loki/chunks`), retention period, schema config
- Template-driven: Go `text/template` reading from `assets/templates/loki.yaml.tmpl`
- Overwrites on every call (configs are always regenerated)

#### T1.5 — Implement `internal/configgen` Grafana datasource generator
- `GenerateGrafanaDatasource(cfg *config.Config, outputDir string) error`
- Produces `grafana/provisioning/datasources/loki.yaml` at correct subdirectory under `outputDir`
- Creates subdirectory if it does not exist
- Datasource: name `ErrorProbe-Loki`, type `loki`, URL `http://loki:{cfg.Stack.Loki.Port}`, access `proxy`, isDefault true
- Template-driven: `assets/templates/grafana-datasource.yaml.tmpl`

#### T1.6 — Implement `internal/configgen` Vector stub generator
- `GenerateVector(cfg *config.Config, outputDir string, containers []string) error`
- For Phase 1: `containers` parameter is empty `[]string{}`; produces a minimal valid `vector.toml` with no sources and a single `console` sink (ensures Vector starts without error)
- Full implementation in Phase 2 — the function signature is final now

---

### Tier 3 — Port conflict detection (depends on T0.5 only)

#### T1.7 — Implement port conflict checker
- Standalone function `CheckPorts(cfg *config.Config) error` in `internal/stack` package
- Attempts `net.Listen("tcp", "127.0.0.1:{port}")` for each configured port (Loki, Grafana, ingest)
- If any port is in use: return error listing all conflicting ports with clear message
- Releases listeners immediately after check (defer close)

---

### Tier 4 — Health poller (depends on T0.5 only)

#### T1.8 — Implement stack health poller
- `PollUntilReady(ctx context.Context, cfg *config.Config, onStatus func(string)) error` in `internal/stack`
- Polls `GET http://127.0.0.1:{loki.port}/ready` (Loki) and `GET http://127.0.0.1:{grafana.port}/api/health` (Grafana)
- Retries with 1-second interval; configurable timeout (default 60s)
- Calls `onStatus("loki: ready")` / `onStatus("grafana: ready")` as each becomes available
- Returns error if timeout exceeded before both are ready

---

### Tier 5 — Bootstrap engine (depends on T1.1, T1.2, T1.3, T1.4, T1.5, T1.6, T1.7, T1.8)

#### T1.9 — Implement `internal/stack` bootstrap (up)
- `Up(ctx context.Context, cfg *config.Config, onStatus func(string)) error`
- Sequence:
  1. Check Docker socket reachable (`docker.Ping`)
  2. Check port conflicts (`CheckPorts`) — fail fast before pulling images
  3. Check if stack already running (idempotency check — if all three containers running, print status and return nil)
  4. Pull images: Vector, Loki, Grafana — only pull if not present locally; call `onStatus` with progress
  5. Generate configs: call `GenerateLoki`, `GenerateGrafanaDatasource`, `GenerateVector`
  6. Create network (`errorprobe-net`) if not exists
  7. Create volumes (`errorprobe-loki-data`, `errorprobe-grafana-data`) if not exist
  8. Start Loki container with correct bind mounts and volume
  9. Start Grafana container with correct bind mounts and volume
  10. Start Vector container with correct bind mount
  11. Poll until ready (`PollUntilReady`)
  12. Call `onStatus` with final "Stack ready" message including Grafana URL

#### T1.10 — Implement `internal/stack` teardown (down)
- `Down(ctx context.Context, cfg *config.Config, purge bool) error`
- Stops and removes: Vector, Grafana, Loki containers (in that order — reverse of start)
- Removes `errorprobe-net`
- If `purge` is true: also removes `errorprobe-loki-data` and `errorprobe-grafana-data` volumes
- Idempotent: no error if a container or network is already absent

---

### Tier 6 — Wire CLI commands (depends on T1.9, T1.10)

#### T1.11 — Wire `up` command
- Replace stub in `cmd/up.go` with real implementation calling `stack.Up`
- `onStatus` callback: print each status line to stdout with timestamp prefix
- On error: print error and exit 1

#### T1.12 — Wire `down` command
- Replace stub in `cmd/down.go` with real implementation calling `stack.Down`
- Add `--purge` flag: removes data volumes when present
- On error: print error and exit 1

---

### Tier 7 — Unit tests (depends on T1.1–T1.8; independent of each other)

#### T1.13 — Unit tests: `internal/docker`
- Use `dockertest` or interface-based mock (prefer mock — no Docker daemon required for unit tests)
- Define `DockerAPI` interface matching all `Client` methods; use mock in tests
- `TestPullImage_AlreadyPresent_NoOp`: image exists locally; assert pull not called
- `TestPullImage_Missing_Pulls`: image absent; assert pull called with correct image ref
- `TestContainerRunning_True`: mock returns running container; assert true
- `TestContainerRunning_False`: mock returns no container; assert false
- `TestCreateNetwork_Idempotent`: network exists; assert no error
- `TestRemoveContainer_NotFound_NoError`: container absent; assert no error
- `TestCheckPorts_AllFree`: all ports available; assert no error
- `TestCheckPorts_Conflict`: pre-listen on one port; assert error names the conflicting port

#### T1.14 — Unit tests: `internal/configgen`
- `TestGenerateLoki_OutputMatchesTemplate`: call with known config; assert generated file content matches expected YAML (golden file test)
- `TestGenerateLoki_PortInjected`: custom port in config; assert port appears in output
- `TestGenerateLoki_RetentionInjected`: custom retention in config; assert retention appears in output
- `TestGenerateGrafanaDatasource_OutputMatchesTemplate`: call with known config; assert file content correct
- `TestGenerateGrafanaDatasource_CreatesSubdirectory`: output subdir does not exist; assert it is created
- `TestGenerateVector_Stub_ValidToml`: empty containers list; assert output is valid TOML (parse and verify)

#### T1.15 — Unit tests: `internal/stack` health poller
- `TestPollUntilReady_BothReady`: mock HTTP endpoints respond 200; assert returns nil
- `TestPollUntilReady_Timeout`: mock endpoints never respond; assert returns error after timeout
- `TestPollUntilReady_LokiSlower`: Loki responds after 2 retries; assert waits correctly and returns nil

---

### Final Task

#### T1.16 — Mark phase complete in work-plan.md
- Open `docs/work-plan.md`
- Mark all Phase 1 tasks as `[x]`
- Add completion date next to phase heading

#### T1.17 — Update roadmap.html
- Open `docs/roadmap.html` in a browser and verify Phase 1 is reflected correctly
- In the `PHASES` array, set Phase 1's `status` to `"completed"` and `actualEnd` to the actual finish date
- Compare actual duration against the planned estimate; if velocity differed, adjust `start` / `end` for all subsequent phases accordingly
- Update the `TODAY` constant to the current date
- Recompute and document the revised total story-point burn rate and projected completion date for the remaining phases in a comment above the `PHASES` array

---

## Deliverables

| Deliverable | Description |
|---|---|
| `internal/docker` | Full Docker API client wrapper: ping, pull, network, container, volume lifecycle |
| `internal/configgen` | Loki, Grafana datasource, and Vector (stub) config generators |
| `assets/templates/` | `loki.yaml.tmpl`, `grafana-datasource.yaml.tmpl`, `vector.toml.tmpl` |
| `internal/stack` | `Up`, `Down`, `CheckPorts`, `PollUntilReady` |
| `cmd/up.go` | Wired — starts the full stack |
| `cmd/down.go` | Wired — stops and optionally purges the stack |
| Unit tests | ≥ 90% coverage on `docker`, `configgen`, `stack` packages |

---

## Manual Tests

Run after all tasks are complete, with Docker Desktop running:

1. **`errorprobe up`** — observe image pull progress lines; all three containers start; Grafana reachable at `http://localhost:3000`; Loki datasource pre-wired in Grafana Explore (no manual datasource setup needed).
2. **`errorprobe up` (second run)** — stack already running; tool prints current status and exits 0 cleanly (no duplicate containers, no errors).
3. **`errorprobe down`** — all three containers stopped and removed; `errorprobe-net` network removed; data volumes retained.
4. **`errorprobe down --purge`** — confirm `errorprobe-loki-data` and `errorprobe-grafana-data` volumes are also removed (`docker volume ls`).
5. **`errorprobe down` (stack not running)** — exits 0 cleanly with no error.
6. **Port conflict** — manually bind port 3000; run `errorprobe up`; confirm clear error message naming the conflicting port before any image pull begins.
7. **Configs generated** — after `errorprobe up`, inspect `~/.errorprobe/configs/`; confirm `loki-config.yaml`, `grafana/provisioning/datasources/loki.yaml`, and `vector.toml` all exist with correct content.
8. **`go test ./... -cover`** — all tests pass; coverage ≥ 90% on `internal/docker`, `internal/configgen`, `internal/stack`.
