# ErrorProbe — Work Plan

---

## Overview

This document defines the implementation phases to transform the intent defined in `intent.md` into a working solution, following the architecture defined in `architecture.md`.

Each phase has a clear goal, a definition of done, and an ordered task list. Tasks within a phase are ordered by dependency — each task assumes the previous ones are complete.

**Implementation language:** Go  
**CLI framework:** Cobra  
**Primary platform:** Windows (Docker Desktop)  
**V1 runtime target:** Docker (local)  
**V1 follow-on:** Kubernetes (local)

---

## Phase 0 — Repository & Project Skeleton *(completed 2026-04-28)*

**Goal:** A compilable Go project with the right structure, ready to receive real implementation.  
**No external services. No Docker interaction. Just a clean foundation.**

### Tasks

- [x] Initialise Go module (`go mod init github.com/errorprobe/errorprobe`)
- [x] Define top-level package structure:
  ```
  cmd/                    ← Cobra command entrypoints
  internal/
    config/               ← errorprobe.yaml loading, validation, defaults
    docker/               ← Docker API client wrapper
    stack/                ← bootstrap engine (pull, start, stop, health-poll)
    discovery/            ← container discovery loop
    configgen/            ← Vector/Loki/Grafana config generators
    ingest/               ← HTTP listener + transport interface
    health/               ← health state machine + persistence
    tui/                  ← Bubbletea watch UI
    logger/               ← ErrorProbe's own rotating log
  assets/
    templates/            ← VRL, Loki, Grafana config templates
  ```
- [x] Wire Cobra root command and stub subcommands: `up`, `down`, `reload`, `update`, `list`, `status`, `watch`, `logs`, `check`
- [x] Implement `config` package: load `errorprobe.yaml` with precedence (project-local → global → built-in defaults), validate schema, expose typed struct
- [x] `version: 1` schema field enforced from day one; load fails with clear message on unknown version
- [x] Implement `logger` package: rotating file logger writing to `~/.errorprobe/logs/errorprobe.log`
- [x] State directory initialisation: ensure `~/.errorprobe/{configs,state,logs}/` exist on first run
- [x] CI: build compiles cleanly on Windows, zero lint errors

**Exit criterion:** `errorprobe --help` runs and lists all subcommands. `errorprobe up` prints "not implemented" stub. Project builds with `go build ./...`.

---

## Phase 1 — Stack Bootstrap Engine *(completed 2026-04-28)*

**Goal:** `errorprobe up` pulls images and starts Vector, Loki, and Grafana as Docker containers. `errorprobe down` stops and removes them.

### Tasks

- [x] Implement `docker` package: Docker API client using `client.NewClientWithOpts(client.FromEnv)` (handles Windows named pipe automatically), connectivity check, image pull with progress reporting
- [x] Implement `configgen` package — Loki config generator: produces `loki-config.yaml` from `errorprobe.yaml` settings (port, retention), written to `~/.errorprobe/configs/`
- [x] Implement `configgen` package — Grafana datasource provisioner: produces `grafana/provisioning/datasources/loki.yaml`, auto-wires Loki as datasource
- [x] Implement `configgen` package — Vector config generator (stub for Phase 1): produces minimal `vector.toml` that starts without error; no sources yet (added in Phase 2)
- [x] Implement `stack` package: create dedicated Docker network (`errorprobe-net`), start Loki container (bind-mount config, named volume `errorprobe-loki-data`), start Grafana container (bind-mount provisioning, named volume `errorprobe-grafana-data`), start Vector container (bind-mount config)
- [x] Health-poll loop: ping Loki API (`GET /ready`) and Grafana API (`GET /api/health`) with timeout and retry; report progress to stdout
- [x] Port conflict detection: check configured ports before attempting to start; fail with clear message listing which port is in use
- [x] Idempotency: `errorprobe up` on an already-running stack is a no-op (detect running containers, report status, exit cleanly)
- [x] Implement `down` command: stop and remove the three managed containers; remove `errorprobe-net`; retain data volumes (explicit `--purge` flag removes volumes)
- [x] Startup output: report image pull progress, container start sequence, confirmation with Grafana URL

