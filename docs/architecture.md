# ErrorProbe — Architecture

---

## 1. Mental Model

> **The Go binary is the operator. Vector/Loki/Grafana are its workers, running as containers it owns. Docker and K8s are the environment it reads.**

ErrorProbe is a smoke detector, not a search engine. It does not wait to be asked — it tells you when something is wrong.

Existing tools (Docker Desktop, OpenLens, Grafana) are pull-based: they present data and wait for the developer to investigate. ErrorProbe is push-based: it answers "what needs my attention right now?" without requiring investigation.

---

## 2. Core Architecture

### 2.1 What ErrorProbe Is

- A single Go binary. No runtime, no host dependencies — download and run.
- An operator that pulls and starts Vector, Loki, and Grafana as Docker containers, with generated configs bind-mounted in.
- A Semantic Health Engine that produces a functional health signal no infrastructure tool provides.
- A config generator: the user never touches Vector TOML or Loki YAML directly.
- A lifecycle manager: `errorprobe up` / `down` / `update`.

### 2.2 What ErrorProbe Is Not

- Not a new logging engine.
- Not a competitor to Loki, Grafana, or OpenLens.
- Not responsible for building or deploying user containers/pods — these are assumed to already be running.

### 2.3 Component Diagram

```
┌──────────────────────────────────────────────────────────┐
│                    errorprobe (binary)                    │
│                                                          │
│  CLI Layer          cobra commands: up/down/status/watch  │
│  ──────────────────────────────────────────────────────  │
│  Bootstrap Engine   pull images → start containers       │
│                     generate configs → bind mount        │
│  ──────────────────────────────────────────────────────  │
│  Discovery Loop     Docker API (Windows named pipe)      │
│                     K8s API (kubeconfig) — V1 follow-on  │
│                     reconciles every ~5s → SIGHUP Vector │
│  ──────────────────────────────────────────────────────  │
│  Semantic Health    HTTP listener ← Vector push (Tier 1) │
│  Engine             Loki query (Tier 2, V2)              │
│                     state machine: OK/HAS_ERRORS/FAILING  │
│  ──────────────────────────────────────────────────────  │
│  Output Surface     CLI status / Bubbletea TUI watch     │
│                     JSON mode / check (CI exit codes)    │
└──────────────────────────────────────────────────────────┘
         ↕ Docker API manages lifecycle ↕
  ┌──────────┐   ┌──────┐   ┌─────────┐
  │  Vector  │──▶│ Loki │──▶│ Grafana │
  └──────────┘   └──────┘   └─────────┘
       │
       └──────────────▶ ErrorProbe HTTP listener (real-time Tier 1)
```

### 2.4 What Is Delegated

| Responsibility | Delegated To |
|---|---|
| Log collection and streaming | Vector |
| Log parsing and normalization | Vector (VRL transforms) |
| Severity inference | Vector (VRL) — ErrorProbe trusts the `level` field it receives |
| Log storage and indexing | Loki |
| Log visualization and drill-down | Grafana (Explore view) |
| Container discovery | Docker API / K8s API — compiled into ErrorProbe binary via SDK |

The codebase is **glue + opinion + UX + detection logic**. Heavy lifting is fully delegated.

---

## 3. Zero-Config Contract

**The user installs nothing.** The only prerequisite is Docker (already present — containers are the use case).

The ErrorProbe binary, using the Docker API, pulls and starts Vector, Loki, and Grafana as containers. Configs are generated into `~/.errorprobe/configs/` and bind-mounted. The user never writes a config file to get started.

`errorprobe.yaml` exists for customization, not as a requirement.

### Installation

Distributed as a single static Go binary via `curl | sh` install script from GitHub Releases (OS/arch auto-detected). No package manager dependency at launch. Winget/Scoop/Brew follow later.

A Docker image of ErrorProbe itself is **not** a distribution option — it would require Docker-in-Docker to manage the tool-containers it owns, which is an anti-pattern.

---

## 4. Stack Management

### 4.1 Image Lifecycle

ErrorProbe manages Vector, Loki, and Grafana entirely via the Docker API — no `docker compose` dependency.

On `errorprobe up`:
1. Check Docker socket is reachable
2. Pull required images (once; cached on subsequent runs) with progress reporting
3. Generate configs into `~/.errorprobe/configs/`
4. Start containers via Docker API with configs bind-mounted and data volumes attached
5. Health-poll Loki API + Grafana API until live
6. Print confirmation and Grafana URL

