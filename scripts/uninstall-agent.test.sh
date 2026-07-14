#!/usr/bin/env bash
# uninstall-agent.test.sh validates the published Unix manual uninstall contract.
#
# Responsibilities:
#   - Run uninstall-agent.sh against isolated Linux and macOS fixture roots.
#   - Verify retention, purge, idempotency, ownership boundaries, and structured output.
#
# Boundaries:
#   - Never addresses the developer machine's real service manager or Agent paths.
#   - Does not validate the Windows PowerShell implementation.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="$ROOT/scripts/uninstall-agent.sh"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

fail() {
  echo "uninstall-agent.test: $*" >&2
  exit 1
}

new_fixture() {
  local name="$1"
  local os="$2"
  local fixture="$TMP_DIR/$name"
  mkdir -p "$fixture/root" "$fixture/bin" "$fixture/home"
  : > "$fixture/commands.log"

  cat > "$fixture/bin/uname" <<EOF
#!/usr/bin/env sh
printf '%s\n' '$os'
EOF
  cat > "$fixture/bin/id" <<'EOF'
#!/usr/bin/env sh
if [ "${1:-}" = "-u" ]; then printf '%s\n' "${SUPERDEV_TEST_UID:-501}"; exit 0; fi
exit 2
EOF
  cat > "$fixture/bin/systemctl" <<'EOF'
#!/usr/bin/env sh
printf 'systemctl %s\n' "$*" >> "${SUPERDEV_TEST_COMMAND_LOG:?}"
if [ -n "${SUPERDEV_TEST_SYSTEMCTL_FAIL_ON:-}" ] && [ "${1:-}" = "$SUPERDEV_TEST_SYSTEMCTL_FAIL_ON" ]; then
  echo "fixture systemctl ${1:-} failure" >&2
  exit 7
fi
case "${1:-}" in
  show) printf 'LoadState=loaded\nActiveState=active\n' ;;
esac
EOF
  cat > "$fixture/bin/launchctl" <<'EOF'
#!/usr/bin/env sh
printf 'launchctl %s\n' "$*" >> "${SUPERDEV_TEST_COMMAND_LOG:?}"
if [ -n "${SUPERDEV_TEST_LAUNCHCTL_FAIL_ON:-}" ] && [ "${1:-}" = "$SUPERDEV_TEST_LAUNCHCTL_FAIL_ON" ]; then
  echo "fixture launchctl ${1:-} failure" >&2
  exit 7
fi
if [ "${1:-}" = "print" ]; then
  if [ "${SUPERDEV_TEST_SYSTEM_DOMAIN_MISSING:-}" = "1" ] && [ "${2:-}" = "system/dev.superdev.agent" ]; then
    echo 'Could not find domain for user gui: 501' >&2
    exit 112
  fi
  if [ "${SUPERDEV_TEST_GUI_DOMAIN_MISSING:-}" = "1" ]; then
    case "$2" in
      gui/*) echo 'Could not find domain for user gui: 501' >&2; exit 112 ;;
    esac
  fi
  case "${SUPERDEV_TEST_LAUNCHD_MODE:-missing}:$2" in
    system:system/dev.superdev.agent) exit 0 ;;
    user:gui/*/dev.superdev.agent) exit 0 ;;
    *) echo 'Could not find service dev.superdev.agent' >&2; exit 1 ;;
  esac
fi
EOF
  cat > "$fixture/bin/pgrep" <<'EOF'
#!/usr/bin/env sh
exit "${SUPERDEV_TEST_PGREP_STATUS:-1}"
EOF
  cat > "$fixture/bin/plutil" <<'EOF'
#!/usr/bin/env python3
import plistlib
import sys

if len(sys.argv) != 7 or sys.argv[1:5] != ['-extract', 'ProgramArguments.0', 'raw', '-o'] or sys.argv[5] != '-':
    print('fixture plutil: unsupported arguments', file=sys.stderr)
    raise SystemExit(2)
