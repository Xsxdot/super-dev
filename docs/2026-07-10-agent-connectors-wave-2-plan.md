# Agent Connector Wave 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add verified built-in Agent Connectors for OpenCode, OpenClaw, Hermes Agent, and Kimi Code while preserving the existing open-ID Registry contract and safe mutation guarantees.

**Architecture:** Each Agent is an independent `AgentConnector` registered in the existing `ConnectorRegistry`. Private helpers provide known-environment resolution, lossless JSONC/YAML mutation, safe Skill operations, and bounded official CLI execution; update and verify remain Registry operations over the existing six-method trait.

**Tech Stack:** Rust 2021, Tauri 2, `serde_json`, `jsonc-parser` 0.26.3 with `cst`/`serde`, `yaml-edit` 0.2.3, `tracing`, Vue 3, Pinia, Vitest, GitHub Actions.

---

## File Map

| File | Responsibility |
| --- | --- |
| `desktop/src-tauri/Cargo.toml` / `Cargo.lock` | Pin Rust-1.77-compatible lossless JSONC and YAML editors. |
| `desktop/src-tauri/src/mcp_install/registry.rs` | Carry only known Connector environment overrides. |
| `desktop/src-tauri/src/mcp_install.rs` | Resolve environment once and expose existing safe filesystem primitives. |
| `desktop/src-tauri/src/mcp_install/connectors.rs` | Keep legacy connectors, declare new modules, assemble built-ins. |
| `desktop/src-tauri/src/mcp_install/connectors/common.rs` | Private descriptors, results, safe config mutation, Skill helpers. |
| `desktop/src-tauri/src/mcp_install/connectors/process.rs` | Private bounded argv-based process runner for OpenClaw. |
| `desktop/src-tauri/src/mcp_install/connectors/kimi_code.rs` | `KimiCodeConnector`. |
| `desktop/src-tauri/src/mcp_install/connectors/opencode.rs` | `OpenCodeConnector` and lossless JSONC mutation. |
| `desktop/src-tauri/src/mcp_install/connectors/openclaw.rs` | `OpenClawConnector` using the official MCP CLI. |
| `desktop/src-tauri/src/mcp_install/connectors/hermes.rs` | `HermesConnector`, lossless YAML, owned hook. |
| `desktop/src/dev/onboardingPreview.ts` and focused frontend tests | Seven-Connector dynamic UI fixtures. |
| `README.md` | Seven verified built-ins and explicit Pi deferral. |

## Task 1: Add Known Environment Inputs and Lossless Parser Dependencies

**Files:**

- Modify: `desktop/src-tauri/Cargo.toml`
- Modify: `desktop/src-tauri/Cargo.lock`
- Modify: `desktop/src-tauri/src/mcp_install/registry.rs`
- Modify: `desktop/src-tauri/src/mcp_install.rs`

- [ ] **Step 1: Write failing runtime-context tests**

Add to the `registry.rs` test module:

```rust
#[test]
fn connector_environment_exposes_only_known_path_overrides() {
    let env = ConnectorEnvironment::new(
        Some(PathBuf::from("/tmp/opencode.json")),
        Some(PathBuf::from("/tmp/openclaw.json")),
        Some(PathBuf::from("/tmp/kimi-code")),
    );
    let ctx = context().with_environment(env);
    assert_eq!(ctx.environment().opencode_config(), Some(Path::new("/tmp/opencode.json")));
    assert_eq!(ctx.environment().openclaw_config_path(), Some(Path::new("/tmp/openclaw.json")));
    assert_eq!(ctx.environment().kimi_code_home(), Some(Path::new("/tmp/kimi-code")));
}

#[test]
fn connector_runtime_context_defaults_to_no_overrides() {
    assert_eq!(context().environment(), &ConnectorEnvironment::default());
}
```

- [ ] **Step 2: Run the tests and verify RED**

Run:

```bash
cd desktop/src-tauri
cargo test mcp_install::registry::tests::connector_environment -- --nocapture
```

Expected: compilation fails because `ConnectorEnvironment`, `with_environment`, and `environment` do not exist.

- [ ] **Step 3: Add compatible dependencies**

Add to `[dependencies]`:

```toml
jsonc-parser = { version = "0.26.3", features = ["cst", "serde"] }
yaml-edit = { version = "0.2.1", default-features = false }
```

`jsonc-parser` 0.26.3 is Rust 2021; newer 0.32.x releases use edition 2024 and do not satisfy `rust-version = "1.77.2"`.

- [ ] **Step 4: Implement the known environment value object**

Add above `ConnectorRuntimeContext`:

