# ErrorProbe — Honest Competitive Comparison

> **Basis for ErrorProbe's capabilities**: docs/Intent.md, docs/architecture.md, docs/user-guide.md (current codebase).
> **Basis for competitor capabilities**: official documentation, GitHub repos, and product pages as of May 2026.
> Rating key: ✅ native / zero-config · ⚠️ partial or requires manual setup · ❌ not available

---

## Competitor Set

| # | Tool | Category | Approach |
|---|---|---|---|
| 1 | Docker Desktop | Container management | Native GUI (GUI app) |
| 2 | Lazydocker | Container management | TUI (Go binary) |
| 3 | Dozzle | Log viewer | Web UI (runs as container) |
| 4 | Portainer | Container management | Web UI (runs as container) |
| 5 | k9s | Kubernetes management | TUI (Go binary) |
| 6 | OpenLens (Lens) | Kubernetes IDE | Electron GUI |
| 7 | Stern | K8s log tailing | CLI (Go binary) |
| 8 | kubetail | K8s log tailing | CLI script (bash/Go) |
| 9 | Grafana + Loki (manual) | Log observability | Web UI (multi-component) |
| 10 | ELK Stack | Log management | Web UI (multi-component) |
| 11 | Prometheus + Alertmanager | Metrics monitoring | Web UI (multi-component) |
| 12 | Datadog | Commercial observability | SaaS + agent |
| 13 | Sentry | Error tracking | SaaS + SDK |
| 14 | New Relic | Commercial observability | SaaS + agent |
| 15 | ctop | Container metrics | TUI (Go binary) |
| 16 | SigNoz | OSS APM | Web UI (multi-component) |
| 17 | Splunk | Enterprise log analysis | Web UI (multi-component) |
| 18 | Coroot | Container observability | Web UI + eBPF agent |
| 19 | Vector (standalone) | Log pipeline agent | CLI / daemon |
| 20 | docker logs / docker stats | Native Docker CLI | CLI (built into Docker) |

---

## Table 1 — ErrorProbe's Core Differentiators

Features where ErrorProbe has a meaningful advantage or unique capability.

| Feature | EP | DD | LZD | DOZ | PRT | K9S | OL | STN | G+L | ELK | PRO | DAT | SEN | NR | CTP | SNZ | SPL | COR | VEC | CLI |
|---|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| **Proactively surfaces container log errors (push-based)** | ✅ | ❌ | ❌ | ⚠️¹ | ❌ | ❌ | ❌ | ❌ | ⚠️² | ⚠️² | ❌ | ⚠️³ | ❌⁴ | ⚠️³ | ❌ | ⚠️² | ⚠️² | ⚠️⁵ | ❌ | ❌ |
| **Per-container functional health state (OK / HAS_ERRORS)** | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| **Distinguishes functional health from infra liveness** | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| **Zero-config: auto-bootstraps full observability stack** | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| **Single binary, one command starts entire stack** | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| **No app code changes required (reads stdout/stderr)** | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️⁶ | ❌⁷ | ⚠️⁶ | ✅ | ⚠️⁶ | ⚠️⁶ | ✅ | ✅ | ✅ |
| **Docker container support** | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ | ❌ | ❌ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| **Kubernetes support** | ✅ | ⚠️⁸ | ❌ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ | ✅ | ✅ | ✅ | ✅ | ❌ |
| **Unified Docker + K8s in a single view** | ✅ | ❌ | ❌ | ⚠️ | ✅ | ❌ | ❌ | ❌ | ⚠️⁹ | ⚠️⁹ | ⚠️⁹ | ✅ | ✅ | ✅ | ❌ | ⚠️⁹ | ✅ | ✅ | N/A | ❌ |
| **CI/CD gate: exits non-zero if containers are failing** | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| **Auto-discovers containers (no manual config list)** | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️¹⁰ | ⚠️ | ⚠️ | ⚠️ | ✅ | N/A | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ |
| **Reconciles container set (handles new/removed containers)** | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | ⚠️ | ⚠️ | ✅ | N/A | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | ❌ |
| **Config hot-reload without restarting the stack** | ✅ | N/A | N/A | N/A | N/A | N/A | N/A | N/A | ❌ | ❌ | ❌ | N/A | N/A | N/A | N/A | N/A | N/A | N/A | ✅¹¹ | N/A |
| **Pre-built Grafana dashboards (auto-provisioned, no config)** | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ✅ | ❌ | ✅ | ❌ | ✅ | ✅ | ✅ | ❌ | ❌ |
| **Persistent log storage (queryable history, included)** | ✅ | ❌ | ❌ | ⚠️¹² | ❌ | ❌ | ❌ | ❌ | ✅ | ✅ | ❌ | ✅ | N/A | ✅ | ❌ | ✅ | ✅ | ✅ | ❌ | ❌ |
| **Deep-links into Grafana per container from TUI** | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| **Full-screen live TUI (keyboard-navigable)** | ✅ | ❌ | ✅ | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |

---

## Table 2 — What Competitors Offer That ErrorProbe Does Not (V1)

Features where ErrorProbe is genuinely weaker or out of scope.

| Feature | EP | DD | LZD | DOZ | PRT | K9S | OL | STN | G+L | ELK | PRO | DAT | SEN | NR | CTP | SNZ | SPL | COR | VEC | CLI |
|---|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| **Start / stop / restart user containers** | ❌ | ✅ | ✅ | ❌ | ✅ | ✅ | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ✅ |
| **Real-time CPU / memory / network metrics** | ❌ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ | ❌ | ❌ | ✅ | ✅ | ❌ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ | ✅ |
| **Remote Docker hosts / multi-cluster Kubernetes** | ❌¹³ | ❌ | ❌ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ | ✅ | ✅ | ✅ | ✅ | ⚠️ |
| **Alert routing (Slack, PagerDuty, email, webhook)** | ❌ | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ | ❌ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ | ✅ | ✅ | ✅ | ❌ | ❌ |
| **Native web UI (browser-based, not relying on Grafana)** | ❌¹⁴ | ❌ | ❌ | ✅ | ✅ | ❌ | ❌ | ❌ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ | ✅ | ✅ | ✅ | ❌ | ❌ |
| **App-level error tracking (SDK, stack traces, issue grouping)** | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ✅ | ✅ | ✅ | ❌ | ✅ | ✅ | ❌ | ❌ | ❌ |
| **Distributed tracing** | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ✅ | ✅ | ✅ | ❌ | ✅ | ✅ | ✅ | ❌ | ❌ |
| **Kubernetes resource management (deployments, CRDs, RBAC)** | ❌ | ❌ | ❌ | ❌ | ✅ | ✅ | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| **Docker Swarm support** | ❌ | ❌ | ❌ | ✅ | ✅ | ❌ | ❌ | ❌ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ | ✅ | ✅ | ❌ | ❌ | ❌ |

---

## Footnotes

1. **Dozzle v10**: Added webhook alerts triggered by log line regex match. Requires manual per-container configuration. Fires on a matching log line, not on a sustained error state. Produces no OK/HAS_ERRORS health state.

2. **Grafana+Loki / ELK / SigNoz / Splunk**: Can alert on log patterns via LogQL/ElasticSearch alert rules + Alertmanager. However, this requires writing rules manually, setting up routing, and configuring thresholds. Nothing is zero-config.

3. **Datadog / New Relic**: Have log anomaly detection and error rate dashboards, but (a) require a paid subscription, (b) require a running agent per host, (c) alert policies must be manually configured. They approximate a health signal within a full-platform context, not a simple per-container OK/FAILING state.

4. **Sentry**: Captures errors explicitly submitted via SDK instrumentation in application source code. It does not read container stdout/stderr logs of arbitrary containers. An existing containerised app without Sentry SDK integration is invisible to Sentry. Not zero-config for a container you didn't write.

5. **Coroot**: Uses eBPF to observe network calls, latency, and error rates. Has a "service health" concept based on RED metrics (Rate, Errors, Duration). The health signal is real but derived from network-level observation, not from log content. Requires a privileged eBPF agent deployed as a DaemonSet. Not one-command local setup.

