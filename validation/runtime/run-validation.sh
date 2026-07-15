#!/usr/bin/env sh
# 职责：只定位已解压 bundle 中的 target-native runner，并透传调用参数/stdin。
# 边界：不推断 target、不解析 summary、不修改 foundation。
set -eu

bundle_root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec "$bundle_root/bin/runtime-validation" --bundle-root "$bundle_root" --credential-stdin "$@"
