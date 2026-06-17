#!/usr/bin/env sh
# Static SuperDev remote agent installer for release assets.
#
# Responsibilities:
#   - Detect the target Linux/macOS platform and CPU architecture.
#   - Download a matching superdev-agent release binary.
#   - Install the binary to /usr/local/bin and print the service command.
#
# Boundaries:
#   - Does not configure systemd, launchd, or Windows services.
#   - Does not contact a SuperDev controller.
#   - Does not persist host identity or security state.
set -eu

usage() {
  cat >&2 <<'USAGE'
Usage: install-agent.sh --binary-base-url <url> --host-id <host-id> --transport <transport> --bind-address <address> --port <port> --bootstrap-token <token> --require-auth

Options:
  --binary-base-url <url>  Base URL containing superdev-agent-<os>-<arch> assets.
  --binary-url <url>       Exact binary URL. Overrides --binary-base-url.
  --host-id <host-id>      SuperDev host ID.
  --transport <transport>  direct or tunnel.
  --bind-address <addr>    Agent listen address.
  --port <port>            Agent listen port.
  --bootstrap-token <tok>  One-time bootstrap token.
  --require-auth           Require token authentication.
USAGE
}

BINARY_BASE_URL="${SUPERDEV_AGENT_BINARY_BASE_URL:-}"
BINARY_URL="${SUPERDEV_AGENT_BINARY_URL:-}"
HOST_ID=""
TRANSPORT=""
BIND_ADDRESS=""
PORT=""
BOOTSTRAP_TOKEN=""
REQUIRE_AUTH="false"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --binary-base-url) BINARY_BASE_URL="${2:-}"; shift 2 ;;
    --binary-url) BINARY_URL="${2:-}"; shift 2 ;;
    --host-id) HOST_ID="${2:-}"; shift 2 ;;
    --transport) TRANSPORT="${2:-}"; shift 2 ;;
    --bind-address) BIND_ADDRESS="${2:-}"; shift 2 ;;
    --port) PORT="${2:-}"; shift 2 ;;
    --bootstrap-token) BOOTSTRAP_TOKEN="${2:-}"; shift 2 ;;
    --require-auth) REQUIRE_AUTH="true"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage; exit 2 ;;
  esac
done

if [ -z "$HOST_ID" ] || [ -z "$TRANSPORT" ] || [ -z "$BIND_ADDRESS" ] || [ -z "$PORT" ] || [ -z "$BOOTSTRAP_TOKEN" ] || [ "$REQUIRE_AUTH" != "true" ]; then
  usage
  exit 64
fi

target_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$target_os" in
  linux) target_os="linux" ;;
  darwin) target_os="darwin" ;;
  *) echo "unsupported target OS: $target_os" >&2; exit 64 ;;
esac

target_arch="$(uname -m)"
case "$target_arch" in
  x86_64|amd64) target_arch="amd64" ;;
  arm64|aarch64) target_arch="arm64" ;;
  *) echo "unsupported target arch: $target_arch" >&2; exit 64 ;;
esac

if [ -z "$BINARY_URL" ]; then
  if [ -z "$BINARY_BASE_URL" ]; then
    echo "set --binary-base-url, --binary-url, SUPERDEV_AGENT_BINARY_BASE_URL, or SUPERDEV_AGENT_BINARY_URL" >&2
    exit 64
  fi
  BINARY_URL="${BINARY_BASE_URL%/}/superdev-agent-${target_os}-${target_arch}"
fi

tmp="$(mktemp)"
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT

if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$BINARY_URL" -o "$tmp"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "$tmp" "$BINARY_URL"
else
  echo "curl or wget is required to download $BINARY_URL" >&2
  exit 64
fi

chmod +x "$tmp"
if command -v sudo >/dev/null 2>&1; then
  sudo -n install -m 0755 "$tmp" /usr/local/bin/superdev-agent
else
  install -m 0755 "$tmp" /usr/local/bin/superdev-agent
fi

echo "superdev-agent binary installed for host $HOST_ID."
echo "Configure your service manager to run:"
echo "  /usr/local/bin/superdev-agent --addr ${BIND_ADDRESS}:${PORT} --require-auth --bootstrap-token ${BOOTSTRAP_TOKEN}"
