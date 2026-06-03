# Distribution Guide — Step-by-Step

This is the hands-on guide to follow when releasing ErrorProbe. It covers every manual step required before and during a release, including one-time setup actions that only need to be done once.

> **TL;DR — releasing is two prerequisites + one command:**
> 1. Repo must be **public** (one-time)
> 2. `WINGET_TOKEN` secret must be set (one-time)
> 3. `git tag v1.0.0 && git push origin v1.0.0`
>
> The workflow then runs automatically. GitHub Release, Homebrew, install scripts, and `errorprobe upgrade` are all live within minutes. The only thing that requires waiting is the winget PR — Microsoft reviewers typically approve within 1–3 business days. You can announce and ship to all other platforms immediately; winget just means Windows users can also do `winget install ErrorProbe.ErrorProbe` once it's merged.

---

## Overview: How the Distribution Pipeline Works

When you push a git tag matching `v*.*.*` (e.g. `v1.0.0`), the following happens automatically:

```
git push origin v1.0.0
        │
        ▼
.github/workflows/release.yml
        │
        ├─ [build job] — builds 4 binaries in parallel (Windows/Linux/macOS)
        │
        ├─ [release job] — generates checksums.txt, signs with cosign (keyless
        │                   via GitHub OIDC), creates GitHub Release with all assets
        │
        ├─ [update-winget job] — runs wingetcreate submit, which forks
        │                         microsoft/winget-pkgs and opens a PR automatically
        │                         (Microsoft reviewers must approve; ~1–3 days)
        │
        └─ [update-homebrew job] — updates Formula/errorprobe.rb (version + SHA-256),
                                    commits and pushes directly to this repo
```

After the workflow completes:
- GitHub Release is live with binaries, `checksums.txt`, and `checksums.txt.sig`
- `brew install errorprobe` works immediately (formula is updated in this repo)
- `winget install ErrorProbe.ErrorProbe` works once Microsoft merges the PR
- `install.ps1` / `install.sh` work immediately (they fetch the latest release via GitHub API)
- `errorprobe upgrade` works immediately (same mechanism as install scripts)

---

## One-Time Setup (do these once before the first release)

### Step 1 — Make the GitHub repository public

The repository **must be public** for:
- `wingetcreate` to submit a PR to `microsoft/winget-pkgs` (winget requires publicly accessible binary URLs)
- `brew tap` to work without authentication
- Install scripts to download binaries without a token

Go to: **GitHub → Repository Settings → Danger Zone → Change repository visibility → Public**

---

### Step 2 — Create the `WINGET_TOKEN` secret

The `update-winget` job in `release.yml` requires a GitHub Personal Access Token (PAT) stored as a repository secret named `WINGET_TOKEN`.

**What it is:** A GitHub PAT that `wingetcreate` uses to fork `microsoft/winget-pkgs` and open a pull request on your behalf.

**How to create it:**

1. Go to **GitHub → Settings → Developer settings → Personal access tokens → Tokens (classic)**
2. Click **Generate new token (classic)**
3. Set a descriptive name, e.g. `errorprobe-winget-submit`
4. Set expiration as desired (1 year is reasonable; you will need to rotate it)
5. Under **Select scopes**, check only **`public_repo`**
6. Click **Generate token** and copy the token value immediately (it is shown only once)

**Store it as a repository secret:**

1. Go to your repository → **Settings → Secrets and variables → Actions**
2. Click **New repository secret**
3. Name: `WINGET_TOKEN`
4. Value: paste the token you just copied
5. Click **Add secret**

> **Note:** The `update-homebrew` job uses the built-in `GITHUB_TOKEN` (provided automatically by GitHub Actions) — no extra secret is needed for Homebrew.

---

### Step 3 — First-time winget submission (manual)

The automated `wingetcreate submit` in `release.yml` works for **subsequent releases** but the **very first submission** to `microsoft/winget-pkgs` must pass human review by Microsoft. There is nothing extra you need to do — `wingetcreate` handles the PR automatically — but be aware:

