// connectors.rs 提供内置 Agent Connector 适配器。
//
// 职责：声明 Claude Code、Codex、Cursor 及标准 JSON fixture 连接器，并桥接统一契约。
// 边界：不执行未知命令或网络请求；实际文件写入继续复用 mcp_install 既有安全原语。

mod common;
mod process;

use super::contracts::*;
use super::registry::*;
use super::AgentKind;
#[cfg(test)]
use super::{
    executable_file_names, install_to_path, merge_json_config, remove_json_superdev_config,
    uninstall_from_path,
};
use super::{
    install_hint_for_paths, install_json_kind_to_path, install_mcp_for_paths_with_skill,
    install_session_hook, install_skill_dir, install_toml_kind_to_path, uninstall_mcp_for_paths,
    McpEntry, DEFAULT_AGENT_URL,
};
use std::sync::Arc;

fn descriptor(id: &str, name: &str, built_in: bool, standard: bool) -> AgentConnectorDescriptor {
    let integrations = vec![
        IntegrationSupport {
            capability: IntegrationCapability::Mcp,
            support: SupportMode::Automatic,
        },
        IntegrationSupport {
            capability: IntegrationCapability::Skill,
            support: if standard {
                SupportMode::Unsupported
            } else {
                SupportMode::Automatic
            },
        },
        IntegrationSupport {
            capability: IntegrationCapability::SessionHook,
            support: if standard {
                SupportMode::Unsupported
            } else {
                SupportMode::Automatic
            },
        },
    ];
    AgentConnectorDescriptor::new(AgentConnectorDescriptorInput {
        id: id.to_string(),
        display_name: name.to_string(),
        built_in,
        platforms: vec![
            ConnectorPlatform::Macos,
            ConnectorPlatform::Windows,
            ConnectorPlatform::Linux,
        ],
        integrations,
        operations: [
            ConnectorOperation::Detect,
            ConnectorOperation::Install,
            ConnectorOperation::Update,
            ConnectorOperation::Status,
            ConnectorOperation::Uninstall,
            ConnectorOperation::Verify,
        ]
        .into_iter()
        .map(|operation| OperationSupport {
            operation,
            support: SupportMode::Automatic,
        })
        .collect(),
        docs_url: None,
        verified_versions: standard.then(|| vec!["fixture".into()]),
    })
    .expect("内置连接器描述符必须通过契约校验")
}

/// BuiltInConnector 代表一个受支持的内置 Agent 方言。
pub struct BuiltInConnector {
    descriptor: AgentConnectorDescriptor,
    kind: AgentKind,
}

impl BuiltInConnector {
    /// new 创建指定 Agent 的内置连接器。
    pub fn new(kind: AgentKind) -> Self {
        let (id, name) = match kind {
            AgentKind::ClaudeCode => ("claude-code", "Claude Code"),
            AgentKind::Codex => ("codex", "Codex"),
            AgentKind::Cursor => ("cursor", "Cursor"),
        };
        Self {
            descriptor: descriptor(id, name, true, false),
            kind,
        }
    }
}

/// StandardJsonConnector 是仅支持 mcpServers 的测试/fixture 适配器。
#[cfg(test)]
pub struct StandardJsonConnector {
    descriptor: AgentConnectorDescriptor,
}

#[cfg(test)]
impl StandardJsonConnector {
    /// new 创建开放字符串 ID 的标准 JSON 连接器。
    pub fn new() -> Self {
        Self {
            descriptor: descriptor("fixture-json-agent", "Fixture JSON Agent", false, true),
        }
    }
}

