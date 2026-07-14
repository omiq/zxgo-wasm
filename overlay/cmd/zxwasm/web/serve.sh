#!/usr/bin/env bash
# Build the lean emulator-core wasm bridge and serve the demo harness.
# Usage: cmd/zxwasm/web/serve.sh [port]   (default 8791)
set -euo pipefail
cd "$(dirname "$0")"
PORT="${1:-8791}"
ROOT="$(cd ../../.. && pwd)"

echo "building core wasm (GOOS=js GOARCH=wasm)..."
GOOS=js GOARCH=wasm go build -o zx.wasm "$ROOT/cmd/zxwasm"
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" ./wasm_exec.js

# Stage Spectrum Next assets (licensed ROMs + SD image) into web/assets
# (gitignored -- never committed). ROMs come from the native install dir;
# the SD image from $ZX_WASM_SD_IMG, falling back to roms/next/sd.img.
mkdir -p assets
for rom in enNextZX.rom enNxtmmc.rom enNextMF.rom tbblue_loader.rom; do
  [ -f "$ROOT/roms/next/$rom" ] && cp -f "$ROOT/roms/next/$rom" assets/
done
SD_IMG="${ZX_WASM_SD_IMG:-}"
if [ -n "$SD_IMG" ] && [ -f "$SD_IMG" ]; then
  # cp, not ln -s: macOS TCC denies python (the server) reads into
  # protected dirs like ~/Downloads, turning a symlink into a 404.
  cp -f "$SD_IMG" assets/tbblue.mmc
  echo "SD image: $SD_IMG"
elif [ ! -f assets/tbblue.mmc ] && [ -d "$ROOT/roms/next/sd" ]; then
  # No image supplied: pack the distro tree into a bootable FAT32
  # image with the same builder the native GUI's folder mode uses.
  echo "building FAT32 SD image from roms/next/sd..."
  go run "$ROOT/cmd/zxwasm/mksd" -root "$ROOT/roms/next/sd" -size 64 -o assets/tbblue.mmc
elif [ ! -f assets/tbblue.mmc ]; then
  echo "WARNING: no SD image (set ZX_WASM_SD_IMG or populate roms/next/sd) -- Next mode won't boot"
fi

echo "serving http://localhost:$PORT/index.html  (classic 48K)"
echo "        http://localhost:$PORT/index.html?next=1  (Spectrum Next)"
python3 -m http.server "$PORT"
