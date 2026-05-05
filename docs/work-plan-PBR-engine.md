# PBR Engine — Policy-Based Rules for Health & Infra State

## Motivation

ErrorProbe's goal is to surface application errors. Defining *what counts as an error* is
central to that goal — not a peripheral concern. Currently the classification logic is
hardcoded:

- `HAS_ERRORS` / `FAILING` are hardcoded in `internal/health/engine.go` and `tier2.go`
- `RESTARTING` infra status is hardcoded in `internal/discovery/k8s_list.go`
- Severity classification uses `detection.severity_patterns` (configurable) but the mapping
  to health state is hardcoded

This makes the product inflexible. Users with different definitions of "error" (log level
naming conventions, acceptable restart counts, custom patterns) cannot adapt it without
patching source code.

---

## Design Decisions

### 1. Same rule syntax for both evaluation planes

Rules use the same YAML syntax regardless of whether they evaluate log events or infra
metadata. The `match` field selects the evaluation context and determines which fields are
available in `when` conditions. This keeps the mental model simple — one rule language,
two vocabularies.

### 2. First-match-wins with mandatory unique priority

Rules are evaluated in descending priority order. The first rule whose `when` conditions
all match sets the state. Subsequent rules are not evaluated.

Priority values **must be unique** across all rules. Duplicate priorities are a
configuration error — ErrorProbe will refuse to start and report the conflicting rule
names. This eliminates tie-breaking ambiguity entirely.

### 3. Default when no rule matches

- **Log plane**: no state change. The container stays at its current state.
- **Infra plane**: `infraStatus = "running"`.

Built-in default rules are shipped at the lowest possible priority (effectively priority 0,
below any user rule) so they can always be overridden.

### 4. ep check --explain (future)

First-match-wins makes observability straightforward. A future `ep check --explain
<container>` command will show which rule fired (or "no rule matched — default applied")
for each container. Noted here so the evaluator is designed to capture that information
from the start (matched rule name returned alongside the state).

---

## Rule Schema

```yaml
rules:
  - name: <string>          # unique human-readable identifier; required
    priority: <int>         # unique; higher = evaluated first; required
    match: log | infra      # evaluation context; required
    when:                   # all conditions must match (AND semantics)
      <field>: <value>      # equality
      <field>: "> <value>"  # comparison (>, <, >=, <=)
      <field>: "~<regex>"   # regex match (prefix ~)
    set_state: <state>      # state to assign when all conditions match; required
```

### Log-plane fields (`match: log`)

| Field | Type | Description |
|---|---|---|
| `level` | string | Log event level (`error`, `warn`, `info`, …) |
| `message` | string / regex | Log event message |
| `container` | string / glob | Container name |
| `namespace` | string / glob | K8s namespace (empty for Docker) |
| `runtime` | `docker` \| `k8s` | Runtime |
| `count_in_window` | int comparison | Error count in the last `window` |
| `window` | duration | Lookback window for `count_in_window` (e.g. `3m`) |

### Infra-plane fields (`match: infra`)

| Field | Type | Description |
|---|---|---|
| `runtime` | `docker` \| `k8s` | Runtime |
| `restart_count` | int comparison | Cumulative K8s restart count |
| `uptime` | duration comparison | Time since current container instance started |
| `namespace` | string / glob | K8s namespace |
| `container` | string / glob | Container name |
| `phase` | string | K8s pod phase (`Running`, `Pending`, …) |

### Valid states

| State | Plane | Description |
|---|---|---|
| `OK` | both | No issues |
| `HAS_ERRORS` | log | One or more error-level events observed |
| `FAILING` | log | Error rate exceeds threshold (escalation of HAS_ERRORS) |
| `RESTARTING` | infra | Container is actively crash-looping |
| `DEGRADED` | infra | Infra issue that does not indicate a crash |

### Default built-in rules (shipped, lowest priority, overridable)

Two default policies are shipped out of the box:

1. **Error/Failing state** — log-plane rules that classify containers as `HAS_ERRORS` (any error/warn event) or `FAILING` (error rate exceeds threshold).
2. **Restarting state** — infra-plane rule that classifies a K8s container as `RESTARTING` when it has recent restarts within a short uptime window.

```yaml
rules:
  - name: builtin-log-error
    priority: 100
    match: log
    when:
      level: error
    set_state: HAS_ERRORS

  - name: builtin-log-warn
    priority: 90
    match: log
    when:
      level: warn
    set_state: HAS_ERRORS

  - name: builtin-failing
    priority: 110          # higher than builtin-log-error — catches escalation first
    match: log
    when:
      level: error
      count_in_window: ">= 5"
      window: 3m
    set_state: FAILING

  - name: builtin-k8s-restarting
    priority: 100
    match: infra
    when:
      runtime: k8s
      restart_count: "> 0"
      uptime: "< 2m"
    set_state: RESTARTING
```

