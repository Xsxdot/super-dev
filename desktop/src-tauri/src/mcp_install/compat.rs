//! 旧版 MCP command DTO 与 ConnectorRegistry 结果之间的兼容转换。
//!
//! 职责：保持已有设置页 command 的返回形状，同时确保检测、状态和变更均经过
//! ConnectorRegistry；不实现新的连接器或配置方言。

use super::contracts::*;
use super::{
    CodingAgentAvailability, InstallHint, InstallOutcome, McpStatus, SessionHookOutcome,
    SkillInstallOutcome, UninstallOutcome,
};

/// 将规范化安装结果投影为旧版安装 DTO。
pub fn install_outcome(outcome: ConnectorOperationOutcome) -> InstallOutcome {
    let mcp = outcome
        .integrations
        .iter()
        .find(|i| i.capability == IntegrationCapability::Mcp);
    let skill = outcome
        .integrations
        .iter()
        .find(|i| i.capability == IntegrationCapability::Skill);
    let hook = outcome
        .integrations
        .iter()
        .find(|i| i.capability == IntegrationCapability::SessionHook);
    InstallOutcome {
        installed: mcp.is_some_and(|i| i.result == IntegrationResult::Installed),
        already_present: mcp.is_some_and(|i| i.result == IntegrationResult::AlreadyPresent),
        agent: outcome.connector_id,
        config_path: mcp.and_then(|i| i.target_path.clone()).unwrap_or_default(),
        backup_path: mcp.and_then(|i| i.backup_path.clone()),
        manual_config: outcome
            .manual_instructions
            .as_ref()
            .and_then(|m| m.manual_config.clone())
            .unwrap_or_default(),
        skill: SkillInstallOutcome {
            installed: skill.is_some_and(|i| i.result == IntegrationResult::Installed),
            already_present: skill.is_some_and(|i| i.result == IntegrationResult::AlreadyPresent),
            target_path: skill
                .and_then(|i| i.target_path.clone())
                .unwrap_or_default(),
            backup_path: skill.and_then(|i| i.backup_path.clone()),
            error: None,
        },
        session_hook: SessionHookOutcome {
            installed: hook.is_some_and(|i| i.result == IntegrationResult::Installed),
            already_present: hook.is_some_and(|i| i.result == IntegrationResult::AlreadyPresent),
            config_path: hook.and_then(|i| i.target_path.clone()).unwrap_or_default(),
            backup_path: hook.and_then(|i| i.backup_path.clone()),
            needs_trust: hook.is_some_and(|i| i.result == IntegrationResult::NeedsAction),
            error: None,
        },
    }
}

/// 将规范化卸载结果投影为旧版卸载 DTO。
pub fn uninstall_outcome(outcome: ConnectorOperationOutcome) -> UninstallOutcome {
    let mcp = outcome
        .integrations
        .iter()
        .find(|i| i.capability == IntegrationCapability::Mcp);
    let skill = outcome
        .integrations
        .iter()
        .find(|i| i.capability == IntegrationCapability::Skill);
    let hook = outcome
        .integrations
        .iter()
        .find(|i| i.capability == IntegrationCapability::SessionHook);
    UninstallOutcome {
        agent: outcome.connector_id,
        config_path: mcp.and_then(|i| i.target_path.clone()).unwrap_or_default(),
        removed_config: mcp.is_some_and(|i| i.result == IntegrationResult::Installed),
        config_backup_path: mcp.and_then(|i| i.backup_path.clone()),
        skill_path: skill
            .and_then(|i| i.target_path.clone())
            .unwrap_or_default(),
        removed_skill: skill.is_some_and(|i| i.result == IntegrationResult::Installed),
        hook_config_path: hook.and_then(|i| i.target_path.clone()).unwrap_or_default(),
        removed_hook: hook.is_some_and(|i| i.result == IntegrationResult::Installed),
    }
}

/// 将手动指引投影为旧版安装提示。
pub fn hint(id: &str, instructions: ConnectorManualInstructions) -> InstallHint {
    InstallHint {
        agent: id.to_string(),
        config_path: instructions.config_path.unwrap_or_default(),
        manual_config: instructions.manual_config.unwrap_or_default(),
        skill_target_path: String::new(),
    }
}

/// 将注册表摘要转换为旧版状态 DTO。
pub fn statuses(summaries: Vec<AgentConnectorSummary>) -> Vec<McpStatus> {
    summaries.into_iter().filter_map(status).collect()
}

fn status(summary: AgentConnectorSummary) -> Option<McpStatus> {
    let id = summary.descriptor.id().to_string();
    let mut mcp = None;
    let mut skill_path = String::new();
    let mut skill_installed = false;
    let mut hook_path = String::new();
    let mut hook_installed = false;
    for integration in &summary.state.integrations {
        match integration.capability {
            IntegrationCapability::Mcp => {
                mcp = Some(integration.clone());
            }
            IntegrationCapability::Skill => {
                skill_path = integration.target_path.clone().unwrap_or_default();
                skill_installed = integration.status == IntegrationStateStatus::Configured;
            }
            IntegrationCapability::SessionHook => {
                hook_path = integration.target_path.clone().unwrap_or_default();
                hook_installed = integration.status == IntegrationStateStatus::Configured
                    || integration.status == IntegrationStateStatus::NeedsAction;
            }
        }
    }
    let mcp = mcp?;
    Some(McpStatus {
        agent: id,
        agent_installed: summary.state.detected,
        detection_path: summary.state.detection_path,
        config_path: mcp.target_path.unwrap_or_default(),
        config_exists: mcp.status != IntegrationStateStatus::Missing,
        mcp_configured: mcp.status == IntegrationStateStatus::Configured,
        mcp_command: None,
        agent_url: None,
        config_error: (mcp.status == IntegrationStateStatus::Error)
            .then_some("配置状态异常".into()),
        skill_path,
        skill_installed,
        skill_matches_bundled: None,
        skill_error: None,
        hook_config_path: hook_path,
        hook_installed,
        hook_needs_trust: summary.state.integrations.iter().any(|i| {
            i.capability == IntegrationCapability::SessionHook
                && i.status == IntegrationStateStatus::NeedsAction
        }),
    })
}

/// 将连接器检测结果转换为旧版可用性列表。
pub fn availability(summaries: Vec<AgentConnectorSummary>) -> Vec<CodingAgentAvailability> {
    summaries
        .into_iter()
        .map(|s| CodingAgentAvailability {
            agent: s.descriptor.id().to_string(),
            installed: s.state.detected,
            detection_path: s.state.detection_path,
        })
        .collect()
}