```rust
/// ConnectorEnvironment 保存已批准的 Agent 配置路径覆盖。
///
/// 边界：不保存任意环境变量或秘密值，Connector 只能读取三个公开路径。
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ConnectorEnvironment {
    opencode_config: Option<PathBuf>,
    openclaw_config_path: Option<PathBuf>,
    kimi_code_home: Option<PathBuf>,
}

impl ConnectorEnvironment {
    pub fn new(
        opencode_config: Option<PathBuf>,
        openclaw_config_path: Option<PathBuf>,
        kimi_code_home: Option<PathBuf>,
    ) -> Self {
        Self { opencode_config, openclaw_config_path, kimi_code_home }
    }

    pub fn opencode_config(&self) -> Option<&Path> { self.opencode_config.as_deref() }
    pub fn openclaw_config_path(&self) -> Option<&Path> { self.openclaw_config_path.as_deref() }
    pub fn kimi_code_home(&self) -> Option<&Path> { self.kimi_code_home.as_deref() }
}
```

Add `environment: ConnectorEnvironment` to `ConnectorRuntimeContext`, initialize it with `default()` in `new`, then add documented `with_environment` and `environment` methods.

- [ ] **Step 5: Resolve environment once at the Tauri boundary and add logs**

In `connector_runtime_context`:

```rust
let environment = registry::ConnectorEnvironment::new(
    std::env::var_os("OPENCODE_CONFIG").map(PathBuf::from),
    std::env::var_os("OPENCLAW_CONFIG_PATH").map(PathBuf::from),
    std::env::var_os("KIMI_CODE_HOME").map(PathBuf::from),
);
tracing::debug!(
    has_opencode_config = environment.opencode_config().is_some(),
    has_openclaw_config_path = environment.openclaw_config_path().is_some(),
    has_kimi_code_home = environment.kimi_code_home().is_some(),
    "connector runtime environment resolved"
);
Ok(registry::ConnectorRuntimeContext::new(
    home, command_dirs, app_dirs, mcp_binary, skill_source, skill_source_error,
).with_environment(environment))
```

The log records presence booleans only, never path values.

- [ ] **Step 6: Add intent comments and verify GREEN**

Document all new public methods and update the runtime-context boundary comment. Run:

```bash
cd desktop/src-tauri
cargo test mcp_install::registry::tests::connector_environment -- --nocapture
cargo check
```

Expected: both tests pass and the dependency lockfile resolves.

- [ ] **Step 7: Commit Task 1**

```bash
git add desktop/src-tauri/Cargo.toml desktop/src-tauri/Cargo.lock desktop/src-tauri/src/mcp_install.rs desktop/src-tauri/src/mcp_install/registry.rs
git commit -m "Add connector environment context"
```

## Task 2: Build Private Safe Mutation and Bounded Process Primitives

**Files:**

- Create: `desktop/src-tauri/src/mcp_install/connectors/common.rs`
- Create: `desktop/src-tauri/src/mcp_install/connectors/process.rs`
- Modify: `desktop/src-tauri/src/mcp_install/connectors.rs`

- [ ] **Step 1: Declare modules and write failing tests**

Add to `connectors.rs`:

```rust
mod common;
mod process;
```

Add to `common.rs` tests:

```rust
#[test]
fn descriptor_level_is_derived_from_hook_support() {
    assert_eq!(
        descriptor("standard", "Standard", SupportMode::Manual, None).support_level(),
        Some(SupportLevel::Standard)
    );
    assert_eq!(
        descriptor("full", "Full", SupportMode::Automatic, None).support_level(),
        Some(SupportLevel::Full)
    );
}

#[test]
fn mutate_config_rejects_a_directory_target_without_writing() {
    let root = test_dir("directory-target");
    std::fs::create_dir_all(&root).unwrap();
    let error = mutate_config("fixture", &root, |_| {
        Ok(MergeResult { content: "{}\n".into(), changed: true })
    }).expect_err("directory must fail");
    assert_eq!(error.code(), "unsafe_config_target");
}
```

Add to `process.rs` tests:

```rust
#[test]
fn system_runner_captures_success_without_shell_expansion() {
    let output = SystemCommandRunner
        .run(CommandSpec::new(rustc_program(), ["--version"]))
        .expect("rustc runs");
    assert!(output.success());
    assert!(output.stdout.contains("rustc"));
}

#[test]
fn system_runner_times_out_and_kills_the_child() {
    let spec = sleep_spec(Duration::from_secs(5)).with_timeout(Duration::from_millis(50));
    let error = SystemCommandRunner.run(spec).expect_err("must time out");
    assert_eq!(error.code(), "command_timeout");
}
```

Use `/bin/sh -c "sleep 5"` for the test under `cfg(unix)` and `powershell -NoProfile -Command "Start-Sleep -Seconds 5"` under `cfg(windows)`.

Add a third fixture process that writes more than 64 KiB to both stdout and stderr. Assert both streams are capped, `truncated` is true, and the process still exits without a pipe deadlock. Add an error assertion proving captured output and a secret-looking argv value never appear in the returned user-safe error or structured log fields.

