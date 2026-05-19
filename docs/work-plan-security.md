# Work Plan — Security Hardening

**Source:** Code-quality review, May 2026.  
**Scope:** Top 10 security issues identified across the codebase, ordered by severity.  
**References:** OWASP Top 10 (2021) — A01 Broken Access Control, A03 Injection, A05 Security Misconfiguration, A06 Vulnerable Components, A10 SSRF.

---

## Issues Inventory

| # | Severity | OWASP | Location | Summary |
|---|---|---|---|---|
| V1 | CRITICAL | A01 | `internal/ingest/http.go` | No authentication on `/ingest` endpoint |
| V2 | HIGH | A03 | `internal/pbr/loader.go` | Unchecked user regex → ReDoS |
| V3 | HIGH | A05 | `internal/loki/client.go` | Loki responses decoded without body size limit |
| V4 | HIGH | A10 | `internal/loki/client.go` | SSRF via unvalidated `baseURL` |
| V5 | MEDIUM | A05 | `internal/health/persist.go`, `history.go` | State and history files written world-readable (0o644) |
| V6 | MEDIUM | A03 | `internal/configgen/vector.go` | Incomplete VRL template escaping for container names |
| V7 | MEDIUM | A05 | `internal/ingest/http.go` | No request rate limiting on ingest endpoint |
| V8 | MEDIUM | A05 | `internal/docker/client.go` | Docker socket redirectable via environment variables |
| V9 | LOW | A03 | `internal/learn/overlay.go` | Learned-rule patterns written to YAML without full sanitisation |
| V10 | LOW | A05 | `internal/health/history.go` | `atomicReplace` not atomic on Windows (TOCTOU) |

---

## Atomic Tasks

Tasks are grouped by dependency tier. All tasks within a tier are independent and may be parallelised.

---

### Tier 1 — Independent

#### V1 — Authenticate the `/ingest` endpoint

**File:** `internal/ingest/http.go`

**Problem:** Any process that can reach the ingest HTTP listener can POST arbitrary log events and manipulate health state. The default `127.0.0.1` bind mitigates but does not prevent exploitation from other local processes or misconfigured deployments.

**Fix:**
- Add a shared-secret HMAC-SHA256 request authentication scheme.
- On `NewHTTPTransport`, accept an optional `secret []byte` parameter. When non-empty, validate the `X-ErrorProbe-Signature: sha256=<hex>` header against an HMAC-SHA256 of the raw request body.
- Vector's HTTP sink supports custom headers — document the configuration in user-guide.md.
- When `secret` is empty (default), behaviour is unchanged (backward-compatible); emit a startup `logger.Warn` advising that the endpoint is unauthenticated.

**Config addition:**
```yaml
stack:
  ingest:
    secret: "changeme"   # optional; shared with Vector's HTTP sink Authorization header
```

**Tests:**
- Request with valid signature → `204 No Content`.
- Request with invalid signature → `401 Unauthorized`.
- Request with no header when secret is configured → `401`.
- Request with no header when secret is empty → `204` (backward compat).

---

#### V2 — Guard PBR rule regex compilation against ReDoS

**File:** `internal/pbr/loader.go`

**Problem:** `regexp.Compile` on user-supplied patterns has no length or complexity guard. A crafted pattern (e.g., `(a+)+$`) causes catastrophic backtracking on every log event, freezing the engine goroutine.

**Fix:**
- Enforce a maximum pattern length (e.g., 512 bytes) before calling `regexp.Compile`.
- Use `regexp/syntax.Parse` with `regexp/syntax.ClassNL` to inspect the compiled AST for known problematic constructs (deeply nested repetitions). Reject patterns whose `regexp/syntax` tree depth exceeds a threshold (e.g., 10 levels of `OpRepeat` nesting).
- Alternatively: evaluate patterns inside a goroutine with a short timeout (100 ms) using `context`-cancelled matching via `regexp.MatchString` with a wrapper; reject any pattern that takes longer than the timeout on a benign sample string.
- Return a descriptive validation error from `Load`.

**Tests:** table-driven tests with benign patterns (pass), oversized patterns (reject), and a known-bad ReDoS pattern (reject); assert `Load` returns an error, not a hang.

---

#### V3 — Limit Loki response body size

**File:** `internal/loki/client.go`

**Problem:** `json.NewDecoder(resp.Body).Decode()` reads an unbounded response body. A misconfigured or malicious Loki can return gigabytes of data, exhausting process memory.

**Fix:**
- Wrap `resp.Body` with `io.LimitReader` before decoding:
```go
const maxLokiResponseBytes = 32 * 1024 * 1024 // 32 MB
body := io.LimitReader(resp.Body, maxLokiResponseBytes)
if err := json.NewDecoder(body).Decode(&result); err != nil { ... }
```
- Apply to both `QueryRange` and any other response-reading paths in the file.
- If the limit is hit the decoder will return an `io.ErrUnexpectedEOF`; surface this as a descriptive error.

**Tests:** mock HTTP server returning a body larger than the limit; assert `QueryRange` returns an error, not a memory spike.

---

#### V4 — Validate `baseURL` before use in Loki client

**File:** `internal/loki/client.go`

**Problem:** The `baseURL` string is interpolated directly into request URLs. A value pointing to a cloud metadata service (e.g., `http://169.254.169.254`) causes SSRF.

**Fix:**
- Parse `baseURL` in `NewClient` using `url.Parse` and validate:
  - Scheme must be `http` or `https`.
  - Host must not be a link-local address (`169.254.x.x`, `::1`, `fe80::/10`).
  - Host must not be empty.
