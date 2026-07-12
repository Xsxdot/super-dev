# Grok CLI Agent Connector Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a verified built-in Full-support `GrokConnector` so SuperDev can detect Grok CLI, install MCP via `grok mcp`, install Skill + owned SessionStart hook files, and surface the agent in onboarding/Settings without frontend hardcoding.

**Architecture:** New private module `connectors/grok.rs` implements `AgentConnector` using the existing OpenClaw-style `process::CommandRunner` for MCP CLI and `common` skill helpers for the Skill tree. Session hooks use an owned JSON file under `~/.grok/hooks/`. Register as the eighth built-in; do not extend `AgentKind`. Honest Full: SessionStart is passive on Grok (no additionalContext injection); guidance is Skill-first.

**Tech Stack:** Rust 2021, Tauri 2, `serde_json`, `tracing`, Vue 3, Vitest. Spec: `docs/2026-07-12-grok-cli-connector-design.md`.

---

## File Map

| File | Responsibility |
| --- | --- |
| Create: `desktop/src-tauri/src/mcp_install/connectors/grok.rs` | `GrokConnector`: detect, status, install, uninstall, manual_instructions; MCP CLI + Skill + Hook. |
| Modify: `desktop/src-tauri/src/mcp_install/connectors.rs` | `mod grok`; append to `builtin()`; update seven→eight registry tests. |
| Modify: `desktop/src/dev/onboardingPreview.ts` | Add Grok to deterministic onboarding preview fixtures (eight connectors). |
| Modify: `desktop/src/dev/__tests__/onboardingPreview.test.ts` | Expect eight connectors including `grok` Full. |
| Modify: `desktop/src/components/Settings/__tests__/McpManagerTab.test.ts` | Fixture lists include Grok where they assert “production connectors” count. |
| Modify: `README.md` | Eight built-ins; Full list includes Grok. |
| Modify: `README.zh-CN.md` | Align connector narrative with EN (include Grok + Full/Standard). |

No `ConnectorEnvironment` field, no Cargo.toml deps, no `AgentKind` variant, no direct TOML MCP edit.

---

## Task 1: Scaffold GrokConnector + Registry Registration

**Files:**
- Create: `desktop/src-tauri/src/mcp_install/connectors/grok.rs`
- Modify: `desktop/src-tauri/src/mcp_install/connectors.rs`

- [ ] **Step 1: Write failing registry test update**

In `connectors.rs` test `builtin_registers_seven_connectors_in_stable_order_with_derived_levels`, rename to `builtin_registers_eight_connectors_...` and append Grok:

```rust
#[test]
fn builtin_registers_eight_connectors_in_stable_order_with_derived_levels() {
    assert_eq!(
        builtin()
            .iter()
            .map(|connector| {
                (
                    connector.descriptor().id(),
                    connector.descriptor().support_level(),
                )
            })
            .collect::<Vec<_>>(),
        vec![
            ("claude-code", Some(SupportLevel::Full)),
            ("codex", Some(SupportLevel::Full)),
            ("cursor", Some(SupportLevel::Full)),
            ("opencode", Some(SupportLevel::Standard)),
            ("openclaw", Some(SupportLevel::Standard)),
            ("hermes", Some(SupportLevel::Full)),
            ("kimi-code", Some(SupportLevel::Standard)),
            ("grok", Some(SupportLevel::Full)),
        ]
    );
}
```

Update any comment that says “七个生产内置连接器” near `builtin()` to “八个”.

- [ ] **Step 2: Run test — expect RED**

```bash
cd desktop/src-tauri
cargo test mcp_install::connectors::tests::builtin_registers_eight -- --nocapture
```

Expected: compile/test fail (no `grok` module / wrong length).

- [ ] **Step 3: Minimal Grok module + register**

Create `grok.rs` with file header and stub:

```rust
// grok.rs 实现 Grok CLI 内置 Agent Connector（官方 MCP CLI + Skill + owned Hook）。
//
// 职责：
//   - 通过 argv 调用 grok mcp add/list/remove 管理 user-scope superdev MCP
//   - 安装 SuperDev Skill 到 ~/.grok/skills/superdev
//   - 写入拥有文件 ~/.grok/hooks/superdev-session-start.json（SessionStart）
//
// 边界：
//   - 不直接解析或改写 ~/.grok/config.toml 的 MCP 段
//   - 不把 Claude/Cursor 兼容扫描视为已接入
//   - 不记录 argv、路径、stdout/stderr 或配置正文
//   - Grok SessionStart 为 passive：不承诺 additionalContext 注入

use super::common;
use super::process::{CommandOutput, CommandRunner, CommandSpec, SystemCommandRunner};
use crate::mcp_install::contracts::*;
use crate::mcp_install::registry::*;
use crate::mcp_install::{executable_file_names, DEFAULT_AGENT_URL};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::{Duration, Instant};

const CONNECTOR_ID: &str = "grok";
const DISPLAY_NAME: &str = "Grok";
const CLI_TIMEOUT: Duration = Duration::from_secs(30);
const HOOK_MARKER: &str = "skills/superdev/hooks/run-hook.cmd";
const HOOK_FILE_NAME: &str = "superdev-session-start.json";

/// GrokConnector 适配 Grok CLI 的 MCP/Skill/Session Hook。
pub(super) struct GrokConnector {
    descriptor: AgentConnectorDescriptor,
    runner: Arc<dyn CommandRunner>,
}

impl GrokConnector {
    /// new 使用系统进程执行器创建 Full 级 Grok 连接器。
    pub(super) fn new() -> Self {
        Self::with_runner(Arc::new(SystemCommandRunner))
    }

    #[cfg(test)]
    pub(super) fn with_runner(runner: Arc<dyn CommandRunner>) -> Self {
        Self {
            descriptor: common::descriptor(
                CONNECTOR_ID,
                DISPLAY_NAME,
                SupportMode::Automatic,
                None,
            ),
            runner,
        }
    }

    #[cfg(not(test))]
    fn with_runner(runner: Arc<dyn CommandRunner>) -> Self {
        Self {
            descriptor: common::descriptor(
                CONNECTOR_ID,
                DISPLAY_NAME,
                SupportMode::Automatic,
                None,
            ),
            runner,
        }
    }
}

// Temporary stubs until later tasks — each method returns clear errors
// except descriptor/detect for Task 1–2. Prefer implementing full trait
// with placeholder bodies that return ConnectorError::new("not_implemented", ...)
// only if required to compile; prefer completing methods in subsequent tasks
// in the same PR stack so CI stays green after each task commit.
```