- [ ] **Step 2: Run tests and verify RED**

```bash
cd desktop/src-tauri
cargo test mcp_install::connectors::common::tests -- --nocapture
cargo test mcp_install::connectors::process::tests -- --nocapture
```

Expected: helper types and functions are unresolved.

- [ ] **Step 3: Implement descriptors and safe file mutation**

Create `common.rs` with a responsibility/boundary header. Define:

```rust
pub(super) struct FileMutationOutcome {
    pub changed: bool,
    pub backup_path: Option<String>,
}

pub(super) fn descriptor(
    id: &str,
    display_name: &str,
    hook_support: SupportMode,
    docs_url: Option<&str>,
) -> AgentConnectorDescriptor {
    AgentConnectorDescriptor::new(AgentConnectorDescriptorInput {
        id: id.into(),
        display_name: display_name.into(),
        built_in: true,
        platforms: vec![ConnectorPlatform::Macos, ConnectorPlatform::Windows, ConnectorPlatform::Linux],
        integrations: vec![
            IntegrationSupport { capability: IntegrationCapability::Mcp, support: SupportMode::Automatic },
            IntegrationSupport { capability: IntegrationCapability::Skill, support: SupportMode::Automatic },
            IntegrationSupport { capability: IntegrationCapability::SessionHook, support: hook_support },
        ],
        operations: [
            ConnectorOperation::Detect, ConnectorOperation::Install, ConnectorOperation::Update,
            ConnectorOperation::Status, ConnectorOperation::Uninstall, ConnectorOperation::Verify,
        ].into_iter().map(|operation| OperationSupport { operation, support: SupportMode::Automatic }).collect(),
        docs_url: docs_url.map(str::to_string),
        verified_versions: None,
    }).expect("verified connector descriptor")
}

pub(super) fn entry(ctx: &ConnectorRuntimeContext) -> McpEntry {
    McpEntry {
        command: ctx.mcp_binary().to_string_lossy().into_owned(),
        agent_url: DEFAULT_AGENT_URL.into(),
    }
}
```

Add documented helpers for `integration_result`, `manual_hook_result`, `skill_status`, `install_skill`, and `uninstall_skill`. `manual_hook_result` returns `NeedsAction` with a nonblank message.

Implement `mutate_config(connector_id, path, transform)` and `remove_config` using this exact safety order:

1. `symlink_metadata`; reject symlinks and non-files as `unsafe_config_target`.
2. Read existing UTF-8 or treat NotFound as empty.
3. Run the format-specific transform before creating a backup.
4. Return unchanged without writing when `MergeResult.changed` is false.
5. Create the parent directory.
6. Copy the existing target to `backup_path(path)`.
7. Call the existing `atomic_write_file`.

Log `connector_id`, operation, result, stable error code, and duration. Do not log the path or content.

- [ ] **Step 4: Implement the bounded argv runner**

Create `process.rs` with a responsibility/boundary header and:

```rust
pub(super) const MAX_CAPTURED_BYTES: usize = 64 * 1024;

#[derive(Clone, Debug)]
pub(super) struct CommandSpec {
    pub program: PathBuf,
    pub args: Vec<OsString>,
    pub timeout: Duration,
    pub env: Vec<(OsString, OsString)>,
}

pub(super) trait CommandRunner: Send + Sync {
    fn run(&self, spec: CommandSpec) -> Result<CommandOutput, ConnectorError>;
}

pub(super) struct SystemCommandRunner;

#[derive(Clone, Debug)]
pub(super) struct CommandOutput {
    pub status_code: Option<i32>,
    pub stdout: String,
    pub stderr: String,
    pub truncated: bool,
}
```

`SystemCommandRunner::run` must call `std::process::Command` with argv, drain stdout/stderr on separate threads, keep at most 64 KiB per stream while continuing to drain, poll `try_wait` every 10 ms, kill/wait after the deadline, and return stable errors: `command_spawn_failed`, `command_wait_failed`, `command_timeout`, `command_output_failed`.

- [ ] **Step 5: Add logs and intent comments**

Log before spawn, nonzero/timeout, and success with program basename, argument count, exit code, truncation, and duration. Never log arguments/output. Add an inline comment explaining why readers continue draining after the capture limit. Add doc comments to every sibling-visible item.

- [ ] **Step 6: Verify GREEN**

```bash
cd desktop/src-tauri
cargo test mcp_install::connectors::common::tests -- --nocapture
cargo test mcp_install::connectors::process::tests -- --nocapture
cargo clippy --all-targets -- -D warnings
```

Expected: helper tests pass, timeout returns within one second, and Clippy is clean.

- [ ] **Step 7: Commit Task 2**

