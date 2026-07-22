#!/usr/bin/env bash
# 每次桌面端启动前都刷新本机 sidecar，避免 stale 二进制通过 mtime 缓存被误用。
#
# 职责：
#   - 编译本机 Tauri externalBin sidecar 与可选的 remote-install 资源二进制
#   - 在设置 APPLE_SIGNING_IDENTITY 时，为 agent-install 内的 darwin Mach-O 补 Developer ID 签名
#     （Tauri 只签 Contents/MacOS，不会签 Resources，否则公证会 Invalid）
#
# 边界：
#   - 不负责最终 .app 打包与公证提交（由 tauri build 完成）
#   - 不签 linux/windows 远程安装包（Apple 公证只校验 Mach-O）
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
AGENT_SRC="$ROOT/../agent"
OUT_DIR="$ROOT/src-tauri/binaries"
RESOURCE_ROOT="$ROOT/src-tauri/resources"
RESOURCE_DIR="$RESOURCE_ROOT/agent-install"
REMOTE_TARGETS_FILE="$ROOT/../validation/runtime/targets.txt"
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
UNINSTALL_SCRIPTS=("uninstall-agent.sh" "uninstall-agent.ps1")

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
  bin_mtime=$(stat -c '%Y' "$out" 2>/dev/null || stat -f '%m' "$out")
  local f mtime
  while IFS= read -r -d '' f; do
    mtime=$(stat -c '%Y' "$f" 2>/dev/null || stat -f '%m' "$f")
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
  (
    local tmp
    tmp="$(mktemp "$DEV_OUT_DIR/.${name}.XXXXXX")"
    trap 'rm -f "$tmp"' EXIT

    # The destination may be executed by a long-lived coding agent. Build the
    # complete replacement on the same filesystem, then rename it into place so
    # readers observe either the old inode or the complete new inode, never a
    # truncated executable.
    cp "$src" "$tmp"
    chmod +x "$tmp"

    # If sidecar signing is added, sign and verify "$tmp" here before publish.
    mv -f "$tmp" "$DEV_OUT_DIR/$name"
  )
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

# 为 agent-install 中的 darwin 远程安装二进制补签 Developer ID。
# 原因：Tauri 只会签 Contents/MacOS 下的 externalBin；Resources 内的 Mach-O 仍会进入公证扫描。
# 未签 / 仅 ad-hoc / 无 hardened runtime / 无 timestamp 都会导致 notarize status=Invalid。
sign_darwin_remote_install_agents() {
  local identity="${APPLE_SIGNING_IDENTITY:-}"
  if [[ -z "$identity" ]]; then
    echo "build-agent: APPLE_SIGNING_IDENTITY unset; skip signing agent-install darwin binaries"
    return 0
  fi
  if ! command -v codesign >/dev/null 2>&1; then
    echo "build-agent: codesign not found but APPLE_SIGNING_IDENTITY is set" >&2
    exit 1
  fi
  if [[ ! -d "$RESOURCE_DIR" ]]; then
    echo "build-agent: agent-install dir missing; nothing to sign: $RESOURCE_DIR"
    return 0
  fi

  local signed=0
  local bin
  # 只签 macOS 目标：Apple 公证只校验 Mach-O，linux/windows 安装包保持未签。
  for bin in "$RESOURCE_DIR"/superdev-agent-darwin-*; do
    if [[ ! -f "$bin" ]]; then
      continue
    fi
    echo "build-agent: codesigning remote install agent -> $bin"
    # --options runtime = hardened runtime；--timestamp = 安全时间戳；二者均为公证硬性要求。
    if ! codesign --force --options runtime --timestamp --sign "$identity" "$bin"; then
      echo "build-agent: codesign failed for $bin (identity=$identity)" >&2
      exit 1
    fi
    if ! codesign --verify --verbose=2 "$bin"; then
      echo "build-agent: codesign verify failed for $bin" >&2
      exit 1
    fi
    signed=$((signed + 1))
  done

  if [[ "$signed" -eq 0 ]]; then
    echo "build-agent: APPLE_SIGNING_IDENTITY set but no superdev-agent-darwin-* binaries under $RESOURCE_DIR" >&2
    exit 1
  fi
  echo "build-agent: signed $signed agent-install darwin binary(ies) with identity=$identity"
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
  if [[ ! -f "$REMOTE_TARGETS_FILE" ]]; then
    echo "build-agent: shared target contract not found at $REMOTE_TARGETS_FILE" >&2
    exit 1
  fi
  targets=()
  # Windows checkout 可能把 LF 合同变成 CRLF；GOARCH 尾部的 \r 会让 go 报
  # unsupported GOOS/GOARCH pair（日志里 \r 还常被吞掉，看起来像合法的 darwin/amd64）。
  while IFS=$' \t\r\n' read -r goos goarch extra || [[ -n "${goos:-}" ]]; do
    goos="${goos//$'\r'/}"
    goarch="${goarch//$'\r'/}"
    extra="${extra//$'\r'/}"
    if [[ -z "${goos:-}" || "$goos" == \#* ]]; then
      continue
    fi
    if [[ -z "${goarch:-}" || -n "${extra:-}" ]]; then
      echo "build-agent: invalid target contract row: $goos ${goarch:-} ${extra:-}" >&2
      exit 1
    fi
    targets+=("$goos $goarch")
  done < "$REMOTE_TARGETS_FILE"
  if [[ "${#targets[@]}" -eq 0 ]]; then
    echo "build-agent: shared target contract is empty" >&2
    exit 1
  fi
  for script in "${UNINSTALL_SCRIPTS[@]}"; do
    source_script="$ROOT/../scripts/$script"
    if [[ ! -f "$source_script" ]]; then
      echo "build-agent: uninstall script not found at $source_script" >&2
      exit 1
    fi
    echo "build-agent: bundling manual uninstall script -> $RESOURCE_DIR/$script"
    cp "$source_script" "$RESOURCE_DIR/$script"
  done
  for target in "${targets[@]}"; do
    read -r goos goarch <<<"$target"
    suffix=""
    if [[ "$goos" == "windows" ]]; then
      suffix=".exe"
    fi
    remote_out="$RESOURCE_DIR/superdev-agent-$goos-$goarch$suffix"
    if needs_build "$remote_out"; then
      echo "build-agent: compiling remote agent -> $remote_out (GOOS=$goos GOARCH=$goarch)"
      # CGO_ENABLED=0：远程安装二进制必须可从任意宿主交叉编译，不依赖本机 C 工具链。
      (cd "$AGENT_SRC" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" "$GO_BIN" build -o "$remote_out" .)
    fi
  done
  # 即使二进制因 mtime 缓存未重编，也必须在本机 release/公证路径上重新签一遍，
  # 避免沿用上次 ad-hoc/无 timestamp 的签名导致公证 Invalid。
  sign_darwin_remote_install_agents
fi
