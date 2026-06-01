#!/usr/bin/env sh
# Run the opt-in local-02 pipeline E2E suite from the agent module.
#
# Responsibilities:
#   - Keep the invocation stable for humans and automation.
#   - Run only the e2e-tagged local-02 test.
#
# Boundaries:
#   - Does not clean up remote services.
#   - Does not infer credentials beyond the Go test's environment handling.
set -eu

cd "$(dirname "$0")/.."
go test -tags=e2e ./pipeline/e2e -run TestLocal02Examples -count=1 -v