```bash
git add desktop/src-tauri/src/mcp_install/connectors.rs desktop/src-tauri/src/mcp_install/connectors/common.rs desktop/src-tauri/src/mcp_install/connectors/process.rs
git commit -m "Add safe connector support primitives"
```

## Task 3: Add the Kimi Code Connector

**Files:**

- Create: `desktop/src-tauri/src/mcp_install/connectors/kimi_code.rs`
- Modify: `desktop/src-tauri/src/mcp_install/connectors.rs`

- [ ] **Step 1: Write failing Kimi Code contract and mutation tests**

Create `kimi_code.rs` with the required responsibility/boundary header and a `tests` module covering:

```rust
#[test]
fn descriptor_is_standard_and_uses_the_open_kimi_code_id() {
    let descriptor = KimiCodeConnector::new().descriptor().clone();
    assert_eq!(descriptor.id(), "kimi-code");
    assert_eq!(descriptor.support_level(), Some(SupportLevel::Standard));
}

#[test]
fn kimi_code_home_override_controls_config_and_skill_paths() {
    let root = test_dir("kimi-home");
    let ctx = context().with_environment(ConnectorEnvironment::new(
        None,
        None,
        Some(root.clone()),
    ));
    assert_eq!(config_path(&ctx), root.join("mcp.json"));
    assert_eq!(skill_path(&ctx), root.join("skills/superdev"));
}
```

Add fixture-driven tests proving that install/update/uninstall:

- preserve unrelated top-level fields and other `mcpServers` entries;
- write only `mcpServers.superdev.command` and `.env.SUPERDEV_AGENT_URL`;
- are idempotent and remove only `superdev`;
- reject malformed JSON without backup or overwrite;
- return a working MCP result plus `NeedsAction` for the manual hook, making the aggregate result `Partial`.
- construct default and overridden paths with `PathBuf` so the same assertions run with native separators on macOS, Linux, and Windows CI.

- [ ] **Step 2: Run tests and verify RED**

```bash
cd desktop/src-tauri
cargo test mcp_install::connectors::kimi_code::tests -- --nocapture
```

Expected: the module, connector, and path helpers do not exist.

- [ ] **Step 3: Implement Kimi Code path resolution and status**

Implement `KimiCodeConnector` over the six-method `AgentConnector` trait. Resolve the data root from `ctx.environment().kimi_code_home()` or `ctx.home_dir().join(".kimi-code")`; derive `mcp.json` and `skills/superdev` from that root.

Detection succeeds when `kimi`/`kimi.exe` is present in `command_dirs`, or when the Kimi Code data/config root already exists. Status parses `mcp.json` and marks MCP configured only when the owned `superdev` entry has the expected binary and `SUPERDEV_AGENT_URL`. Return the exact target paths in the public result but never include them in logs.

- [ ] **Step 4: Implement install, update, uninstall, and manual instructions**

Use the existing strict JSON merge/remove helpers through `common::mutate_config`, then:

1. install or update MCP first;
2. re-read status to prove the desired entry is present;
3. install Skill only after MCP is configured;
4. return `manual_hook_result` for Session Hook;
5. on uninstall, remove only `mcpServers.superdev` and the owned Skill directory.

`manual_instructions` must include a pasteable `mcpServers.superdev` object, Skill path, restart guidance, and the current Kimi Code TUI verification commands `/mcp` and `/mcp-config`. Do not introduce an `AgentKind` variant.

- [ ] **Step 5: Add logs and intent comments**

Log operation start/end, connector ID, requested capability count, per-capability result, stable error code, and duration. Add Chinese intent comments explaining why Skill installation is gated by post-write MCP status and why the manual Hook keeps the Connector at Standard support. Document the new connector and all sibling-visible helpers.

- [ ] **Step 6: Verify GREEN**

```bash
cd desktop/src-tauri
cargo test mcp_install::connectors::kimi_code::tests -- --nocapture
cargo test mcp_install::connectors -- --nocapture
cargo clippy --all-targets -- -D warnings
```

Expected: Kimi Code tests pass, existing connectors remain green, and Clippy reports no warnings.

- [ ] **Step 7: Commit Task 3**

```bash
git add desktop/src-tauri/src/mcp_install/connectors.rs desktop/src-tauri/src/mcp_install/connectors/kimi_code.rs
git commit -m "Add Kimi Code connector"
```

## Task 4: Add the OpenCode Connector with Lossless JSONC Mutation

**Files:**

- Create: `desktop/src-tauri/src/mcp_install/connectors/opencode.rs`
- Modify: `desktop/src-tauri/src/mcp_install/connectors.rs`

- [ ] **Step 1: Write failing JSONC preservation tests**

Add tests covering:

