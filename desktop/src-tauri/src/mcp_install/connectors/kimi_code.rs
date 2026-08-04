// kimi_code.rs 实现 Kimi Code 内置 Agent Connector。
//
// 职责：
//   - 解析 KIMI_CODE_HOME 或 ~/.kimi-code 下的 mcp.json 与 skills/superdev
//   - 通过严格 JSON 合并写入 mcpServers.superdev
//   - 在 MCP 写入成功后安装 Skill；Session Hook 保持手动
//
// 边界：
//   - 不引入 AgentKind 变体
//   - 不直接读取环境变量（覆盖路径来自 ConnectorEnvironment）
//   - 日志不记录路径、配置正文或密钥

use super::common;
use crate::mcp_install::contracts::*;
use crate::mcp_install::fs_port::{ConnectorFs, LocalFs};
use crate::mcp_install::registry::*;
use crate::mcp_install::{
    executable_file_names, mcp_server_json_value, merge_json_config, remove_json_superdev_config,
};
// DEFAULT_AGENT_URL 只剩测试在用：生产路径已全部改走 ctx.mcp_launch()。
#[cfg(test)]
use crate::mcp_install::DEFAULT_AGENT_URL;
#[cfg(test)]
use std::fs;
use std::path::{Path, PathBuf};
use std::time::Instant;

const CONNECTOR_ID: &str = "kimi-code";
const DISPLAY_NAME: &str = "Kimi Code";
/// CLI_COMMAND 是 find_cli 探测的命令名，同时也是 cli_commands() 对外汇报的值——
/// 注意它与 CONNECTOR_ID 不同（CLI 叫 "kimi"，连接器 ID 叫 "kimi-code"），两处
/// 共用这一个常量，避免各写一份字符串导致命令名漂移。
const CLI_COMMAND: &str = "kimi";

/// KimiCodeConnector 适配 Kimi Code 的标准 JSON MCP 与 Skill。
pub(super) struct KimiCodeConnector {
    descriptor: AgentConnectorDescriptor,
}

impl KimiCodeConnector {
    /// new 创建标准支持级别（自动 MCP+Skill，手动 Hook）的 Kimi Code 连接器。
    pub(super) fn new() -> Self {
        Self {
            descriptor: common::descriptor(CONNECTOR_ID, DISPLAY_NAME, SupportMode::Manual, None),
        }
    }
}

/// data_root 解析 Kimi Code 数据根目录。
///
/// 优先使用环境覆盖，否则为 `~/.kimi-code`。
fn data_root(ctx: &ConnectorRuntimeContext) -> PathBuf {
    ctx.environment()
        .kimi_code_home()
        .map(Path::to_path_buf)
        .unwrap_or_else(|| ctx.home_dir().join(".kimi-code"))
}

/// config_path 返回 mcp.json 绝对路径。
fn config_path(ctx: &ConnectorRuntimeContext) -> PathBuf {
    data_root(ctx).join("mcp.json")
}

/// skill_path 返回 owned Skill 目录路径。
fn skill_path(ctx: &ConnectorRuntimeContext) -> PathBuf {
    data_root(ctx).join("skills").join("superdev")
}

/// find_cli 在 command_dirs 中查找 kimi / kimi.exe 等可执行文件。
fn find_cli(ctx: &ConnectorRuntimeContext) -> Option<PathBuf> {
    ctx.command_dirs().iter().find_map(|directory| {
        executable_file_names(CLI_COMMAND)
            .into_iter()
            .map(|name| directory.join(name))
            .find(|path| path.is_file())
    })
}

/// map_merge_error 将 JSON 合并错误映射为稳定连接器错误码。
fn map_merge_error(error: String) -> ConnectorError {
    if error.contains("格式异常") {
        ConnectorError::new("invalid_config", error)
    } else {
        ConnectorError::new("config_transform_failed", error)
    }
}