In `connectors.rs`:

```rust
mod grok;
// ...
// in builtin():
Arc::new(kimi_code::KimiCodeConnector::new()),
Arc::new(grok::GrokConnector::new()),
```

Implement the full `AgentConnector` trait with real `descriptor` and temporary safe stubs for other methods that compile (detect returns not detected; status returns all Missing; install/uninstall return NeedsAction). **Do not leave `todo!()`.**

Example stub status (replace in Task 3–5):

```rust
fn status(&self, ctx: &ConnectorRuntimeContext) -> Result<ConnectorStatus, ConnectorError> {
    Ok(ConnectorStatus {
        integrations: vec![
            IntegrationState {
                capability: IntegrationCapability::Mcp,
                status: IntegrationStateStatus::Missing,
                target_path: Some(common::path_string(&config_path(ctx))),
                message: Some("stub".into()),
            },
            common::skill_status(ctx, &skill_path(ctx)),
            IntegrationState {
                capability: IntegrationCapability::SessionHook,
                status: IntegrationStateStatus::Missing,
                target_path: Some(common::path_string(&hook_path(ctx))),
                message: None,
            },
        ],
        requires_restart: false,
        message: None,
        mcp_command: None,
        agent_url: None,
    })
}
```

Add path helpers used above:

```rust
fn data_root(ctx: &ConnectorRuntimeContext) -> PathBuf {
    ctx.home_dir().join(".grok")
}
fn config_path(ctx: &ConnectorRuntimeContext) -> PathBuf {
    data_root(ctx).join("config.toml")
}
fn skill_path(ctx: &ConnectorRuntimeContext) -> PathBuf {
    data_root(ctx).join("skills").join("superdev")
}
fn hook_path(ctx: &ConnectorRuntimeContext) -> PathBuf {
    data_root(ctx).join("hooks").join(HOOK_FILE_NAME)
}
fn resolve_cli(ctx: &ConnectorRuntimeContext) -> Option<PathBuf> {
    ctx.command_dirs().iter().find_map(|directory| {
        executable_file_names("grok")
            .into_iter()
            .map(|name| directory.join(name))
            .find(|path| path.is_file())
    })
}
```

- [ ] **Step 4: Add entry/exit logging on `new` is not needed; add detect stub log later. For registration path, ensure `builtin()` existing debug log still fires.**

- [ ] **Step 5: Run test — expect GREEN**

```bash
cd desktop/src-tauri
cargo test mcp_install::connectors::tests::builtin_registers_eight -- --nocapture
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add desktop/src-tauri/src/mcp_install/connectors/grok.rs \
  desktop/src-tauri/src/mcp_install/connectors.rs
git commit -m "feat(connectors): register Grok built-in scaffold"
```

---

## Task 2: Detect

**Files:**
- Modify: `desktop/src-tauri/src/mcp_install/connectors/grok.rs`

- [ ] **Step 1: Write failing detect tests** (in `grok.rs` `#[cfg(test)] mod tests`)

Reuse OpenClaw’s temp-home + FakeCommandRunner patterns. Minimal helpers:

```rust
fn test_home() -> PathBuf {
    let root = std::env::temp_dir().join(format!(
        "grok-conn-{}-{}",
        std::process::id(),
        COUNTER.fetch_add(1, Ordering::SeqCst)
    ));
    let _ = std::fs::remove_dir_all(&root);
    std::fs::create_dir_all(&root).unwrap();
    root
}

fn context_at(home: PathBuf, command_dirs: Vec<PathBuf>) -> ConnectorRuntimeContext {
    ConnectorRuntimeContext::new(
        home.clone(),
        command_dirs,
        vec![],
        home.join("superdev-mcp"),
        None,
        None,
    )
}
```

Tests:

