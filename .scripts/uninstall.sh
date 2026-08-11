#!/usr/bin/env bash
# sqlgo Uninstaller for Linux/macOS
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/Nulifyer/sqlgo/main/.scripts/uninstall.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/Nulifyer/sqlgo/main/.scripts/uninstall.sh | bash -s -- --purge
set -euo pipefail

PURGE=0
for arg in "$@"; do
    case "$arg" in
        --purge) PURGE=1 ;;
    esac
done

INSTALL_DIR="${HOME}/.local/bin"
BIN="${INSTALL_DIR}/sqlgo"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
if [ "$OS" = "darwin" ]; then
    DATA_DIR="${HOME}/Library/Application Support/sqlgo"
else
    DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/sqlgo"
fi
LEGACY_DIR="${HOME}/.sqlgo"

echo "Uninstalling sqlgo..."

rm -f "$BIN"
echo "  Removed ${BIN}"

if [ "$PURGE" -eq 1 ]; then
    for d in "$DATA_DIR" "$LEGACY_DIR"; do
        if [ -d "$d" ]; then
            rm -rf "$d"
            echo "  Purged ${d}"
        fi
    done
else
    if [ -d "$DATA_DIR" ] || [ -d "$LEGACY_DIR" ]; then
        echo ""
        echo "User data preserved. Re-run with --purge to delete:"
        [ -d "$DATA_DIR" ]   && echo "  ${DATA_DIR}"
        [ -d "$LEGACY_DIR" ] && echo "  ${LEGACY_DIR}"
    fi
fi

echo ""
echo "sqlgo uninstalled."
