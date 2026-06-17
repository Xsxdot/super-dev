#!/usr/bin/env bash
# build-agent.test.sh 验证桌面端 sidecar 构建策略。
#
# 职责：
#   - 用临时工作区和 fake go/rustc 复现构建脚本的缓存边界
#   - 确认本机 sidecar 不会因为 mtime 比源码新而跳过编译
#
# 边界：
#   - 不调用真实 Go 编译器
#   - 不修改仓库中的 sidecar 二进制
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

mkdir -p \
  "$TMP_DIR/desktop/scripts" \
  "$TMP_DIR/desktop/src-tauri/binaries" \
  "$TMP_DIR/desktop/src-tauri/resources/js-debug" \
  "$TMP_DIR/desktop/src-tauri/target/debug" \
  "$TMP_DIR/agent" \
  "$TMP_DIR/bin"
cp "$ROOT/scripts/build-agent.sh" "$TMP_DIR/desktop/scripts/build-agent.sh"

cat > "$TMP_DIR/agent/main.go" <<'EOF'
package main

func main() {}
EOF

cat > "$TMP_DIR/bin/rustc" <<'EOF'
#!/usr/bin/env bash
if [[ "$1" == "--print" && "$2" == "host-tuple" ]]; then
  echo "${BUILD_AGENT_TEST_TARGET:-test-target}"
  exit 0
fi
exit 1
EOF
chmod +x "$TMP_DIR/bin/rustc"

cat > "$TMP_DIR/bin/go" <<'EOF'
#!/usr/bin/env bash
log="${BUILD_AGENT_TEST_LOG:?missing BUILD_AGENT_TEST_LOG}"
out=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o)
      out="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
if [[ "$out" == "" ]]; then
  echo "fake go: missing -o" >&2
  exit 1
fi
printf '%s\n' "$out" >> "$log"
mkdir -p "$(dirname "$out")"
printf 'fake binary\n' > "$out"
EOF
chmod +x "$TMP_DIR/bin/go"

cat > "$TMP_DIR/bin/curl" <<'EOF'
#!/usr/bin/env bash
out=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o)
      out="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
if [[ "$out" == "" ]]; then
  echo "fake curl: missing -o" >&2
  exit 1
fi
mkdir -p "$(dirname "$out")"
printf 'fake js-debug archive\n' > "$out"
EOF
chmod +x "$TMP_DIR/bin/curl"

cat > "$TMP_DIR/bin/tar" <<'EOF'
#!/usr/bin/env bash
dest=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -C)
      dest="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
if [[ "$dest" == "" ]]; then
  echo "fake tar: missing -C" >&2
  exit 1
fi
mkdir -p "$dest/js-debug/src"
printf 'fake dap server\n' > "$dest/js-debug/src/dapDebugServer.js"
EOF
chmod +x "$TMP_DIR/bin/tar"

cat > "$TMP_DIR/bin/stat" <<'EOF'
#!/usr/bin/env bash
if [[ "$1" == "-c" && "$2" == "%Y" ]]; then
  case "$3" in
    *agent-install*) echo 200 ;;
    *) echo 100 ;;
  esac
  exit 0
fi
if [[ "$1" == "-f" ]]; then
  echo "  File: $3"
  exit 0
fi
echo "fake stat: unsupported args $*" >&2
exit 1
EOF
chmod +x "$TMP_DIR/bin/stat"

touch "$TMP_DIR/agent/main.go"
sleep 1
for name in superdev-agent superdev-mcp superdev-sample; do
  printf 'stale but newer\n' > "$TMP_DIR/desktop/src-tauri/binaries/$name-test-target"
  printf 'stale dev copy\n' > "$TMP_DIR/desktop/src-tauri/target/debug/$name"
done

export PATH="$TMP_DIR/bin:$PATH"
export GO_BIN="$TMP_DIR/bin/go"
export BUILD_AGENT_TEST_LOG="$TMP_DIR/go-calls.log"

bash "$TMP_DIR/desktop/scripts/build-agent.sh"

expected_agent="$TMP_DIR/desktop/src-tauri/binaries/superdev-agent-test-target"
expected_mcp="$TMP_DIR/desktop/src-tauri/binaries/superdev-mcp-test-target"
expected_sample="$TMP_DIR/desktop/src-tauri/binaries/superdev-sample-test-target"

for expected in "$expected_agent" "$expected_mcp" "$expected_sample"; do
  if ! grep -qx "$expected" "$BUILD_AGENT_TEST_LOG"; then
    echo "expected local sidecar rebuild for $expected" >&2
    echo "actual go calls:" >&2
    cat "$BUILD_AGENT_TEST_LOG" >&2
    exit 1
  fi
done

for name in superdev-agent superdev-mcp superdev-sample; do
  dev_copy="$TMP_DIR/desktop/src-tauri/target/debug/$name"
  if [[ "$(cat "$dev_copy")" != "fake binary" ]]; then
    echo "expected dev sidecar copy to be refreshed: $dev_copy" >&2
    exit 1
  fi
done

: > "$BUILD_AGENT_TEST_LOG"
BUILD_AGENT_TEST_TARGET="x86_64-pc-windows-msvc" bash "$TMP_DIR/desktop/scripts/build-agent.sh"

for name in superdev-agent superdev-mcp superdev-sample; do
  windows_sidecar="$TMP_DIR/desktop/src-tauri/binaries/$name-x86_64-pc-windows-msvc.exe"
  if ! grep -qx "$windows_sidecar" "$BUILD_AGENT_TEST_LOG"; then
    echo "expected windows sidecar rebuild with .exe suffix: $windows_sidecar" >&2
    echo "actual go calls:" >&2
    cat "$BUILD_AGENT_TEST_LOG" >&2
    exit 1
  fi

  windows_dev_copy="$TMP_DIR/desktop/src-tauri/target/debug/$name.exe"
  if [[ "$(cat "$windows_dev_copy")" != "fake binary" ]]; then
    echo "expected windows dev sidecar copy to be refreshed: $windows_dev_copy" >&2
    exit 1
  fi
done

BUILD_REMOTE_INSTALL=1 bash "$TMP_DIR/desktop/scripts/build-agent.sh"
: > "$BUILD_AGENT_TEST_LOG"
BUILD_REMOTE_INSTALL=1 bash "$TMP_DIR/desktop/scripts/build-agent.sh"

if grep -q '/agent-install/' "$BUILD_AGENT_TEST_LOG"; then
  echo "expected remote install binaries to be skipped when fake GNU stat marks them newer" >&2
  cat "$BUILD_AGENT_TEST_LOG" >&2
  exit 1
fi

js_debug_server="$TMP_DIR/desktop/src-tauri/resources/js-debug/src/dapDebugServer.js"
if [[ ! -f "$js_debug_server" ]]; then
  echo "expected js-debug standalone server to be prepared: $js_debug_server" >&2
  exit 1
fi