`errorprobe up` is **idempotent** — safe to run against an already-running stack.

`errorprobe update` pulls latest pinned images, regenerates configs, restarts containers.

### 4.2 Storage Layout

```
~/.errorprobe/
  configs/                          ← bind-mounted into tool-containers
    vector.toml                     ← generated from errorprobe.yaml + discovery
    loki-config.yaml                ← generated
    grafana/
      provisioning/
        datasources/loki.yaml       ← auto-provisioned (Loki wired up automatically)
  state/
    health.json                     ← current health snapshot, persisted on every update
  logs/
    errorprobe.log                  ← ErrorProbe's own rotating log

Docker named volumes (managed via Docker API):
  errorprobe-loki-data              ← Loki log storage
  errorprobe-grafana-data           ← Grafana state
```

**Rationale for mixed storage strategy:**
- `~/.errorprobe/configs/` uses **bind mounts** — ErrorProbe generates and owns these files; bind mount is correct when the host application actively writes files that containers read.
- Data directories use **named Docker volumes** — ErrorProbe does not need direct host access to these. Volumes give better performance on Docker Desktop (Mac/Windows avoid VM filesystem crossing), survive container recreation, and are cleanly managed via `docker volume rm`.

---

## 5. Container Discovery

### 5.1 Source of Truth

**ErrorProbe is the sole source of truth for what containers are watched.** Vector does not autodiscover independently.

**Why not Vector-driven discovery:**
If Vector owns discovery, the exclusion policy in `errorprobe.yaml` cannot stop Vector from collecting excluded containers — it can only suppress them at display time. Vector would still collect, parse, and store logs for containers the user explicitly excluded. Policy must drive collection, not just display.

### 5.2 Reconciliation Loop

ErrorProbe runs a discovery loop every ~5 seconds via the Docker API. It applies the watch policy (inclusions, exclusions, label selectors from `errorprobe.yaml`).

When the active container set changes (new container matches, container stops, user changes policy), ErrorProbe:
1. Regenerates the Vector config with the updated container list
2. Sends Vector a `SIGHUP` — Vector reloads in milliseconds, no restart, no log loss

Vector's Docker source is scoped to the set ErrorProbe determines:

```toml
# Generated by ErrorProbe
[sources.container_logs]
  type = "docker_logs"
  include_containers = ["payments-api", "auth-service"]
  # OR label-based:
  include_labels = {"errorprobe.watch" = "true"}
```

Label-based selection is the preferred long-term approach — ErrorProbe labels containers it decides to watch per the policy, Vector follows labels. Policy lives in one place.

### 5.3 Runtime Targets

| Runtime | Discovery Mechanism | Scope |
|---|---|---|
| Local Docker | Docker socket / API | V1 |
| Local Kubernetes (Docker Desktop, k3s, minikube) | K8s API via kubeconfig | V1 follow-on |
| Remote Docker | Docker TCP / URI | V2 |
| Remote Kubernetes | kubeconfig / K8s API | V2 |

**Docker first.** K8s discovery is additive — it differs only in the discovery mechanism, not in the downstream pipeline. Ship Docker-first and add K8s once Docker results are validated.

### 5.4 Windows Docker Socket

On Windows (Docker Desktop), the socket path is `//./pipe/docker_engine`, not `/var/run/docker.sock`. The Go Docker SDK handles this automatically via `client.NewClientWithOpts(client.FromEnv)`. All development and testing is Windows-first from day one.

---

## 6. Log Pipeline

```
Container stdout/stderr
        ↓
    Vector (Docker logs source)
        ↓ VRL transform
    Normalized schema: { timestamp, container, level, message, raw }
        ↓              ↓
       Loki        ErrorProbe HTTP listener
    (storage)       (real-time detection)
```

### 6.1 VRL Severity Inference

VRL owns all severity inference. ErrorProbe trusts the `level` field it receives and never re-parses log text. This keeps the detection logic clean and stateless.

Severity patterns are defined in `errorprobe.yaml` and wired into the VRL transform at config generation time — users with non-standard log formats (e.g., Java `[SEVERE]`) extend via config, not code.