/// mcp_configured 判断配置中的 superdev 条目是否匹配当前 MCP 二进制与默认 URL。
fn mcp_configured(ctx: &ConnectorRuntimeContext, content: &str) -> Result<bool, ConnectorError> {
    let root: serde_json::Value = serde_json::from_str(content).map_err(|error| {
        ConnectorError::new("invalid_config", format!("配置 JSON 无法解析: {error}"))
    })?;
    let Some(server) = root
        .get("mcpServers")
        .and_then(|servers| servers.get("superdev"))
    else {
        return Ok(false);
    };
    let command = server
        .get("command")
        .and_then(|value| value.as_str())
        .unwrap_or("");
    // args 键缺席等价于空数组（本机场景恒如此，判断结果与改造前一致）；写入侧
    // （common::entry → mcp_server_json_value）已经按启动规格写 args，判定侧不跟上
    // 就会在远端把「命令对了但缺 mcp 子命令」的坏配置误判成已配置。
    let args: Vec<&str> = server
        .get("args")
        .and_then(|value| value.as_array())
        .map(|items| items.iter().filter_map(|item| item.as_str()).collect())
        .unwrap_or_default();
    let agent_url = server
        .get("env")
        .and_then(|env| env.get("SUPERDEV_AGENT_URL"))
        .and_then(|value| value.as_str())
        .unwrap_or("");
    let launch = ctx.mcp_launch();
    let expected = launch.command.to_string_lossy();
    Ok(command == expected.as_ref() && args == launch.args && agent_url == launch.agent_url)
}

/// install_mcp 通过安全突变写入 mcpServers.superdev。
fn install_mcp(
    fs_port: &dyn ConnectorFs,
    ctx: &ConnectorRuntimeContext,
) -> Result<common::FileMutationOutcome, ConnectorError> {
    let path = config_path(ctx);
    let entry = common::entry(ctx);
    common::mutate_config_with_fs(fs_port, CONNECTOR_ID, &path, |existing| {
        merge_json_config(existing, &entry).map_err(map_merge_error)
    })
}

/// remove_mcp 仅移除 mcpServers.superdev。
fn remove_mcp(
    fs_port: &dyn ConnectorFs,
    ctx: &ConnectorRuntimeContext,
) -> Result<common::FileMutationOutcome, ConnectorError> {
    let path = config_path(ctx);
    common::remove_config_with_fs(fs_port, CONNECTOR_ID, &path, |existing| {
        remove_json_superdev_config(existing).map_err(map_merge_error)
    })
}

/// pasteable_mcp_config 生成可粘贴的 mcpServers.superdev 片段。
fn pasteable_mcp_config(ctx: &ConnectorRuntimeContext) -> String {
    let entry = common::entry(ctx);
    serde_json::to_string_pretty(&serde_json::json!({
        "mcpServers": { "superdev": mcp_server_json_value(&entry) }
    }))
    .unwrap_or_else(|_| "{}".into())
}

