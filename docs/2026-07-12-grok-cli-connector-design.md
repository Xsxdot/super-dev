# Grok CLI Agent Connector Design

## Status

Approved in conversation on 2026-07-12.

## Context

SuperDev ships verified built-in Agent Connectors through a single `ConnectorRegistry`. Seven connectors already exist:

| Level | Connectors |
| --- | --- |
| Full (MCP + Skill + Session Hook automatic) | Claude Code, Codex, Cursor, Hermes |
| Standard (MCP + Skill automatic; Hook manual) | OpenCode, OpenClaw, Kimi Code |

Grok CLI (xAI) is a local coding agent with first-class MCP, Skills, and Hooks. This design adds Grok as the eighth built-in Connector without changing the public `AgentConnector` protocol or hard-coding Agent lists in the frontend.

## Goals

- Add `GrokConnector` (`id = "grok"`, display name `Grok`) implementing `AgentConnector`.
- Register it in `builtin()` with derived **Full** support.
- Automatically install, update, verify, and uninstall:
  - SuperDev MCP via official `grok mcp` CLI
  - SuperDev Skill under `~/.grok/skills/superdev`
  - Owned SessionStart hook file under `~/.grok/hooks/`
- Keep onboarding and Settings driven only by Registry descriptors/state.
- Preserve safety properties: argv-only process execution, backups where files are written, atomic replacement for owned hook files, idempotency, exact removal, structured errors, secret-safe logs.
- Document honest SessionStart semantics (see [Honest Full semantics](#honest-full-semantics)).

## Non-goals

- Installing the Grok application or handling xAI authentication.
- Directly parsing or rewriting `~/.grok/config.toml` for MCP.
- Project-scoped MCP (`--scope project`); SuperDev always uses `--scope user`.
- Calling `grok mcp doctor` as a probe or connectivity test.
- Hosting or presenting Grok sessions (no ACP/PTY layer).
- Treating Claude/Cursor MCP compatibility scans as “Grok connected.”
- Adding a new `AgentKind` enum variant.
- Bundling Grok binaries.

## Architecture

```text
ConnectorRegistry
  └── GrokConnector (AgentConnector)
        ├── process::CommandRunner  → grok mcp add | list --json | remove
        ├── common skill helpers    → ~/.grok/skills/superdev
        └── owned hook file         → ~/.grok/hooks/superdev-session-start.json
```

- New module: `desktop/src-tauri/src/mcp_install/connectors/grok.rs`
- Reuse `process::CommandRunner` (OpenClaw pattern) and `common` skill primitives
- Register in `connectors::builtin()`; no frontend enum changes
- MCP ownership stays with the official CLI; SuperDev never edits Grok’s TOML for MCP

### Install / uninstall order

| Direction | Order |
| --- | --- |
| Install / Update | MCP → Skill → Hook |
| Uninstall | Hook → Skill → MCP |

Update uses the same path as install (`operation = Update`); `grok mcp add` is upsert.

## Honest Full semantics

Grok’s official docs define **SessionStart as a passive hook: stdout is ignored**. SuperDev’s existing `hooks/session-start` injects bootstrap via `additionalContext` JSON for Claude Code / Codex / Cursor. That injection **does not apply** on Grok.

| Capability | Automatic management | Functional parity with Claude |
| --- | --- | --- |
| MCP | Yes | Yes (stdio SuperDev tools) |
| Skill | Yes | Yes (discovered from `~/.grok/skills/superdev`) |
| Session Hook | Yes (owned file install/status/uninstall) | **No** additionalContext injection |

**Product rule:** Registry still reports Full because all three integrations are `automatic`. Primary SuperDev guidance on Grok is the **Skill**. Manual instructions and capability copy must state that SessionStart does not inject conversation context on Grok.

## Runtime paths

| Role | Path |
| --- | --- |
| Data root | `~/.grok/` (`ctx.home_dir().join(".grok")`) |
| MCP config (CLI-owned) | `~/.grok/config.toml` via `--scope user` |
| Skill | `~/.grok/skills/superdev/` |
| Hook | `~/.grok/hooks/superdev-session-start.json` |
| CLI binary | `grok` / `grok.exe` resolved from `command_dirs` |

### Environment overrides

No new `ConnectorEnvironment` fields in this change. Grok user data root is `~/.grok` under the runtime home. Tests isolate via `ConnectorRuntimeContext.home_dir` and mock `CommandRunner`. A future stable official home override (if any) may be added later under the existing “known paths only” rule.

## Detection

Detection succeeds when either:

1. `grok` resolves under `command_dirs` → `detection_path` is the CLI path (preferred), or
2. `~/.grok/` exists as a directory → `detection_path` is the data root.

If only the data root is present (no CLI):

- `detected = true`
- MCP install/status/uninstall return `cli_not_found` / `NeedsAction` with manual instructions (OpenClaw alignment)

## MCP CLI contract

All invocations are argv arrays through `CommandRunner` (no shell), timeout **30s**. Logs must not include argv, stdout, stderr, or absolute paths.

### Install / update

```text
grok mcp add superdev \
  --scope user \
  -e SUPERDEV_AGENT_URL=http://127.0.0.1:57017 \
  -- <absolute-path-to-superdev-mcp>
```

- Transport defaults to stdio (omit `--transport`)
- `DEFAULT_AGENT_URL` matches existing connectors

### Status / verify

```text
grok mcp list --json
```

Do **not** use `grok mcp doctor` for verify. Registry verify remains a read-only normalization of `status`.

Verified element shape (local probe):

```json
{
  "name": "superdev",
  "command": "/abs/path/to/superdev-mcp",
  "args": [],
  "env": { "SUPERDEV_AGENT_URL": "http://127.0.0.1:57017" },
  "enabled": true,
  "scope": "user"
}
```

### Match rules (`Configured`)

An entry is configured when all hold:

- `name == "superdev"`
- `command` equals `ctx.mcp_binary()`
- `env.SUPERDEV_AGENT_URL == DEFAULT_AGENT_URL`
- `enabled` is true (explicit `false` → `NeedsAction`)
- `scope == "user"` (project-only same name → `NeedsAction`; do not auto-remove project entries)

### Uninstall

```text
grok mcp remove superdev --scope user
```

Missing entry is a successful already-absent outcome. Other non-zero exits map to `cli_remove_failed`.

### MCP status matrix

| Condition | MCP status |
| --- | --- |
| CLI missing | Error / NeedsAction (`cli_not_found`) |
| list failed or non-JSON | Error (`cli_list_failed` / `invalid_cli_output`) |
| No superdev entry | Missing |
| Entry present but mismatch | NeedsAction |
| Full match | Configured |

## Skill

- Target: `~/.grok/skills/superdev/`
- Install after successful MCP using existing `install_skill_dir` from `ctx.skill_source()`
- Status via `skill_status_for_target`
- Uninstall via `remove_skill_dir` (only the `superdev` directory)
- Skill failure does **not** roll back MCP; connector result becomes `partial`

## Session Hook

### Owned file

Path: `~/.grok/hooks/superdev-session-start.json`

Installed content:

```json
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "<skill_dir>/hooks/run-hook.cmd session-start",
            "timeout": 15
          }
        ]
      }
    ]
  }
}
```

- `command` uses an absolute path with forward slashes
- Ownership marker: file content contains stable substring `skills/superdev/hooks/run-hook.cmd` (and `session-start`)
- On install/update, SuperDev may overwrite the owned file when marker matches or file is absent
- If the filename exists without the marker → `hook_owned_conflict` / `NeedsAction`; do not delete
- Uninstall deletes the whole file only when marker matches

### Semantics

- Lifecycle alignment and TUI scrollback visibility only
- No claim of `additionalContext` injection on Grok
- Hook failures remain fail-open on Grok’s side

## Result aggregation

Reuse existing connector outcome rules:

| MCP | Skill / Hook | Connector result | Onboarding “connected” |
| --- | --- | --- | --- |
| failed / needs_action | any | failed / needs_action | No |
| installed / already_present | all ok / already / skipped / unsupported | success / unchanged | Yes |
| installed / already_present | any failed / needs_action | **partial** | Yes |

## Error codes

| Code | When |
| --- | --- |
| `cli_not_found` | `grok` not resolved |
| `cli_add_failed` | `mcp add` non-zero or timeout |
| `cli_list_failed` | `mcp list` failed |
| `invalid_cli_output` | list JSON unparsable |
| `cli_remove_failed` | `mcp remove` failed |
| `skill_source_missing` | packaged skill source missing |
| `skill_install_failed` | skill copy failed |
| `hook_write_failed` | owned hook atomic write failed |
| `hook_owned_conflict` | same path exists without SuperDev marker |

User-visible messages must not echo CLI stdout/stderr. Structured logs include only `connector_id`, `operation`, `duration_ms`, and stable error codes.

## Manual instructions

When CLI is missing or MCP needs action, instructions include:

1. Install Grok CLI and ensure `grok` is on PATH
2. Exact user-scope `grok mcp add` example (runtime may fill the mcp binary path in the instruction text only)
3. Skill directory target
4. Hook file example plus note that SessionStart does not inject additionalContext; rely on Skill
5. Restart Grok session or refresh `/mcps`

## Compatibility with other agents

- Claude/Codex/Cursor configs are not modified by this connector
- Grok may also load Claude/Cursor MCP via its own compat layer; that does **not** count as Grok SuperDev MCP configured
- Status only trusts `grok mcp list --json` user-scope `superdev`
- Simultaneous multi-agent installs remain independent

## Product copy

- Display name: **Grok** (subtitle “Grok CLI” only when disambiguation is needed)
- README / README.zh-CN: add Grok to built-in connectors; include Grok in Full list
- CHANGELOG on release: built-in Grok CLI connector (Full)
- No new frontend closed union of agent IDs

## Platforms

macOS, Windows, Linux — same as other Wave 2 connectors.

## Testing

### Unit (default CI)

- Mock `CommandRunner`: add/list/remove success; missing entry; command/URL mismatch; `enabled: false`; project-only scope; bad JSON; timeout
- Skill/hook file round-trip under isolated home
- MCP success + skill failure → `partial`
- `builtin()` length 8, deterministic order, `grok` support level Full
- No writes to the developer’s real `~/.grok` in default tests

### Optional real smoke

- `#[ignore]` test with isolated root / mocked home; never default-on in CI

## Acceptance criteria

1. `builtin()` registers eight connectors including `grok` with Full support.
2. Isolated home + mock CLI: detect → install → three integrations Configured → uninstall cleans SuperDev-owned state.
3. Match rules cover missing, command mismatch, URL mismatch, disabled, project-only scope.
4. MCP success + skill failure yields `partial`; onboarding still counts MCP as connected.
5. No CLI → no corrupt writes; `cli_not_found` + manual instructions.
6. Logs omit argv, paths, and config bodies.
7. README EN/ZH list Grok; frontend has no new hardcoded agent enum.
8. Default tests never touch the real user `~/.grok`.

## Implementation touchpoints

| Item | Location |
| --- | --- |
| Connector module | `desktop/src-tauri/src/mcp_install/connectors/grok.rs` |
| Registration | `connectors.rs` (`mod grok`, `builtin()`) |
| Shared process runner | `connectors/process.rs` |
| Shared skill helpers | `connectors/common.rs` |
| Docs | `README.md`, `README.zh-CN.md` |

## Risks and follow-ups (out of scope)

| Risk | Mitigation |
| --- | --- |
| `list --json` schema drift | Fail closed with `invalid_cli_output`; optional later `verified_versions` |
| No bootstrap injection on SessionStart | Documented; Skill-first guidance; revisit if Grok adds injection |
| Real CLI home vs test home | Production uses real home; unit tests mock runner |

## Decisions log

| Decision | Choice |
| --- | --- |
| Product scope | Built-in Connector (not manual-only, not session host) |
| Support level | Full |
| MCP strategy | Official CLI first (`grok mcp add/list/remove`), no TOML merge |
| Hook strategy | Owned JSON file under `~/.grok/hooks/` |
| Full honesty | Full = manageable integrations; context injection not claimed |
| Scope flag | Always `--scope user` |
| Environment overrides | None this wave |
| doctor | Not used for verify |