6. **Datadog / New Relic / SigNoz / Splunk**: Log collection from container stdout/stderr does not require app code changes (the agent tails log files). APM features — error grouping, issue linking, tracing — do require SDK instrumentation. Rating ⚠️ because "log viewing" is agentless but the killer features that justify the product require instrumentation.

7. **Sentry**: Requires SDK in the application. A container whose app has no Sentry SDK is entirely invisible to Sentry regardless of what it logs to stdout.

8. **Docker Desktop**: Has a basic K8s control plane you can enable. The built-in UI shows running pods but offers no log aggregation, health analysis, or proactive error detection.

9. **Grafana+Loki / ELK / Prometheus / SigNoz**: Can be configured to collect from both Docker and K8s, but require separate Vector/Promtail/Filebeat DaemonSet configurations for each runtime. Not unified out of the box.

10. **Stern**: Requires a pod query (regex, deployment name, or label selector) as a CLI argument. You specify what you want to tail; it does not automatically tail all running containers without explicit targeting.

11. **Vector standalone**: Supports SIGHUP-driven config reload. ErrorProbe exploits this internally. As a standalone tool, Vector has no CLI reload command; the operator must send the signal themselves.

12. **Dozzle**: Maintains an in-memory ring buffer for recent log entries (configurable size). Logs are not persisted to disk or a queryable backend; history is lost on restart.

13. **ErrorProbe remote support**: V2 roadmap item explicitly. V1 is local-only (Docker Desktop socket and local kubeconfig).

14. **ErrorProbe web UI**: Grafana is auto-provisioned and reachable at `localhost:3000` with pre-built dashboards and per-container Explore links. There is no ErrorProbe-native browser dashboard; Grafana is the visualization layer.

---

## Summary

### What ErrorProbe alone delivers in combination

These capabilities are individually present in some competitors, but **no single tool in this list delivers all of them together without manual configuration**:

| Unique combination | Why no competitor closes this gap |
|---|---|
| Push-based functional health signal, zero config | Every tool that can alert on log patterns requires manual rule authoring. Dozzle's webhook alerts fire on a regex match, not on a derived health state. |
| `errorprobe check` exits 1 if a container logged errors | No other tool provides a log-error-aware exit-code command for CI pipelines. Prometheus Alertmanager fires notifications, not exit codes. |
| One binary installs and wires Vector + Loki + Grafana | Every observability stack (ELK, Loki+Grafana, SigNoz) requires multi-container manual setup. ErrorProbe is the only thing in this list that owns its stack completely from a single binary. |
| Infra-healthy + functionally-broken surfaced simultaneously | This is ErrorProbe's founding scenario. No other tool tracks both signals or presents them side by side per container. |

### What ErrorProbe does not cover (V1 honest gaps)

| Gap | Who fills it better |
|---|---|
| Start / stop / restart your own containers | Lazydocker (TUI), Portainer (web UI), k9s (K8s), Docker Desktop |
| Real-time CPU / memory / network charts | ctop (TUI, lightweight), Lazydocker, Dozzle v10, Docker Desktop |
| Remote Docker hosts or multi-cluster K8s | Dozzle (remote Docker), Portainer (multi-host/cluster), k9s (multiple K8s contexts) |
| Slack / PagerDuty / email notifications when something breaks | Dozzle webhooks, Grafana alerting (manual), Datadog, New Relic |
| Kubernetes resource management (scale, edit, RBAC) | k9s, OpenLens |
| App-level error grouping (stack traces, issue dedup, releases) | Sentry — it is in a different category entirely; they are complementary |
| Distributed tracing | Datadog, New Relic, SigNoz — again complementary, not direct competition |
| Browser-based UI without Grafana | Dozzle (log-focused), Portainer (management-focused) |
| Production / staging / multi-team observability at scale | Datadog, New Relic, Splunk — ErrorProbe is local-first by design and does not compete here |