with open(sys.argv[6], 'rb') as handle:
    value = plistlib.load(handle)['ProgramArguments'][0]
print(value)
EOF
  chmod +x "$fixture/bin/"*
  printf '%s\n' "$fixture"
}

run_script() {
  local fixture="$1"
  shift
  PATH="$fixture/bin:/usr/bin:/bin" \
    HOME="$fixture/home" \
    SUPERDEV_UNINSTALL_TESTING=1 \
    SUPERDEV_UNINSTALL_FIXTURE_ROOT="$fixture/root" \
    SUPERDEV_TEST_COMMAND_LOG="$fixture/commands.log" \
    "$SCRIPT" "$@"
}

linux="$(new_fixture linux Linux)"
mkdir -p "$linux/root/etc/systemd/system" "$linux/root/usr/local/bin" "$linux/root/var/lib/superdev-agent"
cat > "$linux/root/etc/systemd/system/superdev-agent.service" <<'EOF'
[Service]
ExecStart=/usr/local/bin/superdev-agent --addr :57017
EOF
: > "$linux/root/usr/local/bin/superdev-agent"
: > "$linux/root/var/lib/superdev-agent/security.json"

linux_output="$(run_script "$linux")"
[[ "$linux_output" == *'level=INFO stage=detect'* ]] || fail "Linux output lacks detect stage"
[[ "$linux_output" == *'level=INFO stage=complete'* ]] || fail "Linux output lacks success stage"
[[ ! -e "$linux/root/etc/systemd/system/superdev-agent.service" ]] || fail "Linux unit was not removed"
[[ ! -e "$linux/root/usr/local/bin/superdev-agent" ]] || fail "Linux binary was not removed"
[[ -e "$linux/root/var/lib/superdev-agent/security.json" ]] || fail "Linux data was removed by default"
grep -qx 'systemctl stop superdev-agent.service' "$linux/commands.log" || fail "expected exact Agent unit stop"
grep -qx 'systemctl disable superdev-agent.service' "$linux/commands.log" || fail "expected exact Agent unit disable"
grep -q 'docker' "$linux/commands.log" && fail "must not address Docker"

run_script "$linux" >/dev/null
run_script "$linux" --purge >/dev/null
[[ ! -e "$linux/root/var/lib/superdev-agent" ]] || fail "Linux purge did not remove Agent data"

linux_failure="$(new_fixture linux-failure Linux)"
mkdir -p "$linux_failure/root/etc/systemd/system" "$linux_failure/root/usr/local/bin"
cat > "$linux_failure/root/etc/systemd/system/superdev-agent.service" <<'EOF'
[Service]
ExecStart=/usr/local/bin/superdev-agent --addr :57017
EOF
: > "$linux_failure/root/usr/local/bin/superdev-agent"
export SUPERDEV_TEST_SYSTEMCTL_FAIL_ON=stop
if run_script "$linux_failure" >"$linux_failure/output.log" 2>&1; then
  fail "Linux systemctl stop failure must fail the script"
fi
unset SUPERDEV_TEST_SYSTEMCTL_FAIL_ON
grep -q 'level=ERROR stage=linux_systemd' "$linux_failure/output.log" || fail "Linux action failure lacks its cleanup stage"
grep -q 'fixture systemctl stop failure' "$linux_failure/output.log" || fail "Linux action failure lacks command context"
[[ -e "$linux_failure/root/etc/systemd/system/superdev-agent.service" ]] || fail "Linux resources changed after stop failure"

mac_user="$(new_fixture mac-user Darwin)"
export SUPERDEV_TEST_LAUNCHD_MODE=user
mkdir -p \
  "$mac_user/home/Library/LaunchAgents" \
  "$mac_user/home/Library/Application Support/SuperDev/Agent/bin" \
  "$mac_user/home/Library/Application Support/SuperDev/Agent/data" \
  "$mac_user/home/Library/Logs"
