# Contributing to SuperDev

Thanks for taking the time to improve SuperDev. The project is early, but the runtime safety boundary is already important: SuperDev lets humans and AI agents share local services, logs, pipelines, ingress, and operation approvals. Contributions should preserve that trust model.

## Development Setup

Prerequisites:

- Go matching `agent/go.mod`
- Node.js 24 and pnpm matching `desktop/package.json`
- Rust and the Tauri 2 toolchain for full desktop packaging

Install and verify:

```bash
cd agent
go test ./...
```

```bash
cd desktop
pnpm install --frozen-lockfile
pnpm build
pnpm test
```

The regular pull request CI runs the Go agent tests, desktop web build, desktop tests, and release metadata version check. Full Tauri packaging is handled by the release process because signing and notarization require maintainer secrets.

## Pull Requests

Use small, focused pull requests. A good PR explains the user-visible change, the runtime or security boundary it touches, and the exact validation commands that passed.

Before opening a PR:

- Run the relevant tests locally.
- Keep generated binaries, local screenshots, profiles, secrets, tokens, and machine-specific files out of the repository.
- Update docs when behavior, configuration, or workflows change.
- Update `CHANGELOG.md` when the change matters to users or contributors.
- Keep `VERSION`, `desktop/package.json`, `desktop/src-tauri/Cargo.toml`, and `desktop/src-tauri/tauri.conf.json` in sync when preparing a release.

## Commit Style

Prefer Conventional Commit prefixes for readable history:

- `feat:` for user-visible features
- `fix:` for bug fixes
- `docs:` for documentation
- `test:` for test-only changes
- `chore:` for build, dependency, or repository maintenance
- `ci:` for GitHub Actions and automation

## Runtime Safety

Changes that start, stop, restart, deploy, tunnel, proxy, or remotely execute anything must keep this boundary intact:

1. The agent produces a preview of the intended operation.
2. The user approves that exact operation.
3. The operation uses a short-lived, one-time approval token.
4. The result is written to the audit trail.

Do not bypass this flow in UI code, MCP tools, tests, or helper scripts. If a change intentionally modifies the boundary, call it out in the PR summary and include focused tests.

One fail-closed exception applies to an explicit Host or Agent connection-target mutation: the agent must durably append a secret-free `tunnel.invalidate/prepared` audit event before committing the mutation, then immediately invalidate any tunnel created for the previous target or credentials and append the terminal `executed` event after persistence. If the prepared event cannot be stored, the mutation must not be committed; if terminal audit persistence fails, the configuration record must retain its pending invalidation revision so a retry can complete the same audit plan. This invalidation only reduces an existing access surface, so delaying it for a second approval would preserve an identity the stored configuration no longer trusts. The UI must disclose the disconnect before saving, and both audit stages must contain the host, trigger, changed field names, and previous tunnel status. This exception does not authorize active connect, user-requested disconnect, proxy, or remote execution operations.

A second exception covers the Desktop-initiated remote Agent uninstall and detach actions in Settings. The user explicitly chooses a managed Host, confirms the action in the Desktop UI with its data-purge impact disclosed, and the operation runs over the already-established, host-key-pinned control channel; no separate operation preview or one-time approval token is required. Uninstall, purge, and detach must never be exposed as MCP tools, must stay restricted to Agent-owned services, startup entries, binaries, data, and logs, and must emit structured application logs for start, success, contextual failure, purge selection, and detach reason. This exception does not authorize any other remote execution path.

## Security Reports

Please do not open public issues for vulnerabilities. Use the process in `SECURITY.md`.
