#!/usr/bin/env bash
# 每次桌面端启动前都刷新本机 sidecar，避免 stale 二进制通过 mtime 缓存被误用。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
AGENT_SRC="$ROOT/../agent"
OUT_DIR="$ROOT/src-tauri/binaries"
RESOURCE_DIR="$ROOT/src-tauri/resources/agent-install"
DEV_OUT_DIR="$ROOT/src-tauri/target/debug"
TARGET="$(rustc --print host-tuple)"
OUT_AGENT="$OUT_DIR/superdev-agent-$TARGET"
OUT_MCP="$OUT_DIR/superdev-mcp-$TARGET"
OUT_SAMPLE="$OUT_DIR/superdev-sample-$TARGET"
BUILD_REMOTE_INSTALL=0

for arg in "$@"; do
  case "$arg" in
    --remote-install) BUILD_REMOTE_INSTALL=1 ;;
    *)
      echo "build-agent: unknown argument $arg" >&2
      exit 2
      ;;
  esac
done

mkdir -p "$OUT_DIR"

if [[ ! -f "$AGENT_SRC/main.go" ]]; then
  echo "build-agent: agent source not found at $AGENT_SRC" >&2
  exit 1
fi

needs_build() {
  local out="$1"
  if [[ ! -f "$out" ]] || [[ ! -s "$out" ]]; then
    return 0
  fi
  local bin_mtime
  bin_mtime=$(stat -f '%m' "$out" 2>/dev/null || stat -c '%Y' "$out")
  local f mtime
  while IFS= read -r -d '' f; do
    mtime=$(stat -f '%m' "$f" 2>/dev/null || stat -c '%Y' "$f")
    if [[ "$mtime" -gt "$bin_mtime" ]]; then
      return 0
    fi
  done < <(find "$AGENT_SRC" -name "*.go" -o -name "go.mod" -o -name "go.sum" | tr '\n' '\0')
  return 1
}

sync_dev_sidecar() {
  local src="$1"
  local name="$2"
  mkdir -p "$DEV_OUT_DIR"
  cp "$src" "$DEV_OUT_DIR/$name"
  chmod +x "$DEV_OUT_DIR/$name"
}

GO_BIN="${GO_BIN:-}"
if [[ -z "$GO_BIN" ]]; then
  if command -v go >/dev/null 2>&1; then
    GO_BIN="$(command -v go)"
  elif [[ -x /opt/homebrew/bin/go ]]; then
    GO_BIN=/opt/homebrew/bin/go
  elif [[ -x /usr/local/go/bin/go ]]; then
    GO_BIN=/usr/local/go/bin/go
  else
    echo "build-agent: go not found; install Go or set GO_BIN" >&2
    exit 1
  fi
fi

echo "build-agent: compiling agent -> $OUT_AGENT"
(cd "$AGENT_SRC" && "$GO_BIN" build -o "$OUT_AGENT" .)

echo "build-agent: compiling mcp -> $OUT_MCP"
(cd "$AGENT_SRC" && "$GO_BIN" build -o "$OUT_MCP" ./cmd/superdev-mcp)

echo "build-agent: compiling sample -> $OUT_SAMPLE"
(cd "$AGENT_SRC" && "$GO_BIN" build -o "$OUT_SAMPLE" ./cmd/superdev-sample)

sync_dev_sidecar "$OUT_AGENT" "superdev-agent"
sync_dev_sidecar "$OUT_MCP" "superdev-mcp"
sync_dev_sidecar "$OUT_SAMPLE" "superdev-sample"

if [[ "$BUILD_REMOTE_INSTALL" == "1" ]]; then
  mkdir -p "$RESOURCE_DIR"
  targets=(
    "darwin amd64"
    "darwin arm64"
    "linux amd64"
    "linux arm64"
  )
  for target in "${targets[@]}"; do
    read -r goos goarch <<<"$target"
    remote_out="$RESOURCE_DIR/superdev-agent-$goos-$goarch"
    if needs_build "$remote_out"; then
      echo "build-agent: compiling remote agent -> $remote_out"
      (cd "$AGENT_SRC" && GOOS="$goos" GOARCH="$goarch" "$GO_BIN" build -o "$remote_out" .)
    fi
  done
fi
