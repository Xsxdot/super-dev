#!/usr/bin/env sh
# uninstall-agent.sh manually removes a supported Linux or macOS SuperDev Agent installation.
#
# Responsibilities:
#   - Detect the supported systemd, LaunchDaemon, or user LaunchAgent layout.
#   - Stop and remove only SuperDev Agent-owned startup entries and binaries.
#   - Preserve Agent data and logs unless --purge is explicitly supplied.
#
# Boundaries:
#   - Does not remove Controller configuration, Hosts, Docker resources, or unrelated services.
#   - Does not accept custom install paths; ambiguous or custom layouts fail without mutation.
set -eu

PURGE=false
FIXTURE_ROOT="${SUPERDEV_UNINSTALL_FIXTURE_ROOT:-}"

log_event() {
  level="$1"
  stage="$2"
  message="$3"
  # Keep one event per line and avoid unescaped quotes breaking the level/stage envelope.
  safe_message="$(printf '%s' "$message" | tr '\r\n' '  ' | sed "s/\"/'/g")"
  printf 'level=%s stage=%s message="%s"\n' "$level" "$stage" "$safe_message"
}

fail() {
  stage="$1"
  message="$2"
  log_event ERROR "$stage" "$message" >&2
  exit 1
}

usage() {
  cat <<'USAGE'
Usage: uninstall-agent.sh [--purge]

Options:
  --purge  Also permanently delete SuperDev Agent-owned data and logs.
  -h, --help  Show this help.

Agent data and logs are preserved by default.
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --purge) PURGE=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) fail arguments "unsupported argument; custom install paths are not accepted" ;;
  esac
done

if [ -n "$FIXTURE_ROOT" ] && [ "${SUPERDEV_UNINSTALL_TESTING:-}" != "1" ]; then
  fail detect "fixture root is test-only"
fi

system_path() {
  printf '%s%s' "$FIXTURE_ROOT" "$1"
}

run_action() {
  stage="$1"
  action="$2"
  shift 2
  log_event INFO "$stage" "starting $action"
  if ! "$@"; then
    fail "$stage" "$action failed"
  fi
  log_event INFO "$stage" "completed $action"
}

run_privileged() {
  # Fixture runs must never cross into sudo; production uses the caller directly only when already root.
  if [ -n "$FIXTURE_ROOT" ] || [ "$(id -u)" = "0" ]; then
    "$@"
    return
  fi
  if ! command -v sudo >/dev/null 2>&1; then
    fail permissions "administrator privileges are required; rerun with sudo"
  fi
  sudo "$@"
}

agent_process_running() {
  command -v pgrep >/dev/null 2>&1 && pgrep -x superdev-agent >/dev/null 2>&1
}

validate_linux_unit() {
  file="$1"
  [ ! -e "$file" ] && return 0
  # Only the actual ExecStart directive proves ownership; comments mentioning the canonical path do not.
  if ! grep -E '^ExecStart=/usr/local/bin/superdev-agent([[:space:]]|$)' "$file" >/dev/null 2>&1; then
    fail detect "unsupported custom Agent systemd unit; no resources were changed"
  fi
}

validate_launchd_plist() {
  file="$1"
  expected_binary="$2"
  [ ! -e "$file" ] && return 0
  if ! command -v plutil >/dev/null 2>&1; then
    fail detect "plutil is required to validate the supported macOS Agent layout"
  fi
  # Use the OS plist parser so comments or unrelated XML nodes cannot impersonate ProgramArguments[0].
  if ! actual_binary="$(plutil -extract ProgramArguments.0 raw -o - "$file" 2>/dev/null)"; then
    fail detect "unsupported or malformed Agent launchd entry; no resources were changed"
  fi
  if [ "$actual_binary" != "$expected_binary" ]; then
    fail detect "unsupported custom Agent launchd entry; no resources were changed"
  fi
}

