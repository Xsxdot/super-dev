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
//
// 注意：
//   - MCP 经官方 CLI 证明就绪；Skill 走 common 文件系统原语
//   - Hook 使用独立 owned 文件（文件名即所有权键），不与用户其它 hook 配置合并
//   - SessionStart 在 Grok 上 stdout 被忽略，产品引导以 Skill 为主

use super::common;
use super::process::{CommandOutput, CommandRunner, CommandSpec, SystemCommandRunner};
use crate::mcp_install::contracts::*;
use crate::mcp_install::registry::*;
use crate::mcp_install::{executable_file_names, MergeResult};
// DEFAULT_AGENT_URL 只剩测试在用：生产路径已全部改走 ctx.mcp_launch()。
#[cfg(test)]
use crate::mcp_install::DEFAULT_AGENT_URL;
use std::ffi::OsString;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::{Duration, Instant};

const CONNECTOR_ID: &str = "grok";
const DISPLAY_NAME: &str = "Grok";
/// CLI_COMMAND 是 resolve_cli 探测的命令名，同时也是 cli_commands() 对外汇报的
/// 值——两处共用同一个常量，避免各写一份字符串导致命令名漂移。
const CLI_COMMAND: &str = "grok";
/// CLI 调用超时（add/list/remove 均受此上限约束）。
const CLI_TIMEOUT: Duration = Duration::from_secs(30);
/// SuperDev 拥有的 SessionStart hook 命令标记子串（幂等识别与安全卸载锚点）。
const HOOK_MARKER: &str = "skills/superdev/hooks/run-hook.cmd";
/// 固定 owned 文件名：整文件归 SuperDev 管理，避免与用户其它 hook 配置混写。
const HOOK_FILE_NAME: &str = "superdev-session-start.json";

/// GrokConnector 适配 Grok CLI 的 MCP/Skill/Session Hook。
pub(super) struct GrokConnector {
    descriptor: AgentConnectorDescriptor,
    /// runner 执行 `grok mcp`；生产为系统进程，测试可注入 Fake。
    runner: Arc<dyn CommandRunner>,
}

impl GrokConnector {
    /// new 使用系统进程执行器创建 Full 级 Grok 连接器。
    pub(super) fn new() -> Self {
        Self::with_runner(Arc::new(SystemCommandRunner))
    }

    /// with_runner 注入可测试的 CommandRunner（仅测试构造）。
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

/// data_root 返回 Grok 用户数据根目录 `~/.grok`。
fn data_root(ctx: &ConnectorRuntimeContext) -> PathBuf {
    ctx.home_dir().join(".grok")
}

/// config_path 返回 Grok 配置文件路径（MCP 段由 CLI 管理，不直接改写）。
fn config_path(ctx: &ConnectorRuntimeContext) -> PathBuf {
    data_root(ctx).join("config.toml")
}

/// skill_path 返回 SuperDev owned Skill 目录。
fn skill_path(ctx: &ConnectorRuntimeContext) -> PathBuf {
    data_root(ctx).join("skills").join("superdev")
}

/// hook_path 返回 SuperDev 拥有的 SessionStart hook 文件路径。
fn hook_path(ctx: &ConnectorRuntimeContext) -> PathBuf {
    data_root(ctx).join("hooks").join(HOOK_FILE_NAME)
}

/// resolve_cli 在 command_dirs 中查找 grok / grok.exe。
fn resolve_cli(ctx: &ConnectorRuntimeContext) -> Option<PathBuf> {
    ctx.command_dirs().iter().find_map(|directory| {
        executable_file_names(CLI_COMMAND)
            .into_iter()
            .map(|name| directory.join(name))
            .find(|path| path.is_file())
    })
}

/// require_cli 要求 status/install/uninstall 的 MCP 路径必须能解析到 grok CLI。
fn require_cli(ctx: &ConnectorRuntimeContext) -> Result<PathBuf, ConnectorError> {
    resolve_cli(ctx).ok_or_else(|| {
        ConnectorError::new(
            "cli_not_found",
            "未找到 grok CLI，无法通过官方命令管理 MCP",
        )
    })
}

/// run_cli 以 argv 方式调用 grok，超时 30s；不注入 HOME，不经 shell。
fn run_cli(
    runner: &dyn CommandRunner,
    program: &Path,
    args: &[&str],
) -> Result<CommandOutput, ConnectorError> {
    let spec = CommandSpec::new(program, args.iter().map(|arg| OsString::from(*arg)))
        .with_timeout(CLI_TIMEOUT);
    runner.run(spec)
}

/// MappedCliError 保存稳定对外错误码与底层 process 错误码（供日志保留类别）。
struct MappedCliError {
    error: ConnectorError,
    /// underlying_code 是 process 层原始码（command_timeout 等），永不写入用户 message。
    underlying_code: String,
}

/// map_process_error 将底层 CommandRunner 错误映射为设计规定的稳定 CLI 错误码。
///
/// 为何映射：process 层返回 command_timeout / command_spawn_failed 等通用码，
/// Connector 对外契约要求 cli_add_failed / cli_list_failed / cli_remove_failed。
/// 调用方必须在日志中同时写 `error_code`（稳定）与 `underlying_code`。
fn map_process_error(error: ConnectorError, stable_code: &str) -> MappedCliError {
    let underlying_code = error.code().to_string();
    let mapped = match error.code() {
        "command_timeout"
        | "command_spawn_failed"
        | "command_output_failed"
        | "command_wait_failed" => ConnectorError::new(stable_code, error.message()),
        code if code == stable_code => error,
        _ => error,
    };
    MappedCliError {
        error: mapped,
        underlying_code,
    }
}

/// shell_quote_posix 用 POSIX 单引号包裹路径（bash/zsh/sh）。
fn shell_quote_posix(value: &str) -> String {
    let mut out = String::with_capacity(value.len() + 2);
    out.push('\'');
    for ch in value.chars() {
        if ch == '\'' {
            out.push_str("'\\''");
        } else {
            out.push(ch);
        }
    }
    out.push('\'');
    out
}

/// shell_quote_powershell 用 PowerShell 单引号字面量（`'` → `''`）。
fn shell_quote_powershell(value: &str) -> String {
    format!("'{}'", value.replace('\'', "''"))
}

/// shell_quote_cmd 用 cmd.exe 双引号字面量（`"` → `""`）。
fn shell_quote_cmd(value: &str) -> String {
    format!("\"{}\"", value.replace('"', "\"\""))
}

/// shell_quote 返回当前宿主默认 shell 的安全路径字面量（manual_instructions 用）。
///
/// 真实 install 走 argv，不经 shell。Windows 默认给出 PowerShell 形态；
/// manual_instructions 会同时附带 cmd.exe 形态以免 cmd 用户粘贴失败。
fn shell_quote(value: &str) -> String {
    if cfg!(windows) {
        shell_quote_powershell(value)
    } else {
        shell_quote_posix(value)
    }
}

/// aggregate_map_err 保留聚合失败的底层上下文，避免 `|_` 吞掉原因。
fn aggregate_map_err(error: impl std::fmt::Display) -> ConnectorError {
    tracing::error!(
        connector_id = CONNECTOR_ID,
        error_code = "aggregate_failed",
        underlying = %error,
        "connector result aggregate failed"
    );
    ConnectorError::new("aggregate_failed", format!("结果聚合失败: {error}"))
}

/// install_skill_mapped 在 common::install_skill 之上补稳定错误码日志。
fn install_skill_mapped(
    ctx: &ConnectorRuntimeContext,
    skill: &Path,
) -> IntegrationOperationResult {
    if ctx.skill_source().is_none() {
        tracing::error!(
            connector_id = CONNECTOR_ID,
            operation = "skill_install",
            error_code = "skill_source_missing",
            "grok skill source missing"
        );
        return common::integration_result(
            IntegrationCapability::Skill,
            IntegrationResult::Failed,
            Some(common::path_string(skill)),
            None,
            Some(
                ctx.skill_source_error()
                    .unwrap_or("SuperDev Skill 源不可用")
                    .to_string(),
            ),
        );
    }
    let result = common::install_skill(ctx, skill);
    if matches!(result.result, IntegrationResult::Failed) {
        tracing::error!(
            connector_id = CONNECTOR_ID,
            operation = "skill_install",
            error_code = "skill_install_failed",
            "grok skill install failed"
        );
    }
    result
}

/// McpListEntry 是 `grok mcp list --json` 单条记录的只读 DTO。
///
/// 从 serde_json::Value 一次性解析字段，避免调用点散落字符串键。
#[derive(Clone, Debug)]
struct McpListEntry {
    name: Option<String>,
    scope: Option<String>,
    command: Option<String>,
    /// args 是 `grok mcp list --json` 里 command 之后的启动参数；缺失视为空。
    ///
    /// 本机 args 恒为空，所以这个字段在本机场景下永远是空 Vec，比较结果与改造前
    /// 完全一致；远端场景下它是「目标机 agent 有没有真的带上 `mcp` 子命令」的唯一
    /// 判据，不比对就会把一条不可用的配置报成"已配置"。
    args: Vec<String>,
    agent_url: Option<String>,
    enabled: bool,
}

impl McpListEntry {
    /// from_value 解析 list 数组中的一个对象；缺省 enabled=true。
    fn from_value(value: &serde_json::Value) -> Self {
        Self {
            name: value
                .get("name")
                .and_then(|v| v.as_str())
                .map(str::to_string),
            scope: value
                .get("scope")
                .and_then(|v| v.as_str())
                .map(str::to_string),
            command: value
                .get("command")
                .and_then(|v| v.as_str())
                .map(str::to_string),
            args: value
                .get("args")
                .and_then(|v| v.as_array())
                .map(|items| {
                    items
                        .iter()
                        .filter_map(|item| item.as_str().map(str::to_string))
                        .collect()
                })
                .unwrap_or_default(),
            agent_url: value
                .get("env")
                .and_then(|env| env.get("SUPERDEV_AGENT_URL"))
                .and_then(|v| v.as_str())
                .map(str::to_string),
            enabled: value
                .get("enabled")
                .and_then(|v| v.as_bool())
                .unwrap_or(true),
        }
    }

