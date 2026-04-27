# Intent — Distributed Systems Error Surface Tool

---

## 1. The Problem (The North Star)

**Docker's and Kubernetes' definition of "healthy" is process-liveness, not functional correctness.**

A container can be:
- Running ✅
- Passing health checks ✅
- Showing green in every dashboard ✅

...while simultaneously emitting a continuous stream of errors in its logs that are silently breaking everything downstream.

### The concrete use case

A developer deploys N containers locally. After running the build script, all containers are up — Kubernetes pods running, Docker Desktop shows everything green. Yet integration (BDD) tests consistently fail.

After hours of investigation, the cause turns out to be a single container repeatedly logging errors — while remaining infrastructure-healthy the entire time. The error never surfaced. No tool raised a flag.

**This is the main use case. The product exists to decisively solve it.**

---

## 2. The Gap in Every Existing Tool

The same problem was reproduced across every tool evaluated:

| Tool | Discovers Containers | Shows Logs | Surfaces Errors Proactively |
|---|---|---|---|
| Docker Desktop | ✅ | ✅ | ❌ |
| OpenLens | ✅✅ (K8s-deep) | ✅✅ | ❌ |
| Portainer | ✅ | ✅ | ❌ |
| Grafana + Loki | ✅ | ✅✅ | ❌ (alerts need manual setup) |
| **This tool** | ✅ | ✅ | **✅ — this is the whole point** |

### Why existing tools don't solve it

All existing tools are **pull-based**: they present data and wait for the developer to investigate. They are intentionally generic, non-opinionated, and designed for infrastructure teams.

> **OpenLens answers: "What is happening in my cluster?"**
> **This tool answers: "What is broken and should get my attention right now?"**

OpenLens is a microscope. This tool is a triage nurse.

---

## 3. Two Health Signals, Not One

Every existing tool tracks only infrastructure health. This tool introduces the second signal:

| Signal | Source | What it detects |
|---|---|---|
| **Infrastructure health** | K8s/Docker probes | Is the process alive? Is the port responding? |
| **Functional health** | This tool's Semantic Health Engine | Is the container actually working correctly? |

A container can be `infrastructure: healthy` and `functional: degraded` simultaneously. No existing tool tracks both.

---

## 4. Two Detection Tiers

### Tier 1 — Visibility
**Trigger:** Any single error log entry.
**Signal:** "FYI: `payments-api` just logged an error."
**Purpose:** Nothing hidden, ever. Total transparency.

### Tier 2 — Problem Confirmed
**Trigger:** Repeating pattern, sustained error rate.
**Signal:** "ALERT: `payments-api` is failing — 47 errors in 3 minutes, same stack trace."
**Purpose:** Confident escalation. This is definitively broken.

Both tiers are kept. Neither replaces the other. Tier 1 serves transparency; Tier 2 serves confidence.

---

## 5. What This Tool Is (Precisely)

> **An opinionated local observability bootstrapper for containerized development — targeting both Docker and Kubernetes runtimes.**

It is **not**:
- A new logging engine
- A competitor to Loki, Elasticsearch, or Grafana
- A Kubernetes observability platform
- A semantic AI reasoning engine (at least not yet)

It **is**:
- An orchestrator that installs and wires together best-of-breed OSS tools
- An opinionated config generator (zero manual wiring)
- A lifecycle manager for the underlying tools
- A semantic detection layer that produces a functional health signal
- A developer-first UX surface that answers "what needs my attention?"

The mental model: a **smoke detector**, not a search engine. You don't ask it "is there a fire?" — it tells you.

---

## 6. Architecture

```
V3  ─── Problem Inference Engine
V2  ─── Pattern Detection + Remote + Web UI
V1  ─── Error/Warning Surfacing
────────────────────────────────────────────  ← this product's logic
FOUNDATION  ─── Orchestration Layer (tool wiring)
────────────────────────────────────────────
        Docker API / Kubernetes API  (discovery)
        Promtail / Vector    (log collection)
        Loki                 (log storage)
        Grafana / custom UI  (presentation)
```

### What this product writes:
- The **installer/bootstrapper** that acquires the underlying tools
- The **config generator** that wires them together correctly
- The **lifecycle manager** that starts/stops/monitors them
- The **Semantic Health Engine** that reads log output and produces the functional health signal
- The **UX surface** that presents the result to the developer

