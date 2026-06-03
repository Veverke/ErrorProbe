# Phase D — Distribution

**Goal:** Zero-friction installation matching the zero-config runtime promise. Binary available on Windows via `winget` and direct download; macOS via Homebrew tap (self-hosted in this repo) and direct download.  
**Prerequisite:** Phase 4 complete (V1 binary is ready to ship).  
**This phase runs in parallel with Phases 5–8.**

**UT coverage requirement: install scripts tested on clean machines; no Go packages added in this phase.**

---

## Atomic Tasks

Tasks are grouped by dependency tier. All tasks within a tier can be implemented independently and in parallel.

---

### Tier 1 — Build pipeline (no Phase D dependencies beyond Phase 4)

#### TD.1 — ✅ Implement `errorprobe version` command
- Replace any version stub in `cmd/root.go`
- `--version` flag and `version` subcommand both print:
  ```
  errorprobe 1.0.0 (commit abc1234, built 2026-04-28T12:00:00Z)
  ```
- Version, commit, and build date injected at build time via `go build -ldflags`:
  ```
  -X github.com/errorprobe/errorprobe/cmd.Version=1.0.0
  -X github.com/errorprobe/errorprobe/cmd.Commit=$(git rev-parse --short HEAD)
  -X github.com/errorprobe/errorprobe/cmd.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  ```
- `cmd.Version`, `cmd.Commit`, `cmd.BuildDate` are package-level vars in `cmd/version.go`

#### TD.2 — ✅ Implement GitHub Actions build workflow
- File: `.github/workflows/release.yml`
- Trigger: `push` to tags matching `v*.*.*`
- Build matrix:
  | OS | Arch | Binary name |
  |---|---|---|
  | Windows | amd64 | `errorprobe-windows-amd64.exe` |
  | Linux | amd64 | `errorprobe-linux-amd64` |
  | Linux | arm64 | `errorprobe-linux-arm64` |
  | macOS | arm64 | `errorprobe-darwin-arm64` |
- Build command: `go build -ldflags "..." -o <binary-name> ./main.go`
- `GOOS` / `GOARCH` set per matrix entry
- Produces stripped binaries (`-ldflags "-s -w"`) for smaller size
- Upload all binaries as GitHub Release assets

#### TD.3 — ✅ Implement checksum and signature generation
- After all binaries built: generate `checksums.txt` with SHA-256 of each binary
- Sign `checksums.txt` with `cosign` (keyless signing via GitHub OIDC) — produces `checksums.txt.sig`
- Upload `checksums.txt` and `checksums.txt.sig` as Release assets
- Document verification steps in release notes

---

### Tier 2 — Install scripts (depends on TD.2)

#### TD.4 — ✅ Implement PowerShell install script (`install.ps1`)
- Detects OS architecture (`$env:PROCESSOR_ARCHITECTURE`)
- Fetches latest release tag from GitHub API: `https://api.github.com/repos/errorprobe/errorprobe/releases/latest`
- Downloads correct binary to temp path
- Verifies SHA-256 checksum against `checksums.txt`
- Copies binary to `$env:LOCALAPPDATA\errorprobe\errorprobe.exe`
- Adds install directory to user `PATH` if not already present (modifies `[Environment]::SetEnvironmentVariable`)
- Prints: `"errorprobe installed. Run 'errorprobe --help' to get started."`
- Hosted at a stable URL: `https://raw.githubusercontent.com/errorprobe/errorprobe/main/install.ps1`
- Usage: `irm https://raw.githubusercontent.com/errorprobe/errorprobe/main/install.ps1 | iex`

#### TD.5 — ✅ Implement shell install script (`install.sh`)
- Detects OS (`uname -s`) and arch (`uname -m`)
- Same flow as TD.4: fetch latest release, download, verify checksum, install to `/usr/local/bin`
- Fallback install path: `~/.local/bin` if `/usr/local/bin` not writable (no `sudo` prompt)
- Usage: `curl -fsSL https://raw.githubusercontent.com/errorprobe/errorprobe/main/install.sh | sh`