cat > "$mac_user/home/Library/LaunchAgents/dev.superdev.agent.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
  <dict>
    <key>ProgramArguments</key>
    <array>
      <string>$mac_user/home/Library/Application Support/SuperDev/Agent/bin/superdev-agent</string>
    </array>
  </dict>
</plist>
EOF
: > "$mac_user/home/Library/Application Support/SuperDev/Agent/bin/superdev-agent"
: > "$mac_user/home/Library/Application Support/SuperDev/Agent/data/security.json"
: > "$mac_user/home/Library/Logs/superdev-agent.log"
: > "$mac_user/home/Library/Logs/superdev-agent.err.log"

run_script "$mac_user" >/dev/null
[[ ! -e "$mac_user/home/Library/LaunchAgents/dev.superdev.agent.plist" ]] || fail "macOS user plist was not removed"
[[ ! -e "$mac_user/home/Library/Application Support/SuperDev/Agent/bin/superdev-agent" ]] || fail "macOS user binary was not removed"
[[ -e "$mac_user/home/Library/Application Support/SuperDev/Agent/data/security.json" ]] || fail "macOS user data was removed by default"
[[ -e "$mac_user/home/Library/Logs/superdev-agent.log" ]] || fail "macOS user logs were removed by default"
run_script "$mac_user" --purge >/dev/null
[[ ! -e "$mac_user/home/Library/Application Support/SuperDev/Agent" ]] || fail "macOS user purge did not remove data root"
[[ ! -e "$mac_user/home/Library/Logs/superdev-agent.log" ]] || fail "macOS user purge did not remove stdout log"
unset SUPERDEV_TEST_LAUNCHD_MODE

mac_failure="$(new_fixture mac-failure Darwin)"
export SUPERDEV_TEST_LAUNCHD_MODE=user
export SUPERDEV_TEST_LAUNCHCTL_FAIL_ON=bootout
mkdir -p "$mac_failure/home/Library/LaunchAgents" "$mac_failure/home/Library/Application Support/SuperDev/Agent/bin"
cat > "$mac_failure/home/Library/LaunchAgents/dev.superdev.agent.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
  <dict>
    <key>ProgramArguments</key>
    <array>
      <string>$mac_failure/home/Library/Application Support/SuperDev/Agent/bin/superdev-agent</string>
    </array>
  </dict>
</plist>
EOF
: > "$mac_failure/home/Library/Application Support/SuperDev/Agent/bin/superdev-agent"
if run_script "$mac_failure" >"$mac_failure/output.log" 2>&1; then
  fail "macOS launchctl bootout failure must fail the script"
fi
unset SUPERDEV_TEST_LAUNCHD_MODE SUPERDEV_TEST_LAUNCHCTL_FAIL_ON
grep -q 'level=ERROR stage=macos_launchagent' "$mac_failure/output.log" || fail "macOS action failure lacks its cleanup stage"
grep -q 'fixture launchctl bootout failure' "$mac_failure/output.log" || fail "macOS action failure lacks command context"
[[ -e "$mac_failure/home/Library/LaunchAgents/dev.superdev.agent.plist" ]] || fail "macOS resources changed after bootout failure"

mac_system="$(new_fixture mac-system Darwin)"
export SUPERDEV_TEST_LAUNCHD_MODE=system
export SUPERDEV_TEST_GUI_DOMAIN_MISSING=1
mkdir -p \
  "$mac_system/root/Library/LaunchDaemons" \
  "$mac_system/root/usr/local/bin" \
  "$mac_system/root/Library/Application Support/SuperDev/Agent" \
  "$mac_system/root/var/log"
cat > "$mac_system/root/Library/LaunchDaemons/dev.superdev.agent.plist" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
  <dict>
    <key>ProgramArguments</key>
    <array>
      <string>/usr/local/bin/superdev-agent</string>
    </array>
  </dict>