uninstall_linux() {
  stage=linux_systemd
  unit="$(system_path /etc/systemd/system/superdev-agent.service)"
  binary="$(system_path /usr/local/bin/superdev-agent)"
  data="$(system_path /var/lib/superdev-agent)"

  validate_linux_unit "$unit"
  service_known=false
  [ -e "$unit" ] && service_known=true
  [ -e "$binary" ] && service_known=true

  if ! command -v systemctl >/dev/null 2>&1; then
    if [ "$service_known" = false ] && ! agent_process_running; then
      [ "$PURGE" = true ] && run_action purge "remove retained Agent data" run_privileged rm -rf "$data"
      return 0
    fi
    fail detect "supported Linux Agent layout requires systemd"
  fi

  # Probe failure is not equivalent to either present or absent; mutating after an unknown state could hit a custom unit.
  if ! state="$(systemctl show superdev-agent.service --property=LoadState --property=ActiveState --property=FragmentPath --property=ExecStart --no-pager 2>/dev/null)"; then
    if [ "$service_known" = false ] && ! agent_process_running; then
      [ "$PURGE" = true ] && run_action purge "remove retained Agent data" run_privileged rm -rf "$data"
      return 0
    fi
    fail detect "could not inspect the Agent systemd unit; no resources were changed"
  fi
  load_state="$(printf '%s\n' "$state" | sed -n 's/^LoadState=//p' | head -n 1)"
  active_state="$(printf '%s\n' "$state" | sed -n 's/^ActiveState=//p' | head -n 1)"
  fragment_path="$(printf '%s\n' "$state" | sed -n 's/^FragmentPath=//p' | head -n 1)"
  exec_start="$(printf '%s\n' "$state" | sed -n 's/^ExecStart=//p' | head -n 1)"
  if [ -z "$load_state" ] || [ -z "$active_state" ]; then
    fail detect "systemd returned an incomplete Agent unit state; no resources were changed"
  fi
  if [ -n "$fragment_path" ] && [ "$fragment_path" != /etc/systemd/system/superdev-agent.service ]; then
    fail detect "unsupported custom Agent systemd unit path; no resources were changed"
  fi
  if [ -n "$exec_start" ]; then
    case "$exec_start" in
      *"path=/usr/local/bin/superdev-agent ;"*) ;;
      *) fail detect "unsupported custom Agent systemd command; no resources were changed" ;;
    esac
  fi
  if [ "$load_state" != "not-found" ] || [ "$active_state" != "inactive" ]; then
    service_known=true
    run_action "$stage" "stop Agent systemd unit" run_privileged systemctl stop superdev-agent.service
  fi

  if [ "$service_known" = false ]; then
    # A clean repeat may leave only retained data; purge it without inventing a service layout.
    if agent_process_running; then
      fail detect "running superdev-agent uses an unsupported custom Linux layout"
    fi
    [ "$PURGE" = true ] && run_action purge "remove retained Agent data" run_privileged rm -rf "$data"
    return 0
  fi

  if [ -e "$unit" ]; then
    run_action "$stage" "disable Agent systemd unit" run_privileged systemctl disable superdev-agent.service
  fi
  run_action "$stage" "remove Agent unit and binary" run_privileged rm -f "$unit" "$binary"
  run_action "$stage" "reload systemd manager" run_privileged systemctl daemon-reload
  if [ "$PURGE" = true ]; then
    run_action purge "remove Agent data" run_privileged rm -rf "$data"
  fi
}