```rust
#[test]
fn detect_prefers_cli_path_when_present() {
    let home = test_home();
    let bin = home.join("bin");
    std::fs::create_dir_all(&bin).unwrap();
    let cli = bin.join("grok");
    std::fs::write(&cli, "").unwrap();
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let mut perms = std::fs::metadata(&cli).unwrap().permissions();
        perms.set_mode(0o755);
        std::fs::set_permissions(&cli, perms).unwrap();
    }
    let ctx = context_at(home.clone(), vec![bin]);
    let hit = GrokConnector::new().detect(&ctx).unwrap();
    assert!(hit.detected);
    assert_eq!(hit.detection_path.as_deref(), Some(cli.as_path()));
    let _ = std::fs::remove_dir_all(home);
}

#[test]
fn detect_falls_back_to_data_root_without_cli() {
    let home = test_home();
    std::fs::create_dir_all(home.join(".grok")).unwrap();
    let ctx = context_at(home.clone(), vec![]);
    let hit = GrokConnector::new().detect(&ctx).unwrap();
    assert!(hit.detected);
    assert_eq!(
        hit.detection_path.as_deref(),
        Some(home.join(".grok").as_path())
    );
    let _ = std::fs::remove_dir_all(home);
}

#[test]
fn detect_misses_when_neither_cli_nor_root() {
    let home = test_home();
    let ctx = context_at(home.clone(), vec![]);
    let hit = GrokConnector::new().detect(&ctx).unwrap();
    assert!(!hit.detected);
    let _ = std::fs::remove_dir_all(home);
}
```

- [ ] **Step 2: Run tests — RED** if detect still stubbed.

```bash
cd desktop/src-tauri
cargo test mcp_install::connectors::grok::tests::detect_ -- --nocapture
```

- [ ] **Step 3: Implement detect**

```rust
fn detect(&self, ctx: &ConnectorRuntimeContext) -> Result<ConnectorDetection, ConnectorError> {
    let started = Instant::now();
    tracing::debug!(
        connector_id = CONNECTOR_ID,
        operation = "detect",
        "grok detect started"
    );
    let cli = resolve_cli(ctx);
    let root = data_root(ctx);
    let hit = cli.or_else(|| root.is_dir().then_some(root));
    let result = ConnectorDetection {
        detected: hit.is_some(),
        detection_path: hit,
        message: Some("Grok 检测完成".into()),
    };
    tracing::info!(
        connector_id = CONNECTOR_ID,
        operation = "detect",
        detected = result.detected,
        duration_ms = started.elapsed().as_millis() as u64,
        "grok detect finished"
    );
    Ok(result)
}
```

- [ ] **Step 4: Run tests — GREEN**

- [ ] **Step 5: Commit**

```bash
git add desktop/src-tauri/src/mcp_install/connectors/grok.rs
git commit -m "feat(connectors): detect Grok CLI and data root"
```

---

## Task 3: MCP CLI — list / match / add / remove

**Files:**
- Modify: `desktop/src-tauri/src/mcp_install/connectors/grok.rs`

### MCP helpers (implement in this task)

```rust
fn require_cli(ctx: &ConnectorRuntimeContext) -> Result<PathBuf, ConnectorError> {
    resolve_cli(ctx).ok_or_else(|| {
        ConnectorError::new(
            "cli_not_found",
            "未找到 grok CLI，无法通过官方命令管理 MCP",
        )
    })
}

fn run_cli(
    runner: &dyn CommandRunner,
    program: &Path,
    args: &[&str],
) -> Result<CommandOutput, ConnectorError> {
    let spec = CommandSpec::new(program, args.iter().map(|a| std::ffi::OsString::from(*a)))
        .with_timeout(CLI_TIMEOUT);
    // Note: do not inject HOME unless a later real-smoke needs it; unit tests mock runner.
    runner.run(spec)
}

fn list_servers(
    runner: &dyn CommandRunner,
    program: &Path,
) -> Result<Vec<serde_json::Value>, ConnectorError> {
    let output = run_cli(runner, program, &["mcp", "list", "--json"])?;
    if !output.success() {
        return Err(ConnectorError::new(
            "cli_list_failed",
            "grok mcp list 失败",
        ));
    }
    let text = output.stdout.trim();
    if text.is_empty() {
        return Ok(Vec::new());
    }
    let value: serde_json::Value = serde_json::from_str(text).map_err(|_| {
        ConnectorError::new(
            "invalid_cli_output",
            "grok mcp list --json 输出无法解析",
        )
    })?;
    match value {
        serde_json::Value::Array(items) => Ok(items),
        _ => Err(ConnectorError::new(
            "invalid_cli_output",
            "grok mcp list --json 根节点必须是数组",
        )),
    }
}

fn find_superdev_user_entry(items: &[serde_json::Value]) -> Option<&serde_json::Value> {
    items.iter().find(|item| {
        item.get("name").and_then(|v| v.as_str()) == Some("superdev")
            && item.get("scope").and_then(|v| v.as_str()) == Some("user")
    })
}

fn entry_matches(ctx: &ConnectorRuntimeContext, value: &serde_json::Value) -> bool {
    let expected = ctx.mcp_binary().to_string_lossy();
    let command = value.get("command").and_then(|v| v.as_str()).unwrap_or("");
    let agent_url = value
        .get("env")
        .and_then(|env| env.get("SUPERDEV_AGENT_URL"))
        .and_then(|v| v.as_str())
        .unwrap_or("");
    let enabled = value
        .get("enabled")
        .and_then(|v| v.as_bool())
        .unwrap_or(true);
    command == expected.as_ref() && agent_url == DEFAULT_AGENT_URL && enabled
}

fn configured_list_json(ctx: &ConnectorRuntimeContext) -> String {
    serde_json::json!([{
        "name": "superdev",
        "command": ctx.mcp_binary().to_string_lossy(),
        "args": [],
        "env": { "SUPERDEV_AGENT_URL": DEFAULT_AGENT_URL },
        "enabled": true,
        "scope": "user"
    }])
    .to_string()
}
```

