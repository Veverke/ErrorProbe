#!/bin/sh
# Install errorprobe on Linux or macOS.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/Veverke/ErrorProbe/main/install.sh | sh
#
# The binary is installed to /usr/local/bin if writable, otherwise ~/.local/bin.
set -e

REPO="Veverke/ErrorProbe"
API_URL="https://api.github.com/repos/${REPO}/releases/latest"

# ── Detect OS and architecture ────────────────────────────────────────────────
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
MACHINE=$(uname -m)

case "$OS" in
    linux)  GOOS="linux"  ;;
    darwin) GOOS="darwin" ;;
    *)      echo "Unsupported OS: $OS" >&2; exit 1 ;;
esac

case "$MACHINE" in
    x86_64)        GOARCH="amd64" ;;
    aarch64|arm64) GOARCH="arm64" ;;
    *)             echo "Unsupported architecture: $MACHINE" >&2; exit 1 ;;
esac

ASSET_NAME="errorprobe-${GOOS}-${GOARCH}"

# ── Fetch latest release metadata ────────────────────────────────────────────
echo "Fetching latest release..."
RELEASE=$(curl -fsSL -H "User-Agent: errorprobe-install" \
               -H "Accept: application/vnd.github+json" \
               "$API_URL")

VERSION=$(echo "$RELEASE" | grep '"tag_name"' \
          | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')

DOWNLOAD_URL=$(echo "$RELEASE" | grep '"browser_download_url"' \
               | grep "\"${ASSET_NAME}\"" \
               | sed 's/.*"browser_download_url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')

CHECKSUMS_URL=$(echo "$RELEASE" | grep '"browser_download_url"' \
                | grep '"checksums\.txt"' \
                | sed 's/.*"browser_download_url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')

if [ -z "$DOWNLOAD_URL" ]; then
    echo "No asset '$ASSET_NAME' found in release $VERSION." >&2
    exit 1
fi
if [ -z "$CHECKSUMS_URL" ]; then
    echo "checksums.txt not found in release $VERSION." >&2
    exit 1
fi

# ── Download ──────────────────────────────────────────────────────────────────
echo "Downloading errorprobe ${VERSION}..."
TMP_BINARY=$(mktemp)
TMP_CHECKSUMS=$(mktemp)

curl -fsSL "$DOWNLOAD_URL"  -o "$TMP_BINARY"
curl -fsSL "$CHECKSUMS_URL" -o "$TMP_CHECKSUMS"

# ── Verify SHA-256 ────────────────────────────────────────────────────────────
EXPECTED_HASH=$(grep "[[:space:]]${ASSET_NAME}$" "$TMP_CHECKSUMS" | awk '{print $1}')
if [ -z "$EXPECTED_HASH" ]; then
    echo "Hash for '$ASSET_NAME' not found in checksums.txt." >&2
    rm -f "$TMP_BINARY" "$TMP_CHECKSUMS"
    exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL_HASH=$(sha256sum "$TMP_BINARY" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
    ACTUAL_HASH=$(shasum -a 256 "$TMP_BINARY" | awk '{print $1}')
else
    echo "Warning: no sha256sum or shasum found — skipping checksum verification." >&2
    ACTUAL_HASH="$EXPECTED_HASH"
fi

if [ "$ACTUAL_HASH" != "$EXPECTED_HASH" ]; then
    echo "Checksum verification FAILED." >&2
    echo "  Expected: $EXPECTED_HASH" >&2
    echo "  Got:      $ACTUAL_HASH"    >&2
    rm -f "$TMP_BINARY" "$TMP_CHECKSUMS"
    exit 1
fi
echo "Checksum verified."

chmod +x "$TMP_BINARY"

# ── Install: /usr/local/bin or ~/.local/bin fallback ─────────────────────────
if [ -w "/usr/local/bin" ]; then
    INSTALL_DIR="/usr/local/bin"
elif mkdir -p "$HOME/.local/bin" 2>/dev/null; then
    INSTALL_DIR="$HOME/.local/bin"
else
    echo "Cannot write to /usr/local/bin and cannot create ~/.local/bin." >&2
    rm -f "$TMP_BINARY" "$TMP_CHECKSUMS"
    exit 1
fi

mv "$TMP_BINARY" "$INSTALL_DIR/errorprobe"
rm -f "$TMP_CHECKSUMS"

echo ""
echo "errorprobe ${VERSION} installed to ${INSTALL_DIR}/errorprobe"

# ── Warn if install dir is not on PATH ───────────────────────────────────────
case ":${PATH}:" in
    *":${INSTALL_DIR}:"*) ;;
    *) echo "Note: ${INSTALL_DIR} is not in your PATH. Add the following to your shell profile:"
       echo "  export PATH=\"\$PATH:${INSTALL_DIR}\"" ;;
esac

echo "Run 'errorprobe --help' to get started."