impl AgentConnector for BuiltInConnector {
    fn descriptor(&self) -> &AgentConnectorDescriptor {
        &self.descriptor
    }
    fn detect(&self, ctx: &ConnectorRuntimeContext) -> Result<ConnectorDetection, ConnectorError> {
        let path =
            self.kind
                .detect_installation(ctx.home_dir(), ctx.command_dirs(), ctx.app_dirs());
        Ok(ConnectorDetection {
            detected: path.is_some(),
            detection_path: path,
            message: Some("Agent 检测完成".into()),
        })
    }
    fn status(&self, ctx: &ConnectorRuntimeContext) -> Result<ConnectorStatus, ConnectorError> {
        let command_dirs = ctx.command_dirs();
        let s = crate::mcp_install::mcp_status_for_kind(
            ctx.home_dir(),
            command_dirs,
            ctx.app_dirs(),
            ctx.skill_source(),
            ctx.skill_source_error().map(str::to_string),
            self.kind,
        );
        if s.config_error.is_some() {
            return Err(ConnectorError::new("invalid_config", "Agent 配置无法解析"));
        }
        let has_command = s
            .mcp_command
            .as_deref()
            .is_some_and(|value| !value.trim().is_empty());
        let has_agent_url = s
            .agent_url
            .as_deref()
            .is_some_and(|value| !value.trim().is_empty());
        let mcp = if s.mcp_configured && has_command && has_agent_url {
            IntegrationStateStatus::Configured
        } else if s.mcp_command.is_some() || s.agent_url.is_some() {
            IntegrationStateStatus::NeedsAction
        } else {
            IntegrationStateStatus::Missing
        };
        let skill = if s.skill_error.is_some() {
            IntegrationStateStatus::Error
        } else if s.skill_installed {
            IntegrationStateStatus::Configured
        } else {
            IntegrationStateStatus::Missing
        };
        let hook = if s.hook_installed && s.hook_needs_trust {
            IntegrationStateStatus::NeedsAction
        } else if s.hook_installed {
            IntegrationStateStatus::Configured
        } else {
            IntegrationStateStatus::Missing
        };
        Ok(ConnectorStatus {
            integrations: vec![
                IntegrationState {
                    capability: IntegrationCapability::Mcp,
                    status: mcp,
                    target_path: Some(s.config_path),
                    message: None,
                },
                IntegrationState {
                    capability: IntegrationCapability::Skill,
                    status: skill,
                    target_path: Some(s.skill_path),
                    message: s.skill_error,
                },
                IntegrationState {
                    capability: IntegrationCapability::SessionHook,
                    status: hook,
                    target_path: Some(s.hook_config_path),
                    message: s
                        .hook_needs_trust
                        .then(|| "需要在 Codex 中信任 hook".into()),
                },
            ],
            requires_restart: s.mcp_configured,
            message: Some("状态读取完成".into()),
        })
    }
    fn install(
        &self,
        ctx: &ConnectorRuntimeContext,
        request: ConnectorInstallRequest,
    ) -> Result<ConnectorOperationOutcome, ConnectorError> {
        let started = std::time::Instant::now();
        if !request.capabilities.contains(&IntegrationCapability::Mcp) {
            let state = crate::mcp_install::mcp_status_for_kind(
                ctx.home_dir(),
                ctx.command_dirs(),
                ctx.app_dirs(),
                ctx.skill_source(),
                ctx.skill_source_error().map(str::to_string),
                self.kind,
            );
            let skill_path = self.kind.skill_dir(ctx.home_dir());
            let mut skill_result = if state.skill_installed {
                IntegrationResult::AlreadyPresent
            } else {
                IntegrationResult::Skipped
            };
            let mut skill_msg = state.skill_error.clone();
            if state.mcp_configured && request.capabilities.contains(&IntegrationCapability::Skill)
            {
                match ctx.skill_source() {
                    Some(src) => match install_skill_dir(src, &skill_path) {
                        Ok(v) => {
                            skill_result = if v.installed {
                                IntegrationResult::Installed
                            } else {
                                IntegrationResult::AlreadyPresent
                            };
                            skill_msg = v.error;
                        }
                        Err(e) => {
                            skill_result = IntegrationResult::Failed;
                            skill_msg = Some(e);
                        }
                    },
                    None => {
                        skill_result = IntegrationResult::Failed;
                        skill_msg = ctx.skill_source_error().map(str::to_string);
                    }
                }
            }
            let hook_path = self.kind.session_hook_path(ctx.home_dir());
            let mut hook_result = if state.hook_installed {
                if state.hook_needs_trust {
                    IntegrationResult::NeedsAction
                } else {
                    IntegrationResult::AlreadyPresent
                }
            } else {
                IntegrationResult::Skipped
            };
            let mut hook_msg = None;
            if state.mcp_configured
                && request
                    .capabilities
                    .contains(&IntegrationCapability::SessionHook)
            {
                match install_session_hook(self.kind, &hook_path, &skill_path) {
                    Ok(v) => {
                        hook_result = if v.needs_trust {
                            IntegrationResult::NeedsAction
                        } else if v.installed {
                            IntegrationResult::Installed
                        } else {
                            IntegrationResult::AlreadyPresent
                        };
                        hook_msg = v.error;
                    }
                    Err(e) => {
                        hook_result = IntegrationResult::Failed;
                        hook_msg = Some(e);
                    }
                }
            }
            let map = |cap, result, path: String, msg| IntegrationOperationResult {
                capability: cap,
                result,
                target_path: Some(path),
                backup_path: None,
                message: msg,
            };
            return aggregate_connector_result(
                self.descriptor.id().into(),
                request.operation,
                vec![
                    map(
                        IntegrationCapability::Mcp,
                        if state.mcp_configured {
                            IntegrationResult::AlreadyPresent
                        } else {
                            IntegrationResult::NeedsAction
                        },
                        state.config_path,
                        state
                            .config_error
                            .clone()
                            .or_else(|| Some("请先完成 SuperDev MCP 配置".to_string())),
                    ),
                    map(
                        IntegrationCapability::Skill,
                        skill_result,
                        state.skill_path,
                        skill_msg,
                    ),
                    map(
                        IntegrationCapability::SessionHook,
                        hook_result,
                        state.hook_config_path,
                        hook_msg,
                    ),
                ],
                Some(self.manual_instructions(ctx)?),
                true,
                Some("增强能力安装完成".into()),
            )
            .map_err(|_| ConnectorError::new("aggregate_failed", "结果聚合失败"));
        }
        let all_capabilities = [
            IntegrationCapability::Mcp,
            IntegrationCapability::Skill,
            IntegrationCapability::SessionHook,
        ]
        .iter()
        .all(|cap| request.capabilities.contains(cap));
        if !all_capabilities {
            let config_path = self.kind.config_path(ctx.home_dir());
            let entry = McpEntry {
                command: ctx.mcp_binary().to_string_lossy().into(),
                agent_url: DEFAULT_AGENT_URL.into(),
            };
            let config = match self.kind {
                AgentKind::ClaudeCode | AgentKind::Cursor => {
                    install_json_kind_to_path(&config_path, &entry, self.kind.label())
                }
                AgentKind::Codex => {
                    install_toml_kind_to_path(&config_path, &entry, self.kind.label())
                }
            };
            let (mcp_result, mcp_backup, mcp_msg) = match config {
                Ok(v) => (
                    if v.installed {
                        IntegrationResult::Installed
                    } else {
                        IntegrationResult::AlreadyPresent
                    },
                    v.backup_path,
                    None,
                ),
                Err(e) => (IntegrationResult::Failed, None, Some(e)),
            };
            let skill_path = self.kind.skill_dir(ctx.home_dir());
            let mcp_ready = matches!(
                mcp_result,
                IntegrationResult::Installed | IntegrationResult::AlreadyPresent
            );
            let (skill_result, skill_backup, skill_msg) = if !mcp_ready {
                // MCP 是增强配置的依赖；失败时只返回可恢复结果，避免留下“半接入”的 Skill。
                (IntegrationResult::Skipped, None, None)
            } else if request.capabilities.contains(&IntegrationCapability::Skill) {
                match ctx.skill_source() {
                    Some(src) => match install_skill_dir(src, &skill_path) {
                        Ok(v) => (
                            if v.installed {
                                IntegrationResult::Installed
                            } else {
                                IntegrationResult::AlreadyPresent
                            },
                            v.backup_path,
                            v.error,
                        ),
                        Err(e) => (IntegrationResult::Failed, None, Some(e)),
                    },
                    None => (
                        IntegrationResult::Failed,
                        None,
                        ctx.skill_source_error().map(str::to_string),
                    ),
                }
            } else {
                (IntegrationResult::Skipped, None, None)
            };
            let hook_path = self.kind.session_hook_path(ctx.home_dir());
            let (hook_result, hook_backup, hook_msg) = if !mcp_ready {
                (IntegrationResult::Skipped, None, None)
            } else if request
                .capabilities
                .contains(&IntegrationCapability::SessionHook)
            {
                match install_session_hook(self.kind, &hook_path, &skill_path) {
                    Ok(v) => (
                        if v.needs_trust {
                            IntegrationResult::NeedsAction
                        } else if v.installed {
                            IntegrationResult::Installed
                        } else {
                            IntegrationResult::AlreadyPresent
                        },
                        v.backup_path,
                        v.error,
                    ),
                    Err(e) => (IntegrationResult::Failed, None, Some(e)),
                }
            } else {
                (IntegrationResult::Skipped, None, None)
            };
            let outcome = aggregate_connector_result(
                self.descriptor.id().into(),
                request.operation,
                vec![
                    IntegrationOperationResult {
                        capability: IntegrationCapability::Mcp,
                        result: mcp_result,
                        target_path: Some(config_path.to_string_lossy().into()),
                        backup_path: mcp_backup,
                        message: mcp_msg,
                    },
                    IntegrationOperationResult {
                        capability: IntegrationCapability::Skill,
                        result: skill_result,
                        target_path: Some(skill_path.to_string_lossy().into()),
                        backup_path: skill_backup,
                        message: skill_msg,
                    },
                    IntegrationOperationResult {
                        capability: IntegrationCapability::SessionHook,
                        result: hook_result,
                        target_path: Some(hook_path.to_string_lossy().into()),
                        backup_path: hook_backup,
                        message: hook_msg,
                    },
                ],
                Some(self.manual_instructions(ctx)?),
                true,
                Some("选择性能力安装完成".into()),
            )
            .map_err(|_| ConnectorError::new("aggregate_failed", "结果聚合失败"))?;
            tracing::info!(connector_id=self.descriptor.id(), operation=?request.operation, result=?outcome.result, duration_ms=started.elapsed().as_millis() as u64, "built-in connector selective install finished");
            return Ok(outcome);
        }
        let old = match install_mcp_for_paths_with_skill(
            self.kind.label(),
            ctx.home_dir(),
            ctx.mcp_binary(),
            ctx.skill_source(),
            ctx.skill_source_error().map(str::to_string),
        ) {
            Ok(value) => value,
            Err(error) => {
                let hint = self.manual_instructions(ctx)?;
                let config_path = self.kind.config_path(ctx.home_dir());
                return Ok(ConnectorOperationOutcome {
                    connector_id: self.descriptor.id().into(),
                    operation: request.operation,
                    result: ConnectorResult::Failed,
                    integrations: vec![
                        IntegrationOperationResult {
                            capability: IntegrationCapability::Mcp,
                            result: IntegrationResult::Failed,
                            target_path: Some(config_path.to_string_lossy().into_owned()),
                            backup_path: None,
                            message: Some(error),
                        },
                        IntegrationOperationResult {
                            capability: IntegrationCapability::Skill,
                            result: IntegrationResult::Skipped,
                            target_path: Some(
                                self.kind
                                    .skill_dir(ctx.home_dir())
                                    .to_string_lossy()
                                    .into_owned(),
                            ),
                            backup_path: None,
                            message: None,
                        },
                        IntegrationOperationResult {
                            capability: IntegrationCapability::SessionHook,
                            result: IntegrationResult::Skipped,
                            target_path: Some(
                                self.kind
                                    .session_hook_path(ctx.home_dir())
                                    .to_string_lossy()
                                    .into_owned(),
                            ),
                            backup_path: None,
                            message: None,
                        },
                    ],
                    manual_instructions: Some(hint),
                    requires_restart: false,
                    message: Some("MCP 自动安装失败，请按指引手动配置".into()),
                });
            }
        };
        let map = |cap, result, path: String, backup: Option<String>, msg: Option<String>| {
            IntegrationOperationResult {
                capability: cap,
                result,
                target_path: Some(path),
                backup_path: backup,
                message: msg,
            }
        };
        let skill_result = if old.skill.error.is_some() {
            IntegrationResult::Failed
        } else if old.skill.installed {
            IntegrationResult::Installed
        } else {
            IntegrationResult::AlreadyPresent
        };
        let hook_needs_trust = old.session_hook.needs_trust;
        let hook_message = if hook_needs_trust {
            Some("需要在 Codex 中信任 hook".to_string())
        } else {
            old.session_hook.error.clone()
        };
        let hook_result = if hook_needs_trust {
            IntegrationResult::NeedsAction
        } else if old.session_hook.error.is_some() {
            IntegrationResult::Failed
        } else if old.session_hook.installed {
            IntegrationResult::Installed
        } else {
            IntegrationResult::AlreadyPresent
        };
        let outcome = aggregate_connector_result(
            self.descriptor.id().into(),
            request.operation,
            vec![
                map(
                    IntegrationCapability::Mcp,
                    if old.installed {
                        IntegrationResult::Installed
                    } else {
                        IntegrationResult::AlreadyPresent
                    },
                    old.config_path,
                    old.backup_path,
                    None,
                ),
                map(
                    IntegrationCapability::Skill,
                    skill_result,
                    old.skill.target_path,
                    old.skill.backup_path,
                    old.skill.error,
                ),
                map(
                    IntegrationCapability::SessionHook,
                    hook_result,
                    old.session_hook.config_path,
                    old.session_hook.backup_path,
                    hook_message,
                ),
            ],
            Some(self.manual_instructions(ctx)?),
            true,
            Some("安装完成".into()),
        )
        .map_err(|_| ConnectorError::new("aggregate_failed", "结果聚合失败"))?;
        tracing::info!(connector_id=self.descriptor.id(), operation=?request.operation, result=?outcome.result, duration_ms=started.elapsed().as_millis() as u64, "built-in connector install finished");
        Ok(outcome)
    }
    fn uninstall(
        &self,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorOperationOutcome, ConnectorError> {
        let o = uninstall_mcp_for_paths(self.kind.label(), ctx.home_dir())
            .map_err(|e| ConnectorError::new("uninstall_failed", e))?;
        let mk = |c, r, p, b| IntegrationOperationResult {
            capability: c,
            result: r,
            target_path: Some(p),
            backup_path: b,
            message: (r == IntegrationResult::Installed).then(|| "已移除".into()),
        };
        let integrations = vec![
            mk(
                IntegrationCapability::Mcp,
                if o.removed_config {
                    IntegrationResult::Installed
                } else {
                    IntegrationResult::Skipped
                },
                o.config_path,
                o.config_backup_path,
            ),
            mk(
                IntegrationCapability::Skill,
                if o.removed_skill {
                    IntegrationResult::Installed
                } else {
                    IntegrationResult::Skipped
                },
                o.skill_path,
                None,
            ),
            mk(
                IntegrationCapability::SessionHook,
                if o.removed_hook {
                    IntegrationResult::Installed
                } else {
                    IntegrationResult::Skipped
                },
                o.hook_config_path,
                None,
            ),
        ];
        let changed = o.removed_config || o.removed_skill || o.removed_hook;
        Ok(ConnectorOperationOutcome {
            connector_id: self.descriptor.id().into(),
            operation: ConnectorOperation::Uninstall,
            result: if changed {
                ConnectorResult::Success
            } else {
                ConnectorResult::Unchanged
            },
            integrations,
            manual_instructions: None,
            requires_restart: changed,
            message: Some("卸载完成".into()),
        })
    }
    fn manual_instructions(
        &self,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorManualInstructions, ConnectorError> {
        let h = install_hint_for_paths(self.kind.label(), ctx.home_dir(), ctx.mcp_binary())
            .map_err(|e| ConnectorError::new("hint_failed", e))?;
        Ok(ConnectorManualInstructions {
            summary: "配置 SuperDev MCP 与 skill".into(),
            steps: vec!["写入 MCP 配置".into(), "安装 superdev skill".into()],
            config_path: Some(h.config_path),
            manual_config: Some(h.manual_config),
            verification_prompt: Some("验证 SuperDev MCP 可用".into()),
        })
    }
}

#[cfg(test)]
impl AgentConnector for StandardJsonConnector {
    fn descriptor(&self) -> &AgentConnectorDescriptor {
        &self.descriptor
    }
    fn detect(&self, ctx: &ConnectorRuntimeContext) -> Result<ConnectorDetection, ConnectorError> {
        let started = std::time::Instant::now();
        let path = self.config_path(ctx);
        let cli = ctx.command_dirs().iter().find_map(|directory| {
            executable_file_names("fixture-json-agent")
                .into_iter()
                .map(|name| directory.join(name))
                .find(|path| path.is_file())
        });
        let hit = cli.or_else(|| path.exists().then_some(path.clone()));
        let result = ConnectorDetection {
            detected: hit.is_some(),
            detection_path: hit,
            message: Some("标准 JSON fixture 检测完成".into()),
        };
        tracing::info!(
            connector_id = self.descriptor.id(),
            detected = result.detected,
            duration_ms = started.elapsed().as_millis() as u64,
            "standard connector detection finished"
        );
        Ok(result)
    }
    fn status(&self, ctx: &ConnectorRuntimeContext) -> Result<ConnectorStatus, ConnectorError> {
        let started = std::time::Instant::now();
        let path = self.config_path(ctx);
        let raw = match std::fs::read_to_string(&path) {
            Ok(content) => Some(content),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => None,
            Err(_) => {
                tracing::error!(
                    connector_id = self.descriptor.id(),
                    operation = "status",
                    error_kind = "read_failed",
                    "standard connector status read failed"
                );
                return Err(ConnectorError::new("read_failed", "标准 JSON 配置无法读取"));
            }
        };
        let (mcp, msg) = match raw.as_deref() {
            None => (IntegrationStateStatus::Missing, "MCP 配置缺失"),
            Some(s) => {
                let v: serde_json::Value = serde_json::from_str(s)
                    .map_err(|_| ConnectorError::new("invalid_json", "配置 JSON 无法解析"))?;
                match v.get("mcpServers").and_then(|x| x.get("superdev")) {
                    None => (IntegrationStateStatus::Missing, "SuperDev MCP 配置缺失"),
                    Some(x)
                        if !x
                            .get("command")
                            .and_then(|v| v.as_str())
                            .unwrap_or("")
                            .trim()
                            .is_empty()
                            && x.get("env")
                                .and_then(|e| e.get("SUPERDEV_AGENT_URL"))
                                .and_then(|v| v.as_str())
                                .map(|v| !v.trim().is_empty())
                                .unwrap_or(false) =>
                    {
                        (IntegrationStateStatus::Configured, "SuperDev MCP 已配置")
                    }
                    Some(_) => (
                        IntegrationStateStatus::NeedsAction,
                        "SuperDev MCP 配置不完整",
                    ),
                }
            }
        };
        let result = ConnectorStatus {
            integrations: vec![
                IntegrationState {
                    capability: IntegrationCapability::Mcp,
                    status: mcp,
                    target_path: Some(path.to_string_lossy().into()),
                    message: Some(msg.into()),
                },
                IntegrationState {
                    capability: IntegrationCapability::Skill,
                    status: IntegrationStateStatus::Unknown,
                    target_path: None,
                    message: Some("不支持 Skill".into()),
                },
                IntegrationState {
                    capability: IntegrationCapability::SessionHook,
                    status: IntegrationStateStatus::Unknown,
                    target_path: None,
                    message: Some("不支持 Session Hook".into()),
                },
            ],
            requires_restart: mcp == IntegrationStateStatus::Configured,
            message: Some(msg.into()),
        };
        tracing::info!(connector_id = self.descriptor.id(), status = ?mcp, duration_ms = started.elapsed().as_millis() as u64, "standard connector status finished");
        Ok(result)
    }
    fn install(
        &self,
        ctx: &ConnectorRuntimeContext,
        request: ConnectorInstallRequest,
    ) -> Result<ConnectorOperationOutcome, ConnectorError> {
        let started = std::time::Instant::now();
        let path = self.config_path(ctx);
        // 未请求 MCP 时只读取状态，避免重试其它能力时改写用户配置。
        if !request.capabilities.contains(&IntegrationCapability::Mcp) {
            let status = self.status(ctx)?;
            let mcp = status
                .integrations
                .iter()
                .find(|item| item.capability == IntegrationCapability::Mcp)
                .unwrap();
            let mcp_result = match mcp.status {
                IntegrationStateStatus::Configured => IntegrationResult::AlreadyPresent,
                IntegrationStateStatus::NeedsAction => IntegrationResult::NeedsAction,
                IntegrationStateStatus::Missing => IntegrationResult::NeedsAction,
                _ => IntegrationResult::Skipped,
            };
            let outcome = aggregate_connector_result(
                self.descriptor.id().into(),
                request.operation,
                vec![
                    IntegrationOperationResult {
                        capability: IntegrationCapability::Mcp,
                        result: mcp_result,
                        target_path: mcp.target_path.clone(),
                        backup_path: None,
                        message: mcp.message.clone(),
                    },
                    IntegrationOperationResult {
                        capability: IntegrationCapability::Skill,
                        result: IntegrationResult::Unsupported,
                        target_path: None,
                        backup_path: None,
                        message: Some("不支持 Skill".into()),
                    },
                    IntegrationOperationResult {
                        capability: IntegrationCapability::SessionHook,
                        result: IntegrationResult::Unsupported,
                        target_path: None,
                        backup_path: None,
                        message: Some("不支持 Session Hook".into()),
                    },
                ],
                None,
                false,
                Some("标准 JSON 未请求 MCP，保持配置不变".into()),
            )
            .map_err(|_| ConnectorError::new("aggregate_failed", "结果聚合失败"))?;
            tracing::info!(connector_id = self.descriptor.id(), operation = ?request.operation, result = ?outcome.result, duration_ms = started.elapsed().as_millis() as u64, "standard connector install skipped mcp write");
            return Ok(outcome);
        }
        let manual = self
            .manual_instructions(ctx)?
            .manual_config
            .unwrap_or_default();
        if path.exists() {
            if let Ok(raw) = std::fs::read_to_string(&path) {
                if serde_json::from_str::<serde_json::Value>(&raw).is_err() {
                    tracing::error!(connector_id = self.descriptor.id(), operation = ?request.operation, "standard connector install rejected malformed JSON");
                    return Ok(ConnectorOperationOutcome {
                        connector_id: self.descriptor.id().into(),
                        operation: request.operation,
                        result: ConnectorResult::Failed,
                        integrations: vec![
                            IntegrationOperationResult {
                                capability: IntegrationCapability::Mcp,
                                result: IntegrationResult::Failed,
                                target_path: Some(path.to_string_lossy().into()),
                                backup_path: None,
                                message: Some("配置 JSON 无法解析".into()),
                            },
                            IntegrationOperationResult {
                                capability: IntegrationCapability::Skill,
                                result: IntegrationResult::Unsupported,
                                target_path: None,
                                backup_path: None,
                                message: Some("不支持 Skill".into()),
                            },
                            IntegrationOperationResult {
                                capability: IntegrationCapability::SessionHook,
                                result: IntegrationResult::Unsupported,
                                target_path: None,
                                backup_path: None,
                                message: Some("不支持 Session Hook".into()),
                            },
                        ],
                        manual_instructions: Some(self.manual_instructions(ctx)?),
                        requires_restart: false,
                        message: Some("标准 JSON 配置无法解析，未覆盖原文件".into()),
                    });
                }
            }
        }
        let entry = McpEntry {
            command: ctx.mcp_binary().to_string_lossy().into(),
            agent_url: DEFAULT_AGENT_URL.into(),
        };
        let r = install_to_path(
            &path,
            &entry,
            merge_json_config,
            "fixture-json-agent".into(),
            manual,
        )
        .map_err(|_| ConnectorError::new("install_failed", "标准 JSON 配置写入失败"))?;
        let m = IntegrationOperationResult {
            capability: IntegrationCapability::Mcp,
            result: if r.installed {
                IntegrationResult::Installed
            } else {
                IntegrationResult::AlreadyPresent
            },
            target_path: Some(r.config_path),
            backup_path: r.backup_path,
            message: Some("MCP 配置已处理".into()),
        };
        let outcome = aggregate_connector_result(
            self.descriptor.id().into(),
            request.operation,
            vec![
                m,
                IntegrationOperationResult {
                    capability: IntegrationCapability::Skill,
                    result: IntegrationResult::Unsupported,
                    target_path: None,
                    backup_path: None,
                    message: Some("不支持 Skill".into()),
                },
                IntegrationOperationResult {
                    capability: IntegrationCapability::SessionHook,
                    result: IntegrationResult::Unsupported,
                    target_path: None,
                    backup_path: None,
                    message: Some("不支持 Session Hook".into()),
                },
            ],
            None,
            true,
            Some("标准 JSON MCP 配置完成".into()),
        )
        .map_err(|_| ConnectorError::new("aggregate_failed", "结果聚合失败"))?;
        tracing::info!(connector_id = self.descriptor.id(), operation = ?request.operation, result = ?outcome.result, duration_ms = started.elapsed().as_millis() as u64, "standard connector install finished");
        Ok(outcome)
    }
    fn uninstall(
        &self,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorOperationOutcome, ConnectorError> {
        let started = std::time::Instant::now();
        let p = self.config_path(ctx);
        let (changed, backup_path) = uninstall_from_path(&p, remove_json_superdev_config)
            .map_err(|_| ConnectorError::new("uninstall_failed", "标准 JSON 配置卸载失败"))?;
        let outcome = ConnectorOperationOutcome {
            connector_id: self.descriptor.id().into(),
            operation: ConnectorOperation::Uninstall,
            result: if changed {
                ConnectorResult::Success
            } else {
                ConnectorResult::Unchanged
            },
            integrations: vec![
                IntegrationOperationResult {
                    capability: IntegrationCapability::Mcp,
                    result: if changed {
                        IntegrationResult::Installed
                    } else {
                        IntegrationResult::Skipped
                    },
                    target_path: Some(p.to_string_lossy().into()),
                    backup_path: backup_path.clone(),
                    message: Some("MCP 配置已卸载".into()),
                },
                IntegrationOperationResult {
                    capability: IntegrationCapability::Skill,
                    result: IntegrationResult::Unsupported,
                    target_path: None,
                    backup_path: None,
                    message: Some("不支持 Skill".into()),
                },
                IntegrationOperationResult {
                    capability: IntegrationCapability::SessionHook,
                    result: IntegrationResult::Unsupported,
                    target_path: None,
                    backup_path: None,
                    message: Some("不支持 Session Hook".into()),
                },
            ],
            manual_instructions: None,
            requires_restart: changed,
            message: Some("卸载完成".into()),
        };
        tracing::info!(connector_id = self.descriptor.id(), operation = "uninstall", result = ?outcome.result, duration_ms = started.elapsed().as_millis() as u64, "standard connector uninstall finished");
        Ok(outcome)
    }
    fn manual_instructions(
        &self,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorManualInstructions, ConnectorError> {
        let p = self.config_path(ctx);
        let config = serde_json::json!({"mcpServers":{"superdev":{"command":ctx.mcp_binary(),"env":{"SUPERDEV_AGENT_URL":DEFAULT_AGENT_URL}}}});
        Ok(ConnectorManualInstructions {
            summary: "在标准 JSON 配置中添加 SuperDev MCP".into(),
            steps: vec!["编辑 mcpServers.superdev".into()],
            config_path: Some(p.to_string_lossy().into()),
            manual_config: Some(serde_json::to_string_pretty(&config).unwrap()),
            verification_prompt: Some("验证 SuperDev MCP 可用".into()),
        })
    }
}

#[cfg(test)]
impl StandardJsonConnector {
    fn config_path(&self, ctx: &ConnectorRuntimeContext) -> std::path::PathBuf {
        ctx.home_dir().join(".fixture-json-agent").join("mcp.json")
    }
}

/// builtin 返回三个生产内置连接器，注册表负责并发门控。
pub fn builtin() -> Vec<Arc<dyn AgentConnector>> {
    vec![
        Arc::new(BuiltInConnector::new(AgentKind::ClaudeCode)),
        Arc::new(BuiltInConnector::new(AgentKind::Codex)),
        Arc::new(BuiltInConnector::new(AgentKind::Cursor)),
    ]
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn builtin_descriptors_expose_automatic_hooks() {
        for kind in [AgentKind::ClaudeCode, AgentKind::Codex, AgentKind::Cursor] {
            let connector = BuiltInConnector::new(kind);
            let hook = connector
                .descriptor()
                .integrations()
                .iter()
                .find(|item| item.capability == IntegrationCapability::SessionHook)
                .expect("hook descriptor");
            assert_eq!(hook.support, SupportMode::Automatic);
        }
    }

    #[test]
    fn builtin_detects_agent_from_command_dir() {
        let root = std::env::temp_dir().join(format!("builtin-detect-{}", std::process::id()));
        let bin = root.join("bin");
        std::fs::create_dir_all(&bin).unwrap();
        for (kind, name) in [
            (AgentKind::ClaudeCode, "claude"),
            (AgentKind::Codex, "codex"),
            (AgentKind::Cursor, "cursor"),
        ] {
            std::fs::write(bin.join(name), "").unwrap();
            let ctx = ConnectorRuntimeContext::new(
                root.clone(),
                vec![bin.clone()],
                vec![],
                root.join("mcp"),
                None,
                None,
            );
            let hit = BuiltInConnector::new(kind).detect(&ctx).unwrap();
            assert!(hit.detected);
            std::fs::remove_file(bin.join(name)).unwrap();
        }
        let _ = std::fs::remove_dir_all(root);
    }

    #[test]
    fn builtin_connectors_round_trip_real_json_and_toml_configs() {
        // Rust 测试线程名包含 `::`，不能直接作为 Windows 路径；时间戳同时避免并发冲突。
        let root = std::env::temp_dir().join(format!(
            "builtin-roundtrip-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        let skill_source = root.join("skill-source");
        std::fs::create_dir_all(skill_source.join("hooks")).unwrap();
        std::fs::write(skill_source.join("SKILL.md"), "fixture skill").unwrap();
        std::fs::write(skill_source.join("hooks/session-start"), "#!/bin/sh\n").unwrap();

        for (kind, command) in [
            (AgentKind::ClaudeCode, "claude"),
            (AgentKind::Codex, "codex"),
            (AgentKind::Cursor, "cursor"),
        ] {
            let home = root.join(kind.label());
            let bin = home.join("bin");
            std::fs::create_dir_all(&bin).unwrap();
            std::fs::write(bin.join(command), "").unwrap();
            let ctx = ConnectorRuntimeContext::new(
                home.clone(),
                vec![bin],
                vec![],
                root.join("superdev-mcp"),
                Some(skill_source.clone()),
                None,
            );
            let connector = BuiltInConnector::new(kind);
            let before = match kind {
                AgentKind::Codex => "[mcp_servers.other]\ncommand = \"other\"\n",
                AgentKind::ClaudeCode | AgentKind::Cursor => {
                    "{\"mcpServers\":{\"other\":{\"command\":\"other\"}}}"
                }
            };
            let config_path = kind.config_path(&home);
            std::fs::create_dir_all(config_path.parent().unwrap()).unwrap();
            std::fs::write(&config_path, before).unwrap();

            let before_status = connector.status(&ctx).unwrap();
            assert_eq!(
                before_status.integrations[0].status,
                IntegrationStateStatus::Missing,
                "{} without SuperDev entry should be missing",
                kind.label()
            );

            let install = connector
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
                .unwrap_or_else(|error| panic!("{} install: {error:?}", kind.label()));
            assert_eq!(install.connector_id, kind.label());
            assert_eq!(install.operation, ConnectorOperation::Install);
            assert!(matches!(
                install.result,
                ConnectorResult::Success | ConnectorResult::Partial
            ));
            assert_eq!(install.integrations.len(), 3);

            let second = connector
                .install(
                    &ctx,
                    ConnectorInstallRequest {
                        operation: ConnectorOperation::Install,
                        capabilities: vec![IntegrationCapability::Mcp],
                    },
                )
                .unwrap();
            assert_eq!(second.integrations.len(), 3);

            let status = connector.status(&ctx).unwrap();
            assert_eq!(status.integrations.len(), 3);
            assert_eq!(
                status.integrations[0].status,
                IntegrationStateStatus::Configured
            );

            let uninstall = connector.uninstall(&ctx).unwrap();
            assert_eq!(uninstall.operation, ConnectorOperation::Uninstall);
            assert_eq!(uninstall.integrations.len(), 3);
            let remaining = std::fs::read_to_string(kind.config_path(&home)).unwrap();
            assert!(remaining.contains("other"));
        }
        let _ = std::fs::remove_dir_all(root);
    }
    use std::fs;
    #[test]
    fn standard_json_detects_fixture_config_path() {
        let root = std::env::temp_dir().join(format!("fixture-json-{}", std::process::id()));
        let path = root.join(".fixture-json-agent/mcp.json");
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(&path, "{}").unwrap();
        let ctx = ConnectorRuntimeContext::new(
            root.clone(),
            vec![],
            vec![],
            root.join("bin/superdev-mcp"),
            None,
            None,
        );
        let detection = StandardJsonConnector::new().detect(&ctx).unwrap();
        assert!(detection.detected);
        assert_eq!(detection.detection_path, Some(path));
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn standard_json_detects_native_cli_file_names_without_config() {
        let root = std::env::temp_dir().join(format!(
            "fixture-json-native-cli-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        let bin = root.join("bin");
        fs::create_dir_all(&bin).unwrap();
        let executable_name = if cfg!(windows) {
            "fixture-json-agent.exe"
        } else {
            "fixture-json-agent"
        };
        let executable = bin.join(executable_name);
        fs::write(&executable, "fixture").unwrap();
        let ctx = ConnectorRuntimeContext::new(
            root.clone(),
            vec![bin],
            vec![],
            root.join(if cfg!(windows) {
                "bin/superdev-mcp.exe"
            } else {
                "bin/superdev-mcp"
            }),
            None,
            None,
        );

        let detection = StandardJsonConnector::new().detect(&ctx).unwrap();

        assert!(detection.detected);
        assert_eq!(detection.detection_path, Some(executable));
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn standard_json_install_is_idempotent_and_uninstall_preserves_backup() {
        let root =
            std::env::temp_dir().join(format!("fixture-json-roundtrip-{}", std::process::id()));
        let ctx = ConnectorRuntimeContext::new(
            root.clone(),
            vec![],
            vec![],
            root.join("bin/superdev-mcp"),
            None,
            None,
        );
        let connector = StandardJsonConnector::new();
        let request = ConnectorInstallRequest {
            operation: ConnectorOperation::Install,
            capabilities: vec![IntegrationCapability::Mcp],
        };
        let first = connector.install(&ctx, request.clone()).unwrap();
        assert_eq!(first.result, ConnectorResult::Success);
        let second = connector.install(&ctx, request).unwrap();
        assert_eq!(second.result, ConnectorResult::Unchanged);
        let removed = connector.uninstall(&ctx).unwrap();
        assert_eq!(removed.result, ConnectorResult::Success);
        assert!(removed.integrations[0].backup_path.is_some());
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn standard_json_corrupt_config_returns_failed_without_overwrite() {
        let root =
            std::env::temp_dir().join(format!("fixture-json-corrupt-{}", std::process::id()));
        let path = root.join(".fixture-json-agent/mcp.json");
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(&path, "{broken").unwrap();
        let ctx = ConnectorRuntimeContext::new(
            root.clone(),
            vec![],
            vec![],
            root.join("bin/superdev-mcp"),
            None,
            None,
        );
        let request = ConnectorInstallRequest {
            operation: ConnectorOperation::Install,
            capabilities: vec![IntegrationCapability::Mcp],
        };
        let outcome = StandardJsonConnector::new().install(&ctx, request).unwrap();
        assert_eq!(outcome.result, ConnectorResult::Failed);
        assert!(outcome
            .manual_instructions
            .and_then(|m| m.manual_config)
            .is_some());
        assert_eq!(fs::read_to_string(path).unwrap(), "{broken");
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn standard_json_status_truth_table() {
        let root = std::env::temp_dir().join(format!("fixture-json-status-{}", std::process::id()));
        let path = root.join(".fixture-json-agent/mcp.json");
        let ctx = ConnectorRuntimeContext::new(
            root.clone(),
            vec![],
            vec![],
            root.join("bin/superdev-mcp"),
            None,
            None,
        );
        let connector = StandardJsonConnector::new();
        assert_eq!(
            connector.status(&ctx).unwrap().integrations[0].status,
            IntegrationStateStatus::Missing
        );
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(&path, "{\"mcpServers\":{\"other\":{}}}").unwrap();
        assert_eq!(
            connector.status(&ctx).unwrap().integrations[0].status,
            IntegrationStateStatus::Missing
        );
        fs::write(&path, "{\"mcpServers\":{\"superdev\":{\"command\":\"x\",\"env\":{\"SUPERDEV_AGENT_URL\":\"u\"}}}}").unwrap();
        assert_eq!(
            connector.status(&ctx).unwrap().integrations[0].status,
            IntegrationStateStatus::Configured
        );
        fs::write(&path, "{\"mcpServers\":{\"superdev\":{}}}").unwrap();
        assert_eq!(
            connector.status(&ctx).unwrap().integrations[0].status,
            IntegrationStateStatus::NeedsAction
        );
        fs::write(&path, "{broken").unwrap();
        assert!(connector.status(&ctx).is_err());
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn standard_json_manual_config_is_valid_json_and_blank_fields_need_action() {
        let root = std::env::temp_dir().join(format!("fixture-json-extra-{}", std::process::id()));
        let ctx = ConnectorRuntimeContext::new(
            root.clone(),
            vec![],
            vec![],
            root.join("bin\\a\"b"),
            None,
            None,
        );
        let connector = StandardJsonConnector::new();
        let manual = connector.manual_instructions(&ctx).unwrap();
        let value: serde_json::Value =
            serde_json::from_str(&manual.manual_config.unwrap()).unwrap();
        assert!(value["mcpServers"]["superdev"]["command"].is_string());
        std::fs::create_dir_all(root.join(".fixture-json-agent")).unwrap();
        std::fs::write(
            root.join(".fixture-json-agent/mcp.json"),
            r#"{"mcpServers":{"superdev":{"command":" ","env":{"SUPERDEV_AGENT_URL":" "}}}}"#,
        )
        .unwrap();
        assert_eq!(
            connector.status(&ctx).unwrap().integrations[0].status,
            IntegrationStateStatus::NeedsAction
        );
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn standard_json_capability_subset_still_returns_all_integration_results() {
        let root = std::env::temp_dir().join(format!("fixture-json-cap-{}", std::process::id()));
        let ctx = ConnectorRuntimeContext::new(
            root.clone(),
            vec![],
            vec![],
            root.join("mcp"),
            None,
            None,
        );
        let request = ConnectorInstallRequest {
            operation: ConnectorOperation::Update,
            capabilities: vec![IntegrationCapability::Skill],
        };
        let outcome = StandardJsonConnector::new().install(&ctx, request).unwrap();
        assert_eq!(outcome.integrations.len(), 3);
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn standard_json_subset_does_not_write_mcp_config() {
        let root = std::env::temp_dir().join(format!("fixture-json-no-mcp-{}", std::process::id()));
        let path = root.join(".fixture-json-agent/mcp.json");
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(&path, r#"{"mcpServers":{"other":{"command":"other"}}}"#).unwrap();
        let before = fs::read(&path).unwrap();
        let ctx = ConnectorRuntimeContext::new(
            root.clone(),
            vec![],
            vec![],
            root.join("mcp"),
            None,
            None,
        );
        let outcome = StandardJsonConnector::new()
            .install(
                &ctx,
                ConnectorInstallRequest {
                    operation: ConnectorOperation::Install,
                    capabilities: vec![IntegrationCapability::Skill],
                },
            )
            .unwrap();
        assert_eq!(fs::read(&path).unwrap(), before);
        assert_eq!(outcome.integrations.len(), 3);
        assert_eq!(
            outcome.integrations[0].result,
            IntegrationResult::NeedsAction
        );
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn builtin_subset_does_not_install_unselected_hook() {
        let root = std::env::temp_dir().join(format!("builtin-subset-{}", std::process::id()));
        let skill_source = root.join("skill-source");
        fs::create_dir_all(skill_source.join("hooks")).unwrap();
        fs::write(skill_source.join("SKILL.md"), "fixture").unwrap();
        fs::write(skill_source.join("hooks/session-start"), "#!/bin/sh\n").unwrap();
        let home = root.join("home");
        let ctx = ConnectorRuntimeContext::new(
            home.clone(),
            vec![],
            vec![],
            root.join("mcp"),
            Some(skill_source),
            None,
        );
        let outcome = BuiltInConnector::new(AgentKind::ClaudeCode)
            .install(
                &ctx,
                ConnectorInstallRequest {
                    operation: ConnectorOperation::Install,
                    capabilities: vec![IntegrationCapability::Mcp, IntegrationCapability::Skill],
                },
            )
            .unwrap();
        assert_eq!(outcome.integrations.len(), 3);
        assert_eq!(outcome.integrations[2].result, IntegrationResult::Skipped);
        assert!(!AgentKind::ClaudeCode.session_hook_path(&home).exists());
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn builtin_does_not_install_enhancements_before_mcp_is_ready() {
        let root = std::env::temp_dir().join(format!("builtin-dependency-{}", std::process::id()));
        let skill_source = root.join("skill-source");
        fs::create_dir_all(skill_source.join("hooks")).unwrap();
        fs::write(skill_source.join("SKILL.md"), "fixture").unwrap();
        fs::write(skill_source.join("hooks/session-start"), "#!/bin/sh\n").unwrap();
        let home = root.join("home");
        let ctx = ConnectorRuntimeContext::new(
            home.clone(),
            vec![],
            vec![],
            root.join("mcp"),
            Some(skill_source),
            None,
        );
        let outcome = BuiltInConnector::new(AgentKind::ClaudeCode)
            .install(
                &ctx,
                ConnectorInstallRequest {
                    operation: ConnectorOperation::Install,
                    capabilities: vec![IntegrationCapability::Skill],
                },
            )
            .unwrap();
        assert_eq!(outcome.result, ConnectorResult::NeedsAction);
        assert_eq!(outcome.integrations[1].result, IntegrationResult::Skipped);
        assert!(!AgentKind::ClaudeCode.skill_dir(&home).exists());
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn builtin_mcp_failure_skips_selected_enhancements() {
        let root = std::env::temp_dir().join(format!("builtin-mcp-failure-{}", std::process::id()));
        let skill_source = root.join("skill-source");
        fs::create_dir_all(skill_source.join("hooks")).unwrap();
        fs::write(skill_source.join("SKILL.md"), "fixture").unwrap();
        fs::write(skill_source.join("hooks/session-start"), "#!/bin/sh\n").unwrap();
        let home = root.join("home");
        let config_path = AgentKind::ClaudeCode.config_path(&home);
        fs::create_dir_all(config_path.parent().unwrap()).unwrap();
        fs::write(&config_path, "{broken").unwrap();
        let ctx = ConnectorRuntimeContext::new(
            home.clone(),
            vec![],
            vec![],
            root.join("mcp"),
            Some(skill_source),
            None,
        );
        let outcome = BuiltInConnector::new(AgentKind::ClaudeCode)
            .install(
                &ctx,
                ConnectorInstallRequest {
                    operation: ConnectorOperation::Install,
                    capabilities: vec![IntegrationCapability::Mcp, IntegrationCapability::Skill],
                },
            )
            .unwrap();
        assert_eq!(outcome.result, ConnectorResult::Failed);
        assert_eq!(outcome.integrations[1].result, IntegrationResult::Skipped);
        assert!(!AgentKind::ClaudeCode.skill_dir(&home).exists());
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn builtin_incomplete_mcp_is_needs_action_not_configured() {
        let root = std::env::temp_dir().join(format!("builtin-incomplete-{}", std::process::id()));
        let home = root.join("home");
        let config_path = AgentKind::ClaudeCode.config_path(&home);
        fs::create_dir_all(config_path.parent().unwrap()).unwrap();
        fs::write(&config_path, "{\"mcpServers\":{\"superdev\":{}}}").unwrap();
        let ctx = ConnectorRuntimeContext::new(home, vec![], vec![], root.join("mcp"), None, None);
        let status = BuiltInConnector::new(AgentKind::ClaudeCode)
            .status(&ctx)
            .unwrap();
        assert_eq!(
            status.integrations[0].status,
            IntegrationStateStatus::NeedsAction
        );
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn standard_json_status_reports_non_not_found_io_error() {
        let root = std::env::temp_dir().join(format!("fixture-json-io-{}", std::process::id()));
        let path = root.join(".fixture-json-agent/mcp.json");
        fs::create_dir_all(&path).unwrap();
        let ctx = ConnectorRuntimeContext::new(
            root.clone(),
            vec![],
            vec![],
            root.join("mcp"),
            None,
            None,
        );
        assert!(StandardJsonConnector::new().status(&ctx).is_err());
        let _ = fs::remove_dir_all(root);
    }
}