**Exit criterion:** `errorprobe up` → Loki, Grafana, and Vector all running as Docker containers. Grafana is reachable at `http://localhost:3000`. Loki datasource is pre-wired and works in Grafana Explore. `errorprobe down` cleanly removes the stack.

---

## Phase 2 — Container Discovery

**Goal:** ErrorProbe discovers all running user containers, applies watch policy, generates a Vector config scoped to the approved set, and reloads Vector when the set changes.

### Tasks

- [ ] Implement `discovery` package: list all running containers via Docker API, extract metadata (name, image, labels, start time, restart count, health probe status)
- [ ] Apply watch policy from `errorprobe.yaml`: filter by `containers.exclude` glob patterns; resolve final approved set
- [ ] Persist approved set to `~/.errorprobe/state/containers.json`
- [ ] Implement Vector config generation (full): produce `vector.toml` with `docker_logs` source scoped to approved container set (by name list); include VRL transform for severity inference using patterns from `errorprobe.yaml`
- [ ] VRL transform: normalise all log lines to schema `{ timestamp, container, level, message, raw }`; apply severity inference against `detection.severity_patterns`; emit to both Loki sink and ErrorProbe HTTP sink
- [ ] Reconciliation loop: run discovery every 5 seconds; diff against previous approved set; on change, regenerate Vector config and send `SIGHUP` to Vector container via Docker API
- [ ] Implement `errorprobe list` command: tabular output — container name, image, status, infra health (running / restarting / exited), watching (yes/no)
- [ ] Validate end-to-end: logs from a running container flow through Vector → Loki; query Loki LogQL to confirm labels (`container`, `level`) are correct

**Exit criterion:** `errorprobe list` shows all running containers with correct watch status. Any log line from a watched container appears in Loki with correct `container` and `level` labels within 2 seconds. Starting a new container while `errorprobe` is running causes it to appear in the next reconciliation cycle.

---

## Phase 3 — Semantic Health Engine (Tier 1)

**Goal:** Real-time error detection. `errorprobe status` shows per-container functional health. `errorprobe watch` provides a live TUI.

### Tasks

- [ ] Implement `ingest` package: transport interface `Ingest(batch []LogEvent)`; HTTP JSON implementation binding on `127.0.0.1:{ingest.port}` (default 9099)
- [ ] Add HTTP sink to Vector config generation: `POST http://127.0.0.1:9099/ingest` with JSON batching; runs alongside existing Loki sink
- [ ] Implement `health` package: in-memory per-container state machine (`OK` → `HAS_ERRORS`); Tier 1 trigger: any inbound log event with `level = error` or `level = warn` flips container to `HAS_ERRORS`
- [ ] Persist health snapshot to `~/.errorprobe/state/health.json` on every state change
- [ ] Track per-container: current state, total error count, timestamp of first error, timestamp of most recent error, most recent error message
- [ ] Implement `errorprobe status` command: one line per watched container — name, functional state (`✓ OK` / `⚠ HAS ERRORS [N]`), infra state (from discovery metadata), last error message excerpt
- [ ] Implement `errorprobe status --json` for machine-readable output
- [ ] Implement `errorprobe watch` command: Bubbletea TUI, refreshes on state change events (not polling); columns: container, functional state, infra state, error count, last error time; keyboard: `[e]` expand errors for selected container, `[q]` quit
- [ ] State reset: `errorprobe status --reset <container>` clears error state for a container (useful after acknowledging a known issue)

**Exit criterion:** Developer runs `errorprobe watch`. A watched container emits an error log line. Within 1–2 seconds, the TUI flips that container from `✓ OK` to `⚠ HAS ERRORS` and shows the error message. Both functional and infra state are visible in the same row.

---

## Phase 4 — Developer UX & CI Integration

**Goal:** Complete the V1 success criterion — hours of debugging → 5 seconds. Composable with test scripts.

### Tasks

