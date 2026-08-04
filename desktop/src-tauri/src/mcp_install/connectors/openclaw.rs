// openclaw.rs 实现 OpenClaw 内置 Agent Connector（官方 MCP CLI）。
//
// 职责：
//   - 通过 argv 调用 openclaw mcp show/set/unset，不直接改写 openclaw.json
//   - 将 OPENCLAW_CONFIG_PATH 仅作为 CLI 环境提示传入 CommandSpec
//   - MCP 经 CLI 证明就绪后再安装 Skill；Session Hook 保持手动
//
// 边界：
//   - 不解析/写入 ~/.openclaw/openclaw.json（OpenClaw 拥有 JSON5/includes/Nix）
//   - 不在日志中记录 argv、canonical JSON、stdout/stderr 或路径
//   - Registry verify 仅委托 status（只读 show），不跑 doctor --probe

use super::common;
use super::process::{CommandOutput, CommandRunner, CommandSpec, SystemCommandRunner};
use crate::mcp_install::contracts::*;
use crate::mcp_install::registry::*;
use crate::mcp_install::{executable_file_names, mcp_server_json_value};
use std::ffi::OsString;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::{Duration, Instant};

const CONNECTOR_ID: &str = "openclaw";
const DISPLAY_NAME: &str = "OpenClaw";
/// CLI_COMMAND 是 resolve_cli 探测的命令名，同时也是 cli_commands() 对外汇报的
/// 值——两处共用同一个常量，避免各写一份字符串导致命令名漂移。
const CLI_COMMAND: &str = "openclaw";
const CLI_TIMEOUT: Duration = Duration::from_secs(30);

/// OpenClawConnector 通过官方 CLI 管理 MCP，并通过文件系统管理 owned Skill。
pub(super) struct OpenClawConnector {
    descriptor: AgentConnectorDescriptor,
    runner: Arc<dyn CommandRunner>,
}

impl OpenClawConnector {
    /// new 使用系统进程执行器创建连接器。
    pub(super) fn new() -> Self {
        Self::with_runner(Arc::new(SystemCommandRunner))
    }

    /// with_runner 注入可测试的 CommandRunner（仅测试构造）。
    #[cfg(test)]
    pub(super) fn with_runner(runner: Arc<dyn CommandRunner>) -> Self {
        Self {
            descriptor: common::descriptor(CONNECTOR_ID, DISPLAY_NAME, SupportMode::Manual, None),
            runner,
        }
    }

    #[cfg(not(test))]
    fn with_runner(runner: Arc<dyn CommandRunner>) -> Self {
        Self {
            descriptor: common::descriptor(CONNECTOR_ID, DISPLAY_NAME, SupportMode::Manual, None),
            runner,
        }
    }
}

fn skill_path(ctx: &ConnectorRuntimeContext) -> PathBuf {
    ctx.home_dir()
        .join(".openclaw")
        .join("skills")
        .join("superdev")
}

fn data_root(ctx: &ConnectorRuntimeContext) -> PathBuf {
    ctx.home_dir().join(".openclaw")
}

/// resolve_cli 在 command_dirs 中查找 openclaw / openclaw.exe。
fn resolve_cli(ctx: &ConnectorRuntimeContext) -> Option<PathBuf> {
    ctx.command_dirs().iter().find_map(|directory| {
        executable_file_names(CLI_COMMAND)
            .into_iter()
            .map(|name| directory.join(name))
            .find(|path| path.is_file())
    })
}

/// require_cli 要求 mutation 与 status 都必须有 CLI。
fn require_cli(ctx: &ConnectorRuntimeContext) -> Result<PathBuf, ConnectorError> {
    resolve_cli(ctx).ok_or_else(|| {
        ConnectorError::new(
            "cli_not_found",
            "未找到 openclaw CLI，无法通过官方命令管理 MCP",
        )
    })
}

/// canonical_mcp_json 仅包含 SuperDev stdio 命令、启动参数与 SUPERDEV_AGENT_URL。
///
/// 复用 `mcp_server_json_value`（与 JSON 方言族共享的那个 helper），而不是本地再手写
/// 一次「args 为空就不写 args 键」的判断——三处各写一份必然分叉。本机 args 恒为空，
/// 因此输出与改造前逐字节相同。
fn canonical_mcp_json(ctx: &ConnectorRuntimeContext) -> String {
    serde_json::to_string(&mcp_server_json_value(&common::entry(ctx))).expect("canonical mcp json")
}

