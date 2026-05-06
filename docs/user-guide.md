# ErrorProbe User Guide

---

## Supported Commands

| Command | Description |
|---|---|
| [`up`](#cmd-up) | Start the observability stack and enter the reconciliation loop |
| [`down`](#cmd-down) | Stop and remove stack containers |
| [`restart`](#cmd-restart) | Stop then immediately start the stack |
| [`list`](#cmd-list) | List Docker and Kubernetes containers and their watch status |
| [`status`](#cmd-status) | Show health state per container (OK / HAS_ERRORS / FAILING) |
| [`watch`](#cmd-watch) | Interactive real-time terminal UI |
| [`logs <container>`](#cmd-logs) | Stream logs for a container from Loki |
| [`check`](#cmd-check) | Exit non-zero if any container exceeds the fail threshold — for CI use |
| [`reload`](#cmd-reload) | Re-read config and apply changes without a full restart |
| [`update`](#cmd-update) | *(Planned)* Pull latest pinned images and restart the stack |

> **`list` vs `check` vs `watch` at a glance**
>
> | Command | Data source | Audience | Purpose |
> |---|---|---|---|
> | `list` | Live Docker / K8s API | Human | Discovery — which containers exist and whether they match the watch policy. Nothing about health state. |
> | `check` | Persisted `health.json` snapshot | CI / scripts | Gate — reads the last known health state and exits 0 or 1. Designed to be non-interactive and scriptable. |
> | `watch` | Persisted `health.json` snapshot | Human | Live monitoring — interactive full-screen TUI that polls for health changes in real time. |
>
> `check` and `watch` both read `health.json`, but serve entirely different consumers: `check` is for automation, `watch` is for humans.

### Command Flags

<a name="cmd-up"></a>
**`up`**
*(no flags)*

<a name="cmd-down"></a>
**`down`**
- `--purge` — also delete data volumes and `~/.errorprobe/` (full uninstall)

<a name="cmd-restart"></a>
**`restart`**
- `--purge` — also remove data volumes and `~/.errorprobe/` before restarting (full clean restart)

<a name="cmd-list"></a>
**`list`**
- `--details` — show the container → image → volume breakdown
- `--json` — output as JSON
- `--runtime docker|k8s` — filter by runtime
- `--compact` — shorten pod names and drop columns where all values are identical

<a name="cmd-status"></a>
**`status`**
- `--reset <container>` — reset a container's error state to OK
- `--json` — output as JSON

<a name="cmd-watch"></a>
**`watch`**
*(no flags)*

<a name="cmd-logs"></a>
**`logs <container>`**
- `--errors-only` — stream error lines only
- `--since <duration>` — start from a time offset (default: `15m`)
- `--json` — output each line as a JSON object `{"time", "container", "line"}`

<a name="cmd-check"></a>
**`check`**
- `--json` — output result as JSON

<a name="cmd-reload"></a>
**`reload`**
*(no flags)*

<a name="cmd-update"></a>
**`update`** *(Planned)*
*(no flags)*

### Global flags

| Flag | Description |
|---|---|
| `--config <path>` | Path to config file (overrides discovery) |
| `--debug` | Enable verbose debug logging |
| `--log-format text\|json` | Log output format for the `~/.errorprobe/logs/errorprobe.log` file (default: `text`) |
| `--version` | Print version and exit |

---

## What ErrorProbe Is

ErrorProbe is a **single Go binary** that watches your local Docker and Kubernetes containers for errors in real time.

It is a *smoke detector*, not a search engine. It does not wait to be asked — it tells you when something is wrong. Existing tools (Docker Desktop, Grafana Explore) are pull-based: they present data and wait for you to investigate. ErrorProbe is push-based: it answers "what needs my attention right now?" without requiring investigation.

You install one binary. You run one command. Everything else is managed for you.

---

## Prerequisites

- **Docker** must be installed and running.
- **Kubernetes** (optional) — a local cluster (Docker Desktop K8s, k3s, or minikube) is auto-detected when present. No additional configuration is required; ErrorProbe reads `KUBECONFIG` or `~/.kube/config` automatically.
- ErrorProbe uses the Docker and Kubernetes APIs directly — no `docker compose`, no separate runtime.

---

## Getting Started

### 1. Start the stack

```
errorprobe up
```

This command:
1. Pulls the pinned Vector, Loki, and Grafana images (once; subsequent runs use the local cache)
2. Generates configuration files into `~/.errorprobe/configs/`
3. Creates the required Docker network and volumes
4. Starts all three containers
5. Health-polls until every service is live
6. Enters the **reconciliation loop** — stays running in the foreground, watching for container changes

Use `Ctrl+C` to stop. The observability stack keeps running in Docker after you exit; only the reconciler process stops.

### 2. Open Grafana

Browse to `http://localhost:3000`.

The Loki data source and an Explore view are pre-configured. Query `{container="your-container-name"}` to see logs. The `level` label (`error`, `warn`, `info`) is applied automatically.

### 3. List watched containers

```
errorprobe list
```

Shows every running user container (Docker and Kubernetes) that passes the watch policy, along with its runtime, image, status, and whether it is currently in the active watch set.

```
RUNTIME  CONTAINER        POD              NAMESPACE  IMAGE              INFRA STATUS   WATCHING
docker   payments-api                                 payments:v2        running        yes
docker   user-service                                 user-svc:latest    running        yes
k8s      api/api          api-7f4d6        default    api:v1             running        yes
```

Filter by runtime:

```
errorprobe list --runtime docker
errorprobe list --runtime k8s
```

Output as JSON:

```
errorprobe list --json
```

#### Correlating containers, images, and volumes

The `--details` flag shows the full per-container breakdown in a single view — image, status, volumes for Docker containers, and pod/namespace/node for Kubernetes containers:

```
errorprobe list --details
```

Each entry shows:
- The container name, watch status, and runtime
- The exact image it was started from
- For **Docker** containers: every mount point — named volumes (`[volume: name]`), anonymous volumes, bind mounts (`[bind]`), and tmpfs mounts — each with source path, destination inside the container, and read/write mode
- For **Kubernetes** containers: pod name, namespace, and node instead of volume information

Docker example:

```
payments-api  [watching]  runtime=docker
  image:   payments:v2
  status:  running
  volumes:
    [volume: pgdata]  /var/lib/docker/volumes/pgdata/_data → /var/lib/postgresql/data  (rw)
    [bind]            /host/certs → /etc/ssl/certs  (ro)
────────────────────────────────────────────────────
user-service  [not watching]  runtime=docker
  image:   user-svc:latest
  status:  running
  volumes: (none)
```

Kubernetes example:

```
api/api  [watching]  runtime=k8s
  image:     api:v1
  status:    running
  pod:       api-7f4d6
  namespace: default
  node:      docker-desktop
```

### 5. Check health state

```
errorprobe status
```

Prints a one-line-per-container health table: functional state (`✓ OK` / `⚠ HAS ERRORS`), infra state, error count, and last error message excerpt.

```
CONTAINER      FUNCTIONAL        INFRA    ERRORS  LAST ERROR
payments-api   ⚠ HAS ERRORS 3   running  3       14:22 ERROR connection refused…
user-service   ✓ OK              running  0       —
```

Grafana Explore deep-links are also printed per container so you can jump straight to the logs without manually constructing a LogQL query.

Reset a container's error state after acknowledging a known issue:

```
errorprobe status --reset payments-api
```

> **Note:** the reset writes directly to the health snapshot on disk. If `errorprobe up` is running, the live health engine will re-flip the container to `HAS_ERRORS` as soon as the next error event arrives (which may be immediate). Reset is most useful when the stack is not running — for example, in a CI step reading a snapshot after a test run.

Output as JSON:

```
errorprobe status --json
```

### 6. Real-time watch TUI

```
errorprobe watch
```

Opens a full-screen terminal UI that polls the health snapshot every second and renders a live-updating table of container states. When both Docker and Kubernetes are active, containers are grouped by runtime with section headers. A scrolling EKG waveform at the top of the screen changes colour to reflect overall health: green (all OK), yellow (some errors), red (FAILING).

**Keyboard shortcuts:**

| Key | Action |
|---|---|
| `↑` / `↓` or `k` / `j` | Navigate containers |
| `e` | Expand / collapse the selected container's detail panel |
| `←` / `→` | Scroll the expanded error message horizontally |
| `r` | Reset the selected container's health state to OK |
| `h` | Hide the selected container for this session |
| `u` | Unhide all session-hidden containers |
| `x` | Permanently exclude the selected container (appends to `containers.exclude` in `errorprobe.yaml`) |
| `g` | Open the selected container in Grafana Explore (system browser) |
| `o` | Open the ErrorProbe overview dashboard in Grafana (system browser) |
| `q` / `Ctrl+C` | Quit |

**Expanded detail panel** (`e`):

Pressing `e` opens a multi-line panel below the selected row showing:
- The full last error message (horizontally scrollable with `←` / `→`); for FAILING containers, the dominant repeating pattern and its repeat count
- For Kubernetes containers: pod name, namespace, and node
- A **Troubleshoot** section with ready-to-copy commands for the selected runtime:
  - Docker: `docker logs`, `docker inspect`, `ep logs --errors-only`, `docker exec`
  - Kubernetes: `kubectl logs`, `kubectl describe pod`, `ep logs --errors-only`, `kubectl exec`

### 7. Stream logs

```
errorprobe logs <container>
```

Streams log output for the named container from Loki in real time. Requires the stack to be running.

```
errorprobe logs payments-api --errors-only       # only lines containing "error"
errorprobe logs payments-api --since 30m         # last 30 minutes (default: 15m)
errorprobe logs payments-api --json              # JSONL: {"time","container","line"} per line
```

### 8. CI / script integration

```
errorprobe check
```

Checks that the stack is running, then reads the persisted health snapshot and exits with a non-zero status code if any watched container has reached the configured fail threshold. Designed to be dropped into CI pipelines and test scripts.

In non-JSON mode, `check` prints a human-readable summary of each failing container, extracting the most diagnostic part of the error message from logfmt, JSON, Erlang/OTP tuples, and Java/Python exception lines automatically.

```bash
errorprobe up
# ... run your tests ...
errorprobe check      # exits 1 if any watched container logged errors during the test run
```

Controlled by `check.fail_on` in config:

| Value | Behaviour |
|---|---|
| `HAS_ERRORS` (default) | Exit 1 if any container has state `HAS_ERRORS` or `FAILING` |
| `FAILING` | Exit 1 only if a container has state `FAILING` (V2 only — not reachable in V1) |

Exclude noisy containers from CI evaluation:

```yaml
check:
  exclude:
    - background-worker
```

Output as JSON (useful for parsing in scripts):

```
errorprobe check --json
```

```json
{
  "ok": false,
  "failing": [
    { "name": "payments-api", "state": "HAS_ERRORS", "last_error_msg": "ERROR connection refused" }
  ]
}
```

### 9. Restart the stack

```
errorprobe restart
```

Equivalent to `errorprobe down` followed immediately by `errorprobe up`. If the `down` phase encounters an error, you are prompted whether to proceed with `up` anyway.

To wipe all volumes and state before restarting (full clean restart):

```
errorprobe restart --purge
```

### 10. Apply config changes without a restart

```
errorprobe reload
```

Re-reads `errorprobe.yaml`, classifies every changed field, and applies the minimum necessary disruption:

| Change type | Examples | Action |
|---|---|---|
| **Soft** | `detection.severity_patterns`, `containers.exclude`, `check.*` | Regenerate Vector config + send SIGHUP to Vector — no container restart |
| **Hard** | Any `stack.*.image`, any port, `ingest.bind`, `ingest.transport` | Stop, remove, and recreate only the affected containers |

If nothing changed: prints `No configuration changes detected` and exits 0.

### 11. Stop the stack

```
errorprobe down
```

Stops and removes the Vector, Loki, and Grafana containers. Data volumes (`loki-data`, `grafana-data`) are preserved so log history survives restarts.

To also delete the volumes **and** the entire `~/.errorprobe/` state directory (full uninstall of all generated configs, state, and logs):

```
errorprobe down --purge
```

> **Important:** `down` automatically terminates any running `errorprobe up` process before touching Docker. You do not need to press `Ctrl+C` first — but if you do, that is harmless.

> **Note:** `errorprobe list` works independently of the stack. It queries Docker directly, so it will still show your user containers even when the stack is down. This is by design — `list` shows what *could* be watched, not what is currently being watched.

---

## Configuration

`errorprobe.yaml` is optional. Without it, built-in defaults are used and the tool is fully functional.

Place the file next to the binary (project-local) or at `~/.errorprobe/config.yaml` (global). Project-local takes precedence — different projects can have different settings.

### Config file precedence

```
./errorprobe.yaml  (highest)
~/.errorprobe/config.yaml
built-in defaults  (lowest)
```

### Full schema with defaults

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
    port: 9099
    bind: 127.0.0.1
    transport: http

detection:
  severity_patterns:
    error: [ERROR, FATAL, panic, Exception, error]
    warn: [WARN, WARNING, warn]
  tier2:
    window: 3m        # Loki query window for error-rate evaluation
    threshold: 10     # errors in <window> required to enter FAILING state
    tick: 30s         # how often the Tier 2 evaluator runs

containers:
  exclude: []         # glob patterns; see Excluding containers below
  include: []         # allow-list; when non-empty only matching containers are watched
  display_name_patterns: []  # regex list; leave empty to use built-in defaults

k8s:
  exclude_namespaces: [kube-system, kube-public, kube-node-lease]

history_retention: 30d   # how long state-transition records are kept in history.jsonl
```

### Excluding containers

Add container names to `containers.exclude` to prevent them from being watched. Glob patterns are supported.

```yaml
containers:
  exclude:
    - my-debug-sidecar
    - temp-*
```

For Kubernetes containers, two additional pattern prefixes are available:

```yaml
containers:
  exclude:
    - "sidecar-*"           # name match (Docker + K8s)
    - "namespace/kube-*"    # K8s namespace match
    - "pod/debug-*"         # K8s pod name match
```

| Pattern prefix | Matches against |
|---|---|
| *(none)* | `ContainerMeta.Name` — applies to both Docker and K8s |
| `namespace/<glob>` | `ContainerMeta.Namespace` — K8s only |
| `pod/<glob>` | `ContainerMeta.Pod` — K8s only |

To override the default excluded Kubernetes system namespaces:

```yaml
k8s:
  exclude_namespaces: [kube-system, kube-public, kube-node-lease, my-infra]
```

Excluded containers are removed from the watch policy entirely: they do not appear in `errorprobe list` and are not included in the Vector log collection config. The exclusion takes effect on the next reconciler tick (within 5 seconds of saving the file, after a reload).

> **Quick exclude from the TUI:** press `x` on any selected container in `errorprobe watch` to append it to `containers.exclude` in your config file immediately. The change takes effect on the next `errorprobe up` run.

### Allowing only specific containers (include list)

`containers.include`, when non-empty, acts as an **allow-list** applied *after* the exclude pass. Only containers matching at least one pattern survive. This enables an "infra-only" mode:

```yaml
# Watch only Kubernetes infrastructure pods:
containers:
  include:
    - namespace/kube-system
```

The same `pod/<glob>`, `namespace/<glob>`, and bare name glob syntax as `exclude` is supported.

### Display name normalisation

Kubernetes appends random suffixes to container names (e.g. `payments-api-7d9f6b8c4-vx8fw`). ErrorProbe strips these in the `list` and `watch` views by matching names against `containers.display_name_patterns`. The field accepts a list of regular expressions, each with exactly one capture group; group 1 becomes the display name. Patterns are evaluated in order; the first match wins.

Built-in default patterns:

```yaml
# Strips the trailing 5-char random pod suffix
# e.g. payments-api-7d9f6b8c4-vx8fw → payments-api-7d9f6b8c4
^(.*)-[a-z0-9]{5}$
```

Override with your own patterns (the built-in list is replaced entirely):

```yaml
containers:
  display_name_patterns:
    - '^(.*)-[a-z0-9]{5,10}-[a-z0-9]{5}$'  # K8s Deployment: strip hash + pod suffix
    - '^(.*)-[a-z0-9]{5}$'                   # K8s StatefulSet / Job: strip pod suffix
```

> **Note:** display names are cosmetic only. All internal tracking (health keys, Loki labels, Grafana Explore links) still uses the original container name.

---

## Key Concepts

### The observability stack

ErrorProbe manages three containers on your behalf:

| Container | Role |
|---|---|
| **Vector** | Collects logs from all watched Docker containers via the Docker daemon. When a Kubernetes cluster is detected, a Vector DaemonSet is also deployed inside the cluster to collect pod logs. Applies VRL transforms to normalise timestamps, infer severity, and attach labels. Forwards to Loki. |
| **Loki** | Stores and indexes the labelled log streams. Queried by Grafana and by ErrorProbe's Tier 2 health engine. |
| **Grafana** | Visualisation layer. Pre-configured with Loki as a data source and two built-in dashboards (overview and per-container detail). Use Explore to search and drill into logs. |

You never write a Vector TOML, a Loki YAML, or a Grafana datasource config. ErrorProbe generates all of these and manages them as a unit.

### Discovery

ErrorProbe uses the **Docker API** to enumerate running containers every ~5 seconds. It also queries the **Kubernetes API** (if a local cluster is detected) to discover running pods across all non-system namespaces. It excludes its own managed containers (identified by the `managed-by: errorprobe` label) and any containers you have excluded in config. The results from both runtimes are merged and sorted — the combined result is the *approved container set*.

Kubernetes discovery is automatic: ErrorProbe reads `KUBECONFIG` or `~/.kube/config` and calls `IsAvailable` before attempting to list pods. If no cluster is reachable, Kubernetes discovery is silently skipped and only Docker containers are watched.

### Reconciliation

The reconciler is the loop that runs inside `errorprobe up`. On every tick it:

1. Queries the Docker API for running containers
2. If a Kubernetes cluster is available, queries the Kubernetes API for running pods
3. Merges the two container lists (sorted by runtime, then name)
4. Applies the watch policy (exclusions, label filters, namespace exclusions)
5. Compares the result to the previously persisted watch set
6. If anything changed: regenerates `vector.toml`, saves the new watch set to `~/.errorprobe/state/containers.json`, then sends a reload signal (SIGHUP) to Vector if Vector is running

This means Vector's configuration is always in sync with what is actually running — containers started or stopped after `errorprobe up` are picked up automatically, without a restart.

### Watch set

The **watch set** is the persisted snapshot of the approved containers at the last successful reconciler tick. It is stored at `~/.errorprobe/state/containers.json`. The `WATCHING` column in `errorprobe list` reflects this snapshot: `yes` means the container is in the current watch set; `no` means it passed policy but the reconciler has not yet ticked since it appeared.

### Severity patterns

Vector classifies each log line as `error`, `warn`, or `info` by matching the raw message against the pattern lists in `detection.severity_patterns`. The patterns are regular expressions. The `level` label attached to every log line in Loki comes from this classification — it is not parsed from the log format itself, so it works regardless of whether your app emits structured JSON or plain text.

Custom patterns for non-standard log formats (e.g. Java `[SEVERE]`):

```yaml
detection:
  severity_patterns:
    error: [ERROR, FATAL, panic, Exception, error, SEVERE]
    warn: [WARN, WARNING, warn]
```

### Tier 2 detection (FAILING state)

ErrorProbe uses a two-tier health model:

| Tier | Trigger | State |
|---|---|---|
| **Tier 1** | Any single error-level log line arrives via the ingest endpoint | `HAS_ERRORS` |
| **Tier 2** | ≥ `detection.tier2.threshold` errors in the last `detection.tier2.window` (evaluated every `detection.tier2.tick`) | `FAILING` |

Tier 2 queries Loki directly, so it can only run while the stack is up. A container in `FAILING` state stays there until it is explicitly reset (via `errorprobe status --reset`, the `r` key in `watch`, or by clearing the health snapshot).

Default thresholds: 10 errors in a 3-minute window, evaluated every 30 seconds. These can be tuned in `errorprobe.yaml`:

```yaml
detection:
  tier2:
    window: 5m
    threshold: 20
    tick: 60s
```

The `check.fail_on: FAILING` mode only exits 1 when a container reaches the Tier 2 `FAILING` state, not just `HAS_ERRORS`.

### Multi-line error continuation

Many runtimes write multi-line errors where the header line ends with a colon and the actual error detail follows on the next line at a different severity level. ErrorProbe detects this pattern and appends the follow-on line to the stored error message automatically. Examples handled:

- Erlang/OTP: `Error in process … with exit value:` → `{database_does_not_exist,[…]}`
- Python: `Traceback (most recent call last):` → `ValueError: invalid input`
- Java: `Exception in thread "main":` → `java.lang.NullPointerException: …`

### State transition history

Every time a container changes health state (OK → HAS_ERRORS, HAS_ERRORS → FAILING, etc.), the transition is appended to `~/.errorprobe/state/history.jsonl`. Each line is a JSON object:

```json
{"container":"payments-api","from":"OK","to":"HAS_ERRORS","at":"2026-05-01T14:22:00Z","reason":"tier1"}
```

Old entries are pruned on startup according to `history_retention` (default: `30d`). This can be adjusted in config:

```yaml
history_retention: 7d
```

### TUI (Terminal User Interface)

A TUI is an interactive, text-based UI that runs inside the terminal — like `htop`, `vim`, or `lazygit`. Rather than scrolling static output, it renders a live-updating screen with panels, colours, and keyboard navigation within a normal terminal window.

ErrorProbe's `watch` command is the TUI entry point: it displays a live dashboard of container health states and recent errors without requiring Grafana. See the [Real-time watch TUI](#6-real-time-watch-tui) section for the full keyboard reference and expanded panel description.

### Generated configs

All generated files live in `~/.errorprobe/configs/`. They are overwritten on every `up` or reconciler reload. Do not edit them by hand — changes will be lost.

| File | Description |
|---|---|
| `vector.toml` | Vector source, transforms, and sinks — generated from your config + current container set |
| `loki.yaml` | Loki retention and storage settings |
| `grafana-datasource.yaml` | Grafana provisioning file that registers Loki as a data source |

## Resetting to a Clean State

To fully remove all ErrorProbe artifacts from a machine, run a single command:

```
errorprobe down --purge
```

This removes the Docker containers, network, data volumes, and the entire `~/.errorprobe/` directory (generated configs, state files, and logs) in one step. After this, `errorprobe up` starts from a completely clean slate — as if the tool was just installed.

---

## Data Directories

| Path | Contents |
|---|---|
| `~/.errorprobe/configs/` | Generated tool configs (`vector.toml`, `loki.yaml`, `grafana-datasource.yaml`) |
| `~/.errorprobe/state/containers.json` | Persisted watch set — the reconciler's last known container list |
| `~/.errorprobe/state/health.json` | Health snapshot — current functional state of every watched container |
| `~/.errorprobe/state/history.jsonl` | State-transition log — one JSON record per health state change |
| `~/.errorprobe/state/ep.pid` | PID file written by `errorprobe up`; used by `errorprobe down` to terminate it |
| `~/.errorprobe/logs/errorprobe.log` | ErrorProbe's own structured log (rotated; max 10 × 5 MB) |

Docker volumes `loki-data` and `grafana-data` hold persisted log data and Grafana state respectively. They survive `errorprobe down` unless `--purge` is used.

`errorprobe down --purge` removes both the Docker volumes **and** the entire `~/.errorprobe/` directory, leaving the machine in a state identical to a fresh install.