- [ ] **Step 1: Write failing unit tests for match + list parsing**

```rust
#[test]
fn entry_matches_requires_command_url_enabled() {
    let home = test_home();
    let ctx = context_at(home.clone(), vec![]);
    let good: serde_json::Value = serde_json::from_str(&configured_list_json(&ctx)).unwrap();
    let entry = &good.as_array().unwrap()[0];
    assert!(entry_matches(&ctx, entry));

    let mut disabled = entry.clone();
    disabled["enabled"] = serde_json::json!(false);
    assert!(!entry_matches(&ctx, &disabled));

    let mut bad_url = entry.clone();
    bad_url["env"]["SUPERDEV_AGENT_URL"] = serde_json::json!("http://127.0.0.1:9");
    assert!(!entry_matches(&ctx, &bad_url));
    let _ = std::fs::remove_dir_all(home);
}

#[test]
fn status_mcp_configured_when_list_matches() {
    let home = test_home();
    let bin = home.join("bin");
    std::fs::create_dir_all(&bin).unwrap();
    let cli = bin.join("grok");
    std::fs::write(&cli, "").unwrap();
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let mut p = std::fs::metadata(&cli).unwrap().permissions();
        p.set_mode(0o755);
        std::fs::set_permissions(&cli, p).unwrap();
    }
    let ctx = context_at(home.clone(), vec![bin]);
    let runner = FakeCommandRunner::default();
    runner.push_ok(0, &configured_list_json(&ctx));
    let status = GrokConnector::with_runner(Arc::new(runner))
        .status(&ctx)
        .unwrap();
    let mcp = status
        .integrations
        .iter()
        .find(|i| i.capability == IntegrationCapability::Mcp)
        .unwrap();
    assert_eq!(mcp.status, IntegrationStateStatus::Configured);
    assert_eq!(status.mcp_command.as_deref(), Some(ctx.mcp_binary().to_str().unwrap()));
    let _ = std::fs::remove_dir_all(home);
}

#[test]
fn status_mcp_needs_action_for_project_only_scope() {
    let home = test_home();
    let bin = home.join("bin");
    std::fs::create_dir_all(&bin).unwrap();
    let cli = bin.join("grok");
    std::fs::write(&cli, "").unwrap();
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let mut p = std::fs::metadata(&cli).unwrap().permissions();
        p.set_mode(0o755);
        std::fs::set_permissions(&cli, p).unwrap();
    }
    let ctx = context_at(home.clone(), vec![bin]);
    let body = serde_json::json!([{
        "name": "superdev",
        "command": ctx.mcp_binary().to_string_lossy(),
        "env": { "SUPERDEV_AGENT_URL": DEFAULT_AGENT_URL },
        "enabled": true,
        "scope": "project"
    }])
    .to_string();
    let runner = FakeCommandRunner::default();
    runner.push_ok(0, &body);
    let status = GrokConnector::with_runner(Arc::new(runner))
        .status(&ctx)
        .unwrap();
    let mcp = status
        .integrations
        .iter()
        .find(|i| i.capability == IntegrationCapability::Mcp)
        .unwrap();
    assert_eq!(mcp.status, IntegrationStateStatus::NeedsAction);
    let _ = std::fs::remove_dir_all(home);
}

#[test]
fn install_mcp_calls_add_with_scope_user_and_env() {
    let home = test_home();
    let bin = home.join("bin");
    std::fs::create_dir_all(&bin).unwrap();
    let cli = bin.join("grok");
    std::fs::write(&cli, "").unwrap();
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let mut p = std::fs::metadata(&cli).unwrap().permissions();
        p.set_mode(0o755);
        std::fs::set_permissions(&cli, p).unwrap();
    }
    // skill source: empty dir is ok for later; for MCP-only path provide skill source
    let skill_src = home.join("skill-src");
    std::fs::create_dir_all(&skill_src).unwrap();
    std::fs::write(skill_src.join("SKILL.md"), "# s\n").unwrap();
    let ctx = ConnectorRuntimeContext::new(
        home.clone(),
        vec![bin],
        vec![],
        home.join("superdev-mcp"),
        Some(skill_src),
        None,
    );
    let runner = FakeCommandRunner::default();
    // list empty → add ok → list configured (install path)
    runner.push_ok(0, "[]");
    runner.push_ok(0, "");
    runner.push_ok(0, &configured_list_json(&ctx));
    let connector = GrokConnector::with_runner(Arc::new(runner.clone()));
    let outcome = connector
        .install(
            &ctx,
            ConnectorInstallRequest {
                operation: ConnectorOperation::Install,
                capabilities: vec![
                    IntegrationCapability::Mcp,
                    IntegrationCapability::Skill,
                    IntegrationCapability::SessionHook,
                ],
            },
        )
        .unwrap();
    assert!(matches!(
        outcome.result,
        ConnectorResult::Success | ConnectorResult::Partial
    ));
    let argv = runner.argv();
    assert!(argv.iter().any(|a| {
        a.get(0).map(String::as_str) == Some("mcp")
            && a.get(1).map(String::as_str) == Some("add")
            && a.iter().any(|x| x == "--scope")
            && a.iter().any(|x| x == "user")
            && a.iter().any(|x| x == "superdev")
    }));
    let _ = std::fs::remove_dir_all(home);
}
```

