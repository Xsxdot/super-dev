#!/usr/bin/env bash
# 每次桌面端启动前都刷新本机 sidecar，避免 stale 二进制通过 mtime 缓存被误用。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
AGENT_SRC="$ROOT/../agent"
OUT_DIR="$ROOT/src-tauri/binaries"
RESOURCE_ROOT="$ROOT/src-tauri/resources"
RESOURCE_DIR="$RESOURCE_ROOT/agent-install"
JS_DEBUG_VERSION="${JS_DEBUG_VERSION:-1.117.0}"
JS_DEBUG_RESOURCE_DIR="$RESOURCE_ROOT/js-debug"
JS_DEBUG_CACHE_DIR="$ROOT/src-tauri/target/js-debug-cache"
JS_DEBUG_ARCHIVE="$JS_DEBUG_CACHE_DIR/js-debug-dap-v$JS_DEBUG_VERSION.tar.gz"
JS_DEBUG_URL="https://github.com/microsoft/vscode-js-debug/releases/download/v$JS_DEBUG_VERSION/js-debug-dap-v$JS_DEBUG_VERSION.tar.gz"
DEV_OUT_DIR="$ROOT/src-tauri/target/debug"
TARGET="$(rustc --print host-tuple)"
HOST_BIN_SUFFIX=""
if [[ "$TARGET" == *windows* ]]; then
  HOST_BIN_SUFFIX=".exe"
fi
OUT_AGENT="$OUT_DIR/superdev-agent-$TARGET$HOST_BIN_SUFFIX"
OUT_MCP="$OUT_DIR/superdev-mcp-$TARGET$HOST_BIN_SUFFIX"
OUT_SAMPLE="$OUT_DIR/superdev-sample-$TARGET$HOST_BIN_SUFFIX"
BUILD_REMOTE_INSTALL="${BUILD_REMOTE_INSTALL:-0}"

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

prepare_js_debug() {
  local server="$JS_DEBUG_RESOURCE_DIR/src/dapDebugServer.js"
  local version_file="$JS_DEBUG_RESOURCE_DIR/.superdev-version"
  if [[ -f "$server" ]] && [[ -s "$server" ]] && [[ -f "$version_file" ]] && [[ "$(cat "$version_file")" == "$JS_DEBUG_VERSION" ]]; then
    printf '# Generated js-debug resources are written here by desktop/scripts/build-agent.sh.\n' > "$JS_DEBUG_RESOURCE_DIR/.gitkeep"
    return 0
  fi
  if ! command -v curl >/dev/null 2>&1; then
    echo "build-agent: curl not found; install curl or pre-populate $JS_DEBUG_RESOURCE_DIR" >&2
    exit 1
  fi

  mkdir -p "$JS_DEBUG_CACHE_DIR"
  if [[ ! -f "$JS_DEBUG_ARCHIVE" ]] || [[ ! -s "$JS_DEBUG_ARCHIVE" ]]; then
    echo "build-agent: downloading js-debug v$JS_DEBUG_VERSION -> $JS_DEBUG_ARCHIVE"
    curl -fsSL "$JS_DEBUG_URL" -o "$JS_DEBUG_ARCHIVE"
  fi

  local tmp_dir
  tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/superdev-js-debug.XXXXXX")"
  trap 'rm -rf "$tmp_dir"' RETURN
  tar -xzf "$JS_DEBUG_ARCHIVE" -C "$tmp_dir"
  if [[ ! -f "$tmp_dir/js-debug/src/dapDebugServer.js" ]]; then
    echo "build-agent: js-debug archive missing js-debug/src/dapDebugServer.js" >&2
    exit 1
  fi
  rm -rf "$JS_DEBUG_RESOURCE_DIR"
  mkdir -p "$(dirname "$JS_DEBUG_RESOURCE_DIR")"
  cp -R "$tmp_dir/js-debug" "$JS_DEBUG_RESOURCE_DIR"
  printf '# Generated js-debug resources are written here by desktop/scripts/build-agent.sh.\n' > "$JS_DEBUG_RESOURCE_DIR/.gitkeep"
  printf '%s\n' "$JS_DEBUG_VERSION" > "$JS_DEBUG_RESOURCE_DIR/.superdev-version"
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

sync_dev_sidecar "$OUT_AGENT" "superdev-agent$HOST_BIN_SUFFIX"
sync_dev_sidecar "$OUT_MCP" "superdev-mcp$HOST_BIN_SUFFIX"
sync_dev_sidecar "$OUT_SAMPLE" "superdev-sample$HOST_BIN_SUFFIX"
prepare_js_debug

if [[ "$BUILD_REMOTE_INSTALL" == "1" ]]; then
  mkdir -p "$RESOURCE_DIR"
  targets=(
    "darwin amd64"
    "darwin arm64"
    "linux amd64"
    "linux arm64"
    "windows amd64"
  )
  for target in "${targets[@]}"; do
    read -r goos goarch <<<"$target"
    suffix=""
    if [[ "$goos" == "windows" ]]; then
      suffix=".exe"
    fi
    remote_out="$RESOURCE_DIR/superdev-agent-$goos-$goarch$suffix"
    if needs_build "$remote_out"; then
      echo "build-agent: compiling remote agent -> $remote_out"
      (cd "$AGENT_SRC" && GOOS="$goos" GOARCH="$goarch" "$GO_BIN" build -o "$remote_out" .)
    fi
  done
fi