- Return an error from `NewClient` (change signature from `*Client` to `(*Client, error)`).
- Update all callers in `cmd/up.go` and `internal/health/tier2.go`.

**Tests:** assert `NewClient` rejects link-local addresses, empty host, and non-http/https schemes; assert valid URLs are accepted.

---

#### V5 — Write state files with restricted permissions (0o600)

**Files:** `internal/health/persist.go`, `internal/health/history.go`

**Problem:** `os.WriteFile(..., 0o644)` makes health snapshot and history log readable by all local users. These files contain container names, error messages, and timestamps.

**Fix:**
- Change all `os.WriteFile` and `os.OpenFile` calls in these two files from `0o644` to `0o600`.
- Apply the same restriction to the temp files (`path + ".tmp"`).
- Audit any other file-writing paths in the codebase (`grep -r "0o644" ./internal`) and apply the same fix where the file content is sensitive.

**Tests:** after `SaveSnapshot` and `HistoryLog.Append`, stat the written file and assert `mode.Perm() == 0o600`.

---

#### V6 — Harden VRL template escaping for container names

**File:** `internal/configgen/vector.go`, `assets/templates/vector.toml.tmpl`

**Problem:** `escapeVRLPattern` only escapes single-quote characters. Container names with other VRL regex metacharacters (`\`, `(`, `)`, `[`, `]`, `^`, `$`, `.`, `*`, `+`, `?`, `{`, `}`, `|`) can break out of the `r'...'` regex literal context in the generated `vector.toml`.

**Fix:**
- Replace `escapeVRLPattern` with a full metacharacter escaper covering all characters that have special meaning inside VRL regex literals.
- Alternatively, switch the VRL template to use an exact string match (`==`) instead of a regex match where the intent is an exact container name comparison, eliminating the regex escaping problem entirely.
- Add a property-based test: for any container name, the generated TOML must parse without error.

**Tests:** table-driven tests with container names containing every regex metacharacter; assert the generated TOML is syntactically valid and matches only the intended container name.

---

#### V7 — Rate-limit the `/ingest` endpoint

**File:** `internal/ingest/http.go`

**Problem:** No per-connection or global request rate limit exists. A local process flooding the endpoint with small, valid requests can saturate the `ProcessBatch` lock and stall health monitoring.

**Fix:**
- Add a `golang.org/x/time/rate` token-bucket limiter to `HTTPTransport` (configurable via constructor option; default 1000 req/s).
- When the limit is exceeded, return `429 Too Many Requests` immediately.
- Expose `WithRateLimit(rps int)` functional option on `NewHTTPTransport`.

**Tests:** assert that requests beyond the configured rate receive `429`; assert requests within the rate receive `204`.

---

#### V8 — Validate Docker host from environment at startup

**File:** `internal/docker/client.go`

**Problem:** `dockerclient.FromEnv` reads `DOCKER_HOST` without validation. A tampered environment can redirect all Docker API calls.

**Fix:**
- After constructing the client, log the resolved `cli.DaemonHost()` at `Info` level so operators can verify the endpoint.
- If `DOCKER_HOST` is set, validate its URI scheme is one of `unix`, `npipe`, `tcp`, or `ssh`; return an error for any other scheme.
- Document the expected values in user-guide.md.

**Tests:** set `DOCKER_HOST` to an invalid scheme in the test environment; assert `NewClient` returns an error.

---

### Tier 2 — Depends on prior work or lower urgency

#### V9 — Sanitise learned-rule patterns before writing to YAML

**File:** `internal/learn/overlay.go`

**Problem:** `LearnedRule.When` values are derived from raw log messages and then serialised to YAML. Log messages can contain YAML block indicators (`|`, `>`) and other control characters that, while handled by a correct YAML encoder, represent an injection surface if the encoder is ever swapped or if the file is manually edited.

**Fix:**
- Before writing a `LearnedRule`, validate that each `When` value is a printable ASCII or UTF-8 string with no YAML block/flow indicators in key positions.
- Sanitise by stripping or replacing non-printable characters and lone `\n`/`\r` in pattern values.
- Use `gopkg.in/yaml.v3` (already a transitive dependency) explicitly, which encodes special characters correctly; add a comment to the overlay write function noting this dependency.

**Tests:** round-trip test: write a `LearnedRule` with special characters, reload it, assert the loaded `When` value matches the original.

---

#### V10 — Make `atomicReplace` truly atomic on Windows

**File:** `internal/health/history.go`

**Problem:** The current `os.Remove(dst)` + `os.Rename(src, dst)` pattern has a TOCTOU window on Windows where `dst` does not exist between the two calls. A concurrent reader of the history file will receive `ENOENT`.

**Fix:**
- On Windows, use `MoveFileExW` with `MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH` via `golang.org/x/sys/windows` to perform a true atomic replacement.
- Abstract behind a `//go:build windows` / `//go:build !windows` pair mirroring the existing `console_windows.go` / `console_nonwin.go` pattern in `cmd/`.
- Expose `atomicReplaceFile(dst, src string) error` as a package-private function in a new `internal/atomicfile` package so both `health/persist.go` and `health/history.go` share the implementation.

**Tests:** on Windows CI, assert that a concurrent reader never receives `ENOENT` during a replace cycle.

---

## Acceptance Criteria

- `golangci-lint run --enable gosec ./...` reports zero new HIGH/CRITICAL findings.
- All existing tests pass; new tests added for each task achieve ≥ 85% branch coverage on the changed functions.
- A manual threat-model review confirms V1–V4 are resolved before the next public release.
- File permission changes (V5) are verified by CI via a file-stat assertion in the relevant `_test.go` files.
