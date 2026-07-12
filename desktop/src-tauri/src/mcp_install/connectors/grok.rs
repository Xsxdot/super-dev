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
//   - detect 已实现（CLI 优先，回退 ~/.grok 目录）
//   - status/install/uninstall 仍为安全占位；真实 MCP CLI / Skill / Hook 写入在后续 Task

use super::common;
use super::process::{CommandRunner, SystemCommandRunner};
use crate::mcp_install::contracts::*;
use crate::mcp_install::registry::*;
use crate::mcp_install::{executable_file_names, DEFAULT_AGENT_URL};
use std::path::PathBuf;
use std::sync::Arc;
use std::time::{Duration, Instant};

const CONNECTOR_ID: &str = "grok";
const DISPLAY_NAME: &str = "Grok";
/// CLI 调用超时；后续 Task 在 CommandSpec 上使用。
#[allow(dead_code)]
const CLI_TIMEOUT: Duration = Duration::from_secs(30);
/// SuperDev 拥有的 SessionStart hook 命令标记子串（后续 Task 匹配用）。
#[allow(dead_code)]
const HOOK_MARKER: &str = "skills/superdev/hooks/run-hook.cmd";
const HOOK_FILE_NAME: &str = "superdev-session-start.json";

/// GrokConnector 适配 Grok CLI 的 MCP/Skill/Session Hook。
pub(super) struct GrokConnector {
    descriptor: AgentConnectorDescriptor,
    /// runner 在后续 Task 中执行 `grok mcp`；脚手架阶段保留注入点。
    #[allow(dead_code)]
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
        executable_file_names("grok")
            .into_iter()
            .map(|name| directory.join(name))
            .find(|path| path.is_file())
    })
}

impl AgentConnector for GrokConnector {
    fn descriptor(&self) -> &AgentConnectorDescriptor {
        &self.descriptor
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
        // MCP/Hook 在后续 Task 实现前一律 Missing；Skill 走 common 真实只读状态。
        let result = ConnectorStatus {
            integrations: vec![
                IntegrationState {
                    capability: IntegrationCapability::Mcp,
                    status: IntegrationStateStatus::Missing,
                    target_path: Some(common::path_string(&config_path(ctx))),
                    message: Some("Grok MCP 状态尚未实现（脚手架）".into()),
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
        };
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = "status",
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
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = ?request.operation,
            capability_count = request.capabilities.len(),
            "grok install started (scaffold stub)"
        );
        // 安全占位：不写盘、不调 CLI，返回 NeedsAction + 手动指引。
        let outcome = aggregate_connector_result(
            CONNECTOR_ID.into(),
            request.operation,
            vec![
                common::integration_result(
                    IntegrationCapability::Mcp,
                    IntegrationResult::NeedsAction,
                    Some(common::path_string(&config_path(ctx))),
                    None,
                    Some("Grok MCP 安装尚未实现，请按手动指引配置".into()),
                ),
                common::integration_result(
                    IntegrationCapability::Skill,
                    IntegrationResult::NeedsAction,
                    Some(common::path_string(&skill_path(ctx))),
                    None,
                    Some("Grok Skill 安装尚未实现".into()),
                ),
                common::integration_result(
                    IntegrationCapability::SessionHook,
                    IntegrationResult::NeedsAction,
                    Some(common::path_string(&hook_path(ctx))),
                    None,
                    Some("Grok Session Hook 安装尚未实现".into()),
                ),
            ],
            Some(self.manual_instructions(ctx)?),
            false,
            Some("Grok 连接器脚手架：安装逻辑待后续 Task 实现".into()),
        )
        .map_err(|_| ConnectorError::new("aggregate_failed", "结果聚合失败"))?;
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = ?request.operation,
            result = ?outcome.result,
            duration_ms = started.elapsed().as_millis() as u64,
            "grok install finished (scaffold stub)"
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
            "grok uninstall started (scaffold stub)"
        );
        // 安全占位：不改写用户配置，报告 Unchanged。
        let outcome = ConnectorOperationOutcome {
            connector_id: CONNECTOR_ID.into(),
            operation: ConnectorOperation::Uninstall,
            result: ConnectorResult::Unchanged,
            integrations: vec![
                common::integration_result(
                    IntegrationCapability::Mcp,
                    IntegrationResult::Skipped,
                    Some(common::path_string(&config_path(ctx))),
                    None,
                    Some("Grok MCP 卸载尚未实现".into()),
                ),
                common::integration_result(
                    IntegrationCapability::Skill,
                    IntegrationResult::Skipped,
                    Some(common::path_string(&skill_path(ctx))),
                    None,
                    Some("Grok Skill 卸载尚未实现".into()),
                ),
                common::integration_result(
                    IntegrationCapability::SessionHook,
                    IntegrationResult::Skipped,
                    Some(common::path_string(&hook_path(ctx))),
                    None,
                    Some("Grok Session Hook 卸载尚未实现".into()),
                ),
            ],
            manual_instructions: None,
            requires_restart: false,
            message: Some("Grok 连接器脚手架：卸载逻辑待后续 Task 实现".into()),
        };
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = "uninstall",
            result = ?outcome.result,
            duration_ms = started.elapsed().as_millis() as u64,
            "grok uninstall finished (scaffold stub)"
        );
        Ok(outcome)
    }

    fn manual_instructions(
        &self,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorManualInstructions, ConnectorError> {
        let mcp_binary = ctx.mcp_binary().to_string_lossy();
        let manual_config = serde_json::to_string_pretty(&serde_json::json!({
            "command": format!(
                "grok mcp add superdev --scope user -e SUPERDEV_AGENT_URL={DEFAULT_AGENT_URL} -- {mcp_binary}"
            )
        }))
        .unwrap_or_else(|_| "{}".into());
        Ok(ConnectorManualInstructions {
            summary: "通过 Grok CLI 接入 SuperDev（MCP + Skill + Session Hook）".into(),
            steps: vec![
                "安装 Grok CLI，并确保 grok 在 PATH 中".into(),
                "使用 grok mcp add 配置 user-scope superdev MCP".into(),
                "安装 SuperDev Skill 与 SessionStart hook（脚手架阶段请等待完整实现）".into(),
            ],
            config_path: Some(common::path_string(&config_path(ctx))),
            manual_config: Some(manual_config),
            verification_prompt: Some("验证 SuperDev MCP 在 Grok 中可用".into()),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicU64, Ordering};

    static COUNTER: AtomicU64 = AtomicU64::new(0);

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

    #[test]
    fn detect_prefers_cli_path_when_present() {
        let home = test_dir("detect-cli");
        let bin = home.join("bin");
        std::fs::create_dir_all(&bin).unwrap();
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
}
