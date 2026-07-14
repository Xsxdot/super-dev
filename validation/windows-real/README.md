# SuperDev Windows real-validation portable source

This directory is the immutable source of a portable Windows 10 x64 validation package.

- Build on macOS with `go run ./cmd/build-windows-validation` from `agent/`.
- Copy the generated ZIP and checksum to Windows together with the two frozen installers.
- Follow `Runbook.md`; run `Prepare-Validation.ps1` before each installer lane, then use `Run-Validation.ps1` and `Cleanup-Validation.ps1` with that exact prepared backup.
- Campaign report schema v2 records `attempted` execution facts and required evidence, then derives only `NOT_RUN`, `BLOCKED`, `PASS`, or `FAIL`; callers must never synthesize verdict strings.
- Installer artifact identity is reported separately from install/start/stop/uninstall lifecycle execution.
- `core_only` is a diagnostic lane that executes and reports the same core/provider/tool/pipeline surfaces without claiming either installer lane; it cannot satisfy final installer acceptance.
- Every persisted report is re-derived from execution facts, prerequisite facts, and evidence obligations before it is written or finalized; stored status strings are never trusted as input.
- The report persists the frozen scenario/step/cleanup and 75-tool coverage catalog; missing, duplicate, or remapped rows are rejected before any aggregate result is derived.
- Cleanup PASS is bound to the exact prepared `baseline.json`: the finalizer recomputes the whole-file and six category hashes, then requires the manifest, cleanup report, campaign ID, and lane to agree.
- Do not add runtime input, installers, backups, or results inside the extracted package.
- A macOS build may report only `package_verified`. It cannot produce Windows MCP or provider verdicts.
