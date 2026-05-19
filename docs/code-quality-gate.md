# Code Quality Gate

**Review date:** May 2026  
**Reviewer:** GitHub Copilot (automated static review)  
**Scope:** Full codebase — `cmd/`, `internal/`, `test/`  
**Overall grade: B+**

A well-structured, idiomatic Go codebase with strong interface hygiene and solid test discipline in core packages. The primary gaps are uneven coverage (several packages at 0%), a coupling issue in the top-level command, and an incomplete linter configuration.

---

## 1. Modularity & SOLID Principles

### Strengths

- **Single Responsibility** — Package decomposition is clean and well-bounded: `ingest` receives and parses events; `health` maintains state; `pbr` evaluates rules; `discovery` resolves container sets; `configgen` renders configuration templates; `stack` manages Docker lifecycle; `tui` owns the terminal UI; `learn` handles adaptive rule generation; `loki` is a minimal HTTP client. No package tries to do too much.

- **Interface Segregation** — Dependency surfaces are deliberately narrow:
  - `LokiQueryClient` exposes only `CountErrors` and `QueryErrorMessages`, not the full `*loki.Client` surface.
  - `docker.DockerAPI` and `k8s.K8sAPI` isolate SDK dependencies from the rest of the codebase.
  - `discovery.VectorGenerator` is a one-method interface that `configgen.DefaultGenerator` satisfies, keeping `discovery` free of a direct `configgen` import.

- **Dependency Inversion** — Constructors consistently accept interfaces, not concrete types. `export_test.go` files expose internal constructors cleanly for tests, with zero test-only code in production paths.

- **Open/Closed** — The PBR rule engine is fully data-driven. New rules, operators, and field types can be added by extending the `RuleConfig` → `pbr.Load` → `pbr.Evaluate` pipeline without touching the evaluator logic.

- **Liskov Substitution** — Mock Docker and K8s clients used in tests are faithful substitutes; the production code cannot distinguish them.

### Issues

| Issue | Location | Impact |
|---|---|---|
| God-object coordination | `cmd/up.go` `RunE` | Wires engine, transport, reconciler, Tier 2, K8s, PBR, PID, SIGHUP in ~160 lines. Should be extracted to an `app.Runner` type. |
| DIP violation | `internal/health/tier2.go` | `Tier2Evaluator` takes `*Engine` (concrete), not an interface. An `EngineWriter` interface would decouple it. |
| Dual state representation | `internal/health/engine.go` + `health.go` | Typed constants (`StateHasErrors`, `StateFailing`) defined in `health.go` are bypassed by raw-string comparisons (`state == "HAS_ERRORS"`) in `engine.go`. Makes refactoring fragile. |
| Scaffolded but untested module | `internal/learn/` | Rich types and logic exist with 0% test coverage, suggesting the module is not yet production-ready. Flag before enabling by default. |

---

## 2. Test Infrastructure & Practices

### Good Practices

- `t.TempDir()` used universally — tests are fully isolated and leave no disk state.
- `require` vs `assert` used correctly: `require` for fatal preconditions, `assert` for non-fatal assertions.
- Integration tests in `test/e2e/` are gated behind `//go:build integration`, keeping the default `go test ./...` run fast.
- Interface-based fakes in `docker` and `k8s` packages avoid third-party mocking frameworks and keep test doubles maintainable.
- `export_test.go` files cleanly expose internal constructors/helpers without polluting the production API.

### Coverage

| Package | Coverage | Grade |
|---|---|---|
| `internal/logger` | 94.0% | ✅ Excellent |
| `internal/links` | 91.7% | ✅ Excellent |
| `internal/ingest` | 90.2% | ✅ Excellent |
| `internal/pbr` | 89.8% | ✅ Strong |
| `internal/stack` | 88.7% | ✅ Strong |
| `internal/health` | 80.9% | ✅ Good |
| `internal/docker` | 78.5% | ✅ Good |
| `internal/configgen` | 76.8% | ✅ Good |
| `internal/loki` | 75.7% | ✅ Good |
| `internal/discovery` | 70.0% | ⚠️ Acceptable |
| `internal/config` | 45.2% | ⚠️ Needs work |
| `internal/k8s` | 25.2% | ❌ Low |
| `cmd` | 5.6% | ❌ Critical gap |
| `internal/learn` | 0.0% | ❌ No tests |
| `internal/tui` | 0.0% | ❌ No tests |
| `internal/pid` | 0.0% | ❌ No tests |

### Gaps & Recommendations

1. **`cmd` coverage (5.6%)** — The `RunE` functions contain substantial business logic (wiring, signal handling, banner output). Extract to testable `app.Runner` / `app.Config` types; test those. The `cobra` layer itself does not need coverage.