```rust
#[test]
fn descriptor_is_standard() {
    assert_eq!(
        OpenCodeConnector::new().descriptor().support_level(),
        Some(SupportLevel::Standard)
    );
}

#[test]
fn opencode_config_override_wins_over_the_default_path() {
    let override_path = test_dir("opencode").join("custom.jsonc");
    let ctx = context().with_environment(ConnectorEnvironment::new(
        Some(override_path.clone()),
        None,
        None,
    ));
    assert_eq!(config_path(&ctx), override_path);
}
```

Use a JSONC fixture containing comments, trailing commas, unrelated settings, and another MCP server. Assert byte-for-byte preservation of all unrelated slices after install and uninstall, idempotence on a second install, precise removal of `mcp.superdev`, fail-closed behavior for malformed JSONC, and native path construction on macOS/Linux/Windows CI.

- [ ] **Step 2: Run tests and verify RED**

```bash
cd desktop/src-tauri
cargo test mcp_install::connectors::opencode::tests -- --nocapture
```

Expected: `OpenCodeConnector` and the JSONC mutation functions are unresolved.

- [ ] **Step 3: Implement lossless OpenCode mutation**

Resolve config from `OPENCODE_CONFIG` in `ConnectorEnvironment`, otherwise `~/.config/opencode/opencode.json`; Skill lives at `~/.config/opencode/skills/superdev`.

Parse with `jsonc_parser::cst::CstRootNode::parse`. Use CST object APIs such as `object_value_or_set` to set only:

```json
{
  "mcp": {
    "superdev": {
      "type": "local",
      "command": ["/absolute/path/to/superdev-mcp"],
      "enabled": true,
      "environment": {
        "SUPERDEV_AGENT_URL": "http://127.0.0.1:57017"
      }
    }
  }
}
```

Compare the full CST text before and after to derive `changed`. Uninstall removes only the `superdev` property and preserves an empty user-owned `mcp` object. Use `parse_to_serde_value` for read-only status comparison; malformed input returns `invalid_config` before backup or write.

- [ ] **Step 4: Implement the six Connector methods**

Detect `opencode`/`opencode.exe` or the config/data root. Install MCP first, verify it by re-reading, then install the Skill. Return a manual Session Hook result with an explicit OpenCode plugin/startup instruction. Uninstall only the owned MCP entry and Skill. Manual instructions must show the exact local MCP schema and restart/verification steps.

- [ ] **Step 5: Add logs and intent comments**

Record the connector ID, operation, capability, changed flag, stable error code, and duration without logging config text or paths. Add an intent comment explaining why CST mutation is mandatory instead of serializing through `serde_json`, plus docs on every sibling-visible helper.

- [ ] **Step 6: Verify GREEN**

```bash
cd desktop/src-tauri
cargo test mcp_install::connectors::opencode::tests -- --nocapture
cargo test mcp_install::connectors -- --nocapture
cargo clippy --all-targets -- -D warnings
```

Expected: JSONC comments and unrelated settings survive every test; all Rust checks pass.

- [ ] **Step 7: Commit Task 4**

```bash
git add desktop/src-tauri/src/mcp_install/connectors.rs desktop/src-tauri/src/mcp_install/connectors/opencode.rs
git commit -m "Add OpenCode connector"
```

## Task 5: Add the OpenClaw Connector through Its Official CLI

**Files:**

- Create: `desktop/src-tauri/src/mcp_install/connectors/openclaw.rs`
- Modify: `desktop/src-tauri/src/mcp_install/connectors.rs`

- [ ] **Step 1: Write failing CLI boundary tests with a fake runner**

Define a `FakeCommandRunner` in the test module and assert exact argv, without invoking a shell:

```rust
#[test]
fn install_uses_the_official_mcp_set_command() {
    let runner = FakeCommandRunner::succeed();
    let connector = OpenClawConnector::with_runner(Arc::new(runner.clone()));
    connector.install(&context(), install_request()).unwrap();
    assert_eq!(runner.argv(), vec![
        vec!["mcp", "show", "superdev", "--json"],
        vec!["mcp", "set", "superdev", expected_canonical_json()],
        vec!["mcp", "show", "superdev", "--json"],
    ]);
}

#[test]
fn uninstall_uses_the_official_mcp_unset_command() {
    // Arrange a configured show response, then assert `mcp unset superdev`.
}
```

Also cover missing CLI, timeout, capped/redacted output, nonzero `set`, malformed `show --json`, Windows executable naming, and the rule that an MCP failure yields `Failed` while Skill is `Skipped` and a manual configuration fallback is present.

- [ ] **Step 2: Run tests and verify RED**

```bash
cd desktop/src-tauri
cargo test mcp_install::connectors::openclaw::tests -- --nocapture
```

Expected: `OpenClawConnector`, injected runner, and CLI parsing do not exist.

- [ ] **Step 3: Implement the injectable official-CLI adapter**