launchd_loaded() {
  target="$1"
  # launchctl's not-found response is an idempotent no-op; permission/manager failures must block mutation.
  if output="$(launchctl print "$target" 2>&1)"; then
    return 0
  fi
  normalized="$(printf '%s' "$output" | tr '[:upper:]' '[:lower:]')"
  case "$normalized" in
    *"could not find service"*|*"service not found"*) return 1 ;;
    *"could not find domain for user gui"*)
      # 仅 gui/<uid> 查询可用“GUI 域不存在”证明 user job 不存在；system 查询出现同文案属于异常，必须阻止后续变更。
      case "$target" in
        gui/*) return 1 ;;
        *) fail detect "could not inspect Agent launchd job: $output" ;;
      esac
      ;;
    *) fail detect "could not inspect Agent launchd job: $output" ;;
  esac
}

uninstall_macos_system() {
  plist="$(system_path /Library/LaunchDaemons/dev.superdev.agent.plist)"
  binary="$(system_path /usr/local/bin/superdev-agent)"
  data="$(system_path '/Library/Application Support/SuperDev/Agent')"
  stdout_log="$(system_path /var/log/superdev-agent.log)"
  stderr_log="$(system_path /var/log/superdev-agent.err.log)"
  if launchd_loaded system/dev.superdev.agent system; then
    run_action macos_launchdaemon "stop Agent LaunchDaemon" run_privileged launchctl bootout system "$plist"
  fi
  run_action macos_launchdaemon "remove Agent LaunchDaemon and binary" run_privileged rm -f "$plist" "$binary"
  if [ "$PURGE" = true ]; then
    run_action purge "remove Agent system data" run_privileged rm -rf "$data"
    run_action purge "remove Agent system logs" run_privileged rm -f "$stdout_log" "$stderr_log"
  fi
}

uninstall_macos_user() {
  uid="$(id -u)"
  root="$HOME/Library/Application Support/SuperDev/Agent"
  plist="$HOME/Library/LaunchAgents/dev.superdev.agent.plist"
  binary="$root/bin/superdev-agent"
  stdout_log="$HOME/Library/Logs/superdev-agent.log"
  stderr_log="$HOME/Library/Logs/superdev-agent.err.log"
  if launchd_loaded "gui/$uid/dev.superdev.agent" user; then
    run_action macos_launchagent "stop Agent user LaunchAgent" launchctl bootout "gui/$uid" "$plist"
  fi
  run_action macos_launchagent "remove Agent user LaunchAgent and binary" rm -f "$plist" "$binary"
  if [ "$PURGE" = true ]; then
    run_action purge "remove Agent user data" rm -rf "$root"
    run_action purge "remove Agent user logs" rm -f "$stdout_log" "$stderr_log"
  fi
}

uninstall_macos() {
  system_plist="$(system_path /Library/LaunchDaemons/dev.superdev.agent.plist)"
  system_binary="$(system_path /usr/local/bin/superdev-agent)"
  system_data="$(system_path '/Library/Application Support/SuperDev/Agent')"
  user_plist="$HOME/Library/LaunchAgents/dev.superdev.agent.plist"
  user_root="$HOME/Library/Application Support/SuperDev/Agent"
  user_binary="$user_root/bin/superdev-agent"
  uid="$(id -u)"

  validate_launchd_plist "$system_plist" /usr/local/bin/superdev-agent
  validate_launchd_plist "$user_plist" "$user_binary"

  system_known=false
  user_known=false
  [ -e "$system_plist" ] || [ -e "$system_binary" ] || [ -e "$system_data" ] && system_known=true
  [ -e "$user_plist" ] || [ -e "$user_binary" ] || [ -e "$user_root" ] && user_known=true
  launchd_loaded system/dev.superdev.agent system && system_known=true
  launchd_loaded "gui/$uid/dev.superdev.agent" user && user_known=true

  # Two layouts at once are ambiguous; selecting one would leave or mutate resources without a reliable owner.
  if [ "$system_known" = true ] && [ "$user_known" = true ]; then
    fail detect "ambiguous macOS Agent layouts found; no resources were changed"
  fi
  if [ "$system_known" = true ]; then
    uninstall_macos_system
    return
  fi
  if [ "$user_known" = true ]; then
    uninstall_macos_user
    return
  fi
  if agent_process_running; then
    fail detect "running superdev-agent uses an unsupported custom macOS layout"
  fi
}

log_event INFO detect "starting Agent uninstall platform detection"
platform="$(uname -s 2>/dev/null || true)"
case "$platform" in
  Linux)
    log_event INFO detect "detected supported Linux systemd layout"
    uninstall_linux
    ;;
  Darwin)
    log_event INFO detect "detected supported macOS launchd platform"
    uninstall_macos
    ;;
  *) fail detect "unsupported operating system; only Linux and macOS are supported" ;;
esac
log_event INFO complete "Agent uninstall completed; purge=$PURGE"
