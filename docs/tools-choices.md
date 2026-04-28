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

## Domain 6 — CLI Framework (Go)

| # | Tool | License | Notes |
|---|------|---------|-------|
| 1 | **Cobra** (spf13/cobra) | Apache 2.0 | De-facto standard Go CLI framework. Used by kubectl, docker, helm. Subcommands, persistent flags, shell completion. |
| 2 | **urfave/cli** | MIT | Minimal and pragmatic. Popular alternative to Cobra. No nested subcommand model. |
| 3 | **Kong** | MIT | Struct-tag driven. Clean declarative API. Less ecosystem adoption than Cobra. |
| 4 | **Kingpin** | MIT | POSIX-compliant flag parsing. Mature but lower activity. |
| 5 | **pflag** | BSD-3-Clause | Low-level POSIX flag library. Building block used by Cobra, not a standalone CLI framework. |

---

## Domain 7 — TUI Library (Go)

| # | Tool | License | Notes |
|---|------|---------|-------|
| 1 | **Bubbletea** (charmbracelet/bubbletea) | MIT | Elm-architecture TUI framework. Clean model/update/view pattern. In-place redraw, terminal resize and clean exit handled. |
| 2 | **tview** | MIT | Rich widget library (tables, forms, modals). More complex layout integration than Bubbletea. |
| 3 | **gocui** | Apache 2.0 | Panel-based TUI. Used by Lazygit. Less opinionated layout model. |
| 4 | **termui** | MIT | Dashboard widgets (charts, gauges). Purpose-built for metrics display, not general interactive TUI. |
| 5 | **tcell** | Apache 2.0 | Low-level terminal primitives. No widget abstractions — building block only. |

---

## Domain 8 — Config Management (Go)

| # | Tool | License | Notes |
|---|------|---------|-------|
| 1 | **Viper** (spf13/viper) | MIT | De-facto standard. YAML/TOML/JSON/ENV support, flag binding, layered precedence. First-class Cobra companion. |
| 2 | **koanf** | MIT | Lightweight and modular. Cleaner API than Viper. Smaller ecosystem. Growing adoption. |
| 3 | **envconfig** | MIT | Struct-tag driven env-var parsing only. No file config support — too limited for `errorprobe.yaml`. |
| 4 | **godotenv** | MIT | .env file loading only. Single format; no layered precedence chain. |
| 5 | **cleanenv** | MIT | Minimal struct-tag config from files + env. Simpler than Viper but lacks the precedence model needed. |

---

## Domain 9 — Log Rotation (Go)

| # | Tool | License | Notes |
|---|------|---------|-------|
| 1 | **Lumberjack** (natefinch/lumberjack) | MIT | Standard Go log rotation. Size/age/count policies. Used as an `io.Writer` with `log/slog` or any Go logger. |
| 2 | **file-rotatelogs** (lestrrat-go) | MIT | Time-based rotation. More complex configuration than Lumberjack. |
| 3 | **logrotate** (system tool) | GPL v2 | OS-level rotation daemon. Requires system configuration — not embeddable in a self-contained binary. |
| 4 | **zap** (with Lumberjack writer) | MIT | Uber's structured logger; rotation delegated to Lumberjack. Heavier than `log/slog`. |
| 5 | **zerolog** (with external writer) | MIT | Very fast structured logger. No built-in rotation; requires an external writer. |

---

## Domain 10 — Testing Framework (Go)

| # | Tool | License | Notes |
|---|------|---------|-------|
| 1 | **testify** (stretchr/testify) | MIT | Standard Go assertion library. `assert`, `require`, `mock`, `suite` packages. Near-universal adoption. |
| 2 | **gomock** (uber-go/mock) | Apache 2.0 | Interface-based mocks, code-generated. Official Google lineage. |
| 3 | **ginkgo + gomega** | MIT | BDD-style test framework. Expressive specs; more ceremony than testify for unit tests. |
| 4 | **gocheck** | BSD-2-Clause | Test suites and assertions. Less active than testify. |
| 5 | **go-cmp** | BSD-3-Clause | Deep struct comparison from Google. Best as a complement to testify, not a standalone framework. |

---

## Selected Stack (V1)

| Domain | Selected Tool | Runner-up |
|--------|--------------|-----------|
| Discovery | Docker Engine API + K8s API | cAdvisor |
| Collection | Vector | Fluent Bit |
| Parsing | Vector (VRL) | Fluent Bit |
| Storage | Loki | OpenSearch |
| Presentation | Grafana OSS | Dozzle |
| CLI Framework | Cobra | urfave/cli |
| TUI Library | Bubbletea | tview |
| Config Management | Viper | koanf |
| Log Rotation | Lumberjack | file-rotatelogs |
| Testing Framework | testify | gomock |

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
| Cobra | Apache 2.0 |
| Bubbletea | MIT |
| Viper | MIT |
| Lumberjack | MIT |
| testify | MIT |

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