Define the connector as descriptor plus `Arc<dyn CommandRunner>`. `new()` uses `SystemCommandRunner`; a `#[cfg(test)] with_runner` constructor accepts the fake. Detect `openclaw`/`openclaw.exe` or `~/.openclaw`, but require the CLI for every mutation.

Use these argv-only operations:

```text
openclaw mcp show superdev --json
openclaw mcp set superdev <canonical-json>
openclaw mcp unset superdev
```

The canonical JSON contains only the SuperDev stdio command and `SUPERDEV_AGENT_URL`. Never edit `~/.openclaw/openclaw.json` directly because OpenClaw owns JSON5/includes/Nix resolution. `OPENCLAW_CONFIG_PATH` is carried only as an environment entry in `CommandSpec` and a target hint in results; it is never parsed or written by SuperDev.

- [ ] **Step 4: Implement status, lifecycle ordering, and manual fallback**

Status is read-only and calls only `mcp show superdev --json`; Registry verify delegates to status, so it must not run `doctor --probe`. Install calls show, set only if needed, show again, and installs Skill only after the second show proves MCP configured. Uninstall calls unset only when show reports the owned entry, then removes the owned Skill.

Manual instructions contain the exact `openclaw mcp set` command and a separate optional `openclaw doctor --probe` verification step. Session Hook remains manual and therefore Standard support.

- [ ] **Step 5: Add safe external-boundary logs and comments**

Log CLI basename, operation name, argument count, status code, result, stable error code, and duration; do not log argv, canonical JSON, stdout, stderr, paths, or inherited environment. Add an intent comment at the no-direct-file-write boundary and document constructors and parser helpers.

- [ ] **Step 6: Verify GREEN**

```bash
cd desktop/src-tauri
cargo test mcp_install::connectors::openclaw::tests -- --nocapture
cargo test mcp_install::connectors::process::tests -- --nocapture
cargo clippy --all-targets -- -D warnings
```

Expected: fake-runner tests prove the exact official CLI contract, timeout is bounded, and Clippy is clean.

- [ ] **Step 7: Commit Task 5**

```bash
git add desktop/src-tauri/src/mcp_install/connectors.rs desktop/src-tauri/src/mcp_install/connectors/openclaw.rs
git commit -m "Add OpenClaw connector"
```

## Task 6: Add the Hermes Connector with Lossless YAML and an Owned Hook

**Files:**

- Create: `desktop/src-tauri/src/mcp_install/connectors/hermes.rs`
- Modify: `desktop/src-tauri/src/mcp_install/connectors.rs`

- [ ] **Step 1: Write failing YAML and hook ownership tests**

Use a fixture containing comments, unrelated settings, another MCP server, and an existing user `on_session_start` hook. Test:

```rust
#[test]
fn descriptor_is_full() {
    assert_eq!(
        HermesConnector::new().descriptor().support_level(),
        Some(SupportLevel::Full)
    );
}
```

Assert install/update preserve comments, sequences, block scalars, and user values; add exactly one SuperDev MCP entry and one marked hook command; are idempotent; and uninstall removes only those owned nodes. Assert malformed or unsupported YAML is never backed up or rewritten. Assert a failed Skill install never leaves an active Hook. Assert status treats an installed hook as `NeedsAction` until its exact event/command pair appears in the Hermes allowlist, then reports `Configured`; a missing hook remains `Missing`.

- [ ] **Step 2: Run tests and verify RED**

```bash
cd desktop/src-tauri
cargo test mcp_install::connectors::hermes::tests -- --nocapture
```

Expected: the connector and lossless YAML transform are unresolved.

- [ ] **Step 3: Implement lossless YAML transforms**

Resolve `~/.hermes/config.yaml` and `~/.hermes/skills/superdev`. Parse with `yaml_edit::Document`; use typed builders for generated fragments, exact block-level splices for nested YAML nodes, and a final parse gate before writing. This avoids the indentation loss in `yaml-edit` 0.2.3 nested `set`/`push` operations while retaining the rest of the CST byte-for-byte. Never interpolate binary paths into hand-written YAML strings.

Set only `mcp_servers.superdev` to the SuperDev command/env and append one uniquely marked `hooks.pre_llm_call` entry that invokes the absolute `skills/superdev/hooks/run-hook.cmd hermes-session-context` wrapper. The script consumes Hermes JSON stdin, injects `{"context":"..."}` once per `session_id`, and returns `{}` thereafter. Migrate only the obsolete SuperDev-owned `on_session_start` entry. The marker must be stable enough for precise status and uninstall. Preserve user parent mappings and build all paths with native `PathBuf` semantics for macOS/Linux/Windows CI.

- [ ] **Step 4: Implement the six Connector methods**

