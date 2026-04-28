# Phase 8 — Remote Docker & Kubernetes (V2)

**Goal:** Extend discovery and collection to remote Docker hosts and remote Kubernetes clusters.  
**Prerequisite:** Phase 7 complete.

**UT coverage requirement: ≥ 90% on all new packages and functions.**

---

## Atomic Tasks

Tasks are grouped by dependency tier. All tasks within a tier can be implemented independently and in parallel.

---

### Tier 1 — Independent implementations (no Phase 8 dependencies beyond Phase 7)

#### T8.1 — Implement remote Docker client
- Extend `internal/docker.NewClient` to accept an optional `host string` parameter
- When `host` is non-empty: use `client.WithHost(host)` — supports `tcp://`, `ssh://` URI schemes
- When `host` is empty: existing local behaviour (auto-detect Windows named pipe or Unix socket)
- Source from `errorprobe.yaml`:
  ```yaml
  docker:
    host: "tcp://192.168.1.100:2376"   # optional; empty = local
  ```
- TLS client certificate support: optional fields `tls_cert`, `tls_key`, `tls_ca` in yaml (paths to files)
- `Client.IsRemote() bool` — used by other components to switch behaviour

#### T8.2 — Implement remote K8s context selection
- Extend `internal/k8s.NewClient` to accept a `contextName string` parameter
- When non-empty: use `clientcmd.NewNonInteractiveDeferredLoadingClientConfig` with explicit context name
- When empty: existing behaviour (current-context in kubeconfig)
- Source from `errorprobe.yaml`:
  ```yaml
  k8s:
    context: "my-remote-cluster"   # optional; empty = current-context
  ```

#### T8.3 — Implement bearer token auth for ingest endpoint
- `GenerateToken() (string, error)` in `internal/ingest` — generates 32-byte cryptographically random token, base64url-encoded
- `SaveToken(path string, token string) error` — writes to `~/.errorprobe/state/token` with restricted file permissions (0600)
- `LoadToken(path string) (string, error)` — reads token from file
- Token is generated once on first `errorprobe up` in remote mode; reused on subsequent runs
- `HTTPTransport` and `GRPCTransport` extended to validate `Authorization: Bearer <token>` header when `cfg.Stack.Ingest.Bind != "127.0.0.1"`
- Requests without valid token on non-localhost binding → HTTP 401 / gRPC Unauthenticated; request rejected before handler called

---

### Tier 2 — Remote mode detection and ingest binding (depends on T8.1, T8.2, T8.3)

#### T8.4 — Remote mode auto-configuration
- `IsRemoteMode(cfg *config.Config) bool` — returns true if `docker.host` or `k8s.context` is set in config
- When remote mode: override `cfg.Stack.Ingest.Bind` to `0.0.0.0` unless user has explicitly set it
- Warn on startup if remote mode active but bind is `127.0.0.1` (user explicitly set it — proceed but warn that remote Vector cannot reach the ingest endpoint)
- Load or generate token on startup when remote mode active

---

### Tier 3 — Remote Vector deployment (depends on T8.1, T8.2, T8.3, T8.4)

#### T8.5 — Implement `errorprobe remote-config export` command
- New command: `cmd/remote-config.go`
- Subcommand: `errorprobe remote-config export --output <dir>`
- Generates a standalone Vector config bundle in `<dir>`:
  - `vector.toml` — configured to read Docker logs or K8s logs on the remote host
  - `docker-compose.yml` — runs Vector as a container on the remote host
  - `k8s-daemonset.yaml` — deploys Vector as a DaemonSet in the remote cluster
  - `README.md` — instructions for deploying on the remote host
- The generated configs embed:
  - ErrorProbe's ingest endpoint URL (developer's machine IP or hostname — prompted if not resolvable)
  - Bearer token from `~/.errorprobe/state/token`
  - Container/pod filter matching current watch policy
- User copies the bundle to the remote host and runs it; no other setup needed

#### T8.6 — Add remote host label to TUI and status
- Extend `ContainerMeta` with `RemoteHost string` field (empty for local)
- When remote Docker: `RemoteHost = cfg.Docker.Host`
- When remote K8s: `RemoteHost = cfg.K8s.Context`
- `errorprobe watch` TUI: group rows by `RemoteHost` / local; section headers show host/context label
- `errorprobe list`: add `HOST` column when any container has a non-empty `RemoteHost`

---

### Tier 4 — Classify remote config changes as hard (depends on T8.4)

#### T8.7 — Extend reload change classifier for remote fields
- Extend `ClassifyChanges` (Phase 4 T4.3):
  - `docker.host` change → HardChanges
  - `k8s.context` change → HardChanges
  - `stack.ingest.bind` change → HardChanges
- On `errorprobe reload` with hard remote change: recreate ingest transport with new binding + regenerate remote Vector config

---

### Tier 5 — Unit tests (depends on T8.1–T8.6; independent of each other)

#### T8.8 — Unit tests: remote Docker client
- `TestNewClient_RemoteHost_UsesHost`: non-empty host; assert client configured with that host
- `TestNewClient_EmptyHost_UsesLocal`: empty host; assert local auto-detect behaviour
- `TestIsRemote_True`: remote host set; assert true
- `TestIsRemote_False`: no remote host; assert false

