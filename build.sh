#!/usr/bin/env bash
# ====================================================================
#  muse-cc build script (macOS / Linux / WSL)
#  Builds the React panel and embeds it into a single Go binary.
# ====================================================================
set -euo pipefail
cd "$(dirname "$0")"

echo "[1/4] Building web panel..."
(
  cd web
  npm install --include=dev --no-audit --no-fund
  npx vite build
)

echo "[2/4] Copying dist into embed folder..."
mkdir -p internal/assets/dist
find internal/assets/dist -mindepth 1 -maxdepth 1 ! -name .gitkeep -exec rm -rf {} +
cp -r web/dist/. internal/assets/dist/

echo "[3/4] Compiling Go binary..."
LDFLAGS="-s -w"
CGO_ENABLED=0 go build -ldflags "$LDFLAGS" -o muse-cc .

echo "[4/4] Done."
echo
echo "  Binary:  ./muse-cc"
echo "  Run it:  ./muse-cc            (then open http://localhost:8788)"
echo "  Test:    go test ./..."
echo