User rules with priority > 110 override all built-ins. User rules with priority < 100 act
as fallbacks.

---

## Architecture

### New package: `internal/pbr`

Owns all rule loading, validation, and evaluation. No other package imports it except
`internal/health`, `internal/discovery`, and `cmd/`.

```
internal/pbr/
    types.go        — Rule, Condition, MatchContext, EvalResult structs
    loader.go       — Load(cfg *config.Config) ([]Rule, error); validates uniqueness of priority
    condition.go    — Condition evaluation: equality, comparison, regex, glob, duration
    evaluator.go    — Evaluate(rules []Rule, ctx EvalContext) (state string, matchedRule string)
    defaults.go     — builtinRules() []Rule
    export_test.go
    pbr_test.go
```

### Changes to existing packages

#### `internal/config`

Add `Rules []RuleConfig` to `Config`. Existing `Detection` and `Containers` fields remain
for backward compatibility during transition; they are mapped to built-in rules at load
time.

```go
type RuleConfig struct {
    Name     string            `mapstructure:"name"`
    Priority int               `mapstructure:"priority"`
    Match    string            `mapstructure:"match"`
    When     map[string]string `mapstructure:"when"`
    SetState string            `mapstructure:"set_state"`
}
```

#### `internal/health/engine.go`

`ProcessBatch` currently hardcodes `level == "error" || level == "warn"`. Replace with a
call to `pbr.Evaluate(rules, LogEvalContext{event})`. The engine receives the compiled rule
set at construction time via `NewEngine(snapshotPath, rules, onChange)`.

#### `internal/health/tier2.go`

`Tier2Evaluator` hardcodes the `count_in_window >= threshold → FAILING` logic. Replace
with log-plane rule evaluation using Loki count as the `count_in_window` field value.
`Tier2Evaluator` is effectively replaced by the PBR evaluator loop — it may be removed or
reduced to a thin scheduler.

#### `internal/discovery/k8s_list.go`

Remove hardcoded `infraStatus = "restarting"` block. Call
`pbr.Evaluate(rules, InfraEvalContext{containerMeta})` to obtain `infraStatus`.

---

## Config file migration

Old config fields (`detection.severity_patterns`, `detection.tier2`) remain parseable.
On load, `pbr.Loader` translates them into equivalent rules at priority < 100 (below
built-ins). A deprecation warning is logged at startup if the old fields are present.
This gives existing users a non-breaking upgrade path.

---

## Atomic Tasks

Tasks are grouped by dependency tier. All tasks within a tier are independent.

---

### Tier 1 — Core PBR package (no external dependencies)

#### T1.1 — Define types
- `Rule` struct: Name, Priority, Match, Conditions, SetState
- `Condition` struct: Field, Operator (eq / gt / lt / gte / lte / regex / glob), Value
- `LogEvalContext` struct: wraps `ingest.LogEvent` + `count_in_window int` + `window time.Duration`
- `InfraEvalContext` struct: wraps `discovery.ContainerMeta`
- `EvalResult` struct: State string, MatchedRule string (empty = default applied)

#### T1.2 — Implement condition evaluator (`condition.go`)
- `EvalCondition(c Condition, ctx EvalContext) bool`
- Supported operators: `eq`, `gt`, `lt`, `gte`, `lte`, `regex`, `glob`
- Duration fields (`uptime`, `window`) parsed with `time.ParseDuration`
- Regex compiled once at load time (not per-evaluation)
- All operator/type mismatches return false (not panic)

#### T1.3 — Implement rule evaluator (`evaluator.go`)
- `Evaluate(rules []Rule, ctx EvalContext) EvalResult`
- Rules pre-sorted descending by priority at load time (not on each call)
- First rule where all conditions match → return `EvalResult{State, RuleName}`
- No match → return `EvalResult{State: ""}` (caller applies its default)

#### T1.4 — Implement loader (`loader.go`)
- `Load(ruleCfgs []config.RuleConfig, builtins []Rule) ([]Rule, error)`
- Parses `when` map into typed `[]Condition`
- Validates: name non-empty, priority unique (error names both conflicts), match is `log`
  or `infra`, set_state is a known state, all field names are valid for the declared match
  context
- Merges user rules with built-ins; sorts by descending priority
- Compiles all regex conditions