- [ ] Implement `errorprobe check` command: reads current health snapshot from `~/.errorprobe/state/health.json`; exits 0 if all watched containers satisfy `check.fail_on` threshold; exits 1 otherwise; requires stack to be running (fails with clear message if not)
- [ ] `check.fail_on` respected: `HAS_ERRORS` (any error triggers failure) vs `FAILING` (only confirmed Tier 2 patterns — Tier 2 is V2, so `FAILING` state is not reachable in V1; document this clearly)
- [ ] `check.exclude` respected: listed containers exempt from exit code evaluation
- [ ] Implement `errorprobe logs <container>` command: stream log lines from Loki for a given container via LogQL; real-time tail
- [ ] Implement `errorprobe logs <container> --errors-only`: stream only `level=error` lines
- [ ] Grafana deep-link: `errorprobe status` and `errorprobe watch` both print/display the Grafana Explore URL pre-filtered to the selected container and current time range
- [ ] Startup output polish: on `errorprobe up` completion, print discovered container count, stack URLs, and hint to run `errorprobe watch`
- [ ] Implement `errorprobe reload` command: re-read `errorprobe.yaml`, classify changed fields as soft (severity patterns, thresholds, exclusions) or hard (ports, images, bind address); apply soft changes via Vector config regeneration + SIGHUP; apply hard changes via targeted container recreation; print summary of what was soft-applied vs recreated
- [ ] Integration test (manual): deploy a known-broken container, run `errorprobe up`, run `errorprobe check`, assert non-zero exit and correct container identified

**Exit criterion:** A new developer clones a repo, runs `errorprobe up`, deploys N containers (one of which is emitting errors), runs `errorprobe check` → exits 1. Runs `errorprobe status` → sees exactly which container is broken and the first error message. Zero prior configuration required. Total time from `errorprobe up` to answer: under 10 seconds.

---

## Phase 5 — K8s Discovery (V1 Follow-on)

**Goal:** Extend discovery to local Kubernetes clusters (Docker Desktop, k3s, minikube). The log pipeline and health engine are unchanged — only the discovery mechanism is added.

### Tasks

- [ ] Implement K8s discovery in `discovery` package: detect available kubeconfig (`~/.kube/config`), list pods and containers across namespaces via K8s API (`client-go`)
- [ ] Runtime auto-detect: if both Docker socket and kubeconfig are reachable, discover from both; merge into a single approved set with `runtime: docker` / `runtime: k8s` metadata field
- [ ] K8s metadata enrichment: add `pod`, `namespace`, `node` fields to container metadata
- [ ] Vector config generation: add K8s log source (Vector `kubernetes_logs` source) for approved K8s containers; apply same VRL transform pipeline
- [ ] `errorprobe list` extended: show `runtime` column; display pod and namespace for K8s containers
- [ ] `errorprobe watch` TUI extended: group by runtime if both are active; K8s containers show pod/namespace as context
- [ ] Watch policy extended: `containers.exclude` patterns match on `pod/*` and `namespace/*` prefixes for K8s exclusions
- [ ] Validate against the original pain case: deploy N pods in local K8s, one emitting errors while infrastructure-healthy; `errorprobe watch` surfaces it within 2 seconds

**Exit criterion:** The original use case from `intent.md` is solved end-to-end: K8s pods running, all showing infrastructure-healthy, one emitting errors — `errorprobe watch` shows it as `⚠ HAS ERRORS` within 2 seconds of the first error log line.

---

## Phase 6 — Tier 2 Detection (V2 Start)

**Goal:** Distinguish confirmed failures from noise. Introduce the `FAILING` state.

### Tasks

- [ ] Implement Loki query engine in `health` package: time-range error rate queries via LogQL HTTP API; configurable window and threshold from `errorprobe.yaml`
- [ ] Error fingerprinting: normalise repeated stack traces (strip line numbers, memory addresses, timestamps) to produce a stable fingerprint per error pattern
- [ ] Tier 2 trigger: N errors with the same fingerprint within the configured window → container transitions to `FAILING`
- [ ] State machine extended: `OK` → `HAS_ERRORS` → `FAILING`; transitions and timestamps persisted to `health.json`
- [ ] `errorprobe watch` TUI: third state `✗ FAILING` rendered distinctly (colour, icon); show fingerprint summary ("same stack trace 47×")
- [ ] `check.fail_on: FAILING` now functional; `HAS_ERRORS` and `FAILING` are both valid configurable thresholds
- [ ] History log introduced: `~/.errorprobe/state/history.jsonl`; one entry per state transition; retention enforced per `history_retention` in `errorprobe.yaml`