Copy `FakeCommandRunner` from `openclaw.rs` tests (same `CommandRunner` trait) into `grok.rs` tests — do not share across modules unless already public; private duplicate is fine (Wave 2 pattern).

- [ ] **Step 2: Run MCP-related tests — RED**

```bash
cd desktop/src-tauri
cargo test mcp_install::connectors::grok::tests:: -- --nocapture
```

- [ ] **Step 3: Implement status MCP branch + install MCP + uninstall MCP**

**status** (MCP part):

```rust
let (mcp_status, mcp_message) = match require_cli(ctx) {
    Ok(program) => match list_servers(self.runner.as_ref(), &program) {
        Ok(items) => {
            let user = find_superdev_user_entry(&items);
            let any_superdev = items.iter().any(|i| {
                i.get("name").and_then(|v| v.as_str()) == Some("superdev")
            });
            match user {
                Some(value) if entry_matches(ctx, value) => (
                    IntegrationStateStatus::Configured,
                    Some("SuperDev MCP 已由 grok CLI 配置".into()),
                ),
                Some(_) => (
                    IntegrationStateStatus::NeedsAction,
                    Some("superdev MCP 条目存在但不匹配期望配置".into()),
                ),
                None if any_superdev => (
                    IntegrationStateStatus::NeedsAction,
                    Some("superdev 仅存在于非 user scope，需改为 --scope user".into()),
                ),
                None => (
                    IntegrationStateStatus::Missing,
                    Some("grok 未配置 user-scope superdev MCP".into()),
                ),
            }
        }
        Err(error) if error.code() == "invalid_cli_output" => (
            IntegrationStateStatus::Error,
            Some("grok mcp list 输出无法解析".into()),
        ),
        Err(error) => {
            tracing::error!(
                connector_id = CONNECTOR_ID,
                operation = "status",
                error_code = error.code(),
                "grok status list failed"
            );
            return Err(error);
        }
    },
    Err(_) => (
        IntegrationStateStatus::Missing,
        Some("未找到 grok CLI，无法读取 MCP 状态".into()),
    ),
};
```

When Configured or NeedsAction with user entry intent, set:

```rust
let (mcp_command, agent_url) = if mcp_status == IntegrationStateStatus::Configured
    || mcp_status == IntegrationStateStatus::NeedsAction
{
    let entry = common::entry(ctx);
    (Some(entry.command), Some(entry.agent_url))
} else {
    (None, None)
};
```

**install MCP** (inside `install` after `require_cli`):

1. `list_servers` — if matching user entry → already
2. Else run:

```rust
let mcp_bin = ctx.mcp_binary().to_string_lossy().into_owned();
let env_arg = format!("SUPERDEV_AGENT_URL={DEFAULT_AGENT_URL}");
let add_args = [
    "mcp",
    "add",
    "superdev",
    "--scope",
    "user",
    "-e",
    env_arg.as_str(),
    "--",
    mcp_bin.as_str(),
];
let add_output = run_cli(self.runner.as_ref(), &program, &add_args)?;
```

3. On failure → aggregate Failed MCP + Skipped skill/hook + manual_instructions  
4. On success → list again and require `entry_matches`  
5. Result `Installed` vs `AlreadyPresent`

**uninstall MCP:**

```rust
// list; if user superdev present → remove --scope user; list to confirm gone
run_cli(runner, program, &["mcp", "remove", "superdev", "--scope", "user"])
```

Missing entry → AlreadyPresent semantics for uninstall (no change).

Use `aggregate_connector_result` from `contracts` like OpenClaw for install failures.

- [ ] **Step 4: Logging at MCP boundaries**

- `install` start/finish with `operation`, `result`, `duration_ms`  
- `mcp add` / `mcp remove` failure: `tracing::error!` with `error_code` / `status_code` / `truncated` only  
- **Never** log argv, command path, stdout, stderr  

- [ ] **Step 5: Intent comments**

- Why list is used instead of doctor  
- Why `--scope user` only  
- Why project-only scope is NeedsAction  

- [ ] **Step 6: Run tests — GREEN**

- [ ] **Step 7: Commit**

```bash
git add desktop/src-tauri/src/mcp_install/connectors/grok.rs
git commit -m "feat(connectors): manage Grok MCP via grok mcp CLI"
```

---

## Task 4: Skill + Session Hook automatic

**Files:**
- Modify: `desktop/src-tauri/src/mcp_install/connectors/grok.rs`

### Hook helpers