### What this product delegates:
- Log parsing and streaming → Promtail / Vector / Fluent Bit
- Log storage and indexing → Loki (default), Elasticsearch (optional)
- Container discovery → Docker API / Kubernetes API
- Log visualization → Grafana or a minimal custom UI

> The codebase is **glue + opinion + UX + detection logic**. The heavy lifting is fully delegated to proven OSS tools.

---

## 7. Product Positioning vs Existing Tools

| Tool | Role |
|---|---|
| `docker logs` | Raw access |
| OpenLens / Grafana | Visual access — *"here is everything"* |
| **This tool** | Interpretive access — *"here is what matters"* |

This tool is the **semantic front door** before the developer reaches for OpenLens or Grafana. It complements those tools — it does not replace them. It can deep-link into them for drill-down investigation.

---

## 8. Target Environment & Strategy

The platform targets **any containerized runtime**. Docker (without orchestration) and Kubernetes are both first-class targets — they differ only in the discovery mechanism, not in the goal.

| Runtime | Discovery mechanism | Roadmap |
|---|---|---|
| Local Docker (no K8s) | Docker socket / API | V1 |
| Local Kubernetes (Docker Desktop, k3s, minikube) | Kubernetes API | V1 / early V2 |
| Remote Docker host | Docker TCP / URI | V2 |
| Remote Kubernetes cluster | kubeconfig / K8s API | V2 |

Local Kubernetes is not materially harder than local Docker — both are reachable via localhost APIs. The original pain case (containers in K8s pods failing silently while showing infrastructure-healthy) is a Kubernetes scenario, which confirms K8s is not a later addition but a core target from day one.

### Why local-first is the right strategy

The pain is sharpest in local dev: no central logging, no dashboards, no alerting, fragmented logs across terminal tabs and pod boundaries. Staging and production already have observability stacks — but even those don't answer "what is broken right now?"

The progression mirrors how Docker Desktop, Compose, and Skaffold all succeeded:
1. Win local dev (Docker + local K8s)
2. Become trusted
3. Extend into CI/staging
4. Integrate with (not compete with) production systems

---

## 9. Version Roadmap

### V1 — The Smoke Detector
*Answers: "Is anything wrong?"*

- Connect to local Docker socket, zero config
- Discover all running containers automatically
- Stream logs from all containers continuously
- **Tier 1 detection:** surface any error/warning immediately
- CLI output: one line per container — `OK` / `HAS ERRORS` / `FAILING`
- JSON log output for error entries
- One command: `tool up`

**Success criterion:** Developer runs one command, within seconds knows which container is broken and sees the first error. Hours of debugging → 5 seconds.

---

### V2 — The Observer
*Answers: "Is this definitely broken, or just noisy?"*

- **Tier 2 detection:** pattern recognition (sustained error rate, looping failures, error spike after restart)
- Distinguishes `HAS ERRORS` (isolated, may be noise) from `FAILING` (confirmed problem)
- Remote Docker / K8s hosts via host URI
- Time-range filtering (last hour, 24h, custom)
- Short-term log buffer (last N minutes — not a full database)
- Minimal local web UI — the "radar screen"
- Log normalization: JSON output regardless of input format
- Severity inference for plain-text logs (regex heuristics)

---

### V3 — The Platform
*Answers: "Why is it broken, and what does it affect?"*

- Full **Problem Inference Engine**
- Cross-container correlation (cascading failures)
- Contextual signals: "errors started immediately after this deploy"
- Configurable detection rules and suppression windows
- Multi-host topologies
- Pluggable backends: forward normalized logs to Loki / Elasticsearch
- Notification integrations (Slack, webhook)
- Embeddable: sidecar or K8s operator mode

---

## 10. The Single Most Important Constraint

> **This tool must stay opinionated, outcome-driven, and intentionally narrow.**

It fails only if scope creeps — if it tries to be generic, to handle every use case, or to replace the storage and collection engines it delegates to.

The industry does not need another logging system. It needs someone to glue the best systems together around what developers actually care about: **knowing immediately when something in their running application is wrong**.
