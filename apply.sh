#!/usr/bin/env bash
# Clone upstream zx_go at a pinned commit, apply the zxwasm overlay over the top,
# and build the WebAssembly core.
#   usage: ./apply.sh [work-dir]   (default: zx_go-wasm-build)
set -euo pipefail

UPSTREAM_URL="https://github.com/conorarmstrong/zx_go.git"
UPSTREAM_SHA="6aef17538910de15f8657f7f6070fe3feb2ab3db"

HERE="$(cd "$(dirname "$0")" && pwd)"
WORK="${1:-zx_go-wasm-build}"

if [ ! -d "$WORK/.git" ]; then
  echo "cloning upstream into $WORK ..."
  git clone "$UPSTREAM_URL" "$WORK"
fi

cd "$WORK"
git fetch origin
git checkout -q "$UPSTREAM_SHA"

echo "applying overlay ..."
cp -R "$HERE/overlay/." .

echo "building GOOS=js GOARCH=wasm ..."
GOOS=js GOARCH=wasm go build -o cmd/zxwasm/web/zx.wasm ./cmd/zxwasm
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" cmd/zxwasm/web/wasm_exec.js

echo
echo "built: $WORK/cmd/zxwasm/web/zx.wasm"
echo "serve the demo with: (cd $WORK && cmd/zxwasm/web/serve.sh)"