Detect `hermes`/`hermes.exe` or `~/.hermes`. Install MCP, prove status, install Skill, then install the owned Hook. Uninstall removes only the owned MCP entry, owned Skill, and marked Hook. Return Full support because all three integrations are automatic. Compare the exact event/command pair with `shell-hooks-allowlist.json`: report `NeedsAction` before trust, `Configured` after approval, and `Error` for an unreadable or malformed trust file. Manual instructions reference `hermes hooks list` and `hermes hooks doctor` for recovery.

- [ ] **Step 5: Add logs and intent comments**

Log operation/capability/result/error code/duration without config, command, or path values. Add intent comments describing the stable ownership marker and why an installed Hook still reports `NeedsAction`; document all sibling-visible helpers.

- [ ] **Step 6: Verify GREEN**

```bash
cd desktop/src-tauri
cargo test mcp_install::connectors::hermes::tests -- --nocapture
cargo test mcp_install::connectors -- --nocapture
cargo clippy --all-targets -- -D warnings
```

Expected: comments and user hooks survive, owned entries round-trip exactly, and all checks pass.

- [ ] **Step 7: Commit Task 6**

```bash
git add desktop/src-tauri/src/mcp_install/connectors.rs desktop/src-tauri/src/mcp_install/connectors/hermes.rs
git commit -m "Add Hermes connector"
```

## Task 7: Register Seven Built-ins and Prove the Frontend Is Registry-Driven

**Files:**

- Modify: `desktop/src-tauri/src/mcp_install/connectors.rs`
- Modify: `desktop/src/dev/onboardingPreview.ts`
- Modify: `desktop/src/dev/__tests__/onboardingPreview.test.ts`
- Modify: `desktop/src/stores/__tests__/onboarding.test.ts`
- Modify: `desktop/src/components/Settings/__tests__/McpManagerTab.test.ts`

- [ ] **Step 1: Write failing Registry ordering and support-level tests**

Update the Rust built-in registration test to assert exact stable order and derived levels:

```rust
assert_eq!(
    builtin()
        .iter()
        .map(|connector| (connector.descriptor().id(), connector.descriptor().support_level()))
        .collect::<Vec<_>>(),
    vec![
        ("claude-code", Some(SupportLevel::Full)),
        ("codex", Some(SupportLevel::Full)),
        ("cursor", Some(SupportLevel::Full)),
        ("opencode", Some(SupportLevel::Standard)),
        ("openclaw", Some(SupportLevel::Standard)),
        ("hermes", Some(SupportLevel::Full)),
        ("kimi-code", Some(SupportLevel::Standard)),
    ]
);
```

- [ ] **Step 2: Write failing dynamic frontend tests**

Add seven-Connector fixtures and assert that onboarding and Settings render all Registry descriptors, their support labels, detection state, manual-action messages, and retry/install actions without a TypeScript connector whitelist. Include a result where Kimi Code MCP succeeds but Hook is `NeedsAction`, and assert the UI preserves the working MCP state while showing overall Partial.

Run:

```bash
cd desktop
pnpm test -- onboardingPreview onboarding McpManagerTab
```

Expected: current three-Connector fixtures and registration make the assertions fail.

- [ ] **Step 3: Register the four new modules**

Declare `hermes`, `kimi_code`, `openclaw`, and `opencode`, then append their `Arc<dyn AgentConnector>` instances to `builtin()` in the tested order. Update that function's doc comment from three to seven production connectors. Do not add `AgentKind` variants, hard-coded command routing, a frontend enum, or special-case Tauri commands.

Add one Registry assembly debug log with `connector_count` only. Add a comment explaining that registration order is part of deterministic first-launch presentation, while connector IDs remain open strings.

- [ ] **Step 4: Refresh preview fixtures and preserve dynamic rendering**

Expand `onboardingPreview.ts` to seven production IDs. Use detected examples for Claude Code, Codex, OpenCode, and Hermes, and undetected examples for Cursor, OpenClaw, and Kimi Code so both UI paths remain visible. Keep all mapping keyed by descriptor data from the backend contract.

Add/refresh comments describing the preview fixture's responsibility and boundary; do not add production-only logging to static fixtures. Existing stores/components should remain unchanged unless a failing test reveals a genuine dynamic-contract bug.

- [ ] **Step 5: Verify GREEN**

```bash
cd desktop/src-tauri
cargo test mcp_install::connectors::tests -- --nocapture
cargo test mcp_install::registry::tests -- --nocapture
cd ..
pnpm test -- onboardingPreview onboarding McpManagerTab
pnpm build
```

Expected: seven built-ins appear in stable order, all UI tests pass without a whitelist, and the frontend build succeeds.

- [ ] **Step 6: Commit Task 7**