```yaml
detection:
  severity_patterns:
    error: ["ERROR", "FATAL", "panic", "Exception", "error"]
    warn:  ["WARN", "WARNING", "warn"]
```

VRL handles JSON logs, logfmt, and plain-text regex. The normalized `level` field is the contract between the pipeline and the health engine.

### 6.2 Loki Label Strategy

Labels indexed in Loki: `container`, `pod`, `namespace`, `level`. These are the axes for LogQL queries and Grafana filtering.

---

## 7. Semantic Health Engine

### 7.1 Two Detection Tiers

| Tier | Trigger | Signal | Purpose |
|---|---|---|---|
| **Tier 1** | Any single `ERROR` or `WARN` log entry | `HAS_ERRORS` | Total transparency. Nothing hidden. |
| **Tier 2** (V2) | Repeating pattern, sustained error rate | `FAILING` | Confident escalation. Definitively broken. |

Both tiers are kept. Neither replaces the other. Tier 1 serves transparency; Tier 2 serves confidence.

### 7.2 Ingest Transport

**V1: HTTP JSON**

Vector pushes log batches to ErrorProbe's local HTTP listener via the `http` sink:

```
Vector → POST http://127.0.0.1:9099/ingest (JSON batch) → ErrorProbe
```

- Simple to implement and operate
- Fully debuggable (`curl -d @payload.json http://localhost:9099/ingest`)
- Vector's `http` sink has mature batching, retries, and backpressure
- Natural extension point for CI pipelines and future remote agents

**V2: gRPC / OTLP (configurable)**

gRPC via OTLP becomes the right foundation when remote/multi-host scenarios are introduced. OTLP is the emerging universal observability wire format — makes ErrorProbe a compatible OTLP consumer regardless of collector.

**Design:** The transport is abstracted behind an interface from day one:

```
internal/ingest/
  handler.go    ← interface: Ingest(batch []LogEvent)
  http.go       ← HTTP JSON implementation
  grpc.go       ← gRPC/OTLP implementation (V2)
```

`errorprobe.yaml` defaults to `transport: http`. User sets `transport: grpc` to switch. Vector config generation reads the same setting to produce the matching sink configuration.

**Dual transport (HTTP and gRPC simultaneously) was considered and rejected** — there is only one ingest path; splitting it across two protocols adds complexity with no architectural gain.

### 7.3 Health State Machine

Three states per container: `OK` → `HAS_ERRORS` → `FAILING`

**State persistence:**
- **Current snapshot** (`~/.errorprobe/state/health.json`): persisted on every state update. Small, fixed-size file. Used by `errorprobe check` (CI mode) without requiring `watch` to be running.
- **State change history**: V2 feature. Only useful once Tier 2 pattern detection is in place. Configurable retention window in `errorprobe.yaml` (e.g., `history_retention: 30d`).

### 7.4 Endpoint Binding

**V1:** `127.0.0.1` only. Local-only scope; no auth required.

**V2 (remote):** For remote Docker/K8s, Vector runs inside the remote environment and pushes back to the developer's machine. ErrorProbe's ingest endpoint becomes configurable (`bind: 0.0.0.0`). When binding on a real network interface, bearer token auth is required — configured in the Vector HTTP sink, validated by ErrorProbe. The binding address and auth layer are config on the transport layer, not scattered through the codebase.

---

## 8. Configuration

### 8.1 `errorprobe.yaml`

Single user-facing config surface. Opinionated defaults for everything — zero-config by default, fully tunable by choice. ErrorProbe reads it and generates all downstream tool configs. The user never touches Vector TOML or Loki YAML directly.

**Precedence:** `./errorprobe.yaml` (project-local) → `~/.errorprobe/config.yaml` (global) → built-in defaults. Project-local wins — different projects can have different thresholds.

**Schema versioning:** `version: 1` field from day one. Future breaking changes increment the version; ErrorProbe can migrate or warn accordingly.

### 8.2 Reference Schema

```yaml
version: 1

stack:
  vector:
    image: timberio/vector:0.38.0-alpine
  loki:
    image: grafana/loki:3.0.0
    port: 3100
    retention: 72h
  grafana:
    image: grafana/grafana:11.0.0
    port: 3000
  ingest:
    transport: http        # http | grpc (V2)
    port: 9099
    bind: 127.0.0.1        # V2: configurable for remote scenarios

detection:
  severity_patterns:
    error: ["ERROR", "FATAL", "panic", "Exception", "error"]
    warn:  ["WARN", "WARNING", "warn"]
  tier2:                   # V2
    window: 3m
    threshold: 10

containers:
  exclude: ["sidecar-*"]   # glob patterns to skip

check:
  fail_on: HAS_ERRORS      # HAS_ERRORS | FAILING
  exclude: []              # containers exempt from CI check

history_retention: 30d     # V2: state change log retention
```