fn apply_config_env(mut spec: CommandSpec, ctx: &ConnectorRuntimeContext) -> CommandSpec {
    // OpenClaw 即使收到独立配置路径，仍会打开 state SQLite。
    // 显式对齐 Connector home，避免调用落到另一个用户/测试运行态目录。
    spec = spec.with_env("OPENCLAW_STATE_DIR", data_root(ctx).as_os_str());
    // OPENCLAW_CONFIG_PATH 只作为 CLI 环境提示透传，SuperDev 从不解析该文件。
    if let Some(path) = ctx.environment().openclaw_config_path() {
        spec = spec.with_env("OPENCLAW_CONFIG_PATH", path.as_os_str());
    }
    spec.with_timeout(CLI_TIMEOUT)
}

fn run_cli(
    runner: &dyn CommandRunner,
    ctx: &ConnectorRuntimeContext,
    program: &Path,
    args: &[&str],
) -> Result<CommandOutput, ConnectorError> {
    let spec = apply_config_env(
        CommandSpec::new(program, args.iter().map(|arg| OsString::from(*arg))),
        ctx,
    );
    runner.run(spec)
}

/// show_superdev 只读查询官方 MCP 条目。
fn show_superdev(
    runner: &dyn CommandRunner,
    ctx: &ConnectorRuntimeContext,
    program: &Path,
) -> Result<Option<serde_json::Value>, ConnectorError> {
    let output = run_cli(runner, ctx, program, &["mcp", "show", "superdev", "--json"])?;
    if !output.success() {
        // 只有 CLI 明确报告条目不存在时才映射 Missing；其它非零退出必须保留故障语义。
        if show_output_reports_missing(&output) {
            return Ok(None);
        }
        return Err(ConnectorError::new(
            "cli_show_failed",
            "openclaw mcp show 失败",
        ));
    }
    let text = output.stdout.trim();
    if text.is_empty() || text == "null" {
        return Ok(None);
    }
    let value: serde_json::Value = serde_json::from_str(text).map_err(|_| {
        ConnectorError::new(
            "invalid_cli_output",
            "openclaw mcp show --json 输出无法解析",
        )
    })?;
    if value.is_null() {
        Ok(None)
    } else {
        Ok(Some(value))
    }
}

/// show_output_reports_missing 识别 OpenClaw 对不存在 server 的稳定文本诊断。
///
/// 输出只用于本地分类，不写入日志或用户错误，避免回显潜在敏感 CLI 内容。
fn show_output_reports_missing(output: &CommandOutput) -> bool {
    if output.status_code != Some(1) {
        return false;
    }
    let diagnostic = format!("{}\n{}", output.stdout, output.stderr).to_ascii_lowercase();
    [
        "not found",
        "does not exist",
        "no mcp server",
        "unknown mcp server",
    ]
    .iter()
    .any(|pattern| diagnostic.contains(pattern))
}

fn entry_matches(ctx: &ConnectorRuntimeContext, value: &serde_json::Value) -> bool {
    let launch = ctx.mcp_launch();
    let expected_command = launch.command.to_string_lossy();
    let command = value.get("command").and_then(|v| v.as_str()).unwrap_or("");
    // args 缺席等价于空数组（本机场景恒如此，与改造前判断结果一致）；远端场景下
    // 不比对 args 会把「命令对了但没带 mcp 子命令」的坏配置误判成已配置。
    let args: Vec<&str> = value
        .get("args")
        .and_then(|v| v.as_array())
        .map(|items| items.iter().filter_map(|item| item.as_str()).collect())
        .unwrap_or_default();
    let agent_url = value
        .get("env")
        .and_then(|env| env.get("SUPERDEV_AGENT_URL"))
        .and_then(|v| v.as_str())
        .unwrap_or("");
    command == expected_command.as_ref()
        && args == launch.args
        && agent_url == launch.agent_url
}

fn config_hint(ctx: &ConnectorRuntimeContext) -> Option<String> {
    ctx.environment()
        .openclaw_config_path()
        .map(common::path_string)
        .or_else(|| Some(common::path_string(&data_root(ctx).join("openclaw.json"))))
}