    fn is_superdev_user(&self) -> bool {
        self.name.as_deref() == Some("superdev") && self.scope.as_deref() == Some("user")
    }

    fn is_named_superdev(&self) -> bool {
        self.name.as_deref() == Some("superdev")
    }

    fn matches_expected(&self, ctx: &ConnectorRuntimeContext) -> bool {
        let launch = ctx.mcp_launch();
        let expected = launch.command.to_string_lossy();
        self.command.as_deref() == Some(expected.as_ref())
            && self.args == launch.args
            && self.agent_url.as_deref() == Some(launch.agent_url.as_str())
            && self.enabled
    }
}

/// log_op_finish 在 install/uninstall 结束时统一写 finished/failed 日志。
///
/// Ok(Failed/Partial/NeedsAction) 走 error 级，避免失败结果被伪装成 info 成功收尾。
fn log_op_finish(
    operation: &str,
    started: Instant,
    outcome: &Result<ConnectorOperationOutcome, ConnectorError>,
) {
    let duration_ms = started.elapsed().as_millis() as u64;
    match outcome {
        Ok(result) => {
            let is_failure = matches!(
                result.result,
                ConnectorResult::Failed | ConnectorResult::Partial | ConnectorResult::NeedsAction
            );
            if is_failure {
                tracing::error!(
                    connector_id = CONNECTOR_ID,
                    operation,
                    result = ?result.result,
                    duration_ms,
                    "grok operation finished with failure result"
                );
            } else {
                tracing::info!(
                    connector_id = CONNECTOR_ID,
                    operation,
                    result = ?result.result,
                    duration_ms,
                    "grok operation finished"
                );
            }
        }
        Err(error) => {
            tracing::error!(
                connector_id = CONNECTOR_ID,
                operation,
                error_code = error.code(),
                duration_ms,
                "grok operation failed"
            );
        }
    }
}

/// mcp_blocked_outcome 在 MCP 失败时聚合 Failed MCP + Skipped Skill/Hook（消除重复骨架）。
fn mcp_blocked_outcome(
    ctx: &ConnectorRuntimeContext,
    operation: ConnectorOperation,
    mcp_message: &str,
    summary: &str,
    manual: Option<ConnectorManualInstructions>,
) -> Result<ConnectorOperationOutcome, ConnectorError> {
    let skill = skill_path(ctx);
    aggregate_connector_result(
        CONNECTOR_ID.into(),
        operation,
        vec![
            common::integration_result(
                IntegrationCapability::Mcp,
                IntegrationResult::Failed,
                Some(common::path_string(&config_path(ctx))),
                None,
                Some(mcp_message.into()),
            ),
            common::integration_result(
                IntegrationCapability::Skill,
                IntegrationResult::Skipped,
                Some(common::path_string(&skill)),
                None,
                Some("MCP 失败，已跳过 Skill".into()),
            ),
            hook_skipped_for_mcp(ctx, "MCP 失败，已跳过 Hook"),
        ],
        manual,
        false,
        Some(summary.into()),
    )
    .map_err(aggregate_map_err)
}

/// list_servers 调用 `grok mcp list --json`。
///
/// 使用 list 而非 doctor：doctor 面向连通性诊断，status/verify 只需配置证明。
/// 根节点必须是数组；空 stdout 视为无条目。
fn list_servers(
    runner: &dyn CommandRunner,
    program: &Path,
) -> Result<Vec<McpListEntry>, ConnectorError> {
    let output = run_cli(runner, program, &["mcp", "list", "--json"])?;
    if !output.success() {
        return Err(ConnectorError::new("cli_list_failed", "grok mcp list 失败"));
    }
    let text = output.stdout.trim();
    if text.is_empty() {
        return Ok(Vec::new());
    }
    let value: serde_json::Value = serde_json::from_str(text).map_err(|error| {
        ConnectorError::new(
            "invalid_cli_output",
            format!("grok mcp list --json 输出无法解析: {error}"),
        )
    })?;
    match value {
        serde_json::Value::Array(items) => Ok(items.iter().map(McpListEntry::from_value).collect()),
        _ => Err(ConnectorError::new(
            "invalid_cli_output",
            "grok mcp list --json 根节点必须是数组",
        )),
    }
}

/// mcp_add_args 构造 `grok mcp add` 的完整 argv（不含程序名）。
///
/// 参数：
///   - ctx: 运行上下文，MCP 启动规格取自 `ctx.mcp_launch()`
///
/// 返回：
///   - `["mcp", "add", "superdev", "--scope", "user", "-e", "SUPERDEV_AGENT_URL=…", "--", 命令, args…]`
///
/// 注意：
///   - `--` 之后是「命令 + 启动规格 args」：本机 args 恒为空，因此只有裸二进制一项，
///     与改造前逐字节相同；远端场景会多出 `mcp` 一项，否则目标机上的 `superdev-agent`
///     不会进入 MCP 模式，用户只会看到"安装成功"却连不上（静默错误）。
fn mcp_add_args(ctx: &ConnectorRuntimeContext) -> Vec<String> {
    let launch = ctx.mcp_launch();
    let mut args = vec![
        "mcp".to_string(),
        "add".to_string(),
        "superdev".to_string(),
        "--scope".to_string(),
        "user".to_string(),
        "-e".to_string(),
        format!("SUPERDEV_AGENT_URL={}", launch.agent_url),
        "--".to_string(),
        launch.command.to_string_lossy().into_owned(),
    ];
    args.extend(launch.args.iter().cloned());
    args
}

/// find_superdev_user_entry 仅匹配 user-scope 的 superdev 条目。
///
/// 始终使用 --scope user：项目级配置不属于 SuperDev 自动管理范围。
fn find_superdev_user_entry(items: &[McpListEntry]) -> Option<&McpListEntry> {
    items.iter().find(|item| item.is_superdev_user())
}

/// entry_matches 判断 list 条目是否与期望的 SuperDev MCP 完全一致。
fn entry_matches(ctx: &ConnectorRuntimeContext, entry: &McpListEntry) -> bool {
    entry.matches_expected(ctx)
}

/// configured_list_json 构造匹配期望的 list --json 夹具（测试与假 runner 复用）。
#[cfg(test)]
fn configured_list_json(ctx: &ConnectorRuntimeContext) -> String {
    let launch = ctx.mcp_launch();
    serde_json::json!([{
        "name": "superdev",
        "command": launch.command.to_string_lossy(),
        "args": launch.args,
        "env": { "SUPERDEV_AGENT_URL": launch.agent_url },
        "enabled": true,
        "scope": "user"
    }])
    .to_string()
}

/// hook_command 生成绝对路径的 run-hook 调用；统一正斜杠以便 HOOK_MARKER 跨平台匹配。
fn hook_command(skill_dir: &Path) -> String {
    let runner = skill_dir.join("hooks").join("run-hook.cmd");
    let runner = runner.to_string_lossy().replace('\\', "/");
    format!("\"{runner}\" session-start")
}

/// hook_file_body 构造 SuperDev 拥有的 SessionStart hook JSON 正文。
///
/// 使用独立文件而非合并进用户配置，是为了整文件所有权清晰：
/// 安装/卸载只动这一个文件，不会误改用户其它 hooks。
fn hook_file_body(skill_dir: &Path) -> String {
    let command = hook_command(skill_dir);
    // command 路径含 skills/superdev/hooks/run-hook.cmd，作为所有权标记。
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

/// hook_has_marker 判断文件内容是否为 SuperDev 拥有的 SessionStart hook。
///
/// 同时要求 HOOK_MARKER 与 session-start，避免误匹配其它 run-hook 用途。
fn hook_has_marker(content: &str) -> bool {
    content.contains(HOOK_MARKER) && content.contains("session-start")
}

/// hook_status 只读检查 owned SessionStart hook 文件状态。
///
/// 消息刻意写明「引导以 Skill 为主」：Grok 的 SessionStart 是 passive hook，
/// stdout 不会注入 additionalContext，与 Claude/Codex/Cursor 行为不同。
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
            message: Some(
                "SessionStart hook 已安装（Grok 不注入 additionalContext，引导以 Skill 为主）"
                    .into(),
            ),
        },
        Ok(_) => {
            tracing::error!(
                connector_id = CONNECTOR_ID,
                operation = "hook_status",
                error_code = "hook_owned_conflict",
                "grok session hook present but not SuperDev-owned"
            );
            IntegrationState {
                capability: IntegrationCapability::SessionHook,
                status: IntegrationStateStatus::NeedsAction,
                target_path: Some(target),
                message: Some("hook 文件存在但不是 SuperDev 拥有格式".into()),
            }
        }
        Err(error) => {
            tracing::error!(
                connector_id = CONNECTOR_ID,
                operation = "hook_status",
                error_code = "hook_read_failed",
                error_kind = ?error.kind(),
                "grok session hook read failed"
            );
            IntegrationState {
                capability: IntegrationCapability::SessionHook,
                status: IntegrationStateStatus::Error,
                target_path: Some(target),
                message: Some("无法读取 hook 文件".into()),
            }
        }
    }
}