```bash
git add desktop/src-tauri/src/mcp_install/connectors.rs desktop/src/dev/onboardingPreview.ts desktop/src/dev/__tests__/onboardingPreview.test.ts desktop/src/stores/__tests__/onboarding.test.ts desktop/src/components/Settings/__tests__/McpManagerTab.test.ts
git commit -m "Register second wave agent connectors"
```

## Task 8: Document, Review, and Run Full Cross-Platform Acceptance

**Files:**

- Modify: `README.md`
- Verify: `.github/workflows/*.yml`

- [ ] **Step 1: Update user-facing support documentation**

Replace the three-Agent statement with the seven verified built-ins: Claude Code, Codex, Cursor, OpenCode, OpenClaw, Hermes, and Kimi Code. Explain that Standard means automatic MCP + Skill with a manual Hook, Full means all three are automatic, unknown compatible Agents can still use Connector/manual materials, and Pi is explicitly deferred from this wave.

- [ ] **Step 2: Audit instrumentation and comments before claiming completion**

Run:

```bash
rg -n 'tracing::(info|warn|error|debug)!' desktop/src-tauri/src/mcp_install
rg -n 'println!|eprintln!|dbg!|console\.log' desktop/src-tauri/src/mcp_install desktop/src
rg -n '^pub\(|^pub (struct|enum|trait|fn)|^pub fn' desktop/src-tauri/src/mcp_install/connectors
```

Expected: each new operation boundary and error path has structured logging; there are no ad-hoc prints; every new file has a responsibility/boundary header and every sibling-visible/exported item has a doc comment. Inspect success paths to ensure none are silent and confirm logs contain no secrets, argv, config bodies, or user paths.

- [ ] **Step 3: Run the complete local verification matrix**

```bash
cd desktop/src-tauri
cargo fmt --all -- --check
cargo test -q
cargo clippy --all-targets -- -D warnings
cd ..
pnpm test
pnpm build
```

Expected: every command exits 0 with no ignored new Connector failures.

- [ ] **Step 4: Verify workflow coverage without mutating CI**

Parse all workflow YAML and inspect the native jobs:

```bash
ruby -e 'require "yaml"; Dir[".github/workflows/*.{yml,yaml}"].sort.each { |path| YAML.load_file(path); puts path }'
rg -n 'cargo test mcp_install::connectors|superdev-desktop-(linux|windows)' .github/workflows/ci.yml .github/workflows/build-desktop-clients.yml
```

Confirm the Linux and Windows jobs still execute `cargo test mcp_install::connectors -- --nocapture`, package the desktop application, and upload `superdev-desktop-linux` / `superdev-desktop-windows`. If the four new focused Rust tests are not covered by that native test command, update the workflow in a separate TDD-style commit and repeat YAML validation.

- [ ] **Step 5: Request code review and address findings**

Use `superpowers:requesting-code-review` against the design and this plan. Resolve every correctness, compatibility, security, logging, and preservation finding, rerun the focused checks for touched files, then rerun Step 3.

- [ ] **Step 6: Commit documentation and any verified CI adjustment**

```bash
git add README.md
git add .github/workflows
git commit -m "Document expanded agent connector support"
```

If workflow files are unchanged, the second `git add` is a no-op. Confirm none of the locally forbidden paths (`AGENTS.md`, `docs/superpowers/`, `specs/`) are staged.

- [ ] **Step 7: Push only with user authorization, then wait for artifacts**

After the user explicitly authorizes publication:

```bash
git status --short
git push -u origin codex/agent-connector-registry
gh workflow run build-desktop-clients.yml --ref codex/agent-connector-registry -f target=windows-linux
gh run list --workflow build-desktop-clients.yml --branch codex/agent-connector-registry --event workflow_dispatch --limit 5
```

Open the exact workflow-dispatch run for the pushed HEAD with `gh run view <run-id>`, wait until both Windows and Linux desktop jobs complete, then run:

```bash
gh run download <run-id> --dir /private/tmp/superdev-agent-connectors-wave-2-<run-id>
find /private/tmp/superdev-agent-connectors-wave-2-<run-id> -type f -print
find /private/tmp/superdev-agent-connectors-wave-2-<run-id> -type f -exec shasum -a 256 {} \;
```

Do not report success while either job is queued/in progress or while an artifact is missing.

- [ ] **Step 8: Verify downloaded deliverables and close with evidence**

List the downloaded artifact filenames, sizes, and SHA-256 digests; inspect archive/package contents for the expected desktop binary and bundled SuperDev MCP executable. Run `superpowers:verification-before-completion`, then report:

- the seven registered Connector IDs and derived levels;
- focused and full local test/build evidence;
- GitHub run URL and final Windows/Linux job conclusions;
- absolute local artifact paths and digests;
- the explicit limitation that Pi remains deferred.

If any backend structure changed materially during review, refresh `backend-linkmap` and compare the resulting chain with the approved design before marking the work complete.