#### TD.4.5 — ✅ Implement `errorprobe upgrade` (binary self-update)
- New file: `cmd/upgrade.go`; register in `cmd/root.go` alongside other subcommands
- Note: `cmd/update.go` already exists for stack image updates — this is a separate command
- Behaviour:
  1. Read current version from `cmd.Version` (set at build time via `go build -ldflags`)
  2. Fetch latest release tag from GitHub API: `https://api.github.com/repos/Veverke/ErrorProbe/releases/latest`
  3. If `tag_name` matches current version: print `"errorprobe is already at the latest version (x.y.z)"` and exit 0
  4. Download the correct binary for current platform using `runtime.GOOS` / `runtime.GOARCH` to select the right release asset
  5. Verify SHA-256 checksum against `checksums.txt` in the same release
  6. **Atomic replacement — platform-specific:**
     - Detect install path via `os.Executable()` — do not assume a hardcoded path
     - Download to a temp file in the **same directory** as the running binary (ensures same filesystem, so rename is atomic)
     - **Linux/macOS:** `os.Rename(tempPath, execPath)` — atomic on POSIX; if permission denied, print:
       `"upgrade requires write permission to <path>; try: sudo errorprobe upgrade"` and exit non-zero
     - **Windows:** Cannot rename over a running `.exe`; use the deferred-rename pattern:
       1. Rename current binary to `errorprobe.exe.old`
       2. Rename temp file to `errorprobe.exe`
       3. On next startup, in `cmd/root.go` `init()`, silently delete `errorprobe.exe.old` if it exists alongside the running binary
  7. Print: `"errorprobe upgraded to <new-version>. Run 'errorprobe --version' to confirm."`
- No external dependencies: use only stdlib (`net/http`, `crypto/sha256`, `os`, `runtime`)
- Depends on: TD.1 (version), TD.2 (GitHub Release assets), TD.3 (checksums.txt)

#### TD.5.5 — ✅ Implement GitHub Action (`setup-errorprobe`)
- Create a new public repository `Veverke/setup-errorprobe` (a GitHub Action)
- `action.yml` with `inputs.version` (default: `latest`)
- Action steps:
  1. Call GitHub releases API to resolve `latest` or a pinned version
  2. Download the correct binary for the runner OS/arch (`${{ runner.os }}` / `${{ runner.arch }}`)
  3. Verify SHA-256 against `checksums.txt`
  4. Add binary directory to `$GITHUB_PATH`
- Usage in user workflows:
  ```yaml
  - uses: Veverke/setup-errorprobe@v1
  - run: errorprobe --version
  ```
- Replaces the `curl | sh` antipattern that is rejected by many CI security policies
- Depends on: TD.2, TD.3

---

### Tier 3 — Package managers (depends on TD.2, TD.3; independent of each other)

#### TD.6 — ✅ Submit `winget` package (automation implemented; first-time PR to microsoft/winget-pkgs requires manual submission — see distribution guide)
- **No template files or separate repo required.** Use `wingetcreate` which generates all 3 manifests from the binary URL automatically.
- Requires: GitHub repo is **public** and the `WINGET_TOKEN` secret (PAT with `public_repo` scope) is set in repo settings
- Automated in `release.yml` `update-winget` job:
  ```powershell
  winget install --id Microsoft.WingetCreate --silent
  wingetcreate submit --token $env:WINGET_TOKEN \
    https://github.com/Veverke/ErrorProbe/releases/download/v1.0.0/errorprobe-windows-amd64.exe
  ```
- `wingetcreate` forks `microsoft/winget-pkgs`, pushes manifests, and opens the PR automatically
- Usage once PR is merged: `winget install ErrorProbe.ErrorProbe`
- **Manual approval still required** by Microsoft's winget-pkgs reviewers

#### TD.7 — ✅ ~~`scoop` bucket~~ — **dropped**
- Redundant given winget covers all Windows users; no separate repo needed

#### TD.8 — ✅ Homebrew formula (macOS — self-hosted in this repo)
- Formula lives at `Formula/errorprobe.rb` **in this repo** (no separate tap repo needed)
- `release.yml` `update-homebrew` job updates version + SHA-256 and commits directly to this repo on every release tag
- Usage:
  ```sh
  brew tap Veverke/errorprobe https://github.com/Veverke/ErrorProbe
  brew install errorprobe
  ```
- Full URL required in the tap command because repo is not named `homebrew-*`

---

### Tier 4 — Release automation (depends on TD.2, TD.6, TD.8)