Image versions are pinned defaults. User overrides a version → ErrorProbe pulls that image. `errorprobe update` bumps pinned defaults and rewrites the config for non-overridden fields.

**Port conflict:** If configured ports are already in use, ErrorProbe fails loudly with a clear message. A valid single-user scenario for running two instances simultaneously does not exist — one instance per machine is the supported model.

### 8.3 Config Hot-Reload

Config changes do not require a full stack restart. `errorprobe reload` re-reads `errorprobe.yaml`, classifies every changed field, and applies the minimum necessary disruption:

| Change class | Examples | How applied |
|---|---|---|
| **Soft** | Severity patterns, detection thresholds, container exclusions | Regenerate Vector config → SIGHUP Vector. Stack keeps running, no log loss. New settings take effect in milliseconds. |
| **Hard** | Ports, image versions, bind address | Recreate only the affected containers. Scoped disruption — not a full stack restart. |
| **Mixed** | Both in the same reload | Soft changes applied first via SIGHUP, then hard changes via container recreation. User sees a clear summary of what was soft-applied vs what required recreation. |

**File watching (automatic reload) is V2.** A background file watcher on `errorprobe.yaml` would trigger reloads mid-save on a half-edited file — a half-applied config is worse than a deliberate one. `errorprobe reload` keeps the user in control of when changes are applied. A `--watch` flag or daemon mode may be added in V2.

---

## 9. CLI Surface

Built with **Cobra**. The standard Go CLI framework (used by `kubectl`, `docker`, `helm`).

| Command | Description |
|---|---|
| `errorprobe up` | Pull images, generate configs, start stack, health-poll until live |
| `errorprobe down` | Stop and remove stack containers |
| `errorprobe update` | Pull latest pinned images, regenerate configs, restart stack |
| `errorprobe reload` | Re-read `errorprobe.yaml`, apply soft changes live, recreate containers only if hard changes detected |
| `errorprobe list` | Tabular output of all discovered containers |
| `errorprobe status` | Per-container health: `OK` / `HAS ERRORS [N]` / `FAILING` |
| `errorprobe watch` | Live-updating Bubbletea TUI (redraws in place, keyboard nav) |
| `errorprobe logs <container>` | Stream logs from Loki for a container |
| `errorprobe logs <container> --errors-only` | Stream error-level lines only |
| `errorprobe check` | Exit 0 (all OK) or 1 (fail condition met) — CI/script integration |

`errorprobe status --json` for machine-readable output.

`errorprobe check` requires the stack to already be running (starting the stack is out of scope for a check command). Exit condition is configurable via `check.fail_on` in `errorprobe.yaml`.

**`errorprobe watch` TUI** (Bubbletea): redraws in place, handles terminal resize and clean exit. The primary developer surface during a debugging session.

```
┌─────────────────────────────────────────┐
│ ErrorProbe — watching 4 containers      │
├──────────────────┬──────────┬───────────┤
│ Container        │ Status   │ Errors    │
├──────────────────┼──────────┼───────────┤
│ payments-api     │ ⚠ ERRORS │ 7 (14:32) │
│ auth-service     │ ✓ OK     │ —         │
│ gateway          │ ✓ OK     │ —         │
│ db-migrations    │ ✗ FAILING│ 42 (14:31)│
└──────────────────┴──────────┴───────────┘
  Press [e] to view errors  [q] to quit
```

---

## 10. Product Focus — What ErrorProbe Does Not Build

ErrorProbe's value is the **second health signal** — functional health — which no existing tool provides. The moment it builds a full infrastructure health UI it becomes another dashboard, and always a worse one than OpenLens or Docker Desktop which have years of investment in that space. It loses the thing that makes it worth using.

**The rule:** ErrorProbe displays infrastructure state only where it adds meaning to the functional health signal. It never builds features whose primary purpose is infrastructure monitoring.

