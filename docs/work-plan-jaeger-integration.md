# Jaeger Integration — Deployment Call Graph in `ep watch`

**Goal:** When a container surfaces `HAS_ERRORS` or `FAILING`, enrich the `[e]` expanded
panel in `ep watch` and the verbose output of `ep status` with:
1. A "Troubleshoot" block of ready-to-run commands parameterised with live metadata.
2. (when Jaeger is present) The active call graph at the time of the error, reconstructed
   from Jaeger's trace API — showing which services were actually involved in the failing
   request and the direction of calls.

**Prerequisite:** Phase 7 complete.  
**Jaeger dependency:** opt-in and gracefully absent — all functionality degrades to the
Troubleshoot block alone when Jaeger is not reachable or has no spans for the service.

**UT coverage requirement: ≥ 90% on all new packages and functions.**

---

## Background and Design Decisions

### Why not "Namespace peers"

Listing all co-deployed containers in the same namespace or Docker network was considered
and rejected: it adds noise without signal. A namespace can have 20+ services; showing them
all when only one is broken tells the user nothing about causality. The call graph (Jaeger)
solves the "who was involved" question far more precisely when available. When Jaeger is
absent, the Troubleshoot block with concrete commands is more actionable than a peer list.

### Jaeger detection strategy

EP detects Jaeger opportunistically at startup by querying
`GET http://localhost:<jaeger_port>/api/services`. If the request succeeds and returns a
non-empty service list, EP treats Jaeger as available and stores the base URL. No
configuration required from the user; EP discovers the port from `errorprobe.yaml`
(`integrations.jaeger.port`, default 16686). If no port is configured and no Jaeger
responds on the default port, the feature is silently absent.

### Call graph reconstruction

Jaeger's `/api/traces` endpoint returns full trace data including:
- `traceID`, `spanID`, `parentSpanID` (via `references` array)
- `operationName` — the called operation
- `startTime`, `duration`
- `tags` — includes `peer.service`, `span.kind`, `error`, `db.system`, `http.method`,
  `rpc.service`, etc.
- `processes` map — `serviceName` and `host.name` (pod name) per process

EP queries for traces in a ±30 s window around the error timestamp, walks the span
`references` to build a parent→child tree, and renders the first 3 levels. Spans with
`error=true` tag are highlighted. The full Jaeger UI URL is appended as a deep link.

### Span classification

| Tag present | Display label |
|---|---|
| `db.system` | `[db: <db.name>]` |
| `http.method` | `[http <method> <http.target>]` |
| `rpc.service` | `[grpc: <rpc.service>]` |
| none of the above | `[<operationName>]` |

---

## Atomic Tasks

Tasks are grouped by dependency tier.

---

### Tier 1 — Config and Jaeger client (no upstream dependencies)

#### JI.1 — Add Jaeger config to `errorprobe.yaml` and `config` package

- Add optional `integrations` section to `config.go`:
  ```go
  type IntegrationsConfig struct {
      Jaeger JaegerConfig `yaml:"jaeger"`
  }
  type JaegerConfig struct {
      Port int    `yaml:"port"` // default 16686
      URL  string `yaml:"url"`  // overrides auto-detect; empty = use Port
  }
  ```
- Default `Jaeger.Port` to `16686` in `SetDefaults`
- `JaegerBaseURL(cfg *config.Config) string` helper — returns `cfg.Integrations.Jaeger.URL`
  if set, otherwise `http://localhost:<port>`
- Add schema comment to `errorprobe.yaml`:
  ```yaml
  integrations:
    jaeger:
      port: 16686   # optional; set to 0 to disable Jaeger integration
  ```
- Unit tests: `TestJaegerBaseURL_ExplicitURL`, `TestJaegerBaseURL_DefaultPort`,
  `TestJaegerBaseURL_PortZeroDisabled`

#### JI.2 — Implement `internal/jaeger` client package

New package `internal/jaeger`:

- `Client` struct with `baseURL string` and `http.Client` with 2 s timeout
- `NewClient(baseURL string) *Client`
- `IsAvailable(ctx context.Context) bool` — `GET /api/services`; returns true iff status
  200 and at least one non-"jaeger" service in the list
- `ServicesWithTraces(ctx context.Context) ([]string, error)` — returns service names from
  `/api/services`, excluding `"jaeger"` itself
- `TracesNear(ctx context.Context, service string, at time.Time, window time.Duration, limit int) ([]Trace, error)` —
  queries `/api/traces?service=<service>&start=<at-window>&end=<at+window>&limit=<limit>`;
  parses response into `[]Trace`
