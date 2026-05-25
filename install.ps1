#Requires -Version 5.1
<#
.SYNOPSIS
    Install errorprobe on Windows.
.DESCRIPTION
    Downloads the latest errorprobe release from GitHub, verifies the SHA-256
    checksum, installs the binary to %LOCALAPPDATA%\errorprobe\, and adds that
    directory to the current user's PATH (persisted across sessions).
.EXAMPLE
    irm https://raw.githubusercontent.com/Veverke/ErrorProbe/main/install.ps1 | iex
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

$Repo       = 'Veverke/ErrorProbe'
$InstallDir = Join-Path $env:LOCALAPPDATA 'errorprobe'
$BinaryName = 'errorprobe.exe'

# ── Detect architecture ───────────────────────────────────────────────────────
$Arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    default { throw "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}
$AssetName = "errorprobe-windows-$Arch.exe"

# ── Fetch latest release metadata ────────────────────────────────────────────
Write-Host "Fetching latest release..."
$ApiUrl  = "https://api.github.com/repos/$Repo/releases/latest"
$Headers = @{ 'User-Agent' = 'errorprobe-install'; 'Accept' = 'application/vnd.github+json' }
$Release = Invoke-RestMethod -Uri $ApiUrl -Headers $Headers
$Version = $Release.tag_name

$BinaryAsset = $Release.assets | Where-Object { $_.name -eq $AssetName }
if (-not $BinaryAsset) {
    throw "No asset '$AssetName' found in release $Version."
}

$ChecksumsAsset = $Release.assets | Where-Object { $_.name -eq 'checksums.txt' }
if (-not $ChecksumsAsset) {
    throw "checksums.txt not found in release $Version."
}

# ── Download to temp ──────────────────────────────────────────────────────────
$TmpBinary    = Join-Path $env:TEMP "$AssetName.tmp"
$TmpChecksums = Join-Path $env:TEMP 'checksums-ep.txt'

Write-Host "Downloading errorprobe $Version..."
Invoke-WebRequest -Uri $BinaryAsset.browser_download_url    -OutFile $TmpBinary    -UseBasicParsing
Invoke-WebRequest -Uri $ChecksumsAsset.browser_download_url -OutFile $TmpChecksums -UseBasicParsing

# ── Verify SHA-256 ────────────────────────────────────────────────────────────
$ExpectedLine = Get-Content $TmpChecksums | Where-Object { $_ -match "\s$([regex]::Escape($AssetName))$" }
if (-not $ExpectedLine) {
    Remove-Item $TmpBinary, $TmpChecksums -Force -ErrorAction SilentlyContinue
    throw "Hash for '$AssetName' not found in checksums.txt."
}
$ExpectedHash = ($ExpectedLine -split '\s+')[0].ToLower()
$ActualHash   = (Get-FileHash -Path $TmpBinary -Algorithm SHA256).Hash.ToLower()

if ($ActualHash -ne $ExpectedHash) {
    Remove-Item $TmpBinary, $TmpChecksums -Force -ErrorAction SilentlyContinue
    throw "Checksum verification FAILED.`n  Expected: $ExpectedHash`n  Got:      $ActualHash"
}
Write-Host "Checksum verified."

# ── Install ───────────────────────────────────────────────────────────────────
New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
$Dest = Join-Path $InstallDir $BinaryName
Move-Item -Path $TmpBinary -Destination $Dest -Force
Remove-Item $TmpChecksums -Force -ErrorAction SilentlyContinue

# ── Add to user PATH (persisted) ──────────────────────────────────────────────
$UserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($UserPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable('Path', "$UserPath;$InstallDir", 'User')
    Write-Host "Added '$InstallDir' to user PATH (open a new terminal for it to take effect)."
}

Write-Host ""
Write-Host "errorprobe $Version installed to $Dest"
Write-Host "Run 'errorprobe --help' to get started."
