#!/usr/bin/env bash
# Build SuperDev and restart the app.
# Usage: ./scripts/rebuild-superdev.sh
# Env:   CONFIG=Debug|Release  SCHEME=SuperDev

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PROJECT_DIR="$REPO_ROOT/SuperDev"
XCODEPROJ="$PROJECT_DIR/SuperDev.xcodeproj"
SCHEME="${SCHEME:-SuperDev}"
CONFIG="${CONFIG:-Debug}"

if [[ ! -d "$XCODEPROJ" ]]; then
  echo "error: Xcode project not found: $XCODEPROJ" >&2
  exit 1
fi

echo "==> Building $SCHEME ($CONFIG)..."
xcodebuild \
  -project "$XCODEPROJ" \
  -scheme "$SCHEME" \
  -configuration "$CONFIG" \
  build

BUILT_PRODUCTS_DIR="$(
  xcodebuild \
    -project "$XCODEPROJ" \
    -scheme "$SCHEME" \
    -configuration "$CONFIG" \
    -showBuildSettings 2>/dev/null \
    | awk -F' = ' '/^    BUILT_PRODUCTS_DIR / {print $2; exit}'
)"

APP="$BUILT_PRODUCTS_DIR/SuperDev.app"
if [[ ! -d "$APP" ]]; then
  echo "error: app bundle not found: $APP" >&2
  exit 1
fi

echo "==> Restarting SuperDev..."
killall SuperDev 2>/dev/null || true
sleep 0.5
open "$APP"

echo "==> Done."
echo "    App: $APP"
pgrep -fl SuperDev 2>/dev/null || echo "    (process not listed yet)"
