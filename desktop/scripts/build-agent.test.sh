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
  "$TMP_DIR/validation/runtime" \
  "$TMP_DIR/agent" \
  "$TMP_DIR/bin"
cp "$ROOT/scripts/build-agent.sh" "$TMP_DIR/desktop/scripts/build-agent.sh"
cat > "$TMP_DIR/validation/runtime/targets.txt" <<'EOF'
darwin amd64
darwin arm64
linux amd64
linux arm64
windows amd64
EOF

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
if [[ "${BUILD_AGENT_TEST_ATOMIC_COPY:-0}" == "1" && "$out" == *superdev-mcp-* ]]; then
  printf '%1048576s' '' | tr ' ' 'N' > "$out"
else
  printf 'fake binary\n' > "$out"
fi
EOF
chmod +x "$TMP_DIR/bin/go"

cat > "$TMP_DIR/bin/cp" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [[ "${BUILD_AGENT_TEST_ATOMIC_COPY:-0}" == "1" && "$#" -eq 2 && "$1" == */binaries/superdev-mcp-* && "$2" == */target/debug/*superdev-mcp* ]]; then
  src="$1"
  dest="$2"
  : > "$dest"
  size="$(wc -c < "$src")"
  block=0
  while (( block * 4096 < size )); do
    dd if="$src" bs=4096 skip="$block" count=1 2>/dev/null >> "$dest"
    if [[ "${BUILD_AGENT_TEST_COPY_FAIL:-0}" == "1" ]]; then
      exit 23
    fi
    block=$((block + 1))
    sleep 0.002
  done
  exit 0
fi

exec /bin/cp "$@"
EOF
chmod +x "$TMP_DIR/bin/cp"

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

# Slow the MCP copy down and continuously inspect the published path. Atomic
# staging guarantees every observation is either the complete old executable
# or the complete new executable; a direct cp exposes truncated intermediate
# lengths and fails this check deterministically.
atomic_target="$TMP_DIR/desktop/src-tauri/target/debug/superdev-mcp"
atomic_old="$TMP_DIR/atomic-old"
atomic_new="$TMP_DIR/atomic-new"
printf '%1048576s' '' | tr ' ' 'O' > "$atomic_old"
printf '%1048576s' '' | tr ' ' 'N' > "$atomic_new"
/bin/cp "$atomic_old" "$atomic_target"

BUILD_AGENT_TEST_ATOMIC_COPY=1 bash "$TMP_DIR/desktop/scripts/build-agent.sh" &
atomic_pid=$!
atomic_invalid=0
atomic_samples=0
while kill -0 "$atomic_pid" 2>/dev/null; do
  if ! cmp -s "$atomic_target" "$atomic_old" && ! cmp -s "$atomic_target" "$atomic_new"; then
    atomic_invalid=1
    break
  fi
  atomic_samples=$((atomic_samples + 1))
  sleep 0.002
done
if ! wait "$atomic_pid"; then
  echo "expected atomic staging build to succeed" >&2
  exit 1
fi
if [[ "$atomic_invalid" == "1" || "$atomic_samples" -eq 0 ]]; then
  echo "published MCP sidecar exposed a partial copy" >&2
  exit 1
fi
if ! cmp -s "$atomic_target" "$atomic_new"; then
  echo "expected complete new MCP sidecar after atomic publish" >&2
  exit 1
fi
if compgen -G "$TMP_DIR/desktop/src-tauri/target/debug/.superdev-mcp.*" >/dev/null; then
  echo "expected successful atomic publish to leave no MCP temp file" >&2
  exit 1
fi

# A failed copy must preserve the prior published inode and clean its temporary
# file. This is the failure mode that previously left Codex launching a corrupt
# executable.
printf 'protected old MCP\n' > "$atomic_target"
if BUILD_AGENT_TEST_ATOMIC_COPY=1 BUILD_AGENT_TEST_COPY_FAIL=1 \
  bash "$TMP_DIR/desktop/scripts/build-agent.sh"; then
  echo "expected injected MCP copy failure" >&2
  exit 1
fi
if [[ "$(cat "$atomic_target")" != "protected old MCP" ]]; then
  echo "failed MCP staging replaced or truncated the published sidecar" >&2
  exit 1
fi
if compgen -G "$TMP_DIR/desktop/src-tauri/target/debug/.superdev-mcp.*" >/dev/null; then
  echo "failed MCP staging left a temporary sidecar behind" >&2
  exit 1
fi

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
