# Phase D — Distribution

**Goal:** Zero-friction installation matching the zero-config runtime promise. Binary available on Windows via `winget`, `scoop`, and direct download.  
**Prerequisite:** Phase 4 complete (V1 binary is ready to ship).  
**This phase runs in parallel with Phases 5–8.**

**UT coverage requirement: install scripts tested on clean machines; no Go packages added in this phase.**

---

## Atomic Tasks

Tasks are grouped by dependency tier. All tasks within a tier can be implemented independently and in parallel.

---

### Tier 1 — Build pipeline (no Phase D dependencies beyond Phase 4)

#### TD.1 — Implement `errorprobe version` command
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

#### TD.2 — Implement GitHub Actions build workflow
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

#### TD.3 — Implement checksum and signature generation
- After all binaries built: generate `checksums.txt` with SHA-256 of each binary
- Sign `checksums.txt` with `cosign` (keyless signing via GitHub OIDC) — produces `checksums.txt.sig`
- Upload `checksums.txt` and `checksums.txt.sig` as Release assets
- Document verification steps in release notes

---

### Tier 2 — Install scripts (depends on TD.2)

#### TD.4 — Implement PowerShell install script (`install.ps1`)
- Detects OS architecture (`$env:PROCESSOR_ARCHITECTURE`)
- Fetches latest release tag from GitHub API: `https://api.github.com/repos/errorprobe/errorprobe/releases/latest`
- Downloads correct binary to temp path
- Verifies SHA-256 checksum against `checksums.txt`
- Copies binary to `$env:LOCALAPPDATA\errorprobe\errorprobe.exe`
- Adds install directory to user `PATH` if not already present (modifies `[Environment]::SetEnvironmentVariable`)
- Prints: `"errorprobe installed. Run 'errorprobe --help' to get started."`
- Hosted at a stable URL: `https://raw.githubusercontent.com/errorprobe/errorprobe/main/install.ps1`
- Usage: `irm https://raw.githubusercontent.com/errorprobe/errorprobe/main/install.ps1 | iex`

#### TD.5 — Implement shell install script (`install.sh`)
- Detects OS (`uname -s`) and arch (`uname -m`)
- Same flow as TD.4: fetch latest release, download, verify checksum, install to `/usr/local/bin`
- Fallback install path: `~/.local/bin` if `/usr/local/bin` not writable (no `sudo` prompt)
- Usage: `curl -fsSL https://raw.githubusercontent.com/errorprobe/errorprobe/main/install.sh | sh`

---

### Tier 3 — Package managers (depends on TD.2, TD.3; independent of each other)

#### TD.6 — Submit `winget` package
- Create `winget-pkgs` PR with manifest files:
  - `manifests/e/ErrorProbe/ErrorProbe/<version>/ErrorProbe.ErrorProbe.installer.yaml`
  - `manifests/e/ErrorProbe/ErrorProbe/<version>/ErrorProbe.ErrorProbe.locale.en-US.yaml`
  - `manifests/e/ErrorProbe/ErrorProbe/<version>/ErrorProbe.ErrorProbe.yaml`
- Installer type: `portable` (single `.exe`, no MSI needed)
- Points to GitHub Release asset URL for Windows amd64 binary
- SHA-256 hash from `checksums.txt`
- Usage once merged: `winget install ErrorProbe.ErrorProbe`

#### TD.7 — Submit `scoop` bucket
- Create public `scoop-errorprobe` GitHub repository (a Scoop bucket)
- Add `bucket/errorprobe.json` manifest:
  ```json
  {
    "version": "1.0.0",
    "url": "https://github.com/errorprobe/errorprobe/releases/download/v1.0.0/errorprobe-windows-amd64.exe",
    "hash": "<sha256>",
    "bin": "errorprobe-windows-amd64.exe",
    "checkver": { "github": "https://github.com/errorprobe/errorprobe" },
    "autoupdate": { "url": "..." }
  }
  ```
- Usage: `scoop bucket add errorprobe https://github.com/errorprobe/scoop-errorprobe ; scoop install errorprobe`

#### TD.8 — Create `brew` formula (macOS — post Windows validation)
- Create `homebrew-errorprobe` GitHub repository (a Homebrew tap)
- Add `Formula/errorprobe.rb` with correct URL, SHA-256, and `bin.install` directive
- Usage: `brew tap errorprobe/errorprobe ; brew install errorprobe`
- Keep macOS binary (darwin-arm64) updated in Release workflow

---

### Tier 4 — Release automation (depends on TD.2, TD.6, TD.7)

#### TD.9 — Automate version bump in package manifests
- GitHub Actions workflow: on release tag push, after binaries are uploaded, automatically open PRs to:
  - `winget-pkgs` with updated manifest for new version
  - `scoop-errorprobe` bucket with updated `errorprobe.json`
  - `homebrew-errorprobe` tap with updated formula
- Uses `gh pr create` with the correct SHA-256 from `checksums.txt`
- Manual step still required: approve/merge each PR (package registries require human review)

---

### Tier 5 — Unit / integration tests for install scripts

#### TD.10 — Test install scripts on clean environments
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

#### TD.11 — Mark phase complete in work-plan.md
- Open `docs/work-plan.md`
- Mark all Distribution tasks as `[x]`
- Add completion date next to phase heading

---

## Deliverables

| Deliverable | Description |
|---|---|
| `cmd/version.go` | Version command with build-time injection |
| `.github/workflows/release.yml` | Cross-platform build + GitHub Release upload |
| `checksums.txt` + `checksums.txt.sig` | Checksum and signature per release |
| `install.ps1` | Windows PowerShell install script |
| `install.sh` | Linux/macOS shell install script |
| `winget` manifest | PR submitted to `winget-pkgs` |
| `scoop-errorprobe` bucket | Repository created with manifest |
| `homebrew-errorprobe` tap | Repository created with formula |
| Release automation workflow | Auto-PR to package registries on new tag |

---

## Manual Tests

Run after all tasks are complete on a clean Windows machine (no prior errorprobe installation):

1. **PowerShell install** — run `irm <install-url> | iex`; confirm binary installed; `errorprobe --version` prints correct version and commit.
2. **PATH persistence** — close and reopen terminal; `errorprobe --version` still works (PATH was persisted, not just set for current session).
3. **Checksum verification** — tamper with downloaded binary byte; confirm install script rejects it with a clear error.
4. **`winget install ErrorProbe.ErrorProbe`** — installs successfully; `errorprobe --version` works.
5. **`scoop install errorprobe`** — installs successfully; `errorprobe --version` works.
6. **`errorprobe update` after install** — confirm update checks for a newer GitHub Release and installs it (or prints "already at latest version").
7. **Release workflow** — push a `v1.0.1` tag; confirm GitHub Actions builds all 4 binaries; Release is created with all assets, `checksums.txt`, and `checksums.txt.sig`.