In practice this means: the `watch` TUI surfaces infra state (restart count, probe status) as a secondary context column alongside the functional health signal — because showing both signals side-by-side *is* the product. The intent doc's core scenario is a container that is `infrastructure: healthy` and `functional: degraded` simultaneously; that comparison requires both signals visible at once.

```
│ payments-api  │ ✗ FAILING  │ infra: ✓ healthy   │ 42 errors (14:31) │
│ db-migrations │ ⚠ ERRORS   │ infra: ⚠ restarting│  7 errors (14:32) │
```

This data is already available from the Docker/K8s discovery queries ErrorProbe runs. No additional collection work is required — just surface it as context.

For infra drill-down beyond that context column, ErrorProbe deep-links to OpenLens or Docker Desktop. It complements those tools; it does not compete with them.

---

## 11. Grafana Integration

**V1:** Loki datasource auto-provisioned only. Grafana Explore view works out of the box — no dashboards built. Sufficient for the V1 success criterion.

**V2:** Pre-built dashboards. Alongside the web UI work.

ErrorProbe can deep-link into Grafana Explore for a specific container — a `errorprobe status` output includes the Grafana URL for drill-down.

**Grafana OSS (AGPL v3):** Running Grafana OSS as a separate container (the pattern used here) does not trigger AGPL copyleft. Copyleft applies only to distribution of a *modified* Grafana binary over a network. Standard usage is clean.

---

## 12. Version Roadmap Alignment

| Version | Theme | Key Additions |
|---|---|---|
| **V1** | Smoke Detector | Bootstrap engine, Docker discovery, Tier 1 detection, CLI/TUI surface |
| **V1 follow-on** | K8s | K8s API discovery (kubeconfig), pod/namespace awareness |
| **V2** | Observer | Tier 2 pattern detection, Loki time-range queries, remote Docker/K8s, gRPC transport, web UI, dashboards, history log |
| **V3** | Platform | Problem inference, multi-cluster, CI/staging integration |

---

## 13. Architecture Decision Record (Summary)

| # | Decision | Choice | Key Rationale |
|---|---|---|---|
| A | K8s in V1? | Docker-first; K8s follows in V1 | Reduces V1 scope; K8s is additive |
| B | Image version pinning | Pinned in `errorprobe.yaml`, user-overridable | Reproducibility; `update` bumps defaults |
| C | Config file | `errorprobe.yaml` | Explicit, readable, ecosystem-consistent |
| D | Ingest transport | HTTP JSON (default), gRPC V2; configurable, interface-abstracted | HTTP is simple and debuggable; gRPC for remote/OTLP future |
| E | Storage | Bind mount for configs, named volumes for data | Performance on Docker Desktop; host needs to write configs |
| F | Discovery owner | ErrorProbe-driven; Vector gets generated config + SIGHUP | Policy must drive collection, not just display |
| G | Config versioning | `version: 1` from day one | Future-proofs breaking changes |
| H | Severity inference owner | Vector (VRL) | Purpose-built; keeps health engine stateless |
| I | Health state persistence | Current snapshot always persisted; history in V2 | CI check usability; history only useful with Tier 2 |
| J | Ingest bind address | `127.0.0.1` V1; configurable + bearer auth for V2 remote | Security; remote requires network exposure |
| K | `watch` UI | Bubbletea TUI | Clean redraw, keyboard nav, terminal-safe |
| L | ErrorProbe logs | `~/.errorprobe/logs/errorprobe.log`, rotating | Debuggability of the tool itself |
| M | Port conflicts | Configurable in yaml; fail loudly on conflict | One instance per machine is the supported model |
| N | Grafana provisioning scope V1 | Datasource only | Explore view sufficient; dashboards in V2 |
| O | `check` exit contract | Requires stack running; `fail_on` and `exclude` configurable | Composability; policy lives in config |
| P | CLI framework | Cobra | Standard Go CLI; used by kubectl, docker, helm |
| Q | Windows Docker socket | Docker SDK auto-detects named pipe | First-class Windows support from day one |
| R | Severity patterns | Defined in `errorprobe.yaml`; wired into VRL at config generation | Users extend for non-standard log formats without code changes |
| S | Config hot-reload | `errorprobe reload`; soft changes via SIGHUP (no downtime), hard changes via targeted container recreation; file watcher V2 | Explicit reload keeps user in control; no mid-save partial application |
