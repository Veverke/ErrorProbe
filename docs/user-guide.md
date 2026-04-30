# ErrorProbe User Guide

---

## What ErrorProbe Is

ErrorProbe is a **single Go binary** that watches your local Docker containers for errors in real time.

It is a *smoke detector*, not a search engine. It does not wait to be asked — it tells you when something is wrong. Existing tools (Docker Desktop, Grafana Explore) are pull-based: they present data and wait for you to investigate. ErrorProbe is push-based: it answers "what needs my attention right now?" without requiring investigation.

You install one binary. You run one command. Everything else is managed for you.

---

## Prerequisites

- **Docker** must be installed and running. That is the only requirement.
- ErrorProbe uses the Docker API directly — no `docker compose`, no separate runtime.

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

Shows every running user container that passes the watch policy, along with its image, Docker status, and whether it is currently in the active watch set.

```
CONTAINER        IMAGE              INFRA STATUS   WATCHING
payments-api     payments:v2        running        yes
user-service     user-svc:latest    running        yes
```

Output as JSON:

```
errorprobe list --json
```

### 4. Stop the stack

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

## Commands Reference

| Command | Status | Description |
|---|---|---|
| `errorprobe up` | Implemented | Start the observability stack and enter the reconciliation loop |
| `errorprobe down` | Implemented | Stop and remove stack containers |
| `errorprobe list` | Implemented | List containers and their watch status |
| `errorprobe status` | Implemented | Show health state per container (OK / HAS_ERRORS / FAILING) |
| `errorprobe watch` | Implemented | Interactive real-time terminal UI |
| `errorprobe logs <container>` | Planned | Stream logs for a container from Loki |
| `errorprobe check` | Planned | Exit non-zero if any container exceeds the fail threshold — for CI use |
| `errorprobe update` | Planned | Pull latest pinned images and restart the stack |

### Global flags

| Flag | Description |
|---|---|
| `--config <path>` | Path to config file (overrides discovery) |
| `--debug` | Enable verbose debug logging |
| `--version` | Print version and exit |

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
```

### Excluding containers

Add container names to `containers.exclude` to prevent them from being watched. Glob patterns are supported.

```yaml
containers:
  exclude:
    - my-debug-sidecar
    - temp-*
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

ErrorProbe uses the **Docker API** to enumerate running containers every ~5 seconds. It excludes its own managed containers (identified by the `managed-by: errorprobe` label) and any containers you have excluded in config. The result is the *approved container set* — the set of containers that should be watched.

### Reconciliation

The reconciler is the loop that runs inside `errorprobe up`. On every tick it:

1. Queries the Docker API for running containers
2. Applies the watch policy (exclusions, label filters)
3. Compares the result to the previously persisted watch set
4. If anything changed: regenerates `vector.toml`, saves the new watch set to `~/.errorprobe/state/containers.json`, then sends a reload signal (SIGHUP) to Vector if Vector is running

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

---

## Data Directories

| Path | Contents |
|---|---|
| `~/.errorprobe/configs/` | Generated tool configs (vector.toml, loki.yaml, grafana-datasource.yaml) |
| `~/.errorprobe/state/` | Reconciler state (containers.json) |
| `~/.errorprobe/logs/` | ErrorProbe's own log file (errorprobe.log) |

Docker volumes `loki-data` and `grafana-data` hold persisted log data and Grafana state respectively. They survive `errorprobe down` unless `--purge` is used.