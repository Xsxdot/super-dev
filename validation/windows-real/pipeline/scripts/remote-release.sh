#!/bin/sh
# remote-release.sh manages one disposable SuperDev Windows validation release on Linux.
#
# Responsibilities:
#   - validate the exact campaign-owned absolute root and marker;
#   - atomically activate and verify a versioned release;
#   - remove only the exact campaign root during cleanup;
#   - emit JSON-line stage logs without credentials or free-form remote data.
#
# Boundaries:
#   - this script never provisions or removes the SuperDev Agent;
#   - it never operates outside /srv/superdev-validation/<campaign-id>;
#   - it does not choose transport; the pipeline run log must independently prove -> agent.

set -eu

log_event() {
  level="$1"
  stage="$2"
  outcome="$3"
  campaign="$4"
  version="$5"
  printf '{"timestamp":"%s","level":"%s","component":"windows-remote-pipeline","stage":"%s","outcome":"%s","campaign_id":"%s","version":"%s"}\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$level" "$stage" "$outcome" "$campaign" "$version"
}

fail() {
  stage="$1"
  campaign="$2"
  version="$3"
  log_event "error" "$stage" "failed" "$campaign" "$version" >&2
  exit 1
}

validate_campaign() {
  campaign="$1"
  printf '%s\n' "$campaign" | grep -Eq '^w10x64-e3cc94f-[0-9]{8}T[0-9]{6}Z-[A-Za-z0-9]{6}$' || fail "validate_campaign" "invalid" "none"
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
  expected="/srv/superdev-validation/$campaign"
  [ "$root" = "$expected" ] || fail "validate_root" "$campaign" "none"
  [ ! -L "/srv/superdev-validation" ] || fail "validate_root" "$campaign" "none"
  [ ! -L "$root" ] || fail "validate_root" "$campaign" "none"
  [ -d "$root" ] || fail "validate_root" "$campaign" "none"
  [ -f "$root/.campaign-owner" ] || fail "validate_owner" "$campaign" "none"
  [ ! -L "$root/.campaign-owner" ] || fail "validate_owner" "$campaign" "none"
  [ "$(wc -l <"$root/.campaign-owner")" -eq 1 ] || fail "validate_owner" "$campaign" "none"
  owner="$(cat "$root/.campaign-owner")"
  [ "$owner" = "$campaign" ] || fail "validate_owner" "$campaign" "none"
  [ -d "$root/releases" ] || fail "validate_layout" "$campaign" "none"
  [ ! -L "$root/releases" ] || fail "validate_layout" "$campaign" "none"
  [ -d "$root/temp" ] || fail "validate_layout" "$campaign" "none"
  [ ! -L "$root/temp" ] || fail "validate_layout" "$campaign" "none"
  [ -d "$root/logs" ] || fail "validate_layout" "$campaign" "none"
  [ ! -L "$root/logs" ] || fail "validate_layout" "$campaign" "none"
}

activate_release() {
  campaign="$1"
  root="$2"
  version="$3"
  validate_campaign "$campaign"
  validate_version "$version"
  validate_root "$campaign" "$root"
  release="$root/releases/$version"
  [ ! -L "$release" ] || fail "activate_release" "$campaign" "$version"
  [ -d "$release" ] || fail "activate_release" "$campaign" "$version"
  [ -f "$release/version.txt" ] || fail "activate_release" "$campaign" "$version"
  [ ! -L "$release/version.txt" ] || fail "activate_release" "$campaign" "$version"
  artifact_version="$(sed -n '1p' "$release/version.txt")"
  [ "$artifact_version" = "$version" ] || fail "activate_release" "$campaign" "$version"
  [ -f "$release/manifest.json" ] || fail "activate_release" "$campaign" "$version"
  [ ! -L "$release/manifest.json" ] || fail "activate_release" "$campaign" "$version"
  [ -f "$release/payload.txt" ] || fail "activate_release" "$campaign" "$version"
  [ ! -L "$release/payload.txt" ] || fail "activate_release" "$campaign" "$version"
  payload_sha256="$(sha256sum "$release/payload.txt" | awk '{print $1}')"
  expected_payload_sha256="$(sed -n 's/^[[:space:]]*"sha256"[[:space:]]*:[[:space:]]*"\([0-9a-f][0-9a-f]*\)".*/\1/p' "$release/manifest.json")"
  [ "${#expected_payload_sha256}" -eq 64 ] || fail "verify_payload_digest" "$campaign" "$version"
  [ "$payload_sha256" = "$expected_payload_sha256" ] || fail "verify_payload_digest" "$campaign" "$version"
  printf '{"campaign_id":"%s","version":"%s","payload_sha256":"%s"}\n' \
    "$campaign" "$version" "$payload_sha256" >"$release/receipt.json"
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
  current="$(readlink "$root/current")"
  [ "$current" = "releases/$version" ] || fail "verify_release" "$campaign" "$version"
  receipt="$root/releases/$version/receipt.json"
  [ -f "$receipt" ] || fail "verify_release" "$campaign" "$version"
  [ ! -L "$receipt" ] || fail "verify_release" "$campaign" "$version"
  grep -Fq "\"campaign_id\":\"$campaign\"" "$receipt" || fail "verify_release" "$campaign" "$version"
  grep -Fq "\"version\":\"$version\"" "$receipt" || fail "verify_release" "$campaign" "$version"
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