**Exit criterion:** A container looping the same error at high frequency transitions to `FAILING`. A container with a single one-off error remains at `HAS_ERRORS`. `errorprobe check --fail-on FAILING` exits 0 for the noisy container and 1 for the confirmed failure.

---

## Phase 7 — gRPC / OTLP Transport (V2)

**Goal:** Add gRPC as a configurable alternative to HTTP JSON for the Vector → ErrorProbe ingest path.

### Tasks

- [ ] Implement `ingest/grpc.go`: OTLP gRPC receiver implementing the same `Ingest` interface
- [ ] Add gRPC to Vector config generation: emit OTLP gRPC sink when `stack.ingest.transport: grpc` is configured
- [ ] `errorprobe reload` classifies transport change as a hard change (container recreation required)
- [ ] Documentation: when to prefer gRPC (high-volume remote scenarios) vs HTTP (default local use)

**Exit criterion:** Setting `transport: grpc` in `errorprobe.yaml` and running `errorprobe reload` switches the ingest path to gRPC with no manual intervention.

---

## Phase 8 — Remote Docker & Kubernetes (V2)

**Goal:** Extend discovery and collection to remote hosts.

### Tasks

- [ ] Remote Docker: connect via Docker TCP URI (`DOCKER_HOST=tcp://...`); configurable in `errorprobe.yaml`
- [ ] Remote K8s: connect via kubeconfig context selection; configurable context name in `errorprobe.yaml`
- [ ] Ingest binding: when remote mode is active, `stack.ingest.bind` defaults to `0.0.0.0`; bearer token auth generated on first `up`, stored in `~/.errorprobe/state/token`, embedded into generated Vector config
- [ ] Vector deployment for remote: generate a standalone Vector config bundle that can be deployed on the remote host (Docker container or K8s DaemonSet); `errorprobe remote-config export` command produces it
- [ ] `errorprobe watch` TUI: show remote host/context label per container group

**Exit criterion:** `errorprobe` running on a developer's Windows machine receives logs from a remote Docker host or K8s cluster, surfaces errors in the TUI within 2 seconds.

---

## Distribution (Parallel Track — starts after Phase 4)

**Goal:** Zero-friction installation matching the zero-config runtime promise.

### Tasks

- [ ] GitHub Actions: build Windows (amd64), Linux (amd64, arm64), macOS (arm64) binaries on tag push; upload as GitHub Release assets
- [ ] Install script (`install.sh` / `install.ps1`): detect OS and arch, download correct binary from GitHub Releases, place in `$PATH` or prompt user
- [ ] Checksums and signature verification in install script
- [ ] `winget` package submission (Windows-first)
- [ ] `scoop` bucket (Windows alternative)
- [ ] `brew` formula (macOS — post Windows validation)
- [ ] `errorprobe version` command: print version, build commit, build date

---

## Testing Strategy

Each phase includes a manual integration test as its exit criterion. In addition:

| Layer | Approach |
|---|---|
| `config` package | Unit tests: load valid yaml, load with overrides, reject unknown version, apply defaults |
| `configgen` package | Unit tests: given config struct → assert generated file contents match expected templates |
| `health` state machine | Unit tests: state transitions, persistence round-trip, threshold evaluation |
| `ingest` HTTP handler | Unit tests: valid batch accepted, malformed JSON rejected, correct state updates triggered |
| `discovery` policy | Unit tests: exclude glob matching, merge of Docker + K8s sets, reconciliation diff |
| End-to-end (Phase 4+) | Integration test: real Docker containers, `errorprobe up`, inject error logs, assert `check` exit code |

---

## Phase Dependencies

```
Phase 0 (skeleton)
    └── Phase 1 (stack bootstrap)
            └── Phase 2 (discovery + pipeline)
                    └── Phase 3 (health engine Tier 1)
                            └── Phase 4 (UX + CI)       ← V1 complete
                                    └── Phase 5 (K8s)   ← V1 follow-on complete
                                    └── Phase 6 (Tier 2)
                                    └── Phase 7 (gRPC)
                                    └── Phase 8 (remote)
Phase 4 → Distribution (parallel)
```

Phases 6, 7, and 8 are independent of each other once Phase 5 is complete and can be implemented in any order.