- Microsoft reviewers typically respond within **1–3 business days**
- They may request changes to the manifest (e.g. publisher name formatting)
- Once the first PR is merged, the package ID `ErrorProbe.ErrorProbe` is established and future automated PRs are processed faster

You can monitor PR status at: `https://github.com/microsoft/winget-pkgs/pulls?q=ErrorProbe`

---

### Step 4 — Verify Homebrew tap access

The `brew tap` command users run is:

```sh
brew tap Veverke/errorprobe https://github.com/Veverke/ErrorProbe
brew install errorprobe
```

The full HTTPS URL is required because the repo is not named `homebrew-<name>`. No extra setup is needed — the formula at `Formula/errorprobe.rb` is updated automatically by the release workflow.

Test it once after the first release by running the tap command on a macOS machine with Homebrew installed.

---

## Per-Release Checklist

Follow these steps for every release (including `v1.0.0`).

### Step 1 — Ensure all code is merged and CI is green

```sh
git checkout main
git pull
```

Check that the latest CI run on `main` is green: **GitHub → Actions → CI**

---

### Step 2 — Decide the version number

ErrorProbe uses [Semantic Versioning](https://semver.org/):
- `MAJOR.MINOR.PATCH` — e.g. `1.0.0`, `1.1.0`, `1.0.1`
- First public release: `v1.0.0`
- Bug fixes: increment PATCH
- New features (backward-compatible): increment MINOR
- Breaking changes: increment MAJOR

---

### Step 3 — Create and push the tag

```sh
git tag v1.0.0
git push origin v1.0.0
```

This triggers the `release.yml` workflow immediately.

---

### Step 4 — Monitor the release workflow

Go to: **GitHub → Actions → Release**

Watch all four jobs:

| Job | Expected outcome |
|---|---|
| `build (windows/amd64)` | ✅ Binary artifact uploaded |
| `build (linux/amd64)` | ✅ Binary artifact uploaded |
| `build (linux/arm64)` | ✅ Binary artifact uploaded |
| `build (darwin/arm64)` | ✅ Binary artifact uploaded |
| `release` | ✅ GitHub Release created with all 6 assets |
| `update-winget` | ✅ PR opened on `microsoft/winget-pkgs` |
| `update-homebrew` | ✅ `Formula/errorprobe.rb` committed to this repo |

If any job fails, check the logs. Common issues:
- `update-winget` fails if `WINGET_TOKEN` is missing or expired → rotate the PAT and update the secret
- `update-homebrew` fails if `GITHUB_TOKEN` lacks write permission → check repo Actions settings allow write access

---

### Step 5 — Verify the GitHub Release assets

Go to: **GitHub → Releases → v1.0.0**

Confirm the following files are present:
- `errorprobe-windows-amd64.exe`
- `errorprobe-linux-amd64`
- `errorprobe-linux-arm64`
- `errorprobe-darwin-arm64`
- `checksums.txt`
- `checksums.txt.sig`

---

### Step 6 — Smoke-test the install scripts

**Windows (PowerShell):**

Run on a clean machine or VM (no prior errorprobe installation):

```powershell
irm https://raw.githubusercontent.com/Veverke/ErrorProbe/main/install.ps1 | iex
```

Then open a **new** terminal and verify:

```powershell
errorprobe --version
# Expected: errorprobe 1.0.0 (commit xxxxxxx, built ...)
```

Confirm `errorprobe` is in PATH after closing and reopening the terminal.

**Linux / macOS:**

```sh
curl -fsSL https://raw.githubusercontent.com/Veverke/ErrorProbe/main/install.sh | sh
errorprobe --version
```

---

### Step 7 — Smoke-test `errorprobe upgrade`

Install an older build of errorprobe, then run:

```sh
errorprobe upgrade
errorprobe --version  # should print the new version
```

On Windows, confirm `errorprobe.exe.old` is cleaned up after running any subsequent command.

---

### Step 8 — Monitor winget PR

Check: `https://github.com/microsoft/winget-pkgs/pulls?q=ErrorProbe`

Once the PR is merged (typically 1–3 business days), test:

```powershell
winget install ErrorProbe.ErrorProbe
errorprobe --version
```

---

### Step 9 — Test Homebrew (macOS)

```sh
brew tap Veverke/errorprobe https://github.com/Veverke/ErrorProbe
brew install errorprobe
errorprobe --version
```

If you already have the tap, update first:

```sh
brew update
brew upgrade errorprobe
errorprobe --version
```

---

### Step 10 — Announce the release

Update any external links (documentation site, social, etc.) to point to the new version. The GitHub Release page auto-generates release notes from merged PRs if `generate_release_notes: true` is set in the workflow (it is).

---

## Optional: Expose the `setup-errorprobe` GitHub Action

> This is **not required for releasing ErrorProbe**. It is a convenience facility that lets users install errorprobe inside their own GitHub Actions workflows without using `curl | sh`.

The `setup-action/action.yml` file already lives in this repo at `setup-action/action.yml`. **A separate repository is not required.** GitHub supports the `owner/repo/subdirectory@ref` syntax natively, so users can reference it directly:

```yaml
- uses: Veverke/ErrorProbe/setup-action@v1.0.0
- run: errorprobe --version
```

This works as-is — no extra setup needed. The action is pinned to a release tag of ErrorProbe itself (e.g. `@v1.0.0`).

**Alternative: separate `setup-errorprobe` repo (optional)**

If you want a cleaner `uses: Veverke/setup-errorprobe@v1` syntax with an independent major-version tag:

1. Create a new public GitHub repository named `setup-errorprobe` under the `Veverke` account
2. Copy `setup-action/action.yml` to the root of that repo as `action.yml`
3. Commit, push to `main`, and tag: `git tag v1 && git push origin v1`
4. Update the major tag on every release: `git tag -f v1 && git push -f origin v1`

The tradeoff is maintaining a second repo. The subdirectory approach in this repo is simpler and fully functional.

---

## Verifying Release Signatures

Users can verify the integrity of downloaded binaries using `cosign`:

```sh
# Install cosign: https://docs.sigstore.dev/cosign/system_config/installation/
cosign verify-blob \
  --bundle checksums.txt.sig \
  --certificate-identity-regexp "https://github.com/Veverke/ErrorProbe" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  checksums.txt

# Then verify the binary's hash matches checksums.txt
sha256sum errorprobe-linux-amd64
grep errorprobe-linux-amd64 checksums.txt
```

---

## Troubleshooting

### `update-winget` job fails with "wingetcreate: command not found"

The job installs `wingetcreate` via `winget` at runtime on a `windows-latest` runner. If `winget` itself is not available on the runner, pin the install step to use the `wingetcreate` MSI directly:

```powershell
Invoke-WebRequest -Uri "https://github.com/microsoft/winget-create/releases/latest/download/wingetcreate.exe" -OutFile wingetcreate.exe
.\wingetcreate.exe submit --token $env:WINGET_TOKEN <binary-url>
```

### `update-homebrew` job fails with "Permission denied"

Check that the workflow has `contents: write` permission. In `release.yml` the `update-homebrew` job already declares this. If the repo has Actions permissions set to read-only globally, go to: **Settings → Actions → General → Workflow permissions → Read and write permissions**.

### Homebrew: `brew install errorprobe` shows old version after release

Run `brew update` first. Homebrew caches formula indexes locally and `brew update` fetches the latest from the tap.

### `WINGET_TOKEN` expired

1. Go to **GitHub → Settings → Developer settings → Personal access tokens**
2. Find the token and click **Regenerate** (or create a new one with `public_repo` scope)
3. Go to **Repository → Settings → Secrets and variables → Actions**
4. Update the `WINGET_TOKEN` secret with the new value

---

## Summary: What Requires Manual Action

| Action | When | Who |
|---|---|---|
| Make repo public | Once, before first release | You |
| Create `WINGET_TOKEN` PAT and add as secret | Once, before first release | You |
| Push release tag | Every release | You |
| Monitor winget PR and respond to reviewer feedback | Every release (first time especially) | You + Microsoft reviewers |
| Smoke-test install scripts on clean machines | Every release | You |
| Rotate `WINGET_TOKEN` when it expires | Annually (or per expiry setting) | You |