impl AgentConnector for KimiCodeConnector {
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
            "kimi code detect started"
        );
        let cli = find_cli(ctx);
        let root = data_root(ctx);
        let config = config_path(ctx);
        // detect 是【本机】操作，刻意保留直连 std::fs：远端场景下 CLI 存在性
        // 一律来自目标机的 `/api/integrations/detect` 端点，编排层从不调用连接器
        // 自己的 detect()。
        let hit = cli
            .or_else(|| root.is_dir().then_some(root))
            .or_else(|| config.exists().then_some(config));
        let result = ConnectorDetection {
            detected: hit.is_some(),
            detection_path: hit,
            message: Some("Kimi Code 检测完成".into()),
        };
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = "detect",
            detected = result.detected,
            duration_ms = started.elapsed().as_millis() as u64,
            "kimi code detect finished"
        );
        Ok(result)
    }

    fn status(&self, ctx: &ConnectorRuntimeContext) -> Result<ConnectorStatus, ConnectorError> {
        self.status_with_fs(ctx, &LocalFs)
    }

    fn install(
        &self,
        ctx: &ConnectorRuntimeContext,
        request: ConnectorInstallRequest,
    ) -> Result<ConnectorOperationOutcome, ConnectorError> {
        self.install_with_fs(ctx, request, &LocalFs)
    }

    fn uninstall(
        &self,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorOperationOutcome, ConnectorError> {
        self.uninstall_with_fs(ctx, &LocalFs)
    }

    fn port_ops(&self) -> Option<&dyn PortedConnectorOps> {
        Some(self)
    }

    fn manual_instructions(
        &self,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorManualInstructions, ConnectorError> {
        let config = config_path(ctx);
        let skill = skill_path(ctx);
        Ok(ConnectorManualInstructions {
            summary: "手动将 SuperDev 接入 Kimi Code".into(),
            steps: vec![
                format!("将以下 mcpServers.superdev 写入 {}", config.display()),
                format!("确认 Skill 目录存在：{}", skill.display()),
                "重启 Kimi Code 使 MCP 生效".into(),
                "在 Kimi Code TUI 输入 `/mcp` 确认 superdev 已被发现".into(),
                "如需检查或调整 MCP 配置，在 TUI 输入 `/mcp-config`".into(),
                "按需手动配置 Session Hook（本连接器不自动写入 Hook）".into(),
            ],
            config_path: Some(common::path_string(&config)),
            manual_config: Some(pasteable_mcp_config(ctx)),
            verification_prompt: Some(
                "重启 Kimi Code 后在 TUI 输入 /mcp，确认 superdev 出现；使用 /mcp-config 检查配置"
                    .into(),
            ),
        })
    }
}

impl PortedConnectorOps for KimiCodeConnector {
    fn status_with_fs(
        &self,
        ctx: &ConnectorRuntimeContext,
        fs_port: &dyn ConnectorFs,
    ) -> Result<ConnectorStatus, ConnectorError> {
        let started = Instant::now();
        tracing::debug!(
            connector_id = CONNECTOR_ID,
            operation = "status",
            "kimi code status started"
        );
        let config = config_path(ctx);
        let skill = skill_path(ctx);
        let (mcp_status, mcp_message) = match fs_port.read_optional(&config) {
            Ok(Some(content)) => match mcp_configured(ctx, &content) {
                Ok(true) => (
                    IntegrationStateStatus::Configured,
                    Some("SuperDev MCP 已配置".into()),
                ),
                Ok(false) => {
                    if content.trim().is_empty() {
                        (
                            IntegrationStateStatus::Missing,
                            Some("SuperDev MCP 配置缺失".into()),
                        )
                    } else if serde_json::from_str::<serde_json::Value>(&content).is_err() {
                        (
                            IntegrationStateStatus::Error,
                            Some("配置 JSON 无法解析".into()),
                        )
                    } else if content.contains("\"superdev\"") {
                        (
                            IntegrationStateStatus::NeedsAction,
                            Some("SuperDev MCP 配置不完整或不匹配".into()),
                        )
                    } else {
                        (
                            IntegrationStateStatus::Missing,
                            Some("SuperDev MCP 配置缺失".into()),
                        )
                    }
                }
                Err(error) => {
                    tracing::error!(
                        connector_id = CONNECTOR_ID,
                        operation = "status",
                        error_code = error.code(),
                        duration_ms = started.elapsed().as_millis() as u64,
                        "kimi code status parse failed"
                    );
                    return Err(error);
                }
            },
            Ok(None) => (
                IntegrationStateStatus::Missing,
                Some("MCP 配置文件不存在".into()),
            ),
            Err(error) => {
                tracing::error!(
                    connector_id = CONNECTOR_ID,
                    operation = "status",
                    error_code = "config_read_failed",
                    duration_ms = started.elapsed().as_millis() as u64,
                    "kimi code status read failed"
                );
                return Err(ConnectorError::new(
                    "config_read_failed",
                    format!("读取 Kimi Code 配置失败: {error}"),
                ));
            }
        };
        let skill_state = common::skill_status(fs_port, ctx, &skill);
        // Hook 始终手动：状态侧标记 Missing，操作侧返回 NeedsAction。
        let hook_state = IntegrationState {
            capability: IntegrationCapability::SessionHook,
            status: IntegrationStateStatus::Missing,
            target_path: None,
            message: Some("Session Hook 需手动配置".into()),
        };
        // 仅在可读 JSON 时回填运行时字段；不在成功路径塞全局 message，避免设置页误显示告警。
        let (mcp_command, agent_url) = fs_port
            .read_optional(&config)
            .ok()
            .flatten()
            .and_then(|content| serde_json::from_str::<serde_json::Value>(&content).ok())
            .map(|value| common::extract_json_mcp_runtime(&value))
            .unwrap_or((None, None));
        let result = ConnectorStatus {
            integrations: vec![
                IntegrationState {
                    capability: IntegrationCapability::Mcp,
                    status: mcp_status,
                    target_path: Some(common::path_string(&config)),
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
            "kimi code status finished"
        );
        Ok(result)
    }

    fn install_with_fs(
        &self,
        ctx: &ConnectorRuntimeContext,
        request: ConnectorInstallRequest,
        fs_port: &dyn ConnectorFs,
    ) -> Result<ConnectorOperationOutcome, ConnectorError> {
        let started = Instant::now();
        let capability_count = request.capabilities.len();
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = ?request.operation,
            capability_count,
            "kimi code install started"
        );

        let config = config_path(ctx);
        let skill = skill_path(ctx);
        let (mcp_result, mcp_backup, mcp_message) =
            if request.capabilities.contains(&IntegrationCapability::Mcp) {
                match install_mcp(fs_port, ctx) {
                    Ok(outcome) => (
                        if outcome.changed {
                            IntegrationResult::Installed
                        } else {
                            IntegrationResult::AlreadyPresent
                        },
                        outcome.backup_path,
                        Some("MCP 配置已处理".into()),
                    ),
                    Err(error) => {
                        // 畸形 JSON 等在 transform 阶段失败：无备份、无覆盖。
                        tracing::error!(
                            connector_id = CONNECTOR_ID,
                            operation = ?request.operation,
                            capability = "mcp",
                            error_code = error.code(),
                            duration_ms = started.elapsed().as_millis() as u64,
                            "kimi code mcp install failed"
                        );
                        let outcome = aggregate_connector_result(
                            CONNECTOR_ID.into(),
                            request.operation,
                            vec![
                                common::integration_result(
                                    IntegrationCapability::Mcp,
                                    IntegrationResult::Failed,
                                    Some(common::path_string(&config)),
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
                            Some("Kimi Code MCP 配置失败".into()),
                        )
                        .map_err(|_| ConnectorError::new("aggregate_failed", "结果聚合失败"))?;
                        return Ok(outcome);
                    }
                }
            } else {
                // 未请求 MCP 时只读状态，避免重试其它能力时改写用户配置。
                let status = self.status_with_fs(ctx, fs_port)?;
                let mcp = status
                    .integrations
                    .iter()
                    .find(|item| item.capability == IntegrationCapability::Mcp)
                    .expect("mcp status");
                (
                    match mcp.status {
                        IntegrationStateStatus::Configured => IntegrationResult::AlreadyPresent,
                        IntegrationStateStatus::NeedsAction | IntegrationStateStatus::Missing => {
                            IntegrationResult::NeedsAction
                        }
                        _ => IntegrationResult::Failed,
                    },
                    None,
                    mcp.message.clone(),
                )
            };

        // Skill 安装门控：必须先用 post-write status 证明 MCP 已配置。
        // 否则会在半配置状态下写入 Skill，造成「Skill 在但 MCP 不可用」的假成功。
        let status_after = self.status_with_fs(ctx, fs_port)?;
        let mcp_ready = status_after.integrations.iter().any(|item| {
            item.capability == IntegrationCapability::Mcp
                && item.status == IntegrationStateStatus::Configured
        });

        let skill_result = if !mcp_ready {
            common::integration_result(
                IntegrationCapability::Skill,
                IntegrationResult::Skipped,
                Some(common::path_string(&skill)),
                None,
                Some("MCP 未就绪，已跳过 Skill".into()),
            )
        } else if request.capabilities.contains(&IntegrationCapability::Skill) {
            let result = common::install_skill(fs_port, ctx, &skill);
            tracing::info!(
                connector_id = CONNECTOR_ID,
                capability = "skill",
                result = ?result.result,
                "kimi code skill install finished"
            );
            result
        } else {
            let skill_state = common::skill_status(fs_port, ctx, &skill);
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

        // Hook 保持 Manual：聚合结果为 Partial（MCP 成功 + Hook NeedsAction）。
        // 这是 Standard 支持级别的有意设计，而非失败。
        let hook_result = common::manual_hook_result(None);
        let outcome = aggregate_connector_result(
            CONNECTOR_ID.into(),
            request.operation,
            vec![
                common::integration_result(
                    IntegrationCapability::Mcp,
                    mcp_result,
                    Some(common::path_string(&config)),
                    mcp_backup,
                    mcp_message,
                ),
                skill_result,
                hook_result,
            ],
            Some(self.manual_instructions(ctx)?),
            true,
            Some("Kimi Code 安装完成".into()),
        )
        .map_err(|_| ConnectorError::new("aggregate_failed", "结果聚合失败"))?;

        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = ?request.operation,
            result = ?outcome.result,
            duration_ms = started.elapsed().as_millis() as u64,
            "kimi code install finished"
        );
        Ok(outcome)
    }

    fn uninstall_with_fs(
        &self,
        ctx: &ConnectorRuntimeContext,
        fs_port: &dyn ConnectorFs,
    ) -> Result<ConnectorOperationOutcome, ConnectorError> {
        let started = Instant::now();
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = "uninstall",
            "kimi code uninstall started"
        );
        let config = config_path(ctx);
        let skill = skill_path(ctx);

        let mcp_outcome = match remove_mcp(fs_port, ctx) {
            Ok(outcome) => outcome,
            Err(error) if error.code() == "unsafe_config_target" => {
                tracing::error!(
                    connector_id = CONNECTOR_ID,
                    operation = "uninstall",
                    error_code = error.code(),
                    "kimi code uninstall rejected unsafe target"
                );
                return Err(error);
            }
            Err(error) if error.code() == "invalid_config" => {
                // 畸形配置不覆盖、不备份；MCP 卸载标记 Failed。
                let skill_result = common::uninstall_skill(fs_port, &skill);
                let changed = matches!(skill_result.result, IntegrationResult::Installed);
                return Ok(ConnectorOperationOutcome {
                    connector_id: CONNECTOR_ID.into(),
                    operation: ConnectorOperation::Uninstall,
                    result: ConnectorResult::Failed,
                    integrations: vec![
                        common::integration_result(
                            IntegrationCapability::Mcp,
                            IntegrationResult::Failed,
                            Some(common::path_string(&config)),
                            None,
                            Some(error.message().into()),
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
                    manual_instructions: Some(self.manual_instructions(ctx)?),
                    requires_restart: changed,
                    message: Some("Kimi Code 卸载遇到无效配置".into()),
                });
            }
            Err(error) => {
                tracing::error!(
                    connector_id = CONNECTOR_ID,
                    operation = "uninstall",
                    error_code = error.code(),
                    duration_ms = started.elapsed().as_millis() as u64,
                    "kimi code uninstall failed"
                );
                return Err(error);
            }
        };

        let skill_result = common::uninstall_skill(fs_port, &skill);
        let mcp_changed = mcp_outcome.changed;
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
                    if mcp_changed {
                        IntegrationResult::Installed
                    } else {
                        IntegrationResult::AlreadyPresent
                    },
                    Some(common::path_string(&config)),
                    mcp_outcome.backup_path,
                    Some("已移除 superdev MCP 条目".into()),
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
            message: Some("Kimi Code 卸载完成".into()),
        };
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = "uninstall",
            result = ?outcome.result,
            duration_ms = started.elapsed().as_millis() as u64,
            "kimi code uninstall finished"
        );
        Ok(outcome)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mcp_install::registry::ConnectorEnvironment;
    use std::sync::atomic::{AtomicU64, Ordering};

    static COUNTER: AtomicU64 = AtomicU64::new(0);

    fn test_dir(label: &str) -> PathBuf {
        let root = std::env::temp_dir().join(format!(
            "kimi-code-{}-{}-{}",
            label,
            std::process::id(),
            COUNTER.fetch_add(1, Ordering::Relaxed)
        ));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir_all(&root).unwrap();
        root
    }

    fn context_at(home: PathBuf) -> ConnectorRuntimeContext {
        let skill_source = home.join("bundled-skill");
        fs::create_dir_all(skill_source.join("hooks")).unwrap();
        fs::write(skill_source.join("SKILL.md"), "fixture skill").unwrap();
        fs::write(skill_source.join("hooks/session-start"), "#!/bin/sh\n").unwrap();
        ConnectorRuntimeContext::new(
            home.clone(),
            vec![home.join("bin")],
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

    #[test]
    fn descriptor_is_standard_and_uses_the_open_kimi_code_id() {
        let descriptor = KimiCodeConnector::new().descriptor().clone();
        assert_eq!(descriptor.id(), "kimi-code");
        assert_eq!(descriptor.support_level(), Some(SupportLevel::Standard));
    }

    #[test]
    fn kimi_code_home_override_controls_config_and_skill_paths() {
        let root = test_dir("kimi-home");
        let ctx = context_at(root.clone()).with_environment(ConnectorEnvironment::new(
            None,
            None,
            Some(root.clone()),
        ));
        assert_eq!(config_path(&ctx), root.join("mcp.json"));
        assert_eq!(skill_path(&ctx), root.join("skills").join("superdev"));
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn default_paths_use_native_separators_under_home() {
        let home = test_dir("default-home");
        let ctx = context_at(home.clone());
        assert_eq!(config_path(&ctx), home.join(".kimi-code").join("mcp.json"));
        assert_eq!(
            skill_path(&ctx),
            home.join(".kimi-code").join("skills").join("superdev")
        );
        let _ = fs::remove_dir_all(home);
    }

    #[test]
    fn install_update_uninstall_preserve_unrelated_json_and_are_idempotent() {
        let home = test_dir("roundtrip");
        let bin = home.join("bin");
        fs::create_dir_all(&bin).unwrap();
        fs::write(bin.join("kimi"), "").unwrap();
        let root = home.join(".kimi-code");
        fs::create_dir_all(&root).unwrap();
        let config = root.join("mcp.json");
        let fixture = r#"{
  "theme": "dark",
  "mcpServers": {
    "other": {
      "command": "other-mcp"
    }
  }
}
"#;
        fs::write(&config, fixture).unwrap();
        let ctx = context_at(home.clone());
        let connector = KimiCodeConnector::new();

        let first = connector.install(&ctx, install_request()).unwrap();
        assert_eq!(first.result, ConnectorResult::Partial);
        assert_eq!(first.integrations[0].result, IntegrationResult::Installed);
        assert_eq!(first.integrations[2].result, IntegrationResult::NeedsAction);
        assert!(first.integrations.iter().any(|item| item.capability
            == IntegrationCapability::Skill
            && matches!(
                item.result,
                IntegrationResult::Installed | IntegrationResult::AlreadyPresent
            )));

        let after = fs::read_to_string(&config).unwrap();
        let value: serde_json::Value = serde_json::from_str(&after).unwrap();
        assert_eq!(value["theme"], "dark");
        assert_eq!(value["mcpServers"]["other"]["command"], "other-mcp");
        assert_eq!(
            value["mcpServers"]["superdev"]["command"],
            ctx.mcp_binary().to_string_lossy().as_ref()
        );
        assert_eq!(
            value["mcpServers"]["superdev"]["env"]["SUPERDEV_AGENT_URL"],
            DEFAULT_AGENT_URL
        );
        // 仅写入 command 与 env.SUPERDEV_AGENT_URL。
        let superdev = value["mcpServers"]["superdev"].as_object().unwrap();
        assert_eq!(superdev.len(), 2);
        assert!(superdev.contains_key("command"));
        assert!(superdev.contains_key("env"));

        let second = connector
            .install(
                &ctx,
                ConnectorInstallRequest {
                    operation: ConnectorOperation::Update,
                    capabilities: vec![
                        IntegrationCapability::Mcp,
                        IntegrationCapability::Skill,
                        IntegrationCapability::SessionHook,
                    ],
                },
            )
            .unwrap();
        assert!(matches!(
            second.integrations[0].result,
            IntegrationResult::AlreadyPresent | IntegrationResult::Installed
        ));
        assert_eq!(second.result, ConnectorResult::Partial);

        let removed = connector.uninstall(&ctx).unwrap();
        assert_eq!(removed.result, ConnectorResult::Success);
        let final_value: serde_json::Value =
            serde_json::from_str(&fs::read_to_string(&config).unwrap()).unwrap();
        assert_eq!(final_value["theme"], "dark");
        assert_eq!(final_value["mcpServers"]["other"]["command"], "other-mcp");
        assert!(final_value["mcpServers"].get("superdev").is_none());
        assert!(!skill_path(&ctx).exists());

        let again = connector.uninstall(&ctx).unwrap();
        assert_eq!(again.result, ConnectorResult::Unchanged);
        let _ = fs::remove_dir_all(home);
    }

    #[test]
    fn malformed_json_is_rejected_without_backup_or_overwrite() {
        let home = test_dir("malformed");
        let root = home.join(".kimi-code");
        fs::create_dir_all(&root).unwrap();
        let config = root.join("mcp.json");
        fs::write(&config, "{broken").unwrap();
        let before = fs::read(&config).unwrap();
        let ctx = context_at(home.clone());
        let connector = KimiCodeConnector::new();
        let outcome = connector.install(&ctx, install_request()).unwrap();
        assert_eq!(outcome.result, ConnectorResult::Failed);
        assert_eq!(outcome.integrations[0].result, IntegrationResult::Failed);
        assert_eq!(outcome.integrations[1].result, IntegrationResult::Skipped);
        assert_eq!(fs::read(&config).unwrap(), before);
        // 无 .superdev-bak 旁路文件。
        let backups: Vec<_> = fs::read_dir(&root)
            .unwrap()
            .filter_map(Result::ok)
            .map(|entry| entry.file_name().to_string_lossy().into_owned())
            .filter(|name| name.contains("superdev-bak"))
            .collect();
        assert!(backups.is_empty(), "malformed must not create backup");
        let _ = fs::remove_dir_all(home);
    }

    #[test]
    fn detect_finds_cli_or_data_root() {
        let home = test_dir("detect");
        let bin = home.join("bin");
        fs::create_dir_all(&bin).unwrap();
        fs::write(bin.join("kimi"), "").unwrap();
        let ctx = context_at(home.clone());
        let hit = KimiCodeConnector::new().detect(&ctx).unwrap();
        assert!(hit.detected);

        let home2 = test_dir("detect-root");
        let root = home2.join(".kimi-code");
        fs::create_dir_all(&root).unwrap();
        let ctx2 = context_at(home2.clone());
        assert!(KimiCodeConnector::new().detect(&ctx2).unwrap().detected);
        let _ = fs::remove_dir_all(home);
        let _ = fs::remove_dir_all(home2);
    }

    #[test]
    fn manual_verification_uses_current_tui_commands() {
        let home = test_dir("manual-commands");
        let ctx = context_at(home.clone());
        let manual = KimiCodeConnector::new().manual_instructions(&ctx).unwrap();
        let text = format!(
            "{}\n{}\n{}",
            manual.steps.join("\n"),
            manual.verification_prompt.unwrap_or_default(),
            manual.manual_config.unwrap_or_default()
        );
        assert!(!text.contains("kimi mcp list"), "{text}");
        assert!(text.contains("/mcp"), "{text}");
        assert!(text.contains("/mcp-config"), "{text}");
        let _ = fs::remove_dir_all(home);
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
    fn remote_launch_spec_is_recognised_as_configured() {
        let home = test_dir("kimi-remote-launch");
        let ctx = context_at(home.clone()).with_mcp_launch(remote_launch_spec());

        let entry = common::entry(&ctx);
        let content = serde_json::json!({
            "mcpServers": { "superdev": mcp_server_json_value(&entry) }
        })
        .to_string();
        assert!(
            mcp_configured(&ctx, &content).expect("status parse"),
            "远端 spec 写出的配置必须被自己的状态判定认成已配置: {content}"
        );
        assert!(
            content.contains("http://10.1.2.3:57117") && content.contains("\"mcp\""),
            "{content}"
        );
        let _ = fs::remove_dir_all(home);
    }
}