/// install_hook 写入或更新 SuperDev 拥有的 SessionStart hook 文件。
///
/// 同名文件若无标记则拒绝覆盖：用户可能手动放了同名配置，误删会造成数据丢失。
fn install_hook(ctx: &ConnectorRuntimeContext) -> IntegrationOperationResult {
    let path = hook_path(ctx);
    let skill = skill_path(ctx);
    let target = common::path_string(&path);
    if path.is_file() {
        match std::fs::read_to_string(&path) {
            Ok(existing) if !hook_has_marker(&existing) => {
                tracing::info!(
                    connector_id = CONNECTOR_ID,
                    operation = "hook_install",
                    error_code = "hook_owned_conflict",
                    "foreign hook file present; refusing overwrite"
                );
                return common::integration_result(
                    IntegrationCapability::SessionHook,
                    IntegrationResult::NeedsAction,
                    Some(target),
                    None,
                    Some("同名 hook 文件存在且非 SuperDev 所有，拒绝覆盖".into()),
                );
            }
            Ok(existing) => {
                let desired = hook_file_body(&skill);
                if existing.trim() == desired.trim() {
                    return common::integration_result(
                        IntegrationCapability::SessionHook,
                        IntegrationResult::AlreadyPresent,
                        Some(target),
                        None,
                        Some("SessionStart hook 已存在且内容匹配".into()),
                    );
                }
            }
            Err(error) => {
                tracing::error!(
                    connector_id = CONNECTOR_ID,
                    operation = "hook_install",
                    error_code = "hook_read_failed",
                    error_kind = ?error.kind(),
                    "grok session hook read failed before write"
                );
                return common::integration_result(
                    IntegrationCapability::SessionHook,
                    IntegrationResult::Failed,
                    Some(target),
                    None,
                    Some("无法读取已有 hook 文件".into()),
                );
            }
        }
    }
    match common::mutate_config(CONNECTOR_ID, &path, |_old| {
        Ok(MergeResult {
            content: hook_file_body(&skill),
            changed: true,
        })
    }) {
        Ok(outcome) => {
            tracing::info!(
                connector_id = CONNECTOR_ID,
                operation = "hook_install",
                changed = outcome.changed,
                "grok session hook write finished"
            );
            common::integration_result(
                IntegrationCapability::SessionHook,
                if outcome.changed {
                    IntegrationResult::Installed
                } else {
                    IntegrationResult::AlreadyPresent
                },
                Some(target),
                outcome.backup_path,
                Some("SessionStart hook 已写入（引导以 Skill 为主）".into()),
            )
        }
        Err(error) => {
            // 对外稳定码：设计表中的 hook_write_failed（保留底层 code 于日志对照）。
            tracing::error!(
                connector_id = CONNECTOR_ID,
                operation = "hook_install",
                error_code = "hook_write_failed",
                underlying_code = error.code(),
                "grok session hook write failed"
            );
            common::integration_result(
                IntegrationCapability::SessionHook,
                IntegrationResult::Failed,
                Some(target),
                None,
                Some(error.message().into()),
            )
        }
    }
}

/// uninstall_hook 仅在标记匹配时删除 owned hook 文件；外源文件不删除。
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
            Ok(()) => {
                tracing::info!(
                    connector_id = CONNECTOR_ID,
                    operation = "hook_uninstall",
                    "grok session hook removed"
                );
                // Installed 在卸载语义中表示「发生了删除变更」。
                common::integration_result(
                    IntegrationCapability::SessionHook,
                    IntegrationResult::Installed,
                    Some(target),
                    None,
                    None,
                )
            }
            Err(error) => {
                tracing::error!(
                    connector_id = CONNECTOR_ID,
                    operation = "hook_uninstall",
                    error_code = "hook_write_failed",
                    error_kind = ?error.kind(),
                    "grok session hook remove failed"
                );
                common::integration_result(
                    IntegrationCapability::SessionHook,
                    IntegrationResult::Failed,
                    Some(target),
                    None,
                    Some("删除 hook 文件失败".into()),
                )
            }
        },
        Ok(_) => {
            // 外源同名文件：不删除，避免破坏用户配置。
            tracing::info!(
                connector_id = CONNECTOR_ID,
                operation = "hook_uninstall",
                error_code = "hook_owned_conflict",
                "foreign hook file left untouched"
            );
            common::integration_result(
                IntegrationCapability::SessionHook,
                IntegrationResult::NeedsAction,
                Some(target),
                None,
                Some("hook 文件非 SuperDev 所有，未删除".into()),
            )
        }
        Err(error) => {
            tracing::error!(
                connector_id = CONNECTOR_ID,
                operation = "hook_uninstall",
                error_code = "hook_read_failed",
                error_kind = ?error.kind(),
                "grok session hook read failed during uninstall"
            );
            common::integration_result(
                IntegrationCapability::SessionHook,
                IntegrationResult::Failed,
                Some(target),
                None,
                Some("无法读取 hook 文件".into()),
            )
        }
    }
}

/// hook_skipped_for_mcp 在 MCP 未就绪时跳过 Hook，避免宣称已处理。
fn hook_skipped_for_mcp(ctx: &ConnectorRuntimeContext, reason: &str) -> IntegrationOperationResult {
    common::integration_result(
        IntegrationCapability::SessionHook,
        IntegrationResult::Skipped,
        Some(common::path_string(&hook_path(ctx))),
        None,
        Some(reason.into()),
    )
}

