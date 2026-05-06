# Phase 5 — K8s Discovery (V1 Follow-on)

**Goal:** Extend discovery to local Kubernetes clusters. The log pipeline and health engine are unchanged — only the discovery mechanism is extended.  
**Prerequisite:** Phase 4 complete. Local K8s available (Docker Desktop K8s, k3s, or minikube).

**UT coverage requirement: ≥ 90% on all new packages and functions.**

---

## Known Issues Carried from Phase 4

### `status --reset` is overwritten immediately while `up` is running

**Observed during Phase 4 manual testing (2026-04-30).**

`errorprobe status --reset <container>` writes the cleared state directly to
`~/.errorprobe/state/health.json` on disk. However, the health engine running
inside `errorprobe up` maintains its own in-memory state and continuously
overwrites that file as new log events arrive. If the container is still
emitting errors, the reset is immediately overridden — within milliseconds —
and the next `status` read shows `HAS_ERRORS` again.

**Expected behaviour (V1):** Reset is intentionally advisory — it acknowledges
a known issue for the current snapshot. It works correctly when `up` is not
running (e.g., CI scripts that read a past snapshot offline). The live override
is by design in V1.

**Correct fix target (Phase 6 or later):** Add an IPC mechanism (e.g., a Unix
socket or named pipe command channel) so `status --reset` sends the reset
directly to the running engine's in-memory state rather than patching the file.
Until then, document clearly in `errorprobe status --help` that reset takes
effect permanently only when the engine is not running.

---

## Atomic Tasks

Tasks are grouped by dependency tier. All tasks within a tier can be implemented independently and in parallel.

---

### Tier 1 — K8s client (no Phase 5 dependencies beyond Phase 4)

#### T5.1 — Implement K8s API client wrapper
- `internal/k8s` package (new)
- `NewClient(kubeconfigPath string) (*Client, error)` — loads kubeconfig; uses `client-go` `clientcmd.BuildConfigFromFlags`
- Auto-detect path: use `KUBECONFIG` env → `~/.kube/config` → in-cluster config (future)
- `Client.Ping(ctx context.Context) error` — calls `ServerVersion()` to verify connectivity
- `Client.IsAvailable(ctx context.Context) bool` — non-error wrapper for Ping; used for auto-detect
- Public API design mirrors `internal/docker` client for consistency

#### T5.2 — Implement K8s container lister
- `ListRunning(ctx context.Context, k8sClient K8sAPI) ([]discovery.ContainerMeta, error)` — separate function in `internal/discovery` (not a method on existing Docker lister)
- Lists all pods in all namespaces (`client-go` `PodList`)
- Filters: only pods in `Running` phase; only containers in `Running` state (not init containers)
- Maps to `ContainerMeta`:
  - `Name`: `pod-name/container-name`
  - `Image`: container image
  - `Labels`: pod labels
  - `StartedAt`: container `startedAt` from pod status
  - `RestartCount`: from container status
  - `InfraStatus`: `"running"` | `"restarting"` (restartCount increasing) | `"exited"`
  - `Runtime`: `"k8s"`
  - New fields: `Pod`, `Namespace`, `Node` (extend `ContainerMeta` struct)
- Excludes system namespaces by default: `kube-system`, `kube-public`, `kube-node-lease`; configurable via `errorprobe.yaml` (new field: `k8s.exclude_namespaces`)

---

### Tier 2 — Runtime auto-detect and merge (depends on T5.1, T5.2)

#### T5.3 — Implement runtime auto-detect
- `DetectRuntimes(ctx, dockerClient docker.DockerAPI, k8sClient k8s.K8sAPI) RuntimeSet` in `internal/discovery`
- `RuntimeSet`:
  ```go
  type RuntimeSet struct {
      DockerAvailable bool
      K8sAvailable    bool
  }
  ```
- Calls `docker.Ping` and `k8s.IsAvailable` independently; populates flags
- Both can be true simultaneously (Docker Desktop runs K8s on Docker)

#### T5.4 — Merge Docker and K8s watch sets
- `MergeContainers(docker []ContainerMeta, k8s []ContainerMeta) []ContainerMeta` in `internal/discovery`
- Concatenates both slices; sorts by `Runtime` then `Name` for stable output
- No deduplication needed — Docker containers and K8s containers are distinct objects even on the same host
- Update `ApplyPolicy` to handle K8s-specific exclusion patterns: `pod/<name>` and `namespace/<name>` prefix syntax