- Data types:
  ```go
  type Trace struct {
      TraceID   string
      Spans     []Span
      Processes map[string]Process
  }
  type Span struct {
      TraceID       string
      SpanID        string
      ParentSpanID  string  // empty for root spans
      OperationName string
      StartTime     time.Time
      Duration      time.Duration
      Tags          map[string]string // key → value, all values stringified
      ProcessID     string
      HasError      bool    // true if tags["error"] == "true"
  }
  type Process struct {
      ServiceName string
      HostName    string // from tags["host.name"]
  }
  ```
- `ParseTraces(data []byte) ([]Trace, error)` — pure function parsing the Jaeger JSON
  response; fully unit-testable without HTTP
- Unit tests using fixture JSON (capture one real Jaeger response as testdata):
  `TestParseTraces_SingleSpan`, `TestParseTraces_ParentChildRefs`,
  `TestParseTraces_ErrorSpan`, `TestTracesNear_WindowEncoding`

---

### Tier 2 — Call graph assembly (depends on JI.2)

#### JI.3 — Implement call graph builder

New file `internal/jaeger/graph.go`:

- `BuildGraph(traces []Trace) CallGraph`
- `CallGraph` is a tree of `CallNode`:
  ```go
  type CallNode struct {
      ServiceName   string
      PodName       string   // from Process.HostName
      OperationName string
      Label         string   // classified label: "[db: counter]", "[http GET /api/...]" etc.
      Duration      time.Duration
      HasError      bool
      Children      []*CallNode
  }
  type CallGraph struct {
      Roots []*CallNode // spans with no parent (root spans)
  }
  ```
- Assembly: for each trace, build a `spanID → *CallNode` map; assign children by walking
  `references` with `refType == "CHILD_OF"`; collect root nodes (no parent ref)
- Label classification (see Background section above)
- `CallGraph.IsEmpty() bool` — true if no roots
- `CallGraph.HasError() bool` — true if any node in the tree has `HasError == true`
- Unit tests: `TestBuildGraph_SingleRoot`, `TestBuildGraph_ThreeLevels`,
  `TestBuildGraph_MultipleRoots`, `TestBuildGraph_ErrorPropagation`,
  `TestClassifyLabel_DB`, `TestClassifyLabel_HTTP`, `TestClassifyLabel_gRPC`,
  `TestClassifyLabel_Fallback`

#### JI.4 — Implement call graph renderer

New file `internal/jaeger/render.go`:

- `RenderGraph(graph CallGraph, maxDepth int) []string` — returns one string per line,
  ready for TUI display; `maxDepth` caps recursion (use 3 for the expanded panel)
- Render format (text-tree, no box-drawing dependency):
  ```
  selling-counter  (pod: selling-counter-65f498c568-jhmgf)
    └─ [db: counter]  selling-counter-pgsql  92µs
    └─ [grpc: WicService]  selling-wic  ✗ error  450ms
         └─ [db: wic]  selling-wic-pgsql  12ms
  ```
- Error nodes rendered with `✗` prefix
- Duration: `<1ms` → `<1ms`; `<1s` → `NNNms`; `≥1s` → `N.Ns`
- If graph is empty: return `[]string{"  no trace data near error timestamp"}`
- Pure function; no I/O; no lipgloss dependency (caller applies styling)
- Unit tests: `TestRenderGraph_Empty`, `TestRenderGraph_TwoLevel`,
  `TestRenderGraph_MaxDepthClip`, `TestRenderGraph_ErrorNode`

---

### Tier 3 — TUI and status integration (depends on JI.3, JI.4; independent of each other)

#### JI.5 — Add Jaeger client to TUI model and fetch graph on expand

Changes in `internal/tui/model.go`:

- Add `jaegerClient *jaeger.Client` and `jaegerBaseURL string` to `Model` struct
- Add `callGraph map[string]jaeger.CallGraph` (keyed by health key) to `Model`
- `NewModel` gains optional `jaegerClient *jaeger.Client` and `jaegerBaseURL string`
  parameters (nil-safe — no Jaeger = existing behaviour)
- When `[e]` is pressed to expand a container: if `jaegerClient != nil`, fire a
  `tea.Cmd` that calls `jaegerClient.TracesNear` for the container's service name
  at `ch.LastErrorAt ± 30s`, builds the graph, and returns a `callGraphMsg` with
  the result; store in `callGraph[key]`
