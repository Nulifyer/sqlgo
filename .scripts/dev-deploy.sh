#!/usr/bin/env bash
# dev-deploy.sh -- Build and install dev binaries to local install path
# Usage: ./.scripts/dev-deploy.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
COMMIT_SHORT="$(git -C "$REPO_ROOT" rev-parse --short HEAD)"
VERSION="dev-$COMMIT_SHORT"

INSTALL_DIR="$HOME/.local/bin"
CMDS=(sqlgo sqlgocheck sqlgoseed)

echo "Building sqlgo $VERSION ..."

# Kill running binaries
for name in "${CMDS[@]}"; do
    pkill -9 "$name" 2>/dev/null || true
done

mkdir -p "$INSTALL_DIR"

cd "$REPO_ROOT"
for name in "${CMDS[@]}"; do
    echo "  -> $name"
    go build -ldflags "-s -w" -o "$INSTALL_DIR/$name" "./cmd/$name"
    chmod +x "$INSTALL_DIR/$name"
done

echo "Installed to $INSTALL_DIR ($VERSION)"
echo "Ensure '$INSTALL_DIR' is on PATH."