---

### Tier 3 — K8s watch policy extension (depends on T5.2)

#### T5.5 — Extend watch policy for K8s patterns
- Extend `ApplyPolicy` to handle two new exclusion pattern prefixes:
  - `pod/<glob>` — match against `ContainerMeta.Pod`
  - `namespace/<glob>` — match against `ContainerMeta.Namespace`
  - Unprefixed patterns continue to match against `ContainerMeta.Name` (Docker behaviour unchanged)
- Example config:
  ```yaml
  containers:
    exclude:
      - "sidecar-*"            # name match (Docker + K8s)
      - "namespace/kube-*"     # K8s namespace match
      - "pod/debug-*"          # K8s pod name match
  ```

---

### Tier 4 — Vector K8s log source (depends on T5.2)

#### T5.6 — Extend Vector config generator for K8s sources
- Extend `GenerateVector` to accept `k8sContainers []ContainerMeta` alongside `dockerContainers []ContainerMeta`
- When K8s containers present: add `[sources.k8s_logs]` of type `kubernetes_logs`; filter by pod name list or label selectors
- Shared VRL transform pipeline applies to both sources (normalise → level inference → emit to Loki + ingest)
- Loki label additions for K8s events: `pod`, `namespace`
- Both sources feed the same `[sinks.loki]` and `[sinks.errorprobe_ingest]`

---

### Tier 5 — Reconciler extension (depends on T5.3, T5.4, T5.6)

#### T5.7 — Extend reconciliation loop for K8s
- Extend `Reconciler` to accept optional `k8sClient k8s.K8sAPI`
- On each tick: if K8s available, call K8s `ListRunning`; merge with Docker results via `MergeContainers`
- Policy applied to merged set via extended `ApplyPolicy`
- `GenerateVector` called with both Docker and K8s container lists
- SIGHUP sent to Vector on any change to the merged set (same as before)
- Persisted `WatchSet` now contains mixed `docker` and `k8s` containers

---

### Tier 6 — CLI extensions (depends on T5.7, T5.3)

#### T5.8 — Extend `errorprobe list` for K8s
- Add `RUNTIME` column: `docker` | `k8s`
- For K8s containers: show `POD` and `NAMESPACE` columns (or combined `pod/namespace` in single column)
- `--runtime docker` / `--runtime k8s` filter flags

#### T5.9 — Extend `errorprobe watch` TUI for K8s
- When both runtimes active: group rows by runtime with section headers
- K8s container rows: show pod name and namespace as subtitle or in additional columns
- No change to health state machine — `ContainerHealth` key is still container name

---

### Tier 7 — Unit tests (depends on T5.1–T5.6; independent of each other)

#### T5.10 — Unit tests: `internal/k8s` client
- Use mock K8s client (interface-based; no live cluster required)
- `TestPing_Available_NoError`: mock ServerVersion succeeds; assert nil error
- `TestPing_Unavailable_Error`: mock ServerVersion fails; assert error
- `TestIsAvailable_True`: Ping succeeds; assert true
- `TestIsAvailable_False`: Ping fails; assert false (no panic, no error propagation)

#### T5.11 — Unit tests: K8s container lister
- `TestListRunning_K8s_RunningPodsReturned`: mock PodList with 2 running pods; assert 2 ContainerMeta returned
- `TestListRunning_K8s_NonRunningFiltered`: pod in Pending phase; assert excluded
- `TestListRunning_K8s_SystemNamespacesExcluded`: pod in `kube-system`; assert excluded
- `TestListRunning_K8s_MetadataMapping`: assert Pod, Namespace, Node, RestartCount correctly mapped
- `TestListRunning_K8s_RuntimeField`: assert `Runtime == "k8s"` on all returned entries

#### T5.12 — Unit tests: runtime detection and merge
- `TestDetectRuntimes_BothAvailable`: both clients ping successfully; assert DockerAvailable and K8sAvailable true
- `TestDetectRuntimes_K8sUnavailable`: K8s ping fails; assert K8sAvailable false, DockerAvailable true
- `TestMergeContainers_CombinesBoth`: 2 Docker + 2 K8s; assert 4 total, sorted by runtime then name
- `TestMergeContainers_EmptyK8s`: empty K8s list; assert Docker containers returned unchanged