- On subsequent refreshes while expanded: do not re-fetch (graph is a snapshot)
- On collapse (`[e]` again): clear `callGraph[key]` so next expand fetches fresh data

#### JI.6 — Extend expanded panel to render the Troubleshoot block and call graph

Changes in `internal/tui/model.go` (the `if i == m.cursor && m.expanded` block):

Replace the current single `detail` string approach with a `[]string` of `detailLines`.
Each line is rendered as a separate bordered row, enabling multi-line expansion without
horizontal scrolling for the structured sections.

**Troubleshoot block** (always shown when expanded):

```
  Troubleshoot:
    get logs:     kubectl logs <pod> -n <ns> --tail=100 --since=10m
    describe pod: kubectl describe pod <pod> -n <ns>
    errors only:  ep logs <container> --errors-only
    exec shell:   kubectl exec -it <pod> -n <ns> -- /bin/sh
```

For Docker runtime:
```
  Troubleshoot:
    get logs:    docker logs <container> --tail=100 --since=10m
    inspect:     docker inspect <container>
    errors only: ep logs <container> --errors-only
    exec shell:  docker exec -it <container> /bin/sh
```

**Call graph block** (shown only if `callGraph[key]` is non-empty):

```
  Call graph (±30s of error):
    selling-counter  (pod: selling-counter-65f498c568-jhmgf)
      └─ [db: counter]  selling-counter-pgsql  92µs
      └─ [grpc: WicService]  selling-wic  ✗ error  450ms

  Jaeger: http://localhost:16686/search?service=selling-counter&...
```

If Jaeger client exists but graph fetch is in-flight: show `  fetching call graph…`
If Jaeger client exists but returned empty: show `  no trace data near error timestamp`
If no Jaeger client: block is omitted entirely (no "Jaeger not configured" message —
  silence is less noisy than a permanent absence notice)

The horizontal scroll mechanism on the existing single detail line is **replaced** by
vertical multi-line rendering for the structured sections. The raw error message line
retains horizontal scroll for long stack traces.

#### JI.7 — Add Jaeger deep link to `ep status` output

Changes in `cmd/status.go`:

- After the existing Grafana Explore links section, add a Jaeger section when
  `jaegerBaseURL` is non-empty and the service appears in `ServicesWithTraces`:
  ```
  Jaeger traces:
    selling-counter  http://localhost:16686/search?service=selling-counter&start=...&end=...
  ```
- `links.BuildJaegerSearchURL(baseURL, serviceName string, from, to time.Time) string`
  in `internal/links/links.go` — constructs the URL; `from`/`to` encoded as microseconds
  (Jaeger uses µs, not ms); zero times → omit `start`/`end` (Jaeger defaults to last hour)
- Unit test: `TestBuildJaegerSearchURL_WithRange`, `TestBuildJaegerSearchURL_ZeroTimes`

---

### Tier 4 — Startup detection (depends on JI.1, JI.2)

#### JI.8 — Detect Jaeger at `ep up` / `ep watch` startup

Changes in `cmd/up.go` and `cmd/watch.go`:

- After stack is ready, call `jaeger.NewClient(config.JaegerBaseURL(cfg)).IsAvailable(ctx)`
  with a 3 s timeout
- If available: log `jaeger: detected at <url> (<N> services with traces)` and pass client
  to TUI model
- If not available (timeout, connection refused, or port 0 in config): proceed silently;
  no warning printed — Jaeger is entirely optional
- `ep status` performs the same detection inline (no persistent client needed)

---

### Tier 5 — Unit tests for config and links (depends on JI.1, JI.7; independent)

#### JI.9 — Config and links unit tests

- `TestJaegerConfig_Defaults` — verify port defaults to 16686 when yaml omits the section
- `TestJaegerConfig_PortZeroDisables` — `JaegerBaseURL` returns `""` when port is 0
- `TestBuildJaegerSearchURL_WithRange`
- `TestBuildJaegerSearchURL_ZeroTimes`

---

## Pilot Limitation

As of the time this plan was written, only `selling-counter` exports spans to Jaeger.
The multi-service call graph (the primary value of this feature) requires the other
services to be configured with an OTel exporter pointing at `localhost:4317` (Jaeger's
gRPC OTLP receiver). That is an application-side configuration change, not an EP change.
EP's integration is complete and correct regardless; the call graph simply shows one
service until more are instrumented.

The call graph for `selling-counter` already shows its PostgreSQL dependency
(`peer.service: selling-counter-pgsql`) because `otelsql` instruments the DB driver
automatically — no application code change needed for the DB hop.