```rust
fn hook_command(skill_dir: &Path) -> String {
    let runner = skill_dir.join("hooks").join("run-hook.cmd");
    let runner = runner.to_string_lossy().replace('\\', "/");
    format!("\"{runner}\" session-start")
}

fn hook_file_body(skill_dir: &Path) -> String {
    let command = hook_command(skill_dir);
    // Keep marker substring skills/superdev/hooks/run-hook.cmd in the path.
    serde_json::json!({
        "hooks": {
            "SessionStart": [{
                "hooks": [{
                    "type": "command",
                    "command": command,
                    "timeout": 15
                }]
            }]
        }
    })
    .to_string()
}

fn hook_has_marker(content: &str) -> bool {
    content.contains(HOOK_MARKER) && content.contains("session-start")
}

fn hook_status(ctx: &ConnectorRuntimeContext) -> IntegrationState {
    let path = hook_path(ctx);
    let target = common::path_string(&path);
    if !path.is_file() {
        return IntegrationState {
            capability: IntegrationCapability::SessionHook,
            status: IntegrationStateStatus::Missing,
            target_path: Some(target),
            message: Some("未安装 SuperDev SessionStart hook".into()),
        };
    }
    match std::fs::read_to_string(&path) {
        Ok(content) if hook_has_marker(&content) => IntegrationState {
            capability: IntegrationCapability::SessionHook,
            status: IntegrationStateStatus::Configured,
            target_path: Some(target),
            message: Some("SessionStart hook 已安装（Grok 不注入 additionalContext，引导以 Skill 为主）".into()),
        },
        Ok(_) => IntegrationState {
            capability: IntegrationCapability::SessionHook,
            status: IntegrationStateStatus::NeedsAction,
            target_path: Some(target),
            message: Some("hook 文件存在但不是 SuperDev 拥有格式".into()),
        },
        Err(_) => IntegrationState {
            capability: IntegrationCapability::SessionHook,
            status: IntegrationStateStatus::Error,
            target_path: Some(target),
            message: Some("无法读取 hook 文件".into()),
        },
    }
}

fn install_hook(ctx: &ConnectorRuntimeContext) -> IntegrationOperationResult {
    let path = hook_path(ctx);
    let skill = skill_path(ctx);
    let target = common::path_string(&path);
    if path.is_file() {
        if let Ok(existing) = std::fs::read_to_string(&path) {
            if !hook_has_marker(&existing) {
                return common::integration_result(
                    IntegrationCapability::SessionHook,
                    IntegrationResult::NeedsAction,
                    Some(target),
                    None,
                    Some("同名 hook 文件存在且非 SuperDev 所有，拒绝覆盖".into()),
                );
            }
            let desired = hook_file_body(&skill);
            if existing.trim() == desired.trim() {
                return common::integration_result(
                    IntegrationCapability::SessionHook,
                    IntegrationResult::AlreadyPresent,
                    Some(target),
                    None,
                    None,
                );
            }
        }
    }
    // Atomic write via common::mutate_config style or mcp_install::atomic_write_file
    match common::mutate_config(CONNECTOR_ID, &path, |_old| {
        Ok(crate::mcp_install::MergeResult {
            content: hook_file_body(&skill),
            changed: true,
        })
    }) {
        Ok(outcome) => common::integration_result(
            IntegrationCapability::SessionHook,
            if outcome.changed {
                IntegrationResult::Installed
            } else {
                IntegrationResult::AlreadyPresent
            },
            Some(target),
            outcome.backup_path,
            Some("SessionStart hook 已写入（引导以 Skill 为主）".into()),
        ),
        Err(error) => common::integration_result(
            IntegrationCapability::SessionHook,
            IntegrationResult::Failed,
            Some(target),
            None,
            Some(error.message().into()),
        ),
    }
}

fn uninstall_hook(ctx: &ConnectorRuntimeContext) -> IntegrationOperationResult {
    let path = hook_path(ctx);
    let target = common::path_string(&path);
    if !path.is_file() {
        return common::integration_result(
            IntegrationCapability::SessionHook,
            IntegrationResult::AlreadyPresent,
            Some(target),
            None,
            None,
        );
    }
    match std::fs::read_to_string(&path) {
        Ok(content) if hook_has_marker(&content) => match std::fs::remove_file(&path) {
            Ok(()) => common::integration_result(
                IntegrationCapability::SessionHook,
                IntegrationResult::Installed, // “changed removed”
                Some(target),
                None,
                None,
            ),
            Err(_) => common::integration_result(
                IntegrationCapability::SessionHook,
                IntegrationResult::Failed,
                Some(target),
                None,
                Some("删除 hook 文件失败".into()),
            ),
        },
        Ok(_) => common::integration_result(
            IntegrationCapability::SessionHook,
            IntegrationResult::NeedsAction,
            Some(target),
            None,
            Some("hook 文件非 SuperDev 所有，未删除".into()),
        ),
        Err(_) => common::integration_result(
            IntegrationCapability::SessionHook,
            IntegrationResult::Failed,
            Some(target),
            None,
            Some("无法读取 hook 文件".into()),
        ),
    }
}
```

**Check `MergeResult` fields** in codebase before coding — if the struct differs, match existing `common::mutate_config` transform signature used by Kimi/OpenCode.

If `mutate_config` is wrong for brand-new JSON files, use `atomic_write_file` + parent `create_dir_all` the same way `common::mutate_config_inner` does — prefer calling `common::mutate_config` if empty old content works.

- [ ] **Step 1: Write tests**

```rust
#[test]
fn install_writes_skill_and_owned_hook_after_mcp() {
    // prepare home, cli, skill source with hooks/run-hook.cmd + SKILL.md
    // FakeCommandRunner: list [] → add ok → list configured
    // install all three capabilities
    // assert skill_path exists with SKILL.md
    // assert hook_path exists and contains HOOK_MARKER
    // status all Configured
}

#[test]
fn uninstall_removes_owned_hook_and_skill_and_mcp() {
    // install first, then uninstall
    // hook gone, skill gone, mcp remove argv present
}

#[test]
fn partial_when_mcp_ok_skill_source_missing() {
    // ctx skill_source None with error message
    // MCP install succeeds via fake runner
    // outcome.result == Partial
    // MCP integration Installed/AlreadyPresent
}

#[test]
fn hook_conflict_does_not_overwrite_foreign_file() {
    // write foreign hook file without marker
    // install_hook → NeedsAction
    // file content unchanged
}
```

- [ ] **Step 2: RED → implement skill/hook in status/install/uninstall pipeline**