</plist>
EOF
: > "$mac_system/root/usr/local/bin/superdev-agent"
: > "$mac_system/root/Library/Application Support/SuperDev/Agent/security.json"
: > "$mac_system/root/var/log/superdev-agent.log"
: > "$mac_system/root/var/log/superdev-agent.err.log"

run_script "$mac_system" >/dev/null
[[ ! -e "$mac_system/root/Library/LaunchDaemons/dev.superdev.agent.plist" ]] || fail "macOS system plist was not removed"
[[ ! -e "$mac_system/root/usr/local/bin/superdev-agent" ]] || fail "macOS system binary was not removed"
[[ -e "$mac_system/root/Library/Application Support/SuperDev/Agent/security.json" ]] || fail "macOS system data was removed by default"
[[ -e "$mac_system/root/var/log/superdev-agent.log" ]] || fail "macOS system logs were removed by default"
run_script "$mac_system" --purge >/dev/null
[[ ! -e "$mac_system/root/Library/Application Support/SuperDev/Agent" ]] || fail "macOS system purge did not remove data root"
[[ ! -e "$mac_system/root/var/log/superdev-agent.log" ]] || fail "macOS system purge did not remove stdout log"
unset SUPERDEV_TEST_LAUNCHD_MODE SUPERDEV_TEST_GUI_DOMAIN_MISSING

mac_system_domain_error="$(new_fixture mac-system-domain-error Darwin)"
export SUPERDEV_TEST_SYSTEM_DOMAIN_MISSING=1
mkdir -p \
  "$mac_system_domain_error/root/Library/LaunchDaemons" \
  "$mac_system_domain_error/root/usr/local/bin" \
  "$mac_system_domain_error/root/Library/Application Support/SuperDev/Agent" \
  "$mac_system_domain_error/root/var/log"
cat > "$mac_system_domain_error/root/Library/LaunchDaemons/dev.superdev.agent.plist" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>ProgramArguments</key><array><string>/usr/local/bin/superdev-agent</string></array></dict></plist>
EOF
: > "$mac_system_domain_error/root/usr/local/bin/superdev-agent"
: > "$mac_system_domain_error/root/Library/Application Support/SuperDev/Agent/security.json"
: > "$mac_system_domain_error/root/var/log/superdev-agent.log"
: > "$mac_system_domain_error/root/var/log/superdev-agent.err.log"
if run_script "$mac_system_domain_error" --purge >"$mac_system_domain_error/output.log" 2>&1; then
  fail "a GUI-domain error for the system target must fail closed"
fi
unset SUPERDEV_TEST_SYSTEM_DOMAIN_MISSING
grep -q 'level=ERROR stage=detect' "$mac_system_domain_error/output.log" || fail "system-target domain error lacks detect stage"
grep -q 'launchctl bootout' "$mac_system_domain_error/commands.log" && fail "system-target domain error triggered bootout"
[[ -e "$mac_system_domain_error/root/Library/LaunchDaemons/dev.superdev.agent.plist" ]] || fail "system-target domain error removed the plist"
[[ -e "$mac_system_domain_error/root/usr/local/bin/superdev-agent" ]] || fail "system-target domain error removed the binary"
[[ -e "$mac_system_domain_error/root/Library/Application Support/SuperDev/Agent/security.json" ]] || fail "system-target domain error purged Agent data"
[[ -e "$mac_system_domain_error/root/var/log/superdev-agent.log" ]] || fail "system-target domain error purged Agent logs"

mac_ambiguous="$(new_fixture mac-ambiguous Darwin)"
export SUPERDEV_TEST_LAUNCHD_MODE=system
export SUPERDEV_TEST_GUI_DOMAIN_MISSING=1
mkdir -p \
  "$mac_ambiguous/root/Library/LaunchDaemons" \
  "$mac_ambiguous/root/usr/local/bin" \
  "$mac_ambiguous/home/Library/LaunchAgents" \
  "$mac_ambiguous/home/Library/Application Support/SuperDev/Agent/bin"
