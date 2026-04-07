#!/usr/bin/env bash
set -euo pipefail

workspace="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
os="$(go env GOOS)"
arch="$(go env GOARCH)"
out_dir="$workspace/dist"
ext=""
ldflags="-s -w"

if [[ "$os" == "windows" ]]; then
  ext=".exe"
  ldflags="-linkmode=internal -s -w"
fi

out_file="$out_dir/sqlgo-$os-$arch$ext"

mkdir -p "$out_dir"

cd "$workspace"
go build -trimpath "-ldflags=$ldflags" -o "$out_file" ./cmd/sqlgo
printf 'Built %s\n' "$out_file"
