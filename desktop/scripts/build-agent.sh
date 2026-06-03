#!/usr/bin/env bash
# 仅在 agent 源码比 sidecar 新（或二进制缺失）时编译，避免 tauri dev 监听循环。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
AGENT_SRC="$ROOT/../agent"
OUT_DIR="$ROOT/src-tauri/binaries"
RESOURCE_DIR="$ROOT/src-tauri/resources/agent-install"
TARGET="$(rustc --print host-tuple)"
OUT_AGENT="$OUT_DIR/superdev-agent-$TARGET"
OUT_MCP="$OUT_DIR/superdev-mcp-$TARGET"
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

if ! needs_build "$OUT_AGENT" && ! needs_build "$OUT_MCP" && [[ "$BUILD_REMOTE_INSTALL" != "1" ]]; then
  exit 0
fi

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

if needs_build "$OUT_AGENT"; then
  echo "build-agent: compiling agent -> $OUT_AGENT"
  (cd "$AGENT_SRC" && "$GO_BIN" build -o "$OUT_AGENT" .)
fi

if needs_build "$OUT_MCP"; then
  echo "build-agent: compiling mcp -> $OUT_MCP"
  (cd "$AGENT_SRC" && "$GO_BIN" build -o "$OUT_MCP" ./cmd/superdev-mcp)
fi

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