Install order: MCP → Skill (`common::install_skill`) → Hook (`install_hook`)  
Uninstall order: Hook → Skill → MCP  

Wire Skill/Hook into `status` integrations vec (replace stubs).

- [ ] **Step 3: Logging**

- Hook write success/fail: `operation = "hook_install"` / `"hook_uninstall"`, no path in fields  
- Skill already logged inside common helpers if any; still log connector-level install finish  

- [ ] **Step 4: Comments**

- Why owned filename is the ownership key  
- Why SessionStart message mentions Skill-first  
- Why foreign file is not deleted  

- [ ] **Step 5: GREEN + commit**

```bash
git add desktop/src-tauri/src/mcp_install/connectors/grok.rs
git commit -m "feat(connectors): install Grok Skill and owned SessionStart hook"
```

---

## Task 5: manual_instructions + polish status/install aggregation

**Files:**
- Modify: `desktop/src-tauri/src/mcp_install/connectors/grok.rs`

- [ ] **Step 1: Implement `manual_instructions`**

```rust
fn manual_instructions(
    &self,
    ctx: &ConnectorRuntimeContext,
) -> Result<ConnectorManualInstructions, ConnectorError> {
    let mcp = ctx.mcp_binary().display().to_string();
    let add = format!(
        "grok mcp add superdev --scope user -e SUPERDEV_AGENT_URL={DEFAULT_AGENT_URL} -- {mcp}"
    );
    Ok(ConnectorManualInstructions {
        summary: "通过 Grok CLI 接入 SuperDev（MCP + Skill + Session Hook）".into(),
        steps: vec![
            "安装 Grok CLI，并确保 grok 在 PATH 中".into(),
            format!("运行: {add}"),
            format!("Skill 目标目录: {}", skill_path(ctx).display()),
            format!(
                "Hook 文件: {} （SessionStart；Grok 不注入 additionalContext，引导以 Skill 为主）",
                hook_path(ctx).display()
            ),
            "重启 Grok 会话，或在 /mcps 中刷新".into(),
            "验证: grok mcp list --json".into(),
        ],
        config_path: Some(common::path_string(&config_path(ctx))),
        manual_config: Some(add),
        verification_prompt: Some(
            "运行 grok mcp list --json，确认 name=superdev、scope=user、command 与 SUPERDEV_AGENT_URL 正确"
                .into(),
        ),
    })
}
```

- [ ] **Step 2: Test manual_instructions includes scope user and skill-first note**

```rust
#[test]
fn manual_instructions_document_user_scope_and_skill_first_hook() {
    let home = test_home();
    let ctx = context_at(home.clone(), vec![]);
    let mi = GrokConnector::new().manual_instructions(&ctx).unwrap();
    let blob = format!("{:?}{:?}", mi.steps, mi.summary);
    assert!(blob.contains("--scope user"));
    assert!(blob.contains("Skill") || blob.contains("skill"));
    assert!(blob.contains("additionalContext") || blob.contains("引导"));
    let _ = std::fs::remove_dir_all(home);
}
```

- [ ] **Step 3: CLI missing install returns NeedsAction/Failed with manual_instructions**

Mirror OpenClaw’s aggregate when `require_cli` fails — Skill/Hook Skipped or not started; attach `manual_instructions`.

- [ ] **Step 4: Logging + comments pass (instrumenting-code checklist)**

Confirm every public trait method has:

| Method | Entry log | Exit log | Error log |
| --- | --- | --- | --- |
| detect | debug | info + detected | — |
| status | debug | info + mcp_status | error on list fail |
| install | info + op | info + result | error on cli/mcp fail |
| uninstall | info | info + result | error on cli fail |
| manual_instructions | — | — | — |

File header + exported `new` / `with_runner` docs already present.

- [ ] **Step 5: Full module test run**

```bash
cd desktop/src-tauri
cargo test mcp_install::connectors::grok -- --nocapture
cargo test mcp_install::connectors::tests::builtin_registers_eight -- --nocapture
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add desktop/src-tauri/src/mcp_install/connectors/grok.rs
git commit -m "feat(connectors): complete Grok manual instructions and error paths"
```

---

## Task 6: Frontend preview fixtures + tests

**Files:**
- Modify: `desktop/src/dev/onboardingPreview.ts`
- Modify: `desktop/src/dev/__tests__/onboardingPreview.test.ts`
- Modify: `desktop/src/components/Settings/__tests__/McpManagerTab.test.ts` (if count hardcodes 7)

- [ ] **Step 1: Update onboardingPreview**

In `PREVIEW_CONNECTORS` append:

```ts
{ id: 'grok', name: 'Grok', supportLevel: 'full', hookSupport: 'automatic', detected: true, detectionPath: '/usr/local/bin/grok' },
```

Update file header comments “七个” → “八个”.  
Update `previewConnectorSummaries` comments accordingly.

Pick detection mix: e.g. Grok detected=true so Full+detected path is covered (Hermes already detected Full).

- [ ] **Step 2: Update onboardingPreview.test.ts**

```ts
it('exposes eight production connectors with derived support levels and mixed detection', () => {
  const summaries = previewConnectorSummaries()
  expect(summaries).toHaveLength(8)
  // ... include ['grok', 'full', true]
})
```

- [ ] **Step 3: Update McpManagerTab test “seven production connectors”**

Add `summary('grok', true, false)` with Full-level descriptor fields if the test builds descriptors manually; expect length 8 and support level `full` for grok.