#### T1.5 — Define built-in rules (`defaults.go`)
- `BuiltinRules() []Rule` — returns the default rule set described in the schema section above
- Called by loader when no user rules override them
- Covered by unit tests asserting the exact set

---

### Tier 2 — Config integration (depends on T1.4)

#### T2.1 — Add `Rules []RuleConfig` to `internal/config`
- New field on `Config`; zero value = empty slice (built-ins apply)
- Existing `Detection` fields remain; loader translates them to rules for backward compat

#### T2.2 — Backward-compat migration in loader
- If `detection.severity_patterns.error` non-empty: generate `match: log` rules at
  priority 50 (below built-ins) mapping each pattern to `HAS_ERRORS`
- If `detection.tier2` non-empty: generate a `match: log` + `count_in_window` rule at
  priority 55
- Log deprecation warning once at startup if either old field is present

---

### Tier 3 — Health engine integration (depends on T1.3, T2.1)

#### T3.1 — Thread rule set into health engine
- `NewEngine(snapshotPath string, rules []pbr.Rule, onChange func(HealthSnapshot)) *Engine`
- Store compiled rules on engine struct

#### T3.2 — Replace hardcoded log classification in `ProcessBatch`
- Replace `ev.Level == "error" || ev.Level == "warn"` with
  `pbr.Evaluate(e.rules, pbr.LogEvalContext{Event: ev})`
- State returned from evaluator determines whether the event is health-degrading and which
  state to set

#### T3.3 — Replace Tier2Evaluator hardcoded logic
- `Tier2Evaluator.evaluate` fetches Loki count then calls
  `pbr.Evaluate(e.rules, pbr.LogEvalContext{Event: syntheticEvent, CountInWindow: n})`
- If a rule matches with `FAILING` → transition container to FAILING
- If no rule matches FAILING but container is FAILING → check if it should revert to
  HAS_ERRORS (count dropped below threshold of the matching rule)

---

### Tier 4 — Discovery integration (depends on T1.3, T2.1)

#### T4.1 — Thread rule set into reconciler
- `NewReconciler(..., rules []pbr.Rule, ...)` — add rules parameter
- Pass to `ListRunningK8s` or evaluate in reconciler after listing

#### T4.2 — Replace hardcoded infraStatus in `k8s_list.go`
- Remove `recentRestartWindow` constant and the time-window check
- After building `ContainerMeta`, call
  `pbr.Evaluate(rules, pbr.InfraEvalContext{Container: meta})`
- Use returned state as `InfraStatus`; empty result → `"running"`

---

### Tier 5 — Unit tests (depends on T1–T4; independent of each other)

#### T5.1 — Condition evaluator tests
- Equality, gt/lt/gte/lte for int and duration fields
- Regex match and non-match
- Glob match and non-match
- Unknown field → false (no panic)
- Type mismatch → false (no panic)

#### T5.2 — Rule evaluator tests
- First-match-wins: two matching rules, higher priority wins
- No-match: returns empty state
- Infra context: RESTARTING rule matches only within uptime window
- Log context: FAILING rule matches only when count_in_window threshold met

#### T5.3 — Loader tests
- Duplicate priority → error naming both rules
- Unknown match context → error
- Unknown set_state → error
- Unknown field for match context → error
- Valid config → sorted rule slice
- Backward-compat: detection.tier2 → equivalent rule generated

#### T5.4 — Integration: health engine with PBR rules
- Engine with custom rules classifies log events per rule, not hardcoded level check
- Engine with no user rules falls back to built-ins

#### T5.5 — Integration: discovery with PBR rules
- K8s container with restart_count=1, uptime=1m → RESTARTING
- K8s container with restart_count=1, uptime=5m → running (outside window)
- Custom infra rule overriding built-in

---

### Tier 6 — CLI / observability (depends on T1–T4)

#### T6.1 — `ep check --explain <container>`
- New flag on `ep check`
- Prints which rule fired for each container (or "no rule matched — default applied")
- Uses `EvalResult.MatchedRule` captured during the last evaluation cycle

#### T6.2 — Startup validation error reporting
- On duplicate priority: `errorprobe up` / `errorprobe watch` exit immediately with a
  clear message listing conflicting rule names and their priorities
- On unknown field / state: same — do not silently ignore

---

## Non-goals for this phase

- GUI / web rule editor
- Rule hot-reload without restart (rules are compiled at startup; `ep reload` handles this)
- Per-container rule overrides (all rules apply to all containers; use `when.container`
  conditions for scoping)
- Alert routing / notification rules (separate concern)
