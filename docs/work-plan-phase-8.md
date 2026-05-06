# Phase 8 — gRPC / OTLP Transport (V2)

**Goal:** Add gRPC as a configurable alternative to HTTP JSON for the Vector → ErrorProbe ingest path.  
**Prerequisite:** Phase 7 complete.

**UT coverage requirement: ≥ 90% on all new packages and functions.**

---

## Atomic Tasks

Tasks are grouped by dependency tier. All tasks within a tier can be implemented independently and in parallel.

---

### Tier 1 — Protobuf schema and generated code (no Phase 7 dependencies)

#### T7.1 — Define OTLP log proto schema
- Add `buf` or `protoc` toolchain to repo (generate Go code from `.proto`)
- Use the official OpenTelemetry OTLP logs proto (`opentelemetry-proto/opentelemetry/proto/logs/v1/logs.proto`) — do not write a custom schema
- Add `go.opentelemetry.io/proto/otlp` as a dependency in `go.mod`
- Generated Go types used by T7.2
- Add `buf.gen.yaml` or `Makefile` target for proto regeneration; commit generated files to repo (standard Go practice — no `go generate` required at build time)

#### T7.2 — Implement OTLP log event adapter
- `OTLPToLogEvent(req *otlplogspb.ExportLogsServiceRequest) ([]ingest.LogEvent, error)` in `internal/ingest`
- Maps OTLP `LogRecord` fields to `LogEvent`:
  - `TimeUnixNano` → `Timestamp`
  - Resource attribute `container.name` → `Container`
  - `SeverityText` → `Level`
  - `Body` → `Message` (string value)
  - Full body → `Raw`
- Handles missing fields gracefully (defaults to empty string, not panic)
- Pure function — no I/O; fully unit-testable

---

### Tier 2 — gRPC transport implementation (depends on T7.1, T7.2)

#### T7.3 — Implement gRPC OTLP ingest transport
- `GRPCTransport` struct in `internal/ingest` implementing the existing `Transport` interface
- Implements `opentelemetry.proto.collector.logs.v1.LogsService` gRPC service
- Binds on `cfg.Stack.Ingest.Bind + ":" + cfg.Stack.Ingest.Port` (same port as HTTP transport — user configures one or the other)
- `ExportLogs(ctx, req)` handler: call `OTLPToLogEvent`, call registered `OnBatch` handler, return success response
- Graceful shutdown via `Stop()`: calls `grpcServer.GracefulStop()`
- Max message size: 10 MB (mirrors HTTP transport limit)

---

### Tier 3 — Config and factory (depends on T7.3)

#### T7.4 — Implement transport factory
- `NewTransport(cfg *config.Config) (Transport, error)` in `internal/ingest`
- Reads `cfg.Stack.Ingest.Transport`:
  - `"http"` → returns `HTTPTransport`
  - `"grpc"` → returns `GRPCTransport`
  - Other value → returns error with clear message listing valid options
- Called from `cmd/up.go` during startup — replaces direct `HTTPTransport` instantiation

#### T7.5 — Extend Vector config generator for gRPC sink
- Extend `GenerateVector` to emit different sink based on `cfg.Stack.Ingest.Transport`:
  - `"http"` → `[sinks.errorprobe_ingest]` type `http` (existing)
  - `"grpc"` → `[sinks.errorprobe_ingest]` type `opentelemetry` with gRPC endpoint and OTLP logs protocol
- No other changes to Vector config — sources and transforms are transport-agnostic

#### T7.6 — Classify transport change as hard in reload
- Extend `ClassifyChanges` (Phase 4 T4.3): `stack.ingest.transport` change → HardChanges
- Transport switch via `errorprobe reload`: stops Vector container, regenerates config with new sink, restarts Vector, restarts ingest transport in ErrorProbe binary

---

### Tier 4 — Unit tests (depends on T7.1–T7.5; independent of each other)

#### T7.7 — Unit tests: OTLP adapter
- `TestOTLPToLogEvent_FullRecord`: OTLP record with all fields; assert LogEvent mapped correctly
- `TestOTLPToLogEvent_MissingContainer_DefaultsEmpty`: no container.name attribute; assert Container is empty string
- `TestOTLPToLogEvent_MissingLevel_DefaultsEmpty`: no SeverityText; assert Level is empty string
- `TestOTLPToLogEvent_MultipleRecords`: batch with 3 records; assert 3 LogEvents returned
- `TestOTLPToLogEvent_EmptyBatch`: empty request; assert empty slice, no error