2. **`internal/learn` (0%)** — The `extractor.go`, `gap.go`, `keyword.go`, and `overlay.go` files contain non-trivial algorithms. Cover them before the learning module is enabled by default (`LearnConfig.Enabled: true`).

3. **`internal/tui` (0%)** — The Bubbletea `Model.Update` and `View` functions can be unit-tested using `bubbletea/teatest` or by calling `Update` directly with synthetic `tea.Msg` values. Snapshot tests for `View` output catch regressions.

4. **`internal/k8s` (25%)** — The `buildConfig`, `GetPreviousContainerLogs`, and `ApplyVectorDaemonSet` paths are untested. Fake `kubernetes.Interface` clients (already used for other tests) can exercise these paths.

5. **`-race` flag** — No evidence of `-race` in test runs or CI configuration. Add `go test -race ./...` to the CI pipeline. See the S1 scalability issue for a specific data race that `-race` would catch.

6. **No benchmarks** — The hot paths (`ProcessBatch`, `Evaluate`, `ExtractPattern`, `Tier2Evaluator.evaluate`) have no benchmarks. Add `Benchmark*` functions to track performance regressions.

### Linter Configuration

The current `.golangci.yml` enables only 4 linters: `errcheck`, `govet`, `staticcheck`, `gofmt`. Recommended additions:

| Linter | Rationale |
|---|---|
| `gosec` | Catches security issues (file permissions, SSRF, etc.) — see work-plan-security.md |
| `bodyclose` | Ensures HTTP response bodies are always closed |
| `noctx` | Flags HTTP requests created without a context |
| `exhaustive` | Ensures switch statements on typed enums are exhaustive |
| `wrapcheck` | Ensures external errors are wrapped with context |
| `ineffassign` | Detects assignments whose result is never used |
| `misspell` | Catches typos in comments and strings |
| `revive` | Drop-in replacement for `golint` with better configurability |

Additionally, remove the broad `errcheck` suppression for `cmd/`:
```yaml
# current — too broad
- path: cmd/
  linters:
    - errcheck
```
Replace with per-function `//nolint:errcheck // reason` annotations at the specific call sites where ignoring errors is intentional.

---

## 3. Design Patterns Observed

| Pattern | Where | Assessment |
|---|---|---|
| **Strategy** | PBR rule evaluation — `Evaluate(rules []Rule, ctx EvalContext)` | Well-applied; rules are interchangeable strategies |
| **Observer** | `Engine.onChange` callback; `StateTransitionEvent` channel | Correct; allows Tier 2 and history log to react to state changes |
| **Factory** | `NewEngine`, `NewReconciler`, `NewHTTPTransport` — constructors accept dependencies | Consistent; enables test injection |
| **Adapter** | `configgen.DefaultGenerator` wraps package-level `GenerateVector` behind `VectorGenerator` interface | Clean one-liner adapter |
| **Template Method** (data-driven) | `configgen` + `assets/templates/` — Go `text/template` over embedded FS | Appropriate for config generation |
| **Repository** | `health.SaveSnapshot` / `LoadSnapshot`, `HistoryLog` | Clear separation of domain logic from persistence |

No anti-patterns observed beyond those listed in the SOLID issues section.

---

## 4. Summary Scorecard

| Dimension | Score | Notes |
|---|---|---|
| Package structure & SRP | A | Clean boundaries, no circular imports |
| Interface design (ISP / DIP) | A- | One DIP violation (`Tier2Evaluator`), one missing interface (`EngineWriter`) |
| OCP / extensibility | A | PBR engine is data-driven and extensible |
| Test infrastructure | B+ | Good tooling; gaps in `cmd`, `learn`, `tui`, `k8s` |
| Test coverage (average) | B | Strong core packages; several at 0% |
| Linter configuration | C+ | Only 4 linters; no `gosec`; broad `errcheck` suppression |
| Benchmarks | D | None present |
| Documentation | B | `doc.go` in most packages; inline comments good in `pbr` and `health` |
| **Overall** | **B+** | |

---

## 5. Action Items (non-scalability, non-security)

These items are out of scope for the dedicated work plans but should be tracked:

- [ ] Extract `cmd/up.go` wiring into `internal/app.Runner` to enable unit testing of the startup sequence.
- [ ] Replace `state == "HAS_ERRORS"` raw-string comparisons in `engine.go` with the typed `StateHasErrors` constant.
- [ ] Add `-race` to the CI `go test` invocation.
- [ ] Add benchmarks for `ProcessBatch`, `Evaluate`, and `Tier2Evaluator.evaluate`.
- [ ] Expand `.golangci.yml` with the linters listed in Section 2.
- [ ] Replace the broad `errcheck` `cmd/` suppression with targeted `//nolint` annotations.
- [ ] Add `teatest`-based tests for `internal/tui` before the next TUI feature lands.
- [ ] Gate `LearnConfig.Enabled` default to `false` until `internal/learn` reaches ≥ 80% coverage.