- [ ] **Step 4: Run frontend tests**

```bash
cd desktop
pnpm exec vitest run src/dev/__tests__/onboardingPreview.test.ts src/components/Settings/__tests__/McpManagerTab.test.ts
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add desktop/src/dev/onboardingPreview.ts \
  desktop/src/dev/__tests__/onboardingPreview.test.ts \
  desktop/src/components/Settings/__tests__/McpManagerTab.test.ts
git commit -m "test(desktop): include Grok in onboarding and MCP manager fixtures"
```

---

## Task 7: README product copy

**Files:**
- Modify: `README.md`
- Modify: `README.zh-CN.md`

- [ ] **Step 1: English README**

Replace the seven-connector bullet with:

```markdown
- Choose any detected built-in Connector on first launch. Eight verified built-ins are available: **Claude Code**, **Codex**, **Cursor**, **OpenCode**, **OpenClaw**, **Hermes**, **Kimi Code**, and **Grok**.
  - **Full** support (Claude Code, Codex, Cursor, Hermes, Grok): automatic MCP + Skill + Session Hook.
  - **Standard** support (OpenCode, OpenClaw, Kimi Code): automatic MCP + Skill, with a manual Session Hook step.
```

Optional footnote only if room: Grok SessionStart does not inject additionalContext; SuperDev guidance is Skill-first. Prefer keeping README short — detail stays in Settings messages / manual_instructions.

- [ ] **Step 2: Chinese README**

Align with current EN capability narrative (Wave 2 already outdated in zh). Example:

```markdown
- 首次打开引导页选择检测到的内置 Connector。目前已验证八个内置：Claude Code、Codex、Cursor、OpenCode、OpenClaw、Hermes、Kimi Code、Grok。
  - **完整集成**（Claude Code、Codex、Cursor、Hermes、Grok）：自动 MCP + Skill + Session Hook。
  - **标准集成**（OpenCode、OpenClaw、Kimi Code）：自动 MCP + Skill，Session Hook 需手动。
  - 其他支持本地 stdio MCP 的 Agent 可按标准指引手动接入。
```

- [ ] **Step 3: Commit**

```bash
git add README.md README.zh-CN.md
git commit -m "docs: list Grok as Full built-in Agent Connector"
```

---

## Task 8: Final verification

- [ ] **Step 1: Rust connector tests**

```bash
cd desktop/src-tauri
cargo test mcp_install::connectors -- --nocapture
```

Expected: PASS (or only pre-existing unrelated failures — if any fail, fix Grok-related only).

- [ ] **Step 2: Frontend targeted tests**

```bash
cd desktop
pnpm exec vitest run src/dev/__tests__/onboardingPreview.test.ts src/components/Settings/__tests__/McpManagerTab.test.ts
```

- [ ] **Step 3: instrumenting-code self-check**

- [ ] Error branches log with `error_code`  
- [ ] External CLI calls log failure without secrets  
- [ ] Success paths log finish + result  
- [ ] No `println!` / `dbg!` as logging  
- [ ] File header + exported method docs  
- [ ] Inline why comments on scope/marker/passive SessionStart  

- [ ] **Step 4: Spec acceptance checklist**

Map each item in design §Acceptance criteria to evidence (test name or README line).

- [ ] **Step 5: Final commit only if dirty**

```bash
git status
# if clean, done; else commit remaining fixes
```

---

## Spec coverage (self-review)

| Spec requirement | Task |
| --- | --- |
| Built-in `grok` Full in Registry | 1 |
| Detect CLI or `~/.grok` | 2 |
| MCP via `grok mcp add/list/remove`, `--scope user` | 3 |
| Match command + URL + enabled + user scope | 3 |
| No doctor for verify | 3 (status uses list only) |
| Skill under `~/.grok/skills/superdev` | 4 |
| Owned hook file + marker | 4 |
| Install MCP→Skill→Hook; uninstall reverse | 4 |
| Partial when skill fails after MCP | 4 |
| cli_not_found + manual_instructions | 3, 5 |
| Honest SessionStart / Skill-first copy | 4, 5, 7 |
| Secret-safe logs | 3–5, 8 |
| Onboarding/Settings no hardcoded enum | 6 (fixtures only; runtime still Registry) |
| README EN/ZH | 7 |
| No AgentKind / no TOML MCP edit / no env override | File Map + all tasks |

## Placeholder scan

No TBD/TODO left in tasks. `MergeResult` field names must be verified against source at implementation time — if different, adapt to existing struct (not a product TBD).

## Type consistency

- Connector id: `"grok"`  
- Display: `"Grok"`  
- MCP server name: `"superdev"`  
- Hook file: `superdev-session-start.json`  
- Marker: `skills/superdev/hooks/run-hook.cmd`  
- Errors: `cli_not_found`, `cli_add_failed`, `cli_list_failed`, `invalid_cli_output`, `cli_remove_failed`, `hook_owned_conflict` (map write failures to Failed + message)

---

## Execution Handoff

Plan complete and saved to:

- `docs/2026-07-12-grok-cli-connector-plan.md` (tracked)
- `docs/superpowers/plans/2026-07-12-grok-cli-connector-plan.md` (local gitignored copy)

**Two execution options:**

1. **Subagent-Driven (recommended)** — fresh subagent per task, review between tasks  
2. **Inline Execution** — execute in this session with executing-plans checkpoints  

Which approach?
