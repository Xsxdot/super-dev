#!/usr/bin/env sh
# Remove services and artifacts created by the local-02 pipeline E2E suite.
#
# Responsibilities:
#   - Stop and disable known example systemd units.
#   - Remove the E2E deployment root on local-02.
#
# Boundaries:
#   - Does not run automatically after tests.
#   - Does not remove unrelated systemd units or directories.
set -eu

HOST="${SUPERDEV_E2E_LOCAL02_HOST:-100.90.99.61}"
USER="${SUPERDEV_E2E_LOCAL02_USER:-root}"
PORT="${SUPERDEV_E2E_LOCAL02_PORT:-22}"
KEY="${SUPERDEV_E2E_LOCAL02_KEY:-}"

cleanup_remote='
set -eu
for service in \
  superdev-example-go-http \
  superdev-example-node-http \
  superdev-example-python-http \
  superdev-example-java-springboot \
  superdev-example-rust-http \
  superdev-example-php-http \
  superdev-example-vue-go-combined
do
  systemctl stop "$service.service" 2>/dev/null || true
  systemctl disable "$service.service" 2>/dev/null || true
  rm -f "/etc/systemd/system/$service.service"
done
systemctl daemon-reload
rm -rf /opt/superdev-examples
'

if [ -n "$KEY" ]; then
  ssh -p "$PORT" -i "$KEY" "$USER@$HOST" "$cleanup_remote"
else
  ssh -p "$PORT" "$USER@$HOST" "$cleanup_remote"
fi
