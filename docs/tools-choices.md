# Tools Choices

Methodology: 5 state-of-the-art open-source tools ranked per domain (best → worst) based on public opinion, adoption, known issues, license, ease of use.

---

## Domain 1 — Container Discovery

| # | Tool | License | Notes |
|---|------|---------|-------|
| 1 | **Docker Engine API** | Apache 2.0 | Canonical standard. Direct socket access, official SDK in every language. |
| 2 | **Kubernetes API (client-go / kubectl)** | Apache 2.0 | Standard for K8s workload discovery. Pods, containers, namespaces, labels. |
| 3 | **cAdvisor** (Google) | Apache 2.0 | Auto-discovers containers, exposes metadata + metrics. Docker and K8s native. |
| 4 | **Portainer Agent** | zlib | Lightweight per-host agent. REST API for container inventory. Docker + K8s. |
| 5 | **Netdata** | GPL v3+ | Zero-config container autodiscovery. Primarily metrics but discovery is first-class. |

---

## Domain 2 — Log Collection

| # | Tool | License | Notes |
|---|------|---------|-------|
| 1 | **Vector** (Datadog OSS) | MPL 2.0 | Best-in-class modern agent. Rust-based, extremely fast. Docker + K8s autodiscovery. Excellent VRL transformation. |
| 2 | **Fluent Bit** (CNCF) | Apache 2.0 | De-facto K8s standard. C-based, ~1 MB binary. Docker input plugin, K8s metadata enrichment. |
| 3 | **Promtail** (Grafana) | Apache 2.0 | Purpose-built Loki shipper. Best Docker/K8s autodiscovery when Loki is the store. |
| 4 | **Filebeat** (Elastic) | Apache 2.0 | Battle-tested. Docker autodiscovery. Best fit if Elasticsearch is the storage. Heavier than Vector/Fluent Bit. |
| 5 | **OpenTelemetry Collector** (CNCF) | Apache 2.0 | Vendor-neutral emerging standard. Semantic log conventions built-in. More complex to configure. |

---

## Domain 3 — Log Parsing & Normalization

| # | Tool | License | Notes |
|---|------|---------|-------|
| 1 | **Vector (VRL)** | MPL 2.0 | Most expressive and performant OSS log transformation DSL. JSON, logfmt, regex, custom schemas. |
| 2 | **Logstash** (Elastic) | Apache 2.0 | Most mature parsing pipeline. Grok patterns, hundreds of plugins. Heavy resource usage. |
| 3 | **Fluent Bit** | Apache 2.0 | Built-in JSON, regex, and Lua parsers. Lightweight. Slightly less expressive than Vector. |
| 4 | **OpenTelemetry Collector** | Apache 2.0 | Semantic normalization baked in (`severity_text`, `severity_number`). Best for future-proofing schema. |
| 5 | **Cribl Edge** | Apache 2.0 (Edge) | Very powerful log shaping. Less community adoption than the above. |

---

## Domain 4 — Log Storage & Query

| # | Tool | License | Notes |
|---|------|---------|-------|
| 1 | **Grafana Loki** | Apache 2.0 | Purpose-built for logs. Indexes labels only (lightweight). LogQL powerful. Tight Grafana integration. |
| 2 | **OpenSearch** | Apache 2.0 | AWS fork of Elasticsearch. Truly Apache 2.0. Full-text search, rich aggregations. Heavier than Loki. |
| 3 | **Elasticsearch** | SSPL | Industry standard, most capable. ⚠️ SSPL license (not truly OSS since 2021). |
| 4 | **ClickHouse** | Apache 2.0 | Columnar DB, fast for log analytics. Increasingly adopted as log store. Less purpose-built. |
| 5 | **VictoriaLogs** | Apache 2.0 | Newest entrant. Very lightweight, Loki-compatible API. Promising but younger community. |

---

## Domain 5 — Presentation / UI

| # | Tool | License | Notes |
|---|------|---------|-------|
| 1 | **Grafana OSS** | AGPL v3 | Undisputed #1 OSS dashboard. Native Loki source. Explore view purpose-built for log browsing. |
| 2 | **Dozzle** | MIT | Purpose-built Docker log viewer. Zero-config, real-time streaming. No K8s support. |
| 3 | **OpenSearch Dashboards** | Apache 2.0 | Full-featured. Best fit if OpenSearch is the storage. |
| 4 | **SigNoz** | MIT | Full-stack OSS observability UI (traces + logs + metrics). Modern UX, growing fast. |
| 5 | **Kibana** | SSPL | The original ELK UI. Powerful but tied to Elasticsearch. ⚠️ SSPL license concern. |

---

## Selected Stack (V1)

| Domain | Selected Tool | Runner-up |
|--------|--------------|-----------|
| Discovery | Docker Engine API + K8s API | cAdvisor |
| Collection | Vector | Fluent Bit |
| Parsing | Vector (VRL) | Fluent Bit |
| Storage | Loki | OpenSearch |
| Presentation | Grafana OSS | Dozzle |

> Vector covers both collection and parsing — one tool owns the pipeline from raw container log to normalized JSON. Architecturally clean.

---

## License Summary

### Clean licenses (no concerns)

| Tool | License |
|------|---------|
| Docker Engine API | Apache 2.0 |
| Kubernetes API / client-go | Apache 2.0 |
| cAdvisor | Apache 2.0 |
| Fluent Bit | Apache 2.0 |
| Promtail | Apache 2.0 |
| Loki | Apache 2.0 |
| Dozzle | MIT |
| OpenSearch | Apache 2.0 |
| SigNoz | MIT |

### Tools requiring attention 

**Vector — MPL 2.0**
Free to use, including commercially. The only obligation: if Vector's own source code is modified, those modifications must be published. Orchestrating and configuring Vector (without modifying it) does not trigger this clause.

**Grafana OSS — AGPL v3**
AGPL's copyleft applies when distributing a *modified* version of Grafana itself over a network — not when orchestrating it as an independent process. Running Grafana OSS as a separate process (the standard pattern used by Helm charts, Docker Compose stacks, etc.) is safe.

---

## Grafana OSS vs Enterprise

For V1–V3 of this platform, OSS covers everything needed:

| Capability | OSS | Enterprise |
|-----------|-----|-----------|
| Loki data source | ✅ | ✅ |
| Log browsing (Explore view) | ✅ | ✅ |
| Time-range filtering | ✅ | ✅ |
| Label + field filtering | ✅ | ✅ |
| Dashboards and panels | ✅ | ✅ |
| Unified alerting | ✅ | ✅ |
| Data source plugins (100+) | ✅ | ✅ |
| Basic RBAC | ✅ | ✅ |
| Advanced RBAC (team-level) | ❌ | ✅ |
| PDF reporting / scheduled reports | ❌ | ✅ |
| Enterprise data sources (Splunk, Datadog…) | ❌ | ✅ |
| Audit logging | ❌ | ✅ |
| SSO / SAML | ❌ | ✅ |

Enterprise adds organizational governance and compliance features — irrelevant for a developer-first local observability tool until V3+ at the earliest.