cat > "$mac_ambiguous/root/Library/LaunchDaemons/dev.superdev.agent.plist" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>ProgramArguments</key><array><string>/usr/local/bin/superdev-agent</string></array></dict></plist>
EOF
cat > "$mac_ambiguous/home/Library/LaunchAgents/dev.superdev.agent.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>ProgramArguments</key><array><string>$mac_ambiguous/home/Library/Application Support/SuperDev/Agent/bin/superdev-agent</string></array></dict></plist>
EOF
: > "$mac_ambiguous/root/usr/local/bin/superdev-agent"
: > "$mac_ambiguous/home/Library/Application Support/SuperDev/Agent/bin/superdev-agent"
if run_script "$mac_ambiguous" >"$mac_ambiguous/output.log" 2>&1; then
  fail "ambiguous macOS layouts must fail even when the GUI domain is absent"
fi
unset SUPERDEV_TEST_LAUNCHD_MODE SUPERDEV_TEST_GUI_DOMAIN_MISSING
grep -q 'level=ERROR stage=detect' "$mac_ambiguous/output.log" || fail "ambiguous layout lacks detect error"
grep -q 'launchctl bootout' "$mac_ambiguous/commands.log" && fail "ambiguous layout mutated launchd before failing"
[[ -e "$mac_ambiguous/root/Library/LaunchDaemons/dev.superdev.agent.plist" ]] || fail "ambiguous system plist was removed"
[[ -e "$mac_ambiguous/home/Library/LaunchAgents/dev.superdev.agent.plist" ]] || fail "ambiguous user plist was removed"

custom="$(new_fixture custom Linux)"
mkdir -p "$custom/root/etc/systemd/system"
cat > "$custom/root/etc/systemd/system/superdev-agent.service" <<'EOF'
[Service]
# Old documentation mentioned ExecStart=/usr/local/bin/superdev-agent.
ExecStart=/opt/custom/superdev-agent
EOF
if run_script "$custom" >"$custom/output.log" 2>&1; then
  fail "custom Linux layout must fail"
fi
grep -q 'level=ERROR stage=detect' "$custom/output.log" || fail "custom layout error lacks structured detect stage"
[[ -e "$custom/root/etc/systemd/system/superdev-agent.service" ]] || fail "custom layout was mutated"

mac_custom="$(new_fixture mac-custom Darwin)"
mkdir -p "$mac_custom/home/Library/LaunchAgents"
cat > "$mac_custom/home/Library/LaunchAgents/dev.superdev.agent.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
  <dict>
    <key>FixtureNote</key>
    <string>$mac_custom/home/Library/Application Support/SuperDev/Agent/bin/superdev-agent</string>
    <!--
    <key>ProgramArguments</key>
    <array>
      <string>$mac_custom/home/Library/Application Support/SuperDev/Agent/bin/superdev-agent</string>
    </array>
    -->
    <key>UnrelatedPaths</key>
    <array>
      <string>$mac_custom/home/Library/Application Support/SuperDev/Agent/bin/superdev-agent</string>
    </array>
    <key>ProgramArguments</key>
    <array>
      <string>/opt/custom/superdev-agent</string>
    </array>
  </dict>
</plist>
EOF
if run_script "$mac_custom" >"$mac_custom/output.log" 2>&1; then
  fail "custom macOS layout must fail"
fi
grep -q 'level=ERROR stage=detect' "$mac_custom/output.log" || fail "custom macOS layout error lacks structured detect stage"
[[ -e "$mac_custom/home/Library/LaunchAgents/dev.superdev.agent.plist" ]] || fail "custom macOS layout was mutated"

unsupported="$(new_fixture unsupported FreeBSD)"
if run_script "$unsupported" >"$unsupported/output.log" 2>&1; then
  fail "unsupported OS must fail"
fi
grep -q 'level=ERROR stage=detect' "$unsupported/output.log" || fail "unsupported OS error lacks structured detect stage"

echo "uninstall-agent.test: all checks passed"