#### T5.13 — Unit tests: extended watch policy
- `TestApplyPolicy_PodExclusion`: `pod/debug-*` pattern; assert matching K8s containers excluded
- `TestApplyPolicy_NamespaceExclusion`: `namespace/kube-*`; assert kube-system containers excluded
- `TestApplyPolicy_DockerUnaffectedByK8sPattern`: K8s pattern; assert Docker containers not filtered
- `TestApplyPolicy_MixedPatterns`: both Docker name and K8s namespace patterns; assert both applied correctly

#### T5.14 — Unit tests: Vector K8s config generation
- `TestGenerateVector_K8sSourceIncluded`: K8s containers provided; assert `[sources.k8s_logs]` present in output
- `TestGenerateVector_K8sLabelsInLoki`: K8s containers provided; assert `pod` and `namespace` labels in Loki sink config
- `TestGenerateVector_DockerOnlyUnchanged`: empty K8s list; assert no K8s source in output
- `TestGenerateVector_BothSourcesPresent`: Docker + K8s containers; assert both sources, shared sinks

---

### Final Task

#### T5.15 — Mark phase complete in work-plan.md
- Open `docs/work-plan.md`
- Mark all Phase 5 tasks as `[x]`
- Add completion date next to phase heading

#### T5.16 — Update roadmap.html
- Open `docs/roadmap.html` in a browser and verify Phase 5 is reflected correctly
- In the `PHASES` array, set Phase 5's `status` to `"completed"` and `actualEnd` to the actual finish date
- Compare actual duration against the planned estimate; if velocity differed, adjust `start` / `end` for all subsequent phases accordingly
- Update the `TODAY` constant to the current date
- Recompute and document the revised total story-point burn rate and projected completion date for the remaining phases in a comment above the `PHASES` array
- Verify the **V1 Complete** milestone date in `MILESTONES` still matches reality; correct it if the phase finished earlier or later than `2026-07-18`

---

## Deliverables

| Deliverable | Description |
|---|---|
| `internal/k8s` | K8s API client wrapper with ping and availability check |
| K8s `ListRunning` | Pod/container discovery in `internal/discovery` |
| `DetectRuntimes`, `MergeContainers` | Runtime auto-detect and set merge |
| Extended `ApplyPolicy` | Pod and namespace exclusion pattern support |
| Extended `GenerateVector` | K8s log source added to Vector config |
| Extended `Reconciler` | K8s discovery integrated into reconciliation loop |
| `cmd/list.go` | Runtime column, pod/namespace columns, `--runtime` filter |
| `cmd/watch.go` TUI | Grouped by runtime, pod/namespace context for K8s rows |
| Unit tests | ≥ 90% coverage on `k8s`, extended `discovery`, extended `configgen` |

---

## Manual Tests

Run after all tasks are complete, with Docker Desktop K8s enabled and at least 2 pods deployed (one emitting errors):

1. **Auto-detect** — run `errorprobe up` with both Docker containers and K8s pods active; confirm startup summary shows both runtimes detected.
2. **`errorprobe list`** — shows both Docker containers and K8s pods; `RUNTIME` column correctly shows `docker` / `k8s`; K8s rows show pod and namespace.
3. **`errorprobe list --runtime k8s`** — only K8s containers shown.
4. **`errorprobe list --runtime docker`** — only Docker containers shown.
5. **Namespace exclusion** — add `namespace/kube-system` to `containers.exclude`; confirm `errorprobe list` shows no kube-system pods.
6. **Pod exclusion** — add `pod/debug-*` to `containers.exclude`; confirm debug pods excluded.
7. **Logs flowing** — open Grafana Explore; query `{runtime="k8s"}`; confirm K8s pod log lines appear with `pod` and `namespace` labels.
8. **Error detection — K8s** — deploy a pod that emits `ERROR` lines; `errorprobe watch` shows it as `⚠ HAS ERRORS` within 2 seconds.
9. **Original pain case** — the scenario from `intent.md`: N pods running, all showing K8s infrastructure-healthy (green in K8s dashboard), one emitting errors silently; `errorprobe watch` surfaces it. This is the primary acceptance test for the entire V1.
10. **`errorprobe check` with K8s error pod** — exits 1; names the K8s pod correctly.
11. **`go test ./... -cover`** — all tests pass; coverage ≥ 90% on `internal/k8s`, extended `internal/discovery`.
