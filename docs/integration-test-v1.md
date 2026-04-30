# T4.13 — V1 End-to-End Integration Test Script

**Purpose:** Manual repeatability test for the V1 success criterion: "hours of debugging → 5 seconds".

Run from the repo root after building the binary (`go build -o ep.exe ./cmd/ep`).

---

## Pre-conditions

- Docker daemon running.
- No errorprobe stack running (`errorprobe down --purge` if needed).
- Binary on PATH or reference as `./ep.exe` below.

---

## Steps

### Step 1 — Clean state

```powershell
./ep.exe down --purge
```

**Expected output:** containers removed (or "not found" if none existed); volumes purged.

---

### Step 2 — Start known-broken container

```powershell
docker run -d --name broken-app alpine sh -c "while true; do echo 'ERROR database connection failed'; sleep 1; done"
```

**Expected output:** container ID printed; container running.

---

### Step 3 — Start healthy container

```powershell
docker run -d --name healthy-app alpine sh -c "while true; do echo 'INFO all good'; sleep 2; done"
```

**Expected output:** container ID printed; container running.

---

### Step 4 — Start errorprobe stack (leave running in a separate terminal)

```powershell
./ep.exe up
```

**Expected output:**
```
[HH:MM:SS] checking docker daemon…
[HH:MM:SS] checking port availability…
[HH:MM:SS] pulling loki image…
[HH:MM:SS] pulling grafana image…
[HH:MM:SS] pulling vector image…
[HH:MM:SS] generating configs…
[HH:MM:SS] ensuring docker network…
[HH:MM:SS] starting loki…
[HH:MM:SS] starting grafana…
[HH:MM:SS] starting vector…
[HH:MM:SS] waiting for services to become ready…
[HH:MM:SS] loki: ready
[HH:MM:SS] grafana: ready
ErrorProbe ready
─────────────────────────────────────────────
Watching 2 containers
Grafana:   http://localhost:3000
Loki:      http://localhost:3100
Ingest:    http://127.0.0.1:9099
─────────────────────────────────────────────
Run 'errorprobe watch' to monitor in real-time
Run 'errorprobe check' to use in CI/scripts
```

---

### Step 5 — Wait 10 seconds

Allow Vector to ingest error lines and the health engine to flip `broken-app` to `HAS_ERRORS`.

---

### Step 6 — `errorprobe check` must exit 1

```powershell
./ep.exe check
echo "Exit code: $LASTEXITCODE"
```

**Expected output (stderr):**
```
  broken-app  state=HAS_ERRORS  last_error="ERROR database connection failed"
```

**Expected exit code:** `1`

---

### Step 7 — `errorprobe status` shows correct states

```powershell
./ep.exe status
```

**Expected output (partial):**
```
CONTAINER    FUNCTIONAL         INFRA    ERRORS   LAST ERROR
broken-app   ⚠ HAS ERRORS N    running  N        HH:MM ERROR database…
healthy-app  ✓ OK               running  0        —
```

---

### Step 8 — `errorprobe check --fail-on FAILING` exits 0

```powershell
./ep.exe check  # (FAILING is not reachable in V1; HAS_ERRORS containers pass this threshold)
```

Wait — there is no `--fail-on` CLI flag in the current implementation; the threshold is read from
`errorprobe.yaml` (`check.fail_on`). Edit `errorprobe.yaml` temporarily:

```yaml
check:
  fail_on: FAILING
```

Then:

```powershell
./ep.exe check
echo "Exit code: $LASTEXITCODE"
```

**Expected exit code:** `0` (FAILING is not reachable in V1; `broken-app` is in HAS_ERRORS which passes under FAILING threshold).

Restore `errorprobe.yaml` to `fail_on: HAS_ERRORS` afterwards.

---

### Step 9 — Reset broken-app

```powershell
./ep.exe status --reset broken-app
echo "Exit code: $LASTEXITCODE"
```

**Expected output:**
```
Reset health state for "broken-app" to OK
```

**Expected exit code:** `0`

---

### Step 10 — Verify broken-app shows OK immediately after reset

```powershell
./ep.exe status
```

**Expected:** `broken-app` shows `✓ OK`.

---

### Step 11 — Wait 5 seconds; broken-app flips back to HAS_ERRORS

```powershell
Start-Sleep 5
./ep.exe status
```

**Expected:** `broken-app` shows `⚠ HAS ERRORS` again (Vector continues ingesting new error lines).

---

### Step 12 — Tear down

```powershell
./ep.exe down
docker rm -f broken-app healthy-app
echo "Exit code: $LASTEXITCODE"
```

**Expected:** all containers removed, exit 0.

---

## Pass Criteria

| Step | Criterion                                                           |
|------|---------------------------------------------------------------------|
| 4    | Banner shows "Watching 2 containers" and correct URLs               |
| 6    | `check` exits 1, stderr lists `broken-app`                         |
| 7    | `status` shows `broken-app` as `⚠ HAS ERRORS`, `healthy-app` as `✓ OK` |
| 8    | `check` exits 0 when `fail_on: FAILING`                            |
| 9    | `status --reset` exits 0                                           |
| 10   | `status` shows `broken-app` as `✓ OK` after reset                 |
| 11   | `status` shows `broken-app` back to `⚠ HAS ERRORS` after 5 s      |
| 12   | `down` exits 0                                                      |
