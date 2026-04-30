# ErrorProbe

A single Go binary that monitors your local Docker containers for errors in real time.

No configuration required. Run one command; get answers in seconds.

---

## Quick Start

```
errorprobe up
```

Pulls Vector, Loki, and Grafana (once), starts them as managed containers, discovers every running user container, and begins watching their logs. The ready banner tells you what to do next:

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

---

## Commands

| Command | Description |
|---|---|
| `errorprobe up` | Start the stack and enter the reconciliation loop |
| `errorprobe down` | Stop and remove stack containers (preserves log data) |
| `errorprobe down --purge` | Stop, remove, and delete all log volumes |
| `errorprobe list` | List watched containers with image and infra status |
| `errorprobe list --details` | Show the container → image → volume breakdown |
| `errorprobe status` | Per-container health: OK / HAS ERRORS, error count, last message |
| `errorprobe status --reset <name>` | Acknowledge errors on a container |
| `errorprobe watch` | Live-updating TUI dashboard (`[g]` opens Grafana Explore) |
| `errorprobe logs <container>` | Stream logs from Loki in real time |
| `errorprobe logs <container> --errors-only` | Stream only error-level lines |
| `errorprobe check` | Exit 1 if any watched container has errors — for CI pipelines |
| `errorprobe check --json` | Same, with machine-readable JSON output |
| `errorprobe reload` | Re-read config and apply changes (soft: SIGHUP; hard: recreate) |

---

## Container → Image → Volume Correlation

One of the hardest things to see at a glance in plain Docker tooling is which container came from which image and what volumes or bind-mounts it is using. `errorprobe list --details` makes this explicit:

```
errorprobe list --details
```

```
payments-api  [watching]
  image:   payments:v2
  status:  running
  volumes:
    [volume: pgdata]  /var/lib/docker/volumes/pgdata/_data → /var/lib/postgresql/data  (rw)
    [bind]            /host/certs → /etc/ssl/certs  (ro)
────────────────────────────────────────────────────
user-service  [watching]
  image:   user-svc:latest
  status:  running
  volumes: (none)
```

Named volumes, anonymous volumes, bind mounts, and tmpfs mounts are all shown with their source path, destination inside the container, and read/write mode.

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

---

## Configuration

`errorprobe.yaml` is **optional**. Zero-config use is fully supported.

Place the file next to the binary (project-local) or at `~/.errorprobe/config.yaml` (global). Key settings:

```yaml
version: 1

containers:
  exclude:
    - noisy-sidecar     # glob patterns supported

check:
  fail_on: HAS_ERRORS   # or FAILING (V2)
  exclude:
    - background-worker

detection:
  severity_patterns:
    error: [ERROR, FATAL, panic, SEVERE]
    warn: [WARN, WARNING]
```

See [docs/user-guide.md](docs/user-guide.md) for the full schema and all options.

---

## Resetting to a Clean State

```powershell
errorprobe down --purge
Remove-Item -Recurse -Force "$env:USERPROFILE\.errorprobe"   # Windows
# rm -rf ~/.errorprobe                                        # macOS / Linux
```

---

## Requirements

- Docker (Docker Desktop on Windows/macOS, or Docker Engine on Linux)
- No other dependencies — the binary manages everything else
