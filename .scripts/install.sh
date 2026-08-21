#!/usr/bin/env bash
# sqlgo Installer for Linux/macOS
# Usage: curl -fsSL https://raw.githubusercontent.com/Nulifyer/sqlgo/main/.scripts/install.sh | bash
set -euo pipefail

REPO="Nulifyer/sqlgo"
INSTALL_DIR="${HOME}/.local/bin"

# Detect OS and architecture
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *)
        echo "Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

case "$OS" in
    linux|darwin) ;;
    *)
        echo "Unsupported OS: $OS"
        exit 1
        ;;
esac

echo "Installing sqlgo for ${OS}/${ARCH}..."

# 1. Get latest release info
TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | head -1 | cut -d'"' -f4)
if [ -z "$TAG" ]; then
    echo "ERROR: Could not fetch latest release"
    exit 1
fi
VERSION="${TAG#v}"
ARCHIVE_NAME="sqlgo_${VERSION}_${OS}_${ARCH}.tar.gz"
CHECKSUM_NAME="checksums.txt"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${TAG}/${ARCHIVE_NAME}"
CHECKSUM_URL="https://github.com/${REPO}/releases/download/${TAG}/${CHECKSUM_NAME}"

# 2. Download archive and checksums
echo "  Downloading ${TAG}..."
mkdir -p "$INSTALL_DIR"
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT
curl -fsSL "$DOWNLOAD_URL" -o "${TMP_DIR}/archive.tar.gz"
if ! curl -fsSL "$CHECKSUM_URL" -o "${TMP_DIR}/checksums.txt"; then
    echo "  ERROR: Could not download release checksums"
    exit 1
fi

# 3. Verify checksum
echo "  Verifying checksum..."
EXPECTED=$(awk -v archive="$ARCHIVE_NAME" '$2 == archive || $2 == "*" archive { print $1; exit }' "${TMP_DIR}/checksums.txt")
if [ -z "$EXPECTED" ]; then
    echo "  ERROR: No checksum found for ${ARCHIVE_NAME}"
    exit 1
fi
if command -v sha256sum &>/dev/null; then
    ACTUAL=$(sha256sum "${TMP_DIR}/archive.tar.gz" | awk '{print $1}')
elif command -v shasum &>/dev/null; then
    ACTUAL=$(shasum -a 256 "${TMP_DIR}/archive.tar.gz" | awk '{print $1}')
else
    echo "  ERROR: sha256sum or shasum is required to verify the download"
    exit 1
fi
if [ "$ACTUAL" != "$EXPECTED" ]; then
    echo "  ERROR: Checksum mismatch!"
    echo "    Expected: $EXPECTED"
    echo "    Got:      $ACTUAL"
    exit 1
fi
echo "  Checksum verified"

# 4. Extract and install
tar -xzf "${TMP_DIR}/archive.tar.gz" -C "$TMP_DIR"
chmod +x "${TMP_DIR}/sqlgo"
mv "${TMP_DIR}/sqlgo" "${INSTALL_DIR}/sqlgo"
echo "  Installed to ${INSTALL_DIR}/sqlgo"

# 5. Migrate legacy data dir (~/.sqlgo) into XDG data home
LEGACY_DIR="${HOME}/.sqlgo"
if [ "$OS" = "darwin" ]; then
    DATA_DIR="${HOME}/Library/Application Support/sqlgo"
else
    DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/sqlgo"
fi
if [ -f "${LEGACY_DIR}/sqlgo.db" ] && [ ! -f "${DATA_DIR}/sqlgo.db" ]; then
    echo "  Migrating ${LEGACY_DIR} -> ${DATA_DIR}"
    mkdir -p "$DATA_DIR"
    for f in sqlgo.db sqlgo.db-wal sqlgo.db-shm; do
        if [ -f "${LEGACY_DIR}/${f}" ]; then
            mv "${LEGACY_DIR}/${f}" "${DATA_DIR}/${f}"
        fi
    done
    # Remove the legacy dir only if it's now empty.
    rmdir "$LEGACY_DIR" 2>/dev/null || true
fi

# 6. Check PATH
case ":$PATH:" in
    *":${INSTALL_DIR}:"*) ;;
    *)
        echo "  NOTE: ${INSTALL_DIR} is not in your PATH."
        echo "  Add this to your shell rc file:"
        echo "    export PATH=\"\$HOME/.local/bin:\$PATH\""
        ;;
esac

echo ""
echo "sqlgo ${TAG} installed!"
echo ""
echo "Shell completion (optional):"
echo "  bash: sqlgo completion bash > ~/.local/share/bash-completion/completions/sqlgo"
echo "  zsh:  sqlgo completion zsh  > \"\${fpath[1]}/_sqlgo\"   # then: compinit"
echo "  fish: sqlgo completion fish > ~/.config/fish/completions/sqlgo.fish"
