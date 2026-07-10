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
use crate::mcp_install::{executable_file_names, DEFAULT_AGENT_URL};
use std::ffi::OsString;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::{Duration, Instant};

const CONNECTOR_ID: &str = "openclaw";
const DISPLAY_NAME: &str = "OpenClaw";
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
            descriptor: common::descriptor(
                CONNECTOR_ID,
                DISPLAY_NAME,
                SupportMode::Manual,
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
                SupportMode::Manual,
                None,
            ),
            runner,
        }
    }
}

fn skill_path(ctx: &ConnectorRuntimeContext) -> PathBuf {
    ctx.home_dir().join(".openclaw").join("skills").join("superdev")
}

fn data_root(ctx: &ConnectorRuntimeContext) -> PathBuf {
    ctx.home_dir().join(".openclaw")
}

/// resolve_cli 在 command_dirs 中查找 openclaw / openclaw.exe。
fn resolve_cli(ctx: &ConnectorRuntimeContext) -> Option<PathBuf> {
    ctx.command_dirs().iter().find_map(|directory| {
        executable_file_names("openclaw")
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

/// canonical_mcp_json 仅包含 SuperDev stdio 命令与 SUPERDEV_AGENT_URL。
fn canonical_mcp_json(ctx: &ConnectorRuntimeContext) -> String {
    serde_json::to_string(&serde_json::json!({
        "command": ctx.mcp_binary().to_string_lossy(),
        "env": {
            "SUPERDEV_AGENT_URL": DEFAULT_AGENT_URL
        }
    }))
    .expect("canonical mcp json")
}

fn apply_config_env(mut spec: CommandSpec, ctx: &ConnectorRuntimeContext) -> CommandSpec {
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
        // 缺失条目时 CLI 可能非零退出；视为未配置而非硬失败。
        if output.status_code.is_some() {
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
        ConnectorError::new("invalid_cli_output", "openclaw mcp show --json 输出无法解析")
    })?;
    if value.is_null() {
        Ok(None)
    } else {
        Ok(Some(value))
    }
}

fn entry_matches(ctx: &ConnectorRuntimeContext, value: &serde_json::Value) -> bool {
    let expected_command = ctx.mcp_binary().to_string_lossy();
    let command = value
        .get("command")
        .and_then(|v| v.as_str())
        .unwrap_or("");
    let agent_url = value
        .get("env")
        .and_then(|env| env.get("SUPERDEV_AGENT_URL"))
        .and_then(|v| v.as_str())
        .unwrap_or("");
    command == expected_command.as_ref() && agent_url == DEFAULT_AGENT_URL
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
            requires_restart: mcp_status == IntegrationStateStatus::Configured,
            message: Some("OpenClaw 状态已读取".into()),
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
                let output =
                    run_cli(self.runner.as_ref(), ctx, &program, &["mcp", "unset", "superdev"])?;
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
            mcp_result = IntegrationResult::Skipped;
            mcp_message = Some("未找到 openclaw CLI，跳过 MCP 卸载".into());
        }

        let skill_result = common::uninstall_skill(&skill);
        let skill_changed = matches!(skill_result.result, IntegrationResult::Installed);
        let changed = mcp_changed || skill_changed;
        let outcome = ConnectorOperationOutcome {
            connector_id: CONNECTOR_ID.into(),
            operation: ConnectorOperation::Uninstall,
            result: if changed {
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
            manual_instructions: None,
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
                format!(
                    "Skill 目标目录: {}",
                    skill_path(ctx).display()
                ),
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
            self.responses.lock().unwrap().push_back(Ok(CommandOutput {
                status_code: Some(status),
                stdout: stdout.into(),
                stderr: String::new(),
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
        runner.push_ok(1, "");
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
    fn nonzero_set_fails_mcp_and_skips_skill() {
        let home = test_dir("set-fail");
        let ctx = context_with_cli(home.clone());
        let runner = FakeCommandRunner::succeed();
        runner.push_ok(1, ""); // show missing
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
        let name = executable_file_names("openclaw").into_iter().next().unwrap();
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
    fn openclaw_config_path_is_env_only_hint() {
        let home = test_dir("env-hint");
        let override_path = home.join("custom-openclaw.json");
        let ctx = context_with_cli(home.clone()).with_environment(ConnectorEnvironment::new(
            None,
            Some(override_path.clone()),
            None,
        ));
        let runner = FakeCommandRunner::succeed();
        runner.push_ok(1, "");
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
            call.iter().any(|(k, v)| {
                k == "OPENCLAW_CONFIG_PATH" && Path::new(v) == override_path.as_path()
            })
        }));
        let _ = std::fs::remove_dir_all(home);
    }
}
