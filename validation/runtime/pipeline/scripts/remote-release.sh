#!/bin/sh
# remote-release.sh 管理一个 disposable runtime validation Linux release。
#
# 职责：
#   - 验证精确 campaign-owned root 和 owner marker
#   - 原子激活、校验版本化 release，并在 cleanup 时删除精确根目录
#   - 输出不含凭据和自由输入的结构化阶段日志
#
# 边界：
#   - 不配置或删除 borrowed SuperDev Agent/Host/Tunnel
#   - 不操作 /srv/superdev-runtime-validation/<campaign-id> 以外的路径
#   - 不选择 transport；pipeline run 必须独立证明经 Agent 路由

set -eu

log_event() {
  level="$1"
  stage="$2"
  outcome="$3"
  campaign="$4"
  version="$5"
  printf '{"timestamp":"%s","level":"%s","component":"runtime-validation-pipeline","stage":"%s","outcome":"%s","campaign_id":"%s","version":"%s"}\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$level" "$stage" "$outcome" "$campaign" "$version"
}

fail() {
  log_event "error" "$1" "failed" "$2" "$3" >&2
  exit 1
}

validate_campaign() {
  campaign="$1"
  printf '%s\n' "$campaign" | grep -Eq '^rv-(darwin|linux|windows)-(amd64|arm64)-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}$' || fail "validate_campaign" "invalid" "none"
}

validate_version() {
  version="$1"
  case "$version" in
    ""|*[!A-Za-z0-9._-]*) fail "validate_version" "$campaign" "invalid" ;;
  esac
}

validate_root() {
  campaign="$1"
  root="$2"
  expected="/srv/superdev-runtime-validation/$campaign"
  [ "$root" = "$expected" ] || fail "validate_root" "$campaign" "none"
  [ ! -L "/srv/superdev-runtime-validation" ] || fail "validate_root" "$campaign" "none"
  [ ! -L "$root" ] || fail "validate_root" "$campaign" "none"
  [ -d "$root" ] || fail "validate_root" "$campaign" "none"
  [ -f "$root/.campaign-owner" ] || fail "validate_owner" "$campaign" "none"
  [ ! -L "$root/.campaign-owner" ] || fail "validate_owner" "$campaign" "none"
  [ "$(wc -l <"$root/.campaign-owner")" -eq 1 ] || fail "validate_owner" "$campaign" "none"
  [ "$(cat "$root/.campaign-owner")" = "$campaign" ] || fail "validate_owner" "$campaign" "none"
}

activate_release() {
  campaign="$1"
  root="$2"
  version="$3"
  validate_campaign "$campaign"
  validate_version "$version"
  validate_root "$campaign" "$root"
  release="$root/releases/$version"
  [ -d "$release" ] || fail "activate_release" "$campaign" "$version"
  [ -f "$release/version.txt" ] || fail "activate_release" "$campaign" "$version"
  [ "$(sed -n '1p' "$release/version.txt")" = "$version" ] || fail "activate_release" "$campaign" "$version"
  payload_sha256="$(sha256sum "$release/payload.txt" | awk '{print $1}')"
  expected_payload_sha256="$(sed -n 's/^[[:space:]]*"sha256"[[:space:]]*:[[:space:]]*"\([0-9a-f][0-9a-f]*\)".*/\1/p' "$release/manifest.json")"
  [ "${#expected_payload_sha256}" -eq 64 ] || fail "verify_payload_digest" "$campaign" "$version"
  [ "$payload_sha256" = "$expected_payload_sha256" ] || fail "verify_payload_digest" "$campaign" "$version"
  printf '{"campaign_id":"%s","version":"%s","payload_sha256":"%s"}\n' "$campaign" "$version" "$payload_sha256" >"$release/receipt.json"
  chmod 0600 "$release/receipt.json"
  ln -sfn "releases/$version" "$root/current.next"
  mv -Tf "$root/current.next" "$root/current"
  log_event "info" "activate_release" "success" "$campaign" "$version"
}

verify_release() {
  campaign="$1"
  root="$2"
  version="$3"
  validate_campaign "$campaign"
  validate_version "$version"
  validate_root "$campaign" "$root"
  [ -L "$root/current" ] || fail "verify_release" "$campaign" "$version"
  [ "$(readlink "$root/current")" = "releases/$version" ] || fail "verify_release" "$campaign" "$version"
  grep -Fq "\"campaign_id\":\"$campaign\"" "$root/releases/$version/receipt.json" || fail "verify_release" "$campaign" "$version"
  log_event "info" "verify_release" "success" "$campaign" "$version"
}

cleanup_campaign() {
  campaign="$1"
  root="$2"
  validate_campaign "$campaign"
  validate_root "$campaign" "$root"
  log_event "info" "cleanup_campaign" "started" "$campaign" "all"
  cd /
  rm -rf -- "$root"
  [ ! -e "$root" ] || fail "cleanup_campaign" "$campaign" "all"
  log_event "info" "cleanup_campaign" "success" "$campaign" "all"
}

action="${1:-}"
campaign="${2:-}"
root="${3:-}"
version="${4:-none}"

case "$action" in
  activate) activate_release "$campaign" "$root" "$version" ;;
  verify) verify_release "$campaign" "$root" "$version" ;;
  cleanup) cleanup_campaign "$campaign" "$root" ;;
  *) fail "dispatch" "$campaign" "$version" ;;
esac