/// grok_install_body 是 install 的可 `?` 内层实现（模块内 free fn，便于外层统一收尾日志）。
fn grok_install_body(
    connector: &GrokConnector,
    ctx: &ConnectorRuntimeContext,
    request: ConnectorInstallRequest,
) -> Result<ConnectorOperationOutcome, ConnectorError> {
        let skill = skill_path(ctx);
        let program = match require_cli(ctx) {
            Ok(program) => program,
            Err(error) => {
                tracing::error!(
                    connector_id = CONNECTOR_ID,
                    operation = ?request.operation,
                    error_code = error.code(),
                    "grok cli missing"
                );
                return mcp_blocked_outcome(
                    ctx,
                    request.operation,
                    error.message(),
                    "Grok CLI 不可用",
                    Some(connector.manual_instructions(ctx)?),
                );
            }
        };

        let (mcp_result, mcp_message) =
            if request.capabilities.contains(&IntegrationCapability::Mcp) {
                // list → 按需 add（始终 --scope user）→ list 复核。从不直接写 config.toml。
                let already = match list_servers(connector.runner.as_ref(), &program) {
                    Ok(items) => match find_superdev_user_entry(&items) {
                        Some(value) if entry_matches(ctx, value) => true,
                        _ => false,
                    },
                    Err(error) if error.code() == "invalid_cli_output" => {
                        tracing::error!(
                            connector_id = CONNECTOR_ID,
                            operation = "mcp_list",
                            error_code = "invalid_cli_output",
                            "grok install list output invalid"
                        );
                        return mcp_blocked_outcome(
                            ctx,
                            request.operation,
                            error.message(),
                            "Grok MCP 状态输出无效",
                            Some(connector.manual_instructions(ctx)?),
                        );
                    }
                    Err(error) => {
                        let mapped = map_process_error(error, "cli_list_failed");
                        tracing::error!(
                            connector_id = CONNECTOR_ID,
                            operation = "mcp_list",
                            error_code = mapped.error.code(),
                            underlying_code = mapped.underlying_code.as_str(),
                            "grok install list failed"
                        );
                        return mcp_blocked_outcome(
                            ctx,
                            request.operation,
                            "grok mcp list 失败",
                            "Grok MCP 状态读取失败",
                            Some(connector.manual_instructions(ctx)?),
                        );
                    }
                };

                if !already {
                    let add_args = mcp_add_args(ctx);
                    let add_args: Vec<&str> = add_args.iter().map(String::as_str).collect();
                    let add_output = match run_cli(connector.runner.as_ref(), &program, &add_args) {
                        Ok(output) => output,
                        Err(error) => {
                            let mapped = map_process_error(error, "cli_add_failed");
                            tracing::error!(
                                connector_id = CONNECTOR_ID,
                                operation = "mcp_add",
                                error_code = mapped.error.code(),
                                underlying_code = mapped.underlying_code.as_str(),
                                "grok mcp add failed"
                            );
                            return mcp_blocked_outcome(
                                ctx,
                                request.operation,
                                "grok mcp add 失败",
                                "Grok MCP 配置失败",
                                Some(connector.manual_instructions(ctx)?),
                            );
                        }
                    };
                    if !add_output.success() {
                        tracing::error!(
                            connector_id = CONNECTOR_ID,
                            operation = "mcp_add",
                            error_code = "cli_add_failed",
                            status_code = add_output.status_code,
                            truncated = add_output.truncated,
                            "grok mcp add failed"
                        );
                        return mcp_blocked_outcome(
                            ctx,
                            request.operation,
                            "grok mcp add 返回非零退出码",
                            "Grok MCP 配置失败",
                            Some(connector.manual_instructions(ctx)?),
                        );
                    }
                }

                match list_servers(connector.runner.as_ref(), &program) {
                    Ok(items) => match find_superdev_user_entry(&items) {
                        Some(value) if entry_matches(ctx, value) => (
                            if already {
                                IntegrationResult::AlreadyPresent
                            } else {
                                IntegrationResult::Installed
                            },
                            Some("MCP 已通过 grok CLI 配置".into()),
                        ),
                        _ => {
                            tracing::error!(
                                connector_id = CONNECTOR_ID,
                                operation = "mcp_add",
                                error_code = "cli_add_failed",
                                "add succeeded but list verify did not show matching user-scope superdev"
                            );
                            return mcp_blocked_outcome(
                                ctx,
                                request.operation,
                                "add 后 list 未能证明 user-scope superdev 已配置",
                                "Grok MCP 复核失败",
                                Some(connector.manual_instructions(ctx)?),
                            );
                        }
                    },
                    Err(error) => {
                        let mapped = map_process_error(error, "cli_list_failed");
                        tracing::error!(
                            connector_id = CONNECTOR_ID,
                            operation = "mcp_list",
                            error_code = mapped.error.code(),
                            underlying_code = mapped.underlying_code.as_str(),
                            "grok install verify list failed"
                        );
                        return mcp_blocked_outcome(
                            ctx,
                            request.operation,
                            "add 后 list 复核失败",
                            "Grok MCP 复核失败",
                            Some(connector.manual_instructions(ctx)?),
                        );
                    }
                }
            } else {
                let status = connector.status(ctx)?;
                let mcp = status
                    .integrations
                    .iter()
                    .find(|item| item.capability == IntegrationCapability::Mcp)
                    .expect("mcp");
                (
                    match mcp.status {
                        IntegrationStateStatus::Configured => IntegrationResult::AlreadyPresent,
                        _ => IntegrationResult::NeedsAction,
                    },
                    mcp.message.clone(),
                )
            };

        // 安装顺序固定为 MCP → Skill → Hook：Hook 命令依赖 Skill 内 run-hook.cmd。
        let mcp_ready = matches!(
            mcp_result,
            IntegrationResult::Installed | IntegrationResult::AlreadyPresent
        );
        let skill_result = if !mcp_ready {
            common::integration_result(
                IntegrationCapability::Skill,
                IntegrationResult::Skipped,
                Some(common::path_string(&skill)),
                None,
                Some("MCP 未就绪，已跳过 Skill".into()),
            )
        } else if request.capabilities.contains(&IntegrationCapability::Skill) {
            install_skill_mapped(ctx, &skill)
        } else {
            let skill_state = common::skill_status(ctx, &skill);
            common::integration_result(
                IntegrationCapability::Skill,
                match skill_state.status {
                    IntegrationStateStatus::Configured => IntegrationResult::AlreadyPresent,
                    _ => IntegrationResult::Skipped,
                },
                skill_state.target_path,
                None,
                skill_state.message,
            )
        };
        let skill_ready = matches!(
            skill_result.result,
            IntegrationResult::Installed | IntegrationResult::AlreadyPresent
        );

        let hook_result = if !mcp_ready {
            hook_skipped_for_mcp(ctx, "MCP 未就绪，已跳过 Hook")
        } else if request.capabilities.contains(&IntegrationCapability::SessionHook)
            && !skill_ready
        {
            // Hook 指向 Skill 内脚本；Skill 失败时不写活跃 hook，避免悬空命令。
            common::integration_result(
                IntegrationCapability::SessionHook,
                IntegrationResult::Skipped,
                Some(common::path_string(&hook_path(ctx))),
                None,
                Some("Skill 未就绪，已跳过 Hook".into()),
            )
        } else if request.capabilities.contains(&IntegrationCapability::SessionHook) {
            install_hook(ctx)
        } else {
            let hook_state = hook_status(ctx);
            common::integration_result(
                IntegrationCapability::SessionHook,
                match hook_state.status {
                    IntegrationStateStatus::Configured => IntegrationResult::AlreadyPresent,
                    IntegrationStateStatus::NeedsAction => IntegrationResult::NeedsAction,
                    IntegrationStateStatus::Missing => IntegrationResult::Skipped,
                    _ => IntegrationResult::Failed,
                },
                hook_state.target_path,
                None,
                hook_state.message,
            )
        };

        aggregate_connector_result(
            CONNECTOR_ID.into(),
            request.operation,
            vec![
                common::integration_result(
                    IntegrationCapability::Mcp,
                    mcp_result,
                    Some(common::path_string(&config_path(ctx))),
                    None,
                    mcp_message,
                ),
                skill_result,
                hook_result,
            ],
            Some(connector.manual_instructions(ctx)?),
            true,
            Some("Grok 安装完成".into()),
        )
        .map_err(aggregate_map_err)
}

/// grok_uninstall_body 是 uninstall 的可 `?` 内层实现。
fn grok_uninstall_body(
    connector: &GrokConnector,
    ctx: &ConnectorRuntimeContext,
) -> Result<ConnectorOperationOutcome, ConnectorError> {
        let skill = skill_path(ctx);
        // 卸载顺序固定为 Hook → Skill → MCP，与安装顺序相反。
        let hook_result = uninstall_hook(ctx);
        let hook_changed = matches!(hook_result.result, IntegrationResult::Installed);
        let hook_needs_action = matches!(hook_result.result, IntegrationResult::NeedsAction);
        let hook_failed = matches!(hook_result.result, IntegrationResult::Failed);
        let skill_result = common::uninstall_skill(&skill);
        let skill_changed = matches!(skill_result.result, IntegrationResult::Installed);
        let skill_failed = matches!(skill_result.result, IntegrationResult::Failed);
        if skill_failed {
            tracing::error!(
                connector_id = CONNECTOR_ID,
                operation = "skill_uninstall",
                error_code = "skill_install_failed",
                "grok skill uninstall failed"
            );
        }

        let mut mcp_changed = false;
        let mut mcp_needs_action = false;
        let mut mcp_failed = false;
        let mut mcp_message = Some("未配置 user-scope superdev，跳过 remove".into());
        let mut mcp_result = IntegrationResult::AlreadyPresent;

        if let Ok(program) = require_cli(ctx) {
            let present = match list_servers(connector.runner.as_ref(), &program) {
                Ok(items) => find_superdev_user_entry(&items).is_some(),
                Err(error) => {
                    let mapped = map_process_error(error, "cli_list_failed");
                    tracing::error!(
                        connector_id = CONNECTOR_ID,
                        operation = "uninstall",
                        error_code = mapped.error.code(),
                        underlying_code = mapped.underlying_code.as_str(),
                        "grok uninstall list failed"
                    );
                    mcp_failed = true;
                    mcp_result = IntegrationResult::Failed;
                    mcp_message = Some("卸载前 list 失败，无法确认 MCP 状态".into());
                    false
                }
            };
            if present && !mcp_failed {
                let remove_output = match run_cli(
                    connector.runner.as_ref(),
                    &program,
                    &["mcp", "remove", "superdev", "--scope", "user"],
                ) {
                    Ok(output) => output,
                    Err(error) => {
                        let mapped = map_process_error(error, "cli_remove_failed");
                        tracing::error!(
                            connector_id = CONNECTOR_ID,
                            operation = "mcp_remove",
                            error_code = mapped.error.code(),
                            underlying_code = mapped.underlying_code.as_str(),
                            "grok mcp remove failed"
                        );
                        mcp_failed = true;
                        mcp_result = IntegrationResult::Failed;
                        mcp_message = Some("grok mcp remove 失败".into());
                        CommandOutput {
                            status_code: None,
                            stdout: String::new(),
                            stderr: String::new(),
                            truncated: false,
                        }
                    }
                };
                if !mcp_failed {
                    if !remove_output.success() {
                        tracing::error!(
                            connector_id = CONNECTOR_ID,
                            operation = "mcp_remove",
                            error_code = "cli_remove_failed",
                            status_code = remove_output.status_code,
                            truncated = remove_output.truncated,
                            "grok mcp remove failed"
                        );
                        mcp_failed = true;
                        mcp_result = IntegrationResult::Failed;
                        mcp_message = Some("grok mcp remove 返回非零退出码".into());
                    } else {
                        // 计划要求 remove 后再次 list 验证，避免空成功。
                        match list_servers(connector.runner.as_ref(), &program) {
                            Ok(items) if find_superdev_user_entry(&items).is_none() => {
                                mcp_changed = true;
                                // Installed 在卸载语义中表示「发生了变更」。
                                mcp_result = IntegrationResult::Installed;
                                mcp_message = Some(
                                    "已通过 grok mcp remove 移除 user-scope superdev".into(),
                                );
                            }
                            Ok(_) => {
                                tracing::error!(
                                    connector_id = CONNECTOR_ID,
                                    operation = "mcp_remove",
                                    error_code = "cli_remove_failed",
                                    "grok mcp remove reported success but entry remains"
                                );
                                mcp_failed = true;
                                mcp_result = IntegrationResult::Failed;
                                mcp_message =
                                    Some("remove 后 list 仍见 user-scope superdev".into());
                            }
                            Err(error) => {
                                let mapped = map_process_error(error, "cli_list_failed");
                                tracing::error!(
                                    connector_id = CONNECTOR_ID,
                                    operation = "mcp_remove_verify",
                                    error_code = mapped.error.code(),
                                    underlying_code = mapped.underlying_code.as_str(),
                                    "grok mcp remove verify list failed"
                                );
                                mcp_failed = true;
                                mcp_result = IntegrationResult::Failed;
                                mcp_message = Some("remove 后 list 复核失败".into());
                            }
                        }
                    }
                }
            }
        } else {
            // MCP 可能仍留在 Grok 配置中，不能被 Skill/Hook 删除成功掩盖成整体 Success。
            tracing::error!(
                connector_id = CONNECTOR_ID,
                operation = "uninstall",
                error_code = "cli_not_found",
                "grok uninstall cli missing"
            );
            mcp_needs_action = true;
            mcp_result = IntegrationResult::NeedsAction;
            mcp_message =
                Some("未找到 grok CLI，请手动运行 grok mcp remove superdev --scope user".into());
        }

        let changed = mcp_changed || skill_changed || hook_changed;
        let needs_action = mcp_needs_action || hook_needs_action;
        let any_failed = mcp_failed || skill_failed || hook_failed;
        // Skill/Hook Failed 必须进入总结果，禁止「子项失败却 Success」。
        let result = if any_failed {
            if changed {
                ConnectorResult::Partial
            } else {
                ConnectorResult::Failed
            }
        } else if needs_action && changed {
            ConnectorResult::Partial
        } else if needs_action {
            ConnectorResult::NeedsAction
        } else if changed {
            ConnectorResult::Success
        } else {
            ConnectorResult::Unchanged
        };
        let outcome = ConnectorOperationOutcome {
            connector_id: CONNECTOR_ID.into(),
            operation: ConnectorOperation::Uninstall,
            result,
            integrations: vec![
                common::integration_result(
                    IntegrationCapability::Mcp,
                    mcp_result,
                    Some(common::path_string(&config_path(ctx))),
                    None,
                    mcp_message,
                ),
                skill_result,
                hook_result,
            ],
            manual_instructions: if needs_action || any_failed {
                Some(connector.manual_instructions(ctx)?)
            } else {
                None
            },
            requires_restart: changed,
            message: Some("Grok 卸载完成".into()),
        };
        Ok(outcome)
}