#### T8.9 — Unit tests: remote K8s context
- `TestNewK8sClient_ExplicitContext_UsesContext`: context name provided; assert client uses that context
- `TestNewK8sClient_EmptyContext_UsesCurrentContext`: empty context; assert current-context used

#### T8.10 — Unit tests: bearer token auth
- `TestGenerateToken_Length`: assert token is 32+ chars
- `TestGenerateToken_Unique`: call twice; assert different tokens
- `TestSaveLoadToken_RoundTrip`: save, load; assert identical
- `TestHTTPTransport_ValidToken_Accepted`: request with correct Bearer token; assert handler called
- `TestHTTPTransport_InvalidToken_Returns401`: request with wrong token; assert 401; handler not called
- `TestHTTPTransport_MissingToken_Returns401`: request without Authorization header; assert 401
- `TestHTTPTransport_LocalHost_NoAuthRequired`: bind is `127.0.0.1`; request without token; assert handler called (no auth on localhost)

#### T8.11 — Unit tests: remote mode detection
- `TestIsRemoteMode_DockerHostSet_True`: docker.host set; assert true
- `TestIsRemoteMode_K8sContextSet_True`: k8s.context set; assert true
- `TestIsRemoteMode_NeitherSet_False`: both empty; assert false
- `TestRemoteModeAutoConfig_OverridesBindToAll`: remote mode active, bind not explicitly set; assert bind becomes `0.0.0.0`
- `TestRemoteModeAutoConfig_ExplicitBindRespected`: remote mode active, bind explicitly set; assert bind unchanged

#### T8.12 — Unit tests: reload classifies remote changes as hard
- `TestClassifyChanges_DockerHost_IsHard`: docker.host changes; assert HardChanges
- `TestClassifyChanges_K8sContext_IsHard`: k8s.context changes; assert HardChanges
- `TestClassifyChanges_IngestBind_IsHard`: bind address changes; assert HardChanges

---

### Final Task

#### T8.13 — Mark phase complete in work-plan.md
- Open `docs/work-plan.md`
- Mark all Phase 8 tasks as `[x]`
- Add completion date next to phase heading

#### T8.14 — Update roadmap.html
- Open `docs/roadmap.html` in a browser and mark Phase 8 as the final completed phase
- In the `PHASES` array, set Phase 8's `status` to `"completed"` and `actualEnd` to the actual finish date
- Update the `TODAY` constant to the current date
- Verify the **V2 Complete** milestone date in `MILESTONES` matches the actual Phase 8 finish date; correct it if needed
- Record the final total story-point burn rate (actual days per point across all phases) in a comment above the `PHASES` array for future planning reference

---

## Deliverables

| Deliverable | Description |
|---|---|
| Extended `internal/docker` | Remote host support via Docker TCP/SSH URI |
| Extended `internal/k8s` | Remote context selection via kubeconfig context name |
| Bearer token auth | Token generation, persistence, validation in both transports |
| Remote mode auto-configuration | Auto-bind to `0.0.0.0` and require auth when remote |
| `errorprobe remote-config export` | Generates Vector bundle for remote deployment |
| Extended `ContainerMeta` | `RemoteHost` field |
| TUI and `list` | Remote host grouping and HOST column |
| Extended `ClassifyChanges` | Remote config changes classified as hard |
| Unit tests | ≥ 90% coverage on all new and extended code |

---

## Manual Tests

Run after all tasks are complete. Requires a second machine or VM accessible over the network, with Docker running.

1. **Remote Docker** — set `docker.host: "tcp://<remote-ip>:2375"` in yaml; `errorprobe up`; confirm `errorprobe list` shows containers from the remote machine with `HOST` column.
2. **Auth required** — with remote mode active, attempt to POST to ingest endpoint without token; confirm HTTP 401.
3. **Auth token works** — with token from `~/.errorprobe/state/token`, POST a valid batch; confirm accepted and health state updates.
4. **`errorprobe remote-config export --output ./remote-bundle`** — inspect output directory; confirm `vector.toml`, `docker-compose.yml`, `k8s-daemonset.yaml`, and `README.md` are present; confirm Vector config embeds correct endpoint URL and token.
5. **Deploy Vector bundle on remote host** — copy bundle; run `docker compose up -d`; confirm within 60 seconds that logs from remote containers appear in local Grafana Explore.
6. **Remote K8s** — set `k8s.context: "<remote-context>"` in yaml; `errorprobe up`; confirm pods from that context appear in `errorprobe list` grouped under correct host label.
7. **TUI grouping** — with both local and remote containers active, `errorprobe watch` shows section headers separating local from remote containers.
8. **`errorprobe reload` — remote host change** — change `docker.host` to a different host; `errorprobe reload`; confirm "Hard changes applied"; new remote host's containers appear.
9. **`go test ./... -cover`** — all tests pass; coverage ≥ 90% on extended `internal/docker`, `internal/k8s`, `internal/ingest`.