#### TD.9 — ✅ Automate package manifest updates on release
- Both update steps are jobs inside `.github/workflows/release.yml` (no separate workflow file)
- **`update-winget` job** (runs on `windows-latest` after the `release` job):
  - Installs `wingetcreate` via winget, then calls `wingetcreate submit --token $WINGET_TOKEN <binary-url>`
  - Opens a PR to `microsoft/winget-pkgs` automatically; requires human approval from Microsoft reviewers
  - Requires `WINGET_TOKEN` secret in repo settings
- **`update-homebrew` job** (runs on `ubuntu-latest` after the `release` job):
  - Downloads `checksums.txt` from the new release
  - Updates `version`, `url`, and `sha256` in `Formula/errorprobe.rb` via `sed`
  - Commits and pushes directly to this repo using the built-in `GITHUB_TOKEN` (no extra secret)
- No separate `update-manifests.yml` workflow — everything is consolidated in `release.yml`

---

### Tier 5 — Unit / integration tests for install scripts

#### TD.10 — ❌ Test install scripts on clean environments (not yet done — manual steps required before public release)
- **Manual test on clean Windows VM** (no errorprobe previously installed):
  1. Run `irm <url> | iex`
  2. Open new terminal; run `errorprobe --version`; confirm correct version printed
  3. Confirm binary is in `PATH`
  4. Confirm checksum verification runs and passes
- **Manual test for winget** (once PR merged):
  1. `winget install ErrorProbe.ErrorProbe`
  2. `errorprobe --version`
- **Manual test for scoop** (once bucket live):
  1. `scoop bucket add errorprobe <url> ; scoop install errorprobe`
  2. `errorprobe --version`

---

### Final Task

#### TD.11 — ❌ Mark phase complete in work-plan.md (pending TD.10)
- Open `docs/work-plan.md`
- Mark all Distribution tasks as `[x]`
- Add completion date next to phase heading

---

## Deliverables

| Deliverable | Description |
|---|---|
| `cmd/version.go` | Version command with build-time injection |
| `.github/workflows/release.yml` | Cross-platform build, GitHub Release, winget submit, Homebrew update |
| `checksums.txt` + `checksums.txt.sig` | Checksum and signature per release |
| `install.ps1` | Windows PowerShell install script |
| `install.sh` | Linux/macOS shell install script |
| `cmd/upgrade.go` + `cmd/upgrade_{unix,windows}.go` | Binary self-update command (`errorprobe upgrade`) |
| `setup-action/action.yml` | GitHub Action for CI install (`uses: Veverke/setup-errorprobe@v1`) |
| `Formula/errorprobe.rb` | Homebrew formula (self-hosted in this repo, updated on each release) |
| `winget` manifest | PR auto-submitted to `microsoft/winget-pkgs` via `wingetcreate` |

---

## Manual Tests

Run after all tasks are complete on a clean Windows machine (no prior errorprobe installation):

1. **PowerShell install** — run `irm <install-url> | iex`; confirm binary installed; `errorprobe --version` prints correct version and commit.
2. **PATH persistence** — close and reopen terminal; `errorprobe --version` still works (PATH was persisted, not just set for current session).
3. **Checksum verification** — tamper with downloaded binary byte; confirm install script rejects it with a clear error.
4. **`winget install ErrorProbe.ErrorProbe`** — installs successfully; `errorprobe --version` works.
5. **`errorprobe upgrade` — already at latest** — run on a freshly installed binary matching the latest release; confirm `"already at the latest version"` is printed and exit code is 0.
6. **`errorprobe upgrade` — older binary** — replace installed binary with a prior release build; run `errorprobe upgrade`; confirm correct platform binary downloaded, checksum verified, binary replaced; `errorprobe --version` prints new version.
7. **`errorprobe upgrade` — Windows deferred rename** — after upgrade completes, confirm `errorprobe.exe.old` is present; run any `errorprobe` command; confirm `.old` file is cleaned up automatically.
8. **`errorprobe upgrade` — no write permission (Linux)** — install to `/usr/local/bin` as root, then run `errorprobe upgrade` as an unprivileged user; confirm error message includes the path and suggests `sudo`.
9. **GitHub Action install** — create a test workflow using `uses: Veverke/setup-errorprobe@v1`; confirm `errorprobe --version` works in a subsequent step on ubuntu-latest and windows-latest runners.
10. **Release workflow** — push a `v1.0.1` tag; confirm GitHub Actions builds all 4 binaries; Release is created with all assets, `checksums.txt`, and `checksums.txt.sig`.
