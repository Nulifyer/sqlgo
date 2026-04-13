#!/usr/bin/env bash
# sqlgo Uninstaller for Linux/macOS
# Usage: curl -fsSL https://raw.githubusercontent.com/Nulifyer/sqlgo/main/.scripts/uninstall.sh | bash
set -euo pipefail

INSTALL_DIR="${HOME}/.local/bin"
BIN="${INSTALL_DIR}/sqlgo"

echo "Uninstalling sqlgo..."

rm -f "$BIN"
echo "  Removed ${BIN}"

echo ""
echo "sqlgo uninstalled."
