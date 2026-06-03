# ErrorProbe

[![CI](https://github.com/Veverke/ErrorProbe/actions/workflows/ci.yml/badge.svg)](https://github.com/Veverke/ErrorProbe/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/errorprobe/errorprobe/branch/main/graph/badge.svg)](https://codecov.io/gh/errorprobe/errorprobe)
[![Release](https://img.shields.io/github/v/release/Veverke/ErrorProbe)](https://github.com/Veverke/ErrorProbe/releases/latest)
[![Go Report Card](https://goreportcard.com/badge/github.com/Veverke/ErrorProbe)](https://goreportcard.com/report/github.com/Veverke/ErrorProbe)
[![Go Version](https://img.shields.io/github/go-mod/go-version/Veverke/ErrorProbe)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Real-time error detection for Docker containers and Kubernetes pods — in a single Go binary.**

ErrorProbe watches your local containers for errors as they happen. It is a *smoke detector*, not a search engine: you run one command, and it tells you what needs your attention right now — without requiring you to open Grafana, scroll logs, or write a query.

> **Zero config. Zero dependencies beyond Docker. One command.**

---

## Install

### macOS (Homebrew)

```sh
brew tap Veverke/errorprobe https://github.com/Veverke/ErrorProbe
brew install errorprobe
```

### Windows (winget)

```powershell
winget install ErrorProbe.ErrorProbe
```

### Windows / Linux / macOS (script)

```powershell
# Windows PowerShell
irm https://raw.githubusercontent.com/Veverke/ErrorProbe/main/install.ps1 | iex
```

```sh
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/Veverke/ErrorProbe/main/install.sh | sh
```

### Self-update

```sh
errorprobe upgrade
```

Fetches the latest release from GitHub, verifies the SHA-256 checksum, and atomically replaces the running binary. On Windows the old binary is cleaned up automatically on the next run.

### GitHub Actions

```yaml
- uses: Veverke/ErrorProbe/setup-action@v1.0.0
- run: errorprobe --version
```

### Direct download

Pre-built binaries for every platform are attached to every [GitHub Release](https://github.com/Veverke/ErrorProbe/releases/latest):

| Platform | Binary |
|---|---|
| Windows amd64 | `errorprobe-windows-amd64.exe` |
| Linux amd64 | `errorprobe-linux-amd64` |
| Linux arm64 | `errorprobe-linux-arm64` |
| macOS arm64 (Apple Silicon) | `errorprobe-darwin-arm64` |

Each release also includes `checksums.txt` (SHA-256) and `checksums.txt.sig` (keyless cosign signature).

---

## Quick Start

```sh
errorprobe up
```

Pulls Vector, Loki, and Grafana (once), starts them as managed containers, discovers every running user container (Docker and Kubernetes), and begins watching their logs. The ready banner tells you what to do next:

```
ErrorProbe ready
─────────────────────────────────────────────
Watching 4 containers
Grafana:   http://localhost:3000
Loki:      http://localhost:3100
Ingest:    http://127.0.0.1:9099
─────────────────────────────────────────────
Run 'errorprobe watch' to monitor in real-time
Run 'errorprobe check' to use in CI/scripts
```

Use `Ctrl+C` to stop the reconciler. The observability stack keeps running in Docker after you exit.

---

## Commands

| Command | Description |
|---|---|
| `errorprobe up` | Start the observability stack and enter the reconciliation loop |
| `errorprobe down` | Stop and remove stack containers (log data preserved) |
| `errorprobe down --purge` | Full uninstall — removes containers, volumes, and `~/.errorprobe/` |
| `errorprobe restart` | `down` then `up` in one command |
| `errorprobe restart --purge` | Full clean restart |
| `errorprobe list` | List watched containers (Docker + Kubernetes) with image and infra status |
| `errorprobe list --details` | Show container → image → volume breakdown |
| `errorprobe list --runtime docker\|k8s` | Filter by runtime |
| `errorprobe list --json` | Machine-readable JSON output |
| `errorprobe status` | Per-container health: OK / HAS_ERRORS / FAILING, error count, last message |
| `errorprobe status --reset <name>` | Acknowledge errors on a container |
| `errorprobe status --json` | Machine-readable JSON output |
| `errorprobe watch` | Live-updating TUI dashboard |
| `errorprobe logs <container>` | Stream logs from Loki in real time |
| `errorprobe logs <container> --errors-only` | Stream only error-level lines |
| `errorprobe logs <container> --since 30m` | Tail from a time offset |
| `errorprobe check` | Exit 1 if any watched container has errors — for CI pipelines |
| `errorprobe check --json` | Structured JSON output for scripting |
| `errorprobe check --explain` | Show which rule last set state for each container |
| `errorprobe reload` | Re-read config and apply changes without a restart |
| `errorprobe upgrade` | Upgrade the binary to the latest release |
| `errorprobe version` | Print version, commit, and build date |

---

## Live TUI (`errorprobe watch`)

```sh
errorprobe watch
```

Full-screen terminal dashboard that polls health state every second. Containers from Docker and Kubernetes are grouped by runtime. A scrolling EKG waveform at the top changes colour: green (all OK), yellow (some errors), red (FAILING).

**Keyboard shortcuts:**

| Key | Action |
|---|---|
| `↑` / `↓` or `k` / `j` | Navigate |
| `e` | Expand / collapse detail panel (full error, troubleshoot commands) |
| `r` | Reset selected container's health state to OK |
| `v` | Validate a learned rule — promote from `learned` to `confirmed` |
| `f` | False-positive — reject a learned rule and suppress its pattern |
| `h` / `u` | Hide / unhide containers for this session |
| `x` | Permanently exclude container (appends to `errorprobe.yaml`) |
| `g` | Open container in Grafana Explore |
| `o` | Open ErrorProbe overview dashboard in Grafana |
| `q` / `Ctrl+C` | Quit |

The expanded detail panel (`e`) shows the full last error message, Kubernetes pod/namespace/node info, and ready-to-copy `docker`/`kubectl` troubleshoot commands.

---

## Container → Image → Volume Correlation

```sh
errorprobe list --details
```

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

For Kubernetes containers, pod name, namespace, and node are shown instead of volume information.

---

## Kubernetes Support

ErrorProbe auto-detects a local Kubernetes cluster (Docker Desktop K8s, k3s, minikube) by reading `KUBECONFIG` or `~/.kube/config`. When a cluster is reachable:

- Pods across all non-system namespaces are discovered and added to the watch set alongside Docker containers
- A Vector DaemonSet is deployed inside the cluster to collect pod logs
- `errorprobe list` groups containers by runtime with `k8s` / `docker` labels
- `errorprobe watch` groups them with section headers

No extra configuration is required. If no cluster is reachable, Kubernetes discovery is silently skipped.

---

## CI Integration

```bash
errorprobe up
# ... run your tests ...
errorprobe check   # exits 1 if any container logged errors during the run
```

`errorprobe check --json` outputs structured JSON for scripting:

```json
{
  "ok": false,
  "failing": [
    { "name": "payments-api", "state": "HAS_ERRORS", "last_error_msg": "ERROR connection refused" }
  ]
}
```

Use `check.fail_on: FAILING` in config to only fail CI when a container reaches the high-frequency error threshold (Tier 2), not on every individual error.

Exclude noisy containers from CI evaluation:

```yaml
check:
  exclude:
    - background-worker
```

---

## Configuration

`errorprobe.yaml` is **optional**. Zero-config use is fully supported.

Place the file next to the binary (project-local) or at `~/.errorprobe/config.yaml` (global). Project-local takes precedence.

```yaml
version: 1

stack:
  loki:
    port: 3100
    retention: 72h
  grafana:
    port: 3000
  ingest:
    port: 9099
    bind: 127.0.0.1

containers:
  exclude:
    - noisy-sidecar     # glob patterns supported
  include: []           # allow-list; when non-empty only matching containers are watched

k8s:
  exclude_namespaces: [kube-system, kube-public, kube-node-lease]

detection:
  severity_patterns:
    error: [ERROR, FATAL, panic, Exception, error]
    warn: [WARN, WARNING, warn]
  tier2:
    window: 3m        # Loki query window for FAILING-state evaluation
    threshold: 10     # errors in <window> to enter FAILING state
    tick: 30s

check:
  fail_on: HAS_ERRORS   # or FAILING

history_retention: 30d
```

See [docs/user-guide.md](docs/user-guide.md) for the full schema, all flags, Policy-Based Rules, and the Learning Module.

---

## Policy-Based Rules

The health engine is driven by an ordered rule list — each rule defines *when* it fires and *what state* to assign. Rules are evaluated highest-priority-first; the first match wins.

Built-in rules cover the common cases (single error → `HAS_ERRORS`, ≥ 5 errors in window → `FAILING`, K8s restart loop → `RESTARTING`). Override or extend them in `errorprobe.yaml`:

```yaml
rules:
  - name: suppress-migration-errors
    priority: 200
    match: log
    when:
      container: "eq:db-migrate"
      level: "eq:error"
    set_state: OK

  - name: payments-strict
    priority: 150
    match: log
    when:
      container: "glob:payments-*"
      count_in_window: "gte:3"
    set_state: FAILING
```

Operators: `eq`, `gt`, `gte`, `lt`, `lte`, `regex`, `glob`. Available fields: `level`, `message`, `container`, `namespace`, `runtime`, `count_in_window`, `restart_count`, `uptime`, `phase`.

Rule changes are **soft** — run `errorprobe reload` to apply without restarting anything.

---

## Learning Module

When enabled, ErrorProbe automatically learns new detection rules from observed log patterns. When a container transitions to an error state and no existing rule matches the triggering lines, the learner extracts a regex pattern and generates a candidate rule.

```yaml
learn:
  enabled: true
  auto_apply: true
  confidence_threshold: 0.75
  review_threshold: 0.50
```

Candidates meeting `confidence_threshold` are auto-applied immediately. Candidates meeting `review_threshold` go to a pending file for review via `v` / `f` in the TUI. Containers matched by a learned (unconfirmed) rule show a `⚑ ?` indicator and cyan EKG strip in `errorprobe watch`.

---

## Resetting to a Clean State

```powershell
errorprobe down --purge
```

Removes the Docker containers, network, data volumes, and the entire `~/.errorprobe/` directory (generated configs, state files, and logs) in one step. After this, `errorprobe up` starts from a completely clean slate.

---

## Requirements

- **Docker** — Docker Desktop (Windows/macOS) or Docker Engine (Linux)
- **Kubernetes** — optional; a local cluster is auto-detected when present
- No other dependencies — the binary manages everything else

---

## Keywords

docker error monitoring, container log monitoring, kubernetes error detection, local observability, docker log watcher, container health check, real-time log analysis, docker compose error detection, CI error gate, terminal dashboard, TUI, Grafana Loki Vector, Go CLI tool, developer observability, container debugging