#### T7.8 — Unit tests: gRPC transport
- `TestGRPCTransport_ValidBatch_CallsHandler`: send OTLP request; assert handler called with correct events
- `TestGRPCTransport_MissingContainerField_HandlerCalledWithDefault`: record without container.name; assert handler called, Container is empty string (not panic)
- `TestGRPCTransport_GracefulShutdown`: start server, call Stop; assert in-flight requests complete
- `TestGRPCTransport_OversizeMessage_Rejected`: message > 10 MB; assert gRPC status error

#### T7.9 — Unit tests: transport factory
- `TestNewTransport_HTTP_ReturnsHTTPTransport`: config `transport: http`; assert `*HTTPTransport` returned
- `TestNewTransport_GRPC_ReturnsGRPCTransport`: config `transport: grpc`; assert `*GRPCTransport` returned
- `TestNewTransport_InvalidValue_ReturnsError`: config `transport: invalid`; assert error with helpful message

#### T7.10 — Unit tests: Vector config generation for gRPC
- `TestGenerateVector_GRPCSink_OutputsOTLPSink`: transport grpc in config; assert `type = "opentelemetry"` in sink
- `TestGenerateVector_HTTPSink_OutputsHTTPSink`: transport http in config; assert `type = "http"` in sink
- `TestGenerateVector_GRPCSinkURL`: grpc transport with custom port; assert correct endpoint URL in output

#### T7.11 — Unit tests: reload classifies transport change as hard
- `TestClassifyChanges_TransportHTTPToGRPC_IsHard`: http → grpc; assert HardChanges contains transport change
- `TestClassifyChanges_TransportSame_NotInHard`: both http; assert transport not in HardChanges

---

### Final Task

#### T7.12 — Mark phase complete in work-plan.md
- Open `docs/work-plan.md`
- Mark all Phase 7 tasks as `[x]`
- Add completion date next to phase heading

#### T7.13 — Update roadmap.html
- Open `docs/roadmap.html` in a browser and verify Phase 7 is reflected correctly
- In the `PHASES` array, set Phase 7's `status` to `"completed"` and `actualEnd` to the actual finish date
- Compare actual duration against the planned estimate; if velocity differed, adjust `start` / `end` for Phase 8 accordingly
- Update the `TODAY` constant to the current date
- Recompute and document the revised total story-point burn rate and projected completion date for the remaining phases in a comment above the `PHASES` array

---

## Deliverables

| Deliverable | Description |
|---|---|
| OTLP proto dependency | `go.opentelemetry.io/proto/otlp` in `go.mod` |
| `OTLPToLogEvent` | OTLP → LogEvent adapter |
| `internal/ingest.GRPCTransport` | gRPC OTLP ingest server implementing `Transport` |
| `internal/ingest.NewTransport` | Factory: selects HTTP or gRPC based on config |
| Extended `GenerateVector` | OTLP gRPC sink emitted when transport is grpc |
| Extended `ClassifyChanges` | Transport change classified as hard |
| Unit tests | ≥ 90% coverage on updated `ingest` |

---

## Manual Tests

Run after all tasks are complete:

1. **Default (HTTP) unchanged** — ensure existing HTTP path still works: `transport: http` in yaml; `errorprobe up`; inject errors; confirm detection works as before.
2. **Switch to gRPC** — change `stack.ingest.transport: grpc` in `errorprobe.yaml`; `errorprobe reload`; confirm "Hard changes applied" message; Vector restarts.
3. **gRPC ingest active** — with gRPC transport active, inject errors into a container; confirm `errorprobe watch` detects them (same as HTTP path).
4. **Switch back to HTTP** — change back to `transport: http`; `errorprobe reload`; confirm reload succeeds; HTTP detection resumes.
5. **Invalid transport value** — set `transport: invalid` in yaml; `errorprobe up`; confirm clear error message listing valid options.
6. **`go test ./... -cover`** — all tests pass; coverage ≥ 90% on updated `internal/ingest`.