impl AgentConnector for GrokConnector {
    fn descriptor(&self) -> &AgentConnectorDescriptor {
        &self.descriptor
    }

    fn cli_commands(&self) -> Vec<String> {
        vec![CLI_COMMAND.to_string()]
    }

    fn detect(&self, ctx: &ConnectorRuntimeContext) -> Result<ConnectorDetection, ConnectorError> {
        let started = Instant::now();
        tracing::debug!(
            connector_id = CONNECTOR_ID,
            operation = "detect",
            "grok detect started"
        );
        // CLI 优先；无 CLI 时仅当 ~/.grok 目录存在才视为已检测到安装痕迹。
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

    fn status(&self, ctx: &ConnectorRuntimeContext) -> Result<ConnectorStatus, ConnectorError> {
        let started = Instant::now();
        tracing::debug!(
            connector_id = CONNECTOR_ID,
            operation = "status",
            "grok status started"
        );

        // 仅用 list --json 证明 MCP 配置；不跑 doctor（避免把连通性当接入状态）。
        let (mcp_status, mcp_message) = match require_cli(ctx) {
            Ok(program) => match list_servers(self.runner.as_ref(), &program) {
                Ok(items) => {
                    let user = find_superdev_user_entry(&items);
                    let any_superdev = items.iter().any(|item| item.is_named_superdev());
                    match user {
                        Some(value) if entry_matches(ctx, value) => (
                            IntegrationStateStatus::Configured,
                            Some("SuperDev MCP 已由 grok CLI 配置".into()),
                        ),
                        Some(_) => (
                            IntegrationStateStatus::NeedsAction,
                            Some("superdev MCP 条目存在但不匹配期望配置".into()),
                        ),
                        // 仅有 project-scope 等同名条目时不能自动改写，提示改为 --scope user。
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
                Err(error) if error.code() == "invalid_cli_output" => {
                    tracing::error!(
                        connector_id = CONNECTOR_ID,
                        operation = "status",
                        error_code = "invalid_cli_output",
                        "grok status list output invalid"
                    );
                    (
                        IntegrationStateStatus::Error,
                        Some("grok mcp list 输出无法解析".into()),
                    )
                }
                // list 失败不得向上抛 Err：Registry 会把 hard error 泛化为 Unknown。
                Err(error) => {
                    let mapped = map_process_error(error, "cli_list_failed");
                    tracing::error!(
                        connector_id = CONNECTOR_ID,
                        operation = "status",
                        error_code = mapped.error.code(),
                        underlying_code = mapped.underlying_code.as_str(),
                        "grok status list failed"
                    );
                    (
                        IntegrationStateStatus::Error,
                        Some("grok mcp list 失败".into()),
                    )
                }
            },
            // CLI 缺失：设计矩阵为 Error/NeedsAction；NeedsAction 便于引导用户安装 CLI。
            Err(error) => {
                tracing::error!(
                    connector_id = CONNECTOR_ID,
                    operation = "status",
                    error_code = error.code(),
                    "grok status cli missing"
                );
                (
                    IntegrationStateStatus::NeedsAction,
                    Some("未找到 grok CLI，无法读取 MCP 状态".into()),
                )
            }
        };

        // 已配置或需纠正时回填期望的 SuperDev 运行时字段供设置页展示。
        let (mcp_command, agent_url) = if mcp_status == IntegrationStateStatus::Configured
            || mcp_status == IntegrationStateStatus::NeedsAction
        {
            let entry = common::entry(ctx);
            (Some(entry.command), Some(entry.agent_url))
        } else {
            (None, None)
        };

        let skill_state = common::skill_status(ctx, &skill_path(ctx));
        let hook_state = hook_status(ctx);
        let result = ConnectorStatus {
            integrations: vec![
                IntegrationState {
                    capability: IntegrationCapability::Mcp,
                    status: mcp_status,
                    target_path: Some(common::path_string(&config_path(ctx))),
                    message: mcp_message,
                },
                skill_state,
                hook_state,
            ],
            requires_restart: false,
            message: None,
            mcp_command,
            agent_url,
        };
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = "status",
            mcp_status = ?mcp_status,
            duration_ms = started.elapsed().as_millis() as u64,
            "grok status finished"
        );
        Ok(result)
    }

    fn install(
        &self,
        ctx: &ConnectorRuntimeContext,
        request: ConnectorInstallRequest,
    ) -> Result<ConnectorOperationOutcome, ConnectorError> {
        let started = Instant::now();
        let capability_count = request.capabilities.len();
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = ?request.operation,
            capability_count,
            "grok install started"
        );
        // 内层可用 `?`；外层保证任意返回都有 finished/failed + duration。
        let outcome = grok_install_body(self, ctx, request);
        log_op_finish("install", started, &outcome);
        outcome
    }

    fn uninstall(
        &self,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorOperationOutcome, ConnectorError> {
        let started = Instant::now();
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = "uninstall",
            "grok uninstall started"
        );
        let outcome = grok_uninstall_body(self, ctx);
        log_op_finish("uninstall", started, &outcome);
        outcome
    }

    fn manual_instructions(
        &self,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorManualInstructions, ConnectorError> {
        // 手动指引必须可粘贴：add 命令与 verification 均以 --scope user 为准。
        // SessionStart 在 Grok 上不注入 additionalContext，步骤文案明确 Skill-first。
        // 路径 quoting 分 shell 方言：POSIX / PowerShell / cmd.exe 各一版（真实 install 走 argv）。
        let launch = ctx.mcp_launch();
        let raw_mcp = launch.command.display().to_string();
        // 手动指引必须与 mcp_add_args 生成的 argv 同源：命令之后逐个附加启动规格
        // 的 args（本机恒为空 → 与改造前逐字节相同；远端会多出 `mcp`）。
        let render = |quote: fn(&str) -> String| {
            let mut command = format!(
                "grok mcp add superdev --scope user -e SUPERDEV_AGENT_URL={} -- {}",
                launch.agent_url,
                quote(&raw_mcp)
            );
            for arg in &launch.args {
                command.push(' ');
                command.push_str(&quote(arg));
            }
            command
        };
        let add_posix = render(shell_quote_posix);
        let add_powershell = render(shell_quote_powershell);
        let add_cmd = render(shell_quote_cmd);
        let add_default = if cfg!(windows) {
            add_powershell.clone()
        } else {
            add_posix.clone()
        };
        let hook = hook_path(ctx);
        let skill = skill_path(ctx);
        let hook_example = {
            let body = hook_file_body(&skill);
            format!("在 {} 写入类似内容（owned 文件，勿与用户其它 hook 合并）: {body}", hook.display())
        };
        Ok(ConnectorManualInstructions {
            summary: "通过 Grok CLI 接入 SuperDev（MCP + Skill + Session Hook）".into(),
            steps: vec![
                "安装 Grok CLI，并确保 grok 在 PATH 中".into(),
                format!("POSIX shell: {add_posix}"),
                format!("PowerShell: {add_powershell}"),
                format!("cmd.exe: {add_cmd}"),
                format!("Skill 目标目录: {}", skill.display()),
                format!(
                    "Hook 文件: {} （SessionStart；Grok 不注入 additionalContext，引导以 Skill 为主）",
                    hook.display()
                ),
                hook_example,
                "重启 Grok 会话，或在 /mcps 中刷新".into(),
                "验证: grok mcp list --json".into(),
            ],
            config_path: Some(common::path_string(&config_path(ctx))),
            manual_config: Some(add_default),
            verification_prompt: Some(
                "运行 grok mcp list --json，确认 name=superdev、scope=user、command 与 SUPERDEV_AGENT_URL 正确"
                    .into(),
            ),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::VecDeque;
    use std::sync::atomic::{AtomicU64, Ordering};
    use std::sync::Mutex;

    static COUNTER: AtomicU64 = AtomicU64::new(0);

    /// FakeCommandRunner 按队列返回预设输出，并记录 argv 供断言（不经真实进程）。
    #[derive(Clone, Default)]
    struct FakeCommandRunner {
        calls: Arc<Mutex<Vec<Vec<String>>>>,
        responses: Arc<Mutex<VecDeque<Result<CommandOutput, ConnectorError>>>>,
    }

    impl FakeCommandRunner {
        fn push_ok(&self, status: i32, stdout: &str) {
            self.push_output(status, stdout, "");
        }

        fn push_output(&self, status: i32, stdout: &str, stderr: &str) {
            self.responses.lock().unwrap().push_back(Ok(CommandOutput {
                status_code: Some(status),
                stdout: stdout.into(),
                stderr: stderr.into(),
                truncated: false,
            }));
        }

        fn push_err(&self, code: &str, message: &str) {
            self.responses
                .lock()
                .unwrap()
                .push_back(Err(ConnectorError::new(code, message)));
        }

        fn argv(&self) -> Vec<Vec<String>> {
            self.calls.lock().unwrap().clone()
        }
    }

    impl CommandRunner for FakeCommandRunner {
        fn run(&self, spec: CommandSpec) -> Result<CommandOutput, ConnectorError> {
            let args: Vec<String> = spec
                .args
                .iter()
                .map(|arg| arg.to_string_lossy().into_owned())
                .collect();
            self.calls.lock().unwrap().push(args);
            self.responses
                .lock()
                .unwrap()
                .pop_front()
                .unwrap_or_else(|| {
                    Ok(CommandOutput {
                        status_code: Some(0),
                        stdout: String::new(),
                        stderr: String::new(),
                        truncated: false,
                    })
                })
        }
    }

    fn test_dir(label: &str) -> PathBuf {
        let root = std::env::temp_dir().join(format!(
            "grok-{}-{}-{}",
            label,
            std::process::id(),
            COUNTER.fetch_add(1, Ordering::Relaxed)
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

    fn write_fake_cli(bin: &Path) -> PathBuf {
        std::fs::create_dir_all(bin).unwrap();
        let cli_name = if cfg!(windows) { "grok.exe" } else { "grok" };
        let cli_path = bin.join(cli_name);
        std::fs::write(&cli_path, "").unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mut perms = std::fs::metadata(&cli_path).unwrap().permissions();
            perms.set_mode(0o755);
            std::fs::set_permissions(&cli_path, perms).unwrap();
        }
        cli_path
    }

    #[test]
    fn detect_prefers_cli_path_when_present() {
        let home = test_dir("detect-cli");
        let bin = home.join("bin");
        let cli_path = write_fake_cli(&bin);
        // 同时存在 data root 时，CLI 应优先作为 detection_path
        std::fs::create_dir_all(home.join(".grok")).unwrap();

        let ctx = context_at(home.clone(), vec![bin]);
        let hit = GrokConnector::new().detect(&ctx).unwrap();
        assert!(hit.detected);
        assert_eq!(hit.detection_path.as_deref(), Some(cli_path.as_path()));
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn detect_falls_back_to_data_root_without_cli() {
        let home = test_dir("detect-root");
        let root = home.join(".grok");
        std::fs::create_dir_all(&root).unwrap();
        let empty_bin = home.join("empty-bin");
        std::fs::create_dir_all(&empty_bin).unwrap();

        let ctx = context_at(home.clone(), vec![empty_bin]);
        let hit = GrokConnector::new().detect(&ctx).unwrap();
        assert!(hit.detected);
        assert_eq!(hit.detection_path.as_deref(), Some(root.as_path()));
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn detect_misses_when_neither_cli_nor_root() {
        let home = test_dir("detect-miss");
        let empty_bin = home.join("empty-bin");
        std::fs::create_dir_all(&empty_bin).unwrap();

        let ctx = context_at(home.clone(), vec![empty_bin]);
        let hit = GrokConnector::new().detect(&ctx).unwrap();
        assert!(!hit.detected);
        assert!(hit.detection_path.is_none());
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn entry_matches_requires_command_url_enabled() {
        let home = test_dir("entry-match");
        let ctx = context_at(home.clone(), vec![]);
        let good: serde_json::Value = serde_json::from_str(&configured_list_json(&ctx)).unwrap();
        let entry = McpListEntry::from_value(&good.as_array().unwrap()[0]);
        assert!(entry_matches(&ctx, &entry));

        let mut disabled_json = good.as_array().unwrap()[0].clone();
        disabled_json["enabled"] = serde_json::json!(false);
        let disabled = McpListEntry::from_value(&disabled_json);
        assert!(!entry_matches(&ctx, &disabled));

        let mut bad_url_json = good.as_array().unwrap()[0].clone();
        bad_url_json["env"]["SUPERDEV_AGENT_URL"] = serde_json::json!("http://127.0.0.1:9");
        let bad_url = McpListEntry::from_value(&bad_url_json);
        assert!(!entry_matches(&ctx, &bad_url));
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn status_mcp_configured_when_list_matches() {
        let home = test_dir("status-configured");
        let bin = home.join("bin");
        write_fake_cli(&bin);
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
        assert_eq!(
            status.mcp_command.as_deref(),
            Some(ctx.mcp_binary().to_str().unwrap())
        );
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn status_mcp_needs_action_for_project_only_scope() {
        let home = test_dir("status-project");
        let bin = home.join("bin");
        write_fake_cli(&bin);
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
        assert!(
            mcp.message
                .as_deref()
                .unwrap_or("")
                .contains("--scope user"),
            "project-only should mention --scope user"
        );
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn status_mcp_needs_action_on_user_entry_mismatch() {
        let home = test_dir("status-mismatch");
        let bin = home.join("bin");
        write_fake_cli(&bin);
        let ctx = context_at(home.clone(), vec![bin]);
        let body = serde_json::json!([{
            "name": "superdev",
            "command": "/wrong/mcp",
            "env": { "SUPERDEV_AGENT_URL": DEFAULT_AGENT_URL },
            "enabled": true,
            "scope": "user"
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
    fn status_mcp_error_on_invalid_list_json() {
        let home = test_dir("status-invalid");
        let bin = home.join("bin");
        write_fake_cli(&bin);
        let ctx = context_at(home.clone(), vec![bin]);
        let runner = FakeCommandRunner::default();
        runner.push_ok(0, "not-json");
        let status = GrokConnector::with_runner(Arc::new(runner))
            .status(&ctx)
            .unwrap();
        let mcp = status
            .integrations
            .iter()
            .find(|i| i.capability == IntegrationCapability::Mcp)
            .unwrap();
        assert_eq!(mcp.status, IntegrationStateStatus::Error);
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn status_mcp_needs_action_when_cli_missing() {
        let home = test_dir("status-no-cli");
        let ctx = context_at(home.clone(), vec![]);
        let status = GrokConnector::new().status(&ctx).unwrap();
        let mcp = status
            .integrations
            .iter()
            .find(|i| i.capability == IntegrationCapability::Mcp)
            .unwrap();
        assert_eq!(mcp.status, IntegrationStateStatus::NeedsAction);
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn status_mcp_error_on_list_nonzero_exit() {
        let home = test_dir("status-list-fail");
        let bin = home.join("bin");
        write_fake_cli(&bin);
        let ctx = context_at(home.clone(), vec![bin]);
        let runner = FakeCommandRunner::default();
        runner.push_ok(1, "");
        let status = GrokConnector::with_runner(Arc::new(runner))
            .status(&ctx)
            .unwrap();
        let mcp = status
            .integrations
            .iter()
            .find(|i| i.capability == IntegrationCapability::Mcp)
            .unwrap();
        assert_eq!(mcp.status, IntegrationStateStatus::Error);
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn uninstall_fails_when_remove_verify_list_still_has_entry() {
        let home = test_dir("uninstall-verify-fail");
        let bin = home.join("bin");
        write_fake_cli(&bin);
        let ctx = context_at(home.clone(), vec![bin]);
        let runner = FakeCommandRunner::default();
        // list present → remove ok → list still present
        runner.push_ok(0, &configured_list_json(&ctx));
        runner.push_ok(0, "");
        runner.push_ok(0, &configured_list_json(&ctx));
        let outcome = GrokConnector::with_runner(Arc::new(runner))
            .uninstall(&ctx)
            .unwrap();
        assert_eq!(outcome.result, ConnectorResult::Failed);
        assert_eq!(
            outcome.integrations[0].result,
            IntegrationResult::Failed
        );
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn install_mcp_calls_add_with_scope_user_and_env() {
        let home = test_dir("install-add");
        let bin = home.join("bin");
        write_fake_cli(&bin);
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
        // list empty → add ok → list configured
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
        assert_eq!(
            outcome.integrations[0].result,
            IntegrationResult::Installed
        );
        let argv = runner.argv();
        assert!(argv.iter().any(|a| {
            a.first().map(String::as_str) == Some("mcp")
                && a.get(1).map(String::as_str) == Some("add")
                && a.iter().any(|x| x == "--scope")
                && a.iter().any(|x| x == "user")
                && a.iter().any(|x| x == "superdev")
                && a.iter().any(|x| x.starts_with("SUPERDEV_AGENT_URL="))
        }));
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn uninstall_mcp_calls_remove_with_scope_user() {
        let home = test_dir("uninstall-remove");
        let bin = home.join("bin");
        write_fake_cli(&bin);
        let ctx = context_at(home.clone(), vec![bin]);
        let runner = FakeCommandRunner::default();
        // list with user superdev → remove ok → list empty (verify)
        runner.push_ok(0, &configured_list_json(&ctx));
        runner.push_ok(0, "");
        runner.push_ok(0, "[]");
        let outcome = GrokConnector::with_runner(Arc::new(runner.clone()))
            .uninstall(&ctx)
            .unwrap();
        assert_eq!(outcome.result, ConnectorResult::Success);
        assert_eq!(
            outcome.integrations[0].result,
            IntegrationResult::Installed
        );
        let argv = runner.argv();
        assert!(argv.iter().any(|a| {
            a == &vec![
                "mcp".to_string(),
                "remove".to_string(),
                "superdev".to_string(),
                "--scope".to_string(),
                "user".to_string(),
            ]
        }));
        let _ = std::fs::remove_dir_all(home);
    }

    fn write_skill_source(home: &Path) -> PathBuf {
        let skill_src = home.join("skill-src");
        std::fs::create_dir_all(skill_src.join("hooks")).unwrap();
        std::fs::write(skill_src.join("SKILL.md"), "# superdev\n").unwrap();
        std::fs::write(skill_src.join("hooks/run-hook.cmd"), "echo hook\n").unwrap();
        std::fs::write(skill_src.join("hooks/session-start"), "#!/bin/sh\n").unwrap();
        skill_src
    }

    fn install_all_request() -> ConnectorInstallRequest {
        ConnectorInstallRequest {
            operation: ConnectorOperation::Install,
            capabilities: vec![
                IntegrationCapability::Mcp,
                IntegrationCapability::Skill,
                IntegrationCapability::SessionHook,
            ],
        }
    }

    #[test]
    fn install_writes_skill_and_owned_hook_after_mcp() {
        let home = test_dir("install-skill-hook");
        let bin = home.join("bin");
        write_fake_cli(&bin);
        let skill_src = write_skill_source(&home);
        let ctx = ConnectorRuntimeContext::new(
            home.clone(),
            vec![bin],
            vec![],
            home.join("superdev-mcp"),
            Some(skill_src),
            None,
        );
        let runner = FakeCommandRunner::default();
        // list empty → add ok → list configured
        runner.push_ok(0, "[]");
        runner.push_ok(0, "");
        runner.push_ok(0, &configured_list_json(&ctx));
        // status after install re-lists for MCP
        runner.push_ok(0, &configured_list_json(&ctx));

        let connector = GrokConnector::with_runner(Arc::new(runner));
        let outcome = connector.install(&ctx, install_all_request()).unwrap();
        assert_eq!(outcome.result, ConnectorResult::Success);
        assert!(matches!(
            outcome.integrations[0].result,
            IntegrationResult::Installed | IntegrationResult::AlreadyPresent
        ));
        assert!(matches!(
            outcome.integrations[1].result,
            IntegrationResult::Installed | IntegrationResult::AlreadyPresent
        ));
        assert!(matches!(
            outcome.integrations[2].result,
            IntegrationResult::Installed | IntegrationResult::AlreadyPresent
        ));

        assert!(skill_path(&ctx).join("SKILL.md").is_file());
        let hook = std::fs::read_to_string(hook_path(&ctx)).unwrap();
        assert!(
            hook.contains(HOOK_MARKER) && hook.contains("session-start"),
            "owned hook must carry marker: {hook}"
        );

        let status = connector.status(&ctx).unwrap();
        for capability in [
            IntegrationCapability::Mcp,
            IntegrationCapability::Skill,
            IntegrationCapability::SessionHook,
        ] {
            let state = status
                .integrations
                .iter()
                .find(|item| item.capability == capability)
                .unwrap();
            assert_eq!(
                state.status,
                IntegrationStateStatus::Configured,
                "{capability:?} should be configured"
            );
        }
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn uninstall_removes_owned_hook_and_skill_and_mcp() {
        let home = test_dir("uninstall-full");
        let bin = home.join("bin");
        write_fake_cli(&bin);
        let skill_src = write_skill_source(&home);
        let ctx = ConnectorRuntimeContext::new(
            home.clone(),
            vec![bin],
            vec![],
            home.join("superdev-mcp"),
            Some(skill_src),
            None,
        );
        let runner = FakeCommandRunner::default();
        // install: list empty → add → list configured
        runner.push_ok(0, "[]");
        runner.push_ok(0, "");
        runner.push_ok(0, &configured_list_json(&ctx));
        // uninstall: list present → remove → list empty (verify)
        runner.push_ok(0, &configured_list_json(&ctx));
        runner.push_ok(0, "");
        runner.push_ok(0, "[]");

        let connector = GrokConnector::with_runner(Arc::new(runner.clone()));
        connector.install(&ctx, install_all_request()).unwrap();
        assert!(skill_path(&ctx).exists());
        assert!(hook_path(&ctx).is_file());

        let outcome = connector.uninstall(&ctx).unwrap();
        assert_eq!(outcome.result, ConnectorResult::Success);
        assert!(!hook_path(&ctx).exists(), "owned hook must be removed");
        assert!(!skill_path(&ctx).exists(), "skill dir must be removed");
        let argv = runner.argv();
        assert!(argv.iter().any(|a| {
            a == &vec![
                "mcp".to_string(),
                "remove".to_string(),
                "superdev".to_string(),
                "--scope".to_string(),
                "user".to_string(),
            ]
        }));
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn partial_when_mcp_ok_skill_source_missing() {
        let home = test_dir("partial-skill-missing");
        let bin = home.join("bin");
        write_fake_cli(&bin);
        let ctx = ConnectorRuntimeContext::new(
            home.clone(),
            vec![bin],
            vec![],
            home.join("superdev-mcp"),
            None,
            Some("bundled skill unavailable".into()),
        );
        let runner = FakeCommandRunner::default();
        runner.push_ok(0, "[]");
        runner.push_ok(0, "");
        runner.push_ok(0, &configured_list_json(&ctx));

        let outcome = GrokConnector::with_runner(Arc::new(runner))
            .install(&ctx, install_all_request())
            .unwrap();
        assert_eq!(outcome.result, ConnectorResult::Partial);
        assert!(matches!(
            outcome.integrations[0].result,
            IntegrationResult::Installed | IntegrationResult::AlreadyPresent
        ));
        assert_eq!(
            outcome.integrations[1].result,
            IntegrationResult::Failed
        );
        assert!(matches!(
            outcome.integrations[2].result,
            IntegrationResult::Skipped | IntegrationResult::Failed
        ));
        assert!(
            !hook_path(&ctx).exists(),
            "hook must not be written when skill is missing"
        );
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn hook_conflict_does_not_overwrite_foreign_file() {
        let home = test_dir("hook-conflict");
        let bin = home.join("bin");
        write_fake_cli(&bin);
        let skill_src = write_skill_source(&home);
        let ctx = ConnectorRuntimeContext::new(
            home.clone(),
            vec![bin],
            vec![],
            home.join("superdev-mcp"),
            Some(skill_src),
            None,
        );
        let foreign = r#"{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo foreign"}]}]}}"#;
        std::fs::create_dir_all(hook_path(&ctx).parent().unwrap()).unwrap();
        std::fs::write(hook_path(&ctx), foreign).unwrap();

        let runner = FakeCommandRunner::default();
        runner.push_ok(0, "[]");
        runner.push_ok(0, "");
        runner.push_ok(0, &configured_list_json(&ctx));

        let outcome = GrokConnector::with_runner(Arc::new(runner))
            .install(&ctx, install_all_request())
            .unwrap();
        assert_eq!(
            outcome.integrations[2].result,
            IntegrationResult::NeedsAction
        );
        let after = std::fs::read_to_string(hook_path(&ctx)).unwrap();
        assert_eq!(after, foreign, "foreign hook file must remain unchanged");
        assert!(!after.contains(HOOK_MARKER));
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn manual_instructions_document_user_scope_and_skill_first_hook() {
        let home = test_dir("manual-instructions");
        let spaced = home.join("dir with spaces").join("superdev-mcp");
        let ctx = ConnectorRuntimeContext::new(home.clone(), vec![], vec![], spaced, None, None);
        let mi = GrokConnector::new().manual_instructions(&ctx).unwrap();
        let blob = format!(
            "{:?}{:?}{:?}{:?}",
            mi.summary,
            mi.steps,
            mi.manual_config,
            mi.verification_prompt
        );
        assert!(blob.contains("--scope user"), "must document user scope: {blob}");
        assert!(
            blob.contains("Skill") || blob.contains("skill"),
            "must mention Skill: {blob}"
        );
        assert!(
            blob.contains("additionalContext") || blob.contains("引导"),
            "must document Skill-first / no additionalContext: {blob}"
        );
        assert!(
            mi.manual_config
                .as_deref()
                .is_some_and(|c| c.contains("grok mcp add superdev")),
            "manual_config must be the pasteable add command"
        );
        assert!(
            mi.manual_config.as_deref().is_some_and(|c| {
                c.contains('\'') && c.contains("dir with spaces")
            }),
            "spaced MCP path must use POSIX single quotes: {:?}",
            mi.manual_config
        );
        assert!(
            mi.steps
                .iter()
                .any(|s| s.contains("SessionStart") && s.contains("hooks")),
            "must include hook file example: {:?}",
            mi.steps
        );
        assert!(
            mi.config_path
                .as_deref()
                .is_some_and(|p| p.contains("config.toml")),
            "config_path should point at config.toml"
        );
        assert!(
            mi.verification_prompt
                .as_deref()
                .is_some_and(|p| p.contains("list --json") && p.contains("scope=user")),
            "verification should check list --json user-scope"
        );
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn status_mcp_missing_when_list_empty() {
        let home = test_dir("status-missing");
        let bin = home.join("bin");
        write_fake_cli(&bin);
        let ctx = context_at(home.clone(), vec![bin]);
        let runner = FakeCommandRunner::default();
        runner.push_ok(0, "[]");
        let status = GrokConnector::with_runner(Arc::new(runner))
            .status(&ctx)
            .unwrap();
        let mcp = status
            .integrations
            .iter()
            .find(|i| i.capability == IntegrationCapability::Mcp)
            .unwrap();
        assert_eq!(mcp.status, IntegrationStateStatus::Missing);
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn map_process_error_maps_timeout_to_cli_add_failed_and_keeps_underlying() {
        assert_eq!(CLI_TIMEOUT, Duration::from_secs(30));
        let mapped = map_process_error(
            ConnectorError::new("command_timeout", "命令执行超时"),
            "cli_add_failed",
        );
        assert_eq!(mapped.error.code(), "cli_add_failed");
        assert_eq!(mapped.underlying_code, "command_timeout");
        assert!(mapped.error.message().contains("超时") || mapped.error.message().contains("timeout") || !mapped.error.message().is_empty());
    }

    #[test]
    fn install_maps_command_timeout_to_cli_add_failed() {
        let home = test_dir("install-timeout");
        let bin = home.join("bin");
        write_fake_cli(&bin);
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
        runner.push_ok(0, "[]");
        runner.push_err("command_timeout", "命令执行超时");
        let outcome = GrokConnector::with_runner(Arc::new(runner))
            .install(
                &ctx,
                ConnectorInstallRequest {
                    operation: ConnectorOperation::Install,
                    capabilities: vec![IntegrationCapability::Mcp],
                },
            )
            .unwrap();
        assert_eq!(outcome.result, ConnectorResult::Failed);
        assert_eq!(outcome.integrations[0].result, IntegrationResult::Failed);
        // 映射后用户 message 来自 mcp_blocked 文案，稳定码在日志；直接验证 map_process_error 契约。
        let mapped = map_process_error(
            ConnectorError::new("command_timeout", "命令执行超时"),
            "cli_add_failed",
        );
        assert_eq!(mapped.error.code(), "cli_add_failed");
        assert_eq!(mapped.underlying_code, "command_timeout");
        assert_eq!(CLI_TIMEOUT.as_secs(), 30);
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn shell_quote_dialects_are_safe_for_posix_powershell_and_cmd() {
        let tricky = r"/tmp/Super $HOME/`date`/mcp";
        assert_eq!(shell_quote_posix(tricky), r"'/tmp/Super $HOME/`date`/mcp'");
        assert_eq!(
            shell_quote_powershell(tricky),
            r"'/tmp/Super $HOME/`date`/mcp'"
        );
        assert_eq!(
            shell_quote_cmd(r#"C:\Users\foo\bar"baz"#),
            "\"C:\\Users\\foo\\bar\"\"baz\""
        );
        // PowerShell 把内部 ' 翻倍，而不是 POSIX '\''。
        assert_eq!(shell_quote_powershell("a'b"), "'a''b'");
        assert_eq!(shell_quote_posix("a'b"), r"'a'\''b'");
        // 宿主默认 shell_quote 与平台一致。
        if cfg!(windows) {
            assert_eq!(shell_quote("a'b"), shell_quote_powershell("a'b"));
        } else {
            assert_eq!(shell_quote("a'b"), shell_quote_posix("a'b"));
        }
    }

    #[test]
    fn manual_instructions_include_posix_powershell_and_cmd_forms() {
        let home = test_dir("manual-shells");
        let spaced = home.join("dir with spaces").join("superdev-mcp");
        let ctx = ConnectorRuntimeContext::new(home.clone(), vec![], vec![], spaced, None, None);
        let mi = GrokConnector::new().manual_instructions(&ctx).unwrap();
        let blob = mi.steps.join("\n");
        assert!(blob.contains("POSIX shell:"), "{blob}");
        assert!(blob.contains("PowerShell:"), "{blob}");
        assert!(blob.contains("cmd.exe:"), "{blob}");
        assert!(blob.contains("dir with spaces"), "{blob}");
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn install_without_cli_returns_failed_with_manual_instructions() {
        let home = test_dir("install-no-cli");
        let empty_bin = home.join("empty-bin");
        std::fs::create_dir_all(&empty_bin).unwrap();
        let ctx = context_at(home.clone(), vec![empty_bin]);

        let outcome = GrokConnector::new()
            .install(&ctx, install_all_request())
            .unwrap();
        assert_eq!(outcome.result, ConnectorResult::Failed);
        assert_eq!(
            outcome.integrations[0].result,
            IntegrationResult::Failed,
            "MCP must fail when CLI is missing"
        );
        assert_eq!(
            outcome.integrations[1].result,
            IntegrationResult::Skipped,
            "Skill must be skipped when MCP fails"
        );
        assert_eq!(
            outcome.integrations[2].result,
            IntegrationResult::Skipped,
            "Hook must be skipped when MCP fails"
        );
        let mi = outcome
            .manual_instructions
            .as_ref()
            .expect("manual_instructions required on MCP failure");
        assert!(
            mi.manual_config
                .as_deref()
                .is_some_and(|c| !c.trim().is_empty() && c.contains("--scope user")),
            "manual_config must be non-empty add command"
        );
        let _ = std::fs::remove_dir_all(home);
    }

    /// remote_launch_spec 构造远端安装场景的 MCP 启动规格：目标机 agent 的绝对
    /// 路径 + `mcp` 子命令 + 目标机自己的 Agent URL（刻意不是本机默认端口）。
    ///
    /// 这三项只要有一项没抵达最终写出的配置，用户就会看到"安装成功"但远端那个
    /// 智能体永远连不上——是静默错误，必须由测试钉死。
    fn remote_launch_spec() -> crate::mcp_install::McpLaunchSpec {
        crate::mcp_install::McpLaunchSpec {
            command: PathBuf::from("/opt/superdev/superdev-agent"),
            args: vec!["mcp".to_string()],
            agent_url: "http://10.1.2.3:57117".to_string(),
        }
    }

    #[test]
    fn remote_launch_spec_reaches_grok_mcp_add_args() {
        let home = test_dir("grok-remote-launch");
        let ctx = context_at(home.clone(), vec![]).with_mcp_launch(remote_launch_spec());

        let args = mcp_add_args(&ctx);
        assert!(
            args.contains(&"SUPERDEV_AGENT_URL=http://10.1.2.3:57117".to_string()),
            "-e 必须带目标机 Agent URL: {args:?}"
        );
        let separator = args
            .iter()
            .position(|item| item == "--")
            .expect("grok mcp add 必须保留 -- 分隔符");
        assert_eq!(
            &args[separator + 1..],
            ["/opt/superdev/superdev-agent".to_string(), "mcp".to_string()],
            "-- 之后必须是「命令 + 启动规格 args」: {args:?}"
        );
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn local_grok_mcp_add_args_end_with_the_bare_binary() {
        let home = test_dir("grok-local-launch");
        let ctx = context_at(home.clone(), vec![]);

        let args = mcp_add_args(&ctx);
        let separator = args
            .iter()
            .position(|item| item == "--")
            .expect("grok mcp add 必须保留 -- 分隔符");
        assert_eq!(
            &args[separator + 1..],
            [ctx.mcp_binary().to_string_lossy().into_owned()],
            "本机 args 为空时 -- 之后只能是裸二进制（字节等价约束）: {args:?}"
        );
        assert!(
            args.contains(&format!("SUPERDEV_AGENT_URL={DEFAULT_AGENT_URL}")),
            "{args:?}"
        );
        let _ = std::fs::remove_dir_all(home);
    }

}
