# SuperDev Windows real-validation portable source

This directory is the immutable source of a portable Windows 10 x64 validation package.

- Build on macOS with `go run ./cmd/build-windows-validation` from `agent/`.
- Copy the generated ZIP and checksum to Windows together with the two frozen installers.
- Follow `Runbook.md`; run `Prepare-Validation.ps1` before each installer lane, then use `Run-Validation.ps1` and `Cleanup-Validation.ps1` with that exact prepared backup.
- Do not add runtime input, installers, backups, or results inside the extracted package.
- A macOS build may report only `package_verified`. It cannot produce Windows MCP or provider verdicts.
