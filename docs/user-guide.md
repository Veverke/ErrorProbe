# ErrorProbe User Guide

---

## Supported Commands

| Command | Description |
|---|---|
| [`up`](#cmd-up) | Start the observability stack and enter the reconciliation loop |
| [`down`](#cmd-down) | Stop and remove stack containers |
| [`list`](#cmd-list) | List Docker and Kubernetes containers and their watch status |
| [`status`](#cmd-status) | Show health state per container (OK / HAS_ERRORS / FAILING) |
| [`watch`](#cmd-watch) | Interactive real-time terminal UI |
| [`logs <container>`](#cmd-logs) | Stream logs for a container from Loki |
| [`check`](#cmd-check) | Exit non-zero if any container exceeds the fail threshold — for CI use |
| [`reload`](#cmd-reload) | Re-read config and apply changes without a full restart |
| [`update`](#cmd-update) | *(Planned)* Pull latest pinned images and restart the stack |

### Command Flags

<a name="cmd-up"></a>
**`up`**
*(no flags)*

<a name="cmd-down"></a>
**`down`**
- `--purge` — also delete data volumes (wipes all stored logs and dashboards)

<a name="cmd-list"></a>
**`list`**
- `--details` — show the container → image → volume breakdown
- `--json` — output as JSON
- `--runtime docker|k8s` — filter by runtime

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

Opens a full-screen terminal UI that polls the health snapshot every second and renders a live-updating table of container states. When both Docker and Kubernetes are active, containers are grouped by runtime with section headers.

**Keyboard shortcuts:**

| Key | Action |
|---|---|
| `↑` / `↓` | Navigate containers |
| `e` | Expand / collapse the last error message for the selected container |
| `r` | Reset the selected container's health state to OK |
| `g` | Open the selected container in Grafana Explore (in the system browser) |
| `q` / `Ctrl+C` | Quit |

### 7. Stream logs

```
errorprobe logs <container>
```

Streams log output for the named container from Loki in real time. Requires the stack to be running.

```
errorprobe logs payments-api --errors-only       # only lines containing "error"
errorprobe logs payments-api --since 30m         # last 30 minutes (default: 15m)
```

### 8. CI / script integration

```
errorprobe check
```

Reads the persisted health snapshot and exits with a non-zero status code if any watched container has reached the configured fail threshold. Designed to be dropped into CI pipelines and test scripts.

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

### 9. Apply config changes without a restart

```
errorprobe reload
```

Re-reads `errorprobe.yaml`, classifies every changed field, and applies the minimum necessary disruption:

| Change type | Examples | Action |
|---|---|---|
| **Soft** | `detection.severity_patterns`, `containers.exclude`, `check.*` | Regenerate Vector config + send SIGHUP to Vector — no container restart |
| **Hard** | Any `stack.*.image`, any port, `ingest.bind`, `ingest.transport` | Stop, remove, and recreate only the affected containers |

If nothing changed: prints `No configuration changes detected` and exits 0.

### 10. Stop the stack

```
errorprobe down
```

Stops and removes the Vector, Loki, and Grafana containers. Data volumes (`loki-data`, `grafana-data`) are preserved so log history survives restarts.

To also delete the volumes (wipe all stored data):

```
errorprobe down --purge
```

> **Important:** always press `Ctrl+C` in the `errorprobe up` session *before* running `down`. `down` only removes the stack containers — it does not signal the running `up` process to exit. If `up` is left alive after `down`, the reconciler will keep ticking and log SIGHUP errors on every tick until the process is stopped manually.

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

containers:
  exclude: []

k8s:
  exclude_namespaces: [kube-system, kube-public, kube-node-lease]
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

---

## Key Concepts

### The observability stack

ErrorProbe manages three containers on your behalf:

| Container | Role |
|---|---|
| **Vector** | Collects logs from all watched Docker containers via the Docker daemon. Applies VRL transforms to normalise timestamps, infer severity, and attach labels. Forwards to Loki. |
| **Loki** | Stores and indexes the labelled log streams. Queried by Grafana and (in future) by ErrorProbe's health engine. |
| **Grafana** | Visualisation layer. Pre-configured with Loki as a data source. Use Explore to search and drill into logs. |

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

### TUI (Terminal User Interface)

A TUI is an interactive, text-based UI that runs inside the terminal — like `htop`, `vim`, or `lazygit`. Rather than scrolling static output, it renders a live-updating screen with panels, colours, and keyboard navigation within a normal terminal window.

ErrorProbe's `watch` command is the TUI entry point: it displays a live dashboard of container health states and recent errors without requiring Grafana.

### Generated configs

All generated files live in `~/.errorprobe/configs/`. They are overwritten on every `up` or reconciler reload. Do not edit them by hand — changes will be lost.

| File | Description |
|---|---|
| `vector.toml` | Vector source, transforms, and sinks — generated from your config + current container set |
| `loki.yaml` | Loki retention and storage settings |
| `grafana-datasource.yaml` | Grafana provisioning file that registers Loki as a data source |

## Resetting to a Clean State

To fully remove all ErrorProbe artifacts from a machine:

```powershell
# 1. Remove Docker containers, network, and volumes
errorprobe down --purge

# 2. Remove all generated configs, state files, and logs
Remove-Item -Recurse -Force "$env:USERPROFILE\.errorprobe"   # Windows
rm -rf ~/.errorprobe                                          # macOS / Linux
```

After this, `errorprobe up` starts from a completely clean slate — as if the tool was just installed.

---

## Data Directories

| Path | Contents |
|---|---|
| `~/.errorprobe/configs/` | Generated tool configs (vector.toml, loki.yaml, grafana-datasource.yaml) |
| `~/.errorprobe/state/` | Reconciler state (containers.json), health snapshot (health.json), saved config (config.json) |
| `~/.errorprobe/logs/` | ErrorProbe's own log file (errorprobe.log) |

Docker volumes `loki-data` and `grafana-data` hold persisted log data and Grafana state respectively. They survive `errorprobe down` unless `--purge` is used.