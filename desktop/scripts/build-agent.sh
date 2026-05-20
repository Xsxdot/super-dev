#!/usr/bin/env bash
# 仅在 agent 源码比 sidecar 新（或二进制缺失）时编译，避免 tauri dev 监听循环。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
AGENT_SRC="$ROOT/../agent"
OUT_DIR="$ROOT/src-tauri/binaries"
TARGET="$(rustc --print host-tuple)"
OUT="$OUT_DIR/superdev-agent-$TARGET"

mkdir -p "$OUT_DIR"

if [[ ! -f "$AGENT_SRC/main.go" ]]; then
  echo "build-agent: agent source not found at $AGENT_SRC" >&2
  exit 1
fi

needs_build() {
  if [[ ! -f "$OUT" ]] || [[ ! -s "$OUT" ]]; then
    return 0
  fi
  local bin_mtime
  bin_mtime=$(stat -f '%m' "$OUT" 2>/dev/null || stat -c '%Y' "$OUT")
  local f mtime
  while IFS= read -r -d '' f; do
    mtime=$(stat -f '%m' "$f" 2>/dev/null || stat -c '%Y' "$f")
    if [[ "$mtime" -gt "$bin_mtime" ]]; then
      return 0
    fi
  done < <(find "$AGENT_SRC" -name "*.go" -o -name "go.mod" -o -name "go.sum" | tr '\n' '\0')
  return 1
}

if ! needs_build; then
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

echo "build-agent: compiling agent -> $OUT"
(cd "$AGENT_SRC" && "$GO_BIN" build -o "$OUT" .)