impl AgentConnector for OpenClawConnector {
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
            "openclaw detect started"
        );
        let cli = resolve_cli(ctx);
        let root = data_root(ctx);
        let hit = cli.or_else(|| root.is_dir().then_some(root));
        let result = ConnectorDetection {
            detected: hit.is_some(),
            detection_path: hit,
            message: Some("OpenClaw 检测完成".into()),
        };
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = "detect",
            detected = result.detected,
            duration_ms = started.elapsed().as_millis() as u64,
            "openclaw detect finished"
        );
        Ok(result)
    }

    fn status(&self, ctx: &ConnectorRuntimeContext) -> Result<ConnectorStatus, ConnectorError> {
        let started = Instant::now();
        tracing::debug!(
            connector_id = CONNECTOR_ID,
            operation = "status",
            "openclaw status started"
        );
        let skill = skill_path(ctx);
        let (mcp_status, mcp_message) = match require_cli(ctx) {
            Ok(program) => match show_superdev(self.runner.as_ref(), ctx, &program) {
                Ok(Some(value)) if entry_matches(ctx, &value) => (
                    IntegrationStateStatus::Configured,
                    Some("SuperDev MCP 已由 openclaw CLI 配置".into()),
                ),
                Ok(Some(_)) => (
                    IntegrationStateStatus::NeedsAction,
                    Some("superdev MCP 条目存在但不匹配期望配置".into()),
                ),
                Ok(None) => (
                    IntegrationStateStatus::Missing,
                    Some("openclaw 未配置 superdev MCP".into()),
                ),
                Err(error) if error.code() == "invalid_cli_output" => (
                    IntegrationStateStatus::Error,
                    Some("openclaw mcp show 输出无法解析".into()),
                ),
                Err(error) => {
                    tracing::error!(
                        connector_id = CONNECTOR_ID,
                        operation = "status",
                        error_code = error.code(),
                        duration_ms = started.elapsed().as_millis() as u64,
                        "openclaw status failed"
                    );
                    return Err(error);
                }
            },
            Err(_) => (
                IntegrationStateStatus::Missing,
                Some("未找到 openclaw CLI，无法读取 MCP 状态".into()),
            ),
        };
        let skill_state = common::skill_status(ctx, &skill);
        // OpenClaw 通过 CLI 读取状态；已配置时回填期望的 SuperDev 运行时字段供设置页展示。
        let (mcp_command, agent_url) = if mcp_status == IntegrationStateStatus::Configured
            || mcp_status == IntegrationStateStatus::NeedsAction
        {
            let entry = common::entry(ctx);
            (Some(entry.command), Some(entry.agent_url))
        } else {
            (None, None)
        };
        let result = ConnectorStatus {
            integrations: vec![
                IntegrationState {
                    capability: IntegrationCapability::Mcp,
                    status: mcp_status,
                    target_path: config_hint(ctx),
                    message: mcp_message,
                },
                skill_state,
                IntegrationState {
                    capability: IntegrationCapability::SessionHook,
                    status: IntegrationStateStatus::Missing,
                    target_path: None,
                    message: Some("Session Hook 需手动配置".into()),
                },
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
            "openclaw status finished"
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
            "openclaw install started"
        );
        let skill = skill_path(ctx);
        let program = match require_cli(ctx) {
            Ok(program) => program,
            Err(error) => {
                tracing::error!(
                    connector_id = CONNECTOR_ID,
                    operation = ?request.operation,
                    error_code = error.code(),
                    "openclaw cli missing"
                );
                return aggregate_connector_result(
                    CONNECTOR_ID.into(),
                    request.operation,
                    vec![
                        common::integration_result(
                            IntegrationCapability::Mcp,
                            IntegrationResult::Failed,
                            config_hint(ctx),
                            None,
                            Some(error.message().into()),
                        ),
                        common::integration_result(
                            IntegrationCapability::Skill,
                            IntegrationResult::Skipped,
                            Some(common::path_string(&skill)),
                            None,
                            Some("MCP 失败，已跳过 Skill".into()),
                        ),
                        common::manual_hook_result(None),
                    ],
                    Some(self.manual_instructions(ctx)?),
                    false,
                    Some("OpenClaw CLI 不可用".into()),
                )
                .map_err(|_| ConnectorError::new("aggregate_failed", "结果聚合失败"));
            }
        };

        let (mcp_result, mcp_message) =
            if request.capabilities.contains(&IntegrationCapability::Mcp) {
                // show → 按需 set → show 复核。从不直接写配置文件。
                let already = match show_superdev(self.runner.as_ref(), ctx, &program) {
                    Ok(Some(value)) if entry_matches(ctx, &value) => true,
                    Ok(_) => false,
                    Err(error) if error.code() == "invalid_cli_output" => {
                        return aggregate_connector_result(
                            CONNECTOR_ID.into(),
                            request.operation,
                            vec![
                                common::integration_result(
                                    IntegrationCapability::Mcp,
                                    IntegrationResult::Failed,
                                    config_hint(ctx),
                                    None,
                                    Some(error.message().into()),
                                ),
                                common::integration_result(
                                    IntegrationCapability::Skill,
                                    IntegrationResult::Skipped,
                                    Some(common::path_string(&skill)),
                                    None,
                                    Some("MCP 失败，已跳过 Skill".into()),
                                ),
                                common::manual_hook_result(None),
                            ],
                            Some(self.manual_instructions(ctx)?),
                            false,
                            Some("OpenClaw MCP 状态输出无效".into()),
                        )
                        .map_err(|_| ConnectorError::new("aggregate_failed", "结果聚合失败"));
                    }
                    Err(error) => return Err(error),
                };

                if !already {
                    let canonical = canonical_mcp_json(ctx);
                    let set_output = run_cli(
                        self.runner.as_ref(),
                        ctx,
                        &program,
                        &["mcp", "set", "superdev", &canonical],
                    )?;
                    if !set_output.success() {
                        tracing::error!(
                            connector_id = CONNECTOR_ID,
                            operation = "mcp_set",
                            status_code = set_output.status_code,
                            truncated = set_output.truncated,
                            "openclaw mcp set failed"
                        );
                        return aggregate_connector_result(
                            CONNECTOR_ID.into(),
                            request.operation,
                            vec![
                                common::integration_result(
                                    IntegrationCapability::Mcp,
                                    IntegrationResult::Failed,
                                    config_hint(ctx),
                                    None,
                                    Some("openclaw mcp set 返回非零退出码".into()),
                                ),
                                common::integration_result(
                                    IntegrationCapability::Skill,
                                    IntegrationResult::Skipped,
                                    Some(common::path_string(&skill)),
                                    None,
                                    Some("MCP 失败，已跳过 Skill".into()),
                                ),
                                common::manual_hook_result(None),
                            ],
                            Some(self.manual_instructions(ctx)?),
                            false,
                            Some("OpenClaw MCP 配置失败".into()),
                        )
                        .map_err(|_| ConnectorError::new("aggregate_failed", "结果聚合失败"));
                    }
                }

                match show_superdev(self.runner.as_ref(), ctx, &program) {
                    Ok(Some(value)) if entry_matches(ctx, &value) => (
                        if already {
                            IntegrationResult::AlreadyPresent
                        } else {
                            IntegrationResult::Installed
                        },
                        Some("MCP 已通过 openclaw CLI 配置".into()),
                    ),
                    Ok(_) => {
                        return aggregate_connector_result(
                            CONNECTOR_ID.into(),
                            request.operation,
                            vec![
                                common::integration_result(
                                    IntegrationCapability::Mcp,
                                    IntegrationResult::Failed,
                                    config_hint(ctx),
                                    None,
                                    Some("set 后 show 未能证明 superdev 已配置".into()),
                                ),
                                common::integration_result(
                                    IntegrationCapability::Skill,
                                    IntegrationResult::Skipped,
                                    Some(common::path_string(&skill)),
                                    None,
                                    Some("MCP 失败，已跳过 Skill".into()),
                                ),
                                common::manual_hook_result(None),
                            ],
                            Some(self.manual_instructions(ctx)?),
                            false,
                            Some("OpenClaw MCP 复核失败".into()),
                        )
                        .map_err(|_| ConnectorError::new("aggregate_failed", "结果聚合失败"));
                    }
                    Err(error) => return Err(error),
                }
            } else {
                let status = self.status(ctx)?;
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
            common::install_skill(ctx, &skill)
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

        let outcome = aggregate_connector_result(
            CONNECTOR_ID.into(),
            request.operation,
            vec![
                common::integration_result(
                    IntegrationCapability::Mcp,
                    mcp_result,
                    config_hint(ctx),
                    None,
                    mcp_message,
                ),
                skill_result,
                common::manual_hook_result(None),
            ],
            Some(self.manual_instructions(ctx)?),
            true,
            Some("OpenClaw 安装完成".into()),
        )
        .map_err(|_| ConnectorError::new("aggregate_failed", "结果聚合失败"))?;

        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = ?request.operation,
            result = ?outcome.result,
            duration_ms = started.elapsed().as_millis() as u64,
            "openclaw install finished"
        );
        Ok(outcome)
    }

    fn uninstall(
        &self,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorOperationOutcome, ConnectorError> {
        let started = Instant::now();
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = "uninstall",
            "openclaw uninstall started"
        );
        let skill = skill_path(ctx);
        let mut mcp_changed = false;
        let mut mcp_needs_action = false;
        let mut mcp_message = Some("未配置 superdev，跳过 unset".into());
        let mut mcp_result = IntegrationResult::AlreadyPresent;

        if let Ok(program) = require_cli(ctx) {
            let present = match show_superdev(self.runner.as_ref(), ctx, &program) {
                Ok(Some(_)) => true,
                Ok(None) => false,
                Err(error) => {
                    tracing::error!(
                        connector_id = CONNECTOR_ID,
                        operation = "uninstall",
                        error_code = error.code(),
                        "openclaw uninstall show failed"
                    );
                    return Err(error);
                }
            };
            if present {
                let output = run_cli(
                    self.runner.as_ref(),
                    ctx,
                    &program,
                    &["mcp", "unset", "superdev"],
                )?;
                if !output.success() {
                    return Ok(ConnectorOperationOutcome {
                        connector_id: CONNECTOR_ID.into(),
                        operation: ConnectorOperation::Uninstall,
                        result: ConnectorResult::Failed,
                        integrations: vec![
                            common::integration_result(
                                IntegrationCapability::Mcp,
                                IntegrationResult::Failed,
                                config_hint(ctx),
                                None,
                                Some("openclaw mcp unset 失败".into()),
                            ),
                            common::uninstall_skill(&skill),
                            common::integration_result(
                                IntegrationCapability::SessionHook,
                                IntegrationResult::Skipped,
                                None,
                                None,
                                Some("未管理 Session Hook".into()),
                            ),
                        ],
                        manual_instructions: Some(self.manual_instructions(ctx)?),
                        requires_restart: false,
                        message: Some("OpenClaw 卸载 MCP 失败".into()),
                    });
                }
                mcp_changed = true;
                mcp_result = IntegrationResult::Installed;
                mcp_message = Some("已通过 openclaw mcp unset 移除 superdev".into());
            }
        } else {
            // MCP 可能仍留在 OpenClaw 配置中，不能被 Skill 删除成功掩盖成整体 Success。
            mcp_needs_action = true;
            mcp_result = IntegrationResult::NeedsAction;
            mcp_message =
                Some("未找到 openclaw CLI，请手动运行 openclaw mcp unset superdev".into());
        }

        let skill_result = common::uninstall_skill(&skill);
        let skill_changed = matches!(skill_result.result, IntegrationResult::Installed);
        let changed = mcp_changed || skill_changed;
        let outcome = ConnectorOperationOutcome {
            connector_id: CONNECTOR_ID.into(),
            operation: ConnectorOperation::Uninstall,
            result: if mcp_needs_action && changed {
                ConnectorResult::Partial
            } else if mcp_needs_action {
                ConnectorResult::NeedsAction
            } else if changed {
                ConnectorResult::Success
            } else {
                ConnectorResult::Unchanged
            },
            integrations: vec![
                common::integration_result(
                    IntegrationCapability::Mcp,
                    mcp_result,
                    config_hint(ctx),
                    None,
                    mcp_message,
                ),
                skill_result,
                common::integration_result(
                    IntegrationCapability::SessionHook,
                    IntegrationResult::Skipped,
                    None,
                    None,
                    Some("未管理 Session Hook".into()),
                ),
            ],
            manual_instructions: if mcp_needs_action {
                Some(self.manual_instructions(ctx)?)
            } else {
                None
            },
            requires_restart: changed,
            message: Some("OpenClaw 卸载完成".into()),
        };
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = "uninstall",
            result = ?outcome.result,
            duration_ms = started.elapsed().as_millis() as u64,
            "openclaw uninstall finished"
        );
        Ok(outcome)
    }

    fn manual_instructions(
        &self,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorManualInstructions, ConnectorError> {
        let canonical = canonical_mcp_json(ctx);
        Ok(ConnectorManualInstructions {
            summary: "通过 openclaw 官方 CLI 接入 SuperDev MCP".into(),
            steps: vec![
                format!("运行: openclaw mcp set superdev '{}'", canonical),
                format!("Skill 目标目录: {}", skill_path(ctx).display()),
                "重启或重新加载 OpenClaw".into(),
                "可选验证: openclaw doctor --probe".into(),
                "Session Hook 需按 OpenClaw 文档手动配置".into(),
            ],
            config_path: config_hint(ctx),
            manual_config: Some(format!("openclaw mcp set superdev '{canonical}'")),
            verification_prompt: Some(
                "运行 openclaw mcp show superdev --json 确认条目；可选 openclaw doctor --probe"
                    .into(),
            ),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mcp_install::registry::ConnectorEnvironment;
    use std::collections::VecDeque;
    use std::sync::atomic::{AtomicU64, Ordering};
    use std::sync::Mutex;

    static COUNTER: AtomicU64 = AtomicU64::new(0);

    #[derive(Clone, Default)]
    struct FakeCommandRunner {
        calls: Arc<Mutex<Vec<Vec<String>>>>,
        responses: Arc<Mutex<VecDeque<Result<CommandOutput, ConnectorError>>>>,
    }

    impl FakeCommandRunner {
        fn succeed() -> Self {
            let runner = Self::default();
            // 默认：show missing → set ok → show configured（由测试填充具体 show body）
            runner
        }

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
            "openclaw-{}-{}-{}",
            label,
            std::process::id(),
            COUNTER.fetch_add(1, Ordering::Relaxed)
        ));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(&root).unwrap();
        root
    }

    fn context_with_cli(home: PathBuf) -> ConnectorRuntimeContext {
        let bin = home.join("bin");
        std::fs::create_dir_all(&bin).unwrap();
        let cli_name = if cfg!(windows) {
            "openclaw.exe"
        } else {
            "openclaw"
        };
        std::fs::write(bin.join(cli_name), "").unwrap();
        let skill_source = home.join("bundled-skill");
        std::fs::create_dir_all(skill_source.join("hooks")).unwrap();
        std::fs::write(skill_source.join("SKILL.md"), "fixture").unwrap();
        std::fs::write(skill_source.join("hooks/session-start"), "x").unwrap();
        ConnectorRuntimeContext::new(
            home.clone(),
            vec![bin],
            vec![],
            home.join("superdev-mcp"),
            Some(skill_source),
            None,
        )
    }

    fn install_request() -> ConnectorInstallRequest {
        ConnectorInstallRequest {
            operation: ConnectorOperation::Install,
            capabilities: vec![
                IntegrationCapability::Mcp,
                IntegrationCapability::Skill,
                IntegrationCapability::SessionHook,
            ],
        }
    }

    fn expected_canonical_json(ctx: &ConnectorRuntimeContext) -> String {
        canonical_mcp_json(ctx)
    }

    fn configured_show(ctx: &ConnectorRuntimeContext) -> String {
        expected_canonical_json(ctx)
    }

    #[test]
    fn install_uses_the_official_mcp_set_command() {
        let home = test_dir("install-set");
        let ctx = context_with_cli(home.clone());
        let runner = FakeCommandRunner::succeed();
        // show missing
        runner.push_output(1, "", "MCP server 'superdev' not found");
        // set ok
        runner.push_ok(0, "");
        // show configured
        runner.push_ok(0, &configured_show(&ctx));

        let connector = OpenClawConnector::with_runner(Arc::new(runner.clone()));
        let outcome = connector.install(&ctx, install_request()).unwrap();
        assert_eq!(outcome.integrations[0].result, IntegrationResult::Installed);
        assert_eq!(outcome.result, ConnectorResult::Partial);

        let argv = runner.argv();
        assert_eq!(
            argv,
            vec![
                vec![
                    "mcp".to_string(),
                    "show".to_string(),
                    "superdev".to_string(),
                    "--json".to_string()
                ],
                vec![
                    "mcp".to_string(),
                    "set".to_string(),
                    "superdev".to_string(),
                    expected_canonical_json(&ctx)
                ],
                vec![
                    "mcp".to_string(),
                    "show".to_string(),
                    "superdev".to_string(),
                    "--json".to_string()
                ],
            ]
        );
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn uninstall_uses_the_official_mcp_unset_command() {
        let home = test_dir("uninstall-unset");
        let ctx = context_with_cli(home.clone());
        let runner = FakeCommandRunner::succeed();
        runner.push_ok(0, &configured_show(&ctx));
        runner.push_ok(0, "");
        let connector = OpenClawConnector::with_runner(Arc::new(runner.clone()));
        let outcome = connector.uninstall(&ctx).unwrap();
        assert_eq!(outcome.result, ConnectorResult::Success);
        assert_eq!(
            runner.argv(),
            vec![
                vec![
                    "mcp".to_string(),
                    "show".to_string(),
                    "superdev".to_string(),
                    "--json".to_string()
                ],
                vec![
                    "mcp".to_string(),
                    "unset".to_string(),
                    "superdev".to_string()
                ],
            ]
        );
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn missing_cli_yields_failed_mcp_and_skipped_skill_with_manual_fallback() {
        let home = test_dir("no-cli");
        let skill_source = home.join("bundled-skill");
        std::fs::create_dir_all(skill_source.join("hooks")).unwrap();
        std::fs::write(skill_source.join("SKILL.md"), "x").unwrap();
        std::fs::write(skill_source.join("hooks/session-start"), "x").unwrap();
        let ctx = ConnectorRuntimeContext::new(
            home.clone(),
            vec![home.join("empty-bin")],
            vec![],
            home.join("superdev-mcp"),
            Some(skill_source),
            None,
        );
        std::fs::create_dir_all(home.join("empty-bin")).unwrap();
        let runner = FakeCommandRunner::succeed();
        let connector = OpenClawConnector::with_runner(Arc::new(runner.clone()));
        let outcome = connector.install(&ctx, install_request()).unwrap();
        assert_eq!(outcome.result, ConnectorResult::Failed);
        assert_eq!(outcome.integrations[0].result, IntegrationResult::Failed);
        assert_eq!(outcome.integrations[1].result, IntegrationResult::Skipped);
        assert!(outcome.manual_instructions.is_some());
        assert!(runner.argv().is_empty());
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn uninstall_without_cli_reports_partial_and_keeps_manual_mcp_remediation() {
        let home = test_dir("uninstall-no-cli");
        let skill_source = home.join("bundled-skill");
        std::fs::create_dir_all(skill_source.join("hooks")).unwrap();
        std::fs::write(skill_source.join("SKILL.md"), "x").unwrap();
        let installed_skill = home.join(".openclaw").join("skills").join("superdev");
        std::fs::create_dir_all(&installed_skill).unwrap();
        std::fs::write(installed_skill.join("SKILL.md"), "x").unwrap();
        let ctx = ConnectorRuntimeContext::new(
            home.clone(),
            vec![home.join("empty-bin")],
            vec![],
            home.join("superdev-mcp"),
            Some(skill_source),
            None,
        );
        std::fs::create_dir_all(home.join("empty-bin")).unwrap();
        let connector = OpenClawConnector::with_runner(Arc::new(FakeCommandRunner::succeed()));

        let outcome = connector.uninstall(&ctx).unwrap();
        assert_eq!(outcome.result, ConnectorResult::Partial);
        assert_eq!(
            outcome.integrations[0].result,
            IntegrationResult::NeedsAction
        );
        assert_eq!(outcome.integrations[1].result, IntegrationResult::Installed);
        assert!(outcome.manual_instructions.is_some());
        assert!(!installed_skill.exists());
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn nonzero_set_fails_mcp_and_skips_skill() {
        let home = test_dir("set-fail");
        let ctx = context_with_cli(home.clone());
        let runner = FakeCommandRunner::succeed();
        runner.push_output(1, "", "MCP server 'superdev' not found"); // show missing
        runner.push_ok(2, "boom"); // set fails
        let connector = OpenClawConnector::with_runner(Arc::new(runner));
        let outcome = connector.install(&ctx, install_request()).unwrap();
        assert_eq!(outcome.result, ConnectorResult::Failed);
        assert_eq!(outcome.integrations[0].result, IntegrationResult::Failed);
        assert_eq!(outcome.integrations[1].result, IntegrationResult::Skipped);
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn malformed_show_json_is_fail_closed() {
        let home = test_dir("bad-show");
        let ctx = context_with_cli(home.clone());
        let runner = FakeCommandRunner::succeed();
        runner.push_ok(0, "{not-json");
        let connector = OpenClawConnector::with_runner(Arc::new(runner));
        let outcome = connector.install(&ctx, install_request()).unwrap();
        assert_eq!(outcome.result, ConnectorResult::Failed);
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn nonzero_show_errors_are_not_misclassified_as_missing() {
        let home = test_dir("show-error");
        let ctx = context_with_cli(home.clone());
        let runner = FakeCommandRunner::succeed();
        runner.push_output(2, "", "OpenClaw configuration is invalid");
        let program = require_cli(&ctx).unwrap();

        let error = show_superdev(&runner, &ctx, &program).unwrap_err();
        assert_eq!(error.code(), "cli_show_failed");
        assert_eq!(runner.argv().len(), 1);
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn timeout_error_surfaces_stable_code_without_argv() {
        let home = test_dir("timeout");
        let ctx = context_with_cli(home.clone());
        let runner = FakeCommandRunner::succeed();
        runner.push_err("command_timeout", "命令执行超时");
        let connector = OpenClawConnector::with_runner(Arc::new(runner));
        let error = connector.install(&ctx, install_request()).unwrap_err();
        assert_eq!(error.code(), "command_timeout");
        assert!(!error.message().contains("mcp"));
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn windows_executable_name_is_detected() {
        let home = test_dir("win-name");
        let bin = home.join("bin");
        std::fs::create_dir_all(&bin).unwrap();
        // 在非 Windows 上 executable_file_names 只有 "openclaw"；
        // 这里直接验证 resolve 能命中写入的文件名列表中的任一候选。
        for name in executable_file_names("openclaw") {
            let _ = std::fs::remove_file(bin.join(&name));
        }
        let name = executable_file_names("openclaw")
            .into_iter()
            .next()
            .unwrap();
        std::fs::write(bin.join(&name), "").unwrap();
        let ctx = ConnectorRuntimeContext::new(
            home.clone(),
            vec![bin],
            vec![],
            home.join("mcp"),
            None,
            None,
        );
        assert!(resolve_cli(&ctx).is_some());
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn openclaw_config_and_state_paths_are_cli_env_hints() {
        let home = test_dir("env-hint");
        let override_path = home.join("custom-openclaw.json");
        let ctx = context_with_cli(home.clone()).with_environment(ConnectorEnvironment::new(
            None,
            Some(override_path.clone()),
            None,
        ));
        let runner = FakeCommandRunner::succeed();
        runner.push_output(1, "", "MCP server 'superdev' not found");
        runner.push_ok(0, "");
        runner.push_ok(0, &configured_show(&ctx));
        // 捕获 env：扩展 Fake 以记录 env
        type EnvPairs = Vec<(String, String)>;
        #[derive(Clone)]
        struct EnvCapture {
            inner: FakeCommandRunner,
            envs: Arc<Mutex<Vec<EnvPairs>>>,
        }
        impl CommandRunner for EnvCapture {
            fn run(&self, spec: CommandSpec) -> Result<CommandOutput, ConnectorError> {
                let env: Vec<_> = spec
                    .env
                    .iter()
                    .map(|(k, v)| {
                        (
                            k.to_string_lossy().into_owned(),
                            v.to_string_lossy().into_owned(),
                        )
                    })
                    .collect();
                self.envs.lock().unwrap().push(env);
                self.inner.run(spec)
            }
        }
        let capture = EnvCapture {
            inner: runner,
            envs: Arc::new(Mutex::new(Vec::new())),
        };
        let connector = OpenClawConnector::with_runner(Arc::new(capture.clone()));
        connector.install(&ctx, install_request()).unwrap();
        let envs = capture.envs.lock().unwrap();
        assert!(!envs.is_empty());
        assert!(envs.iter().all(|call| {
            let has_config = call.iter().any(|(k, v)| {
                k == "OPENCLAW_CONFIG_PATH" && Path::new(v) == override_path.as_path()
            });
            let has_state = call.iter().any(|(k, v)| {
                k == "OPENCLAW_STATE_DIR" && Path::new(v) == data_root(&ctx).as_path()
            });
            has_config && has_state
        }));
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
    fn remote_launch_spec_reaches_canonical_mcp_json() {
        let home = test_dir("openclaw-remote-launch");
        let ctx = context_with_cli(home.clone()).with_mcp_launch(remote_launch_spec());

        let raw = canonical_mcp_json(&ctx);
        let value: serde_json::Value = serde_json::from_str(&raw).expect("canonical json");
        assert_eq!(value["command"], "/opt/superdev/superdev-agent");
        assert_eq!(value["args"], serde_json::json!(["mcp"]));
        assert_eq!(value["env"]["SUPERDEV_AGENT_URL"], "http://10.1.2.3:57117");
        assert!(
            entry_matches(&ctx, &value),
            "远端 spec 写出的条目必须被自己的匹配器认成已配置: {raw}"
        );
        let _ = std::fs::remove_dir_all(home);
    }

    #[test]
    fn local_canonical_mcp_json_omits_the_args_key() {
        let home = test_dir("openclaw-local-launch");
        let ctx = context_with_cli(home.clone());

        let raw = canonical_mcp_json(&ctx);
        assert!(
            !raw.contains("args"),
            "本机 args 为空时不得写 args 键（字节等价约束）: {raw}"
        );
        let _ = std::fs::remove_dir_all(home);
    }

}
