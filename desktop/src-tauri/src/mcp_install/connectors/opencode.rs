// opencode.rs 实现 OpenCode 内置 Agent Connector（无损 JSONC）。
//
// 职责：
//   - 解析 OPENCODE_CONFIG 或 ~/.config/opencode/opencode.json
//   - 通过 jsonc-parser CST 仅写入/删除 mcp.superdev，保留注释与无关字段
//   - MCP 就绪后安装 Skill；Session Hook 保持手动
//
// 边界：
//   - 不经 serde_json 往返序列化整文件（那会丢失注释与 trailing commas）
//   - 不读取环境变量（覆盖路径来自 ConnectorEnvironment）
//   - 日志不记录路径或配置正文

use super::common;
use crate::mcp_install::contracts::*;
use crate::mcp_install::registry::*;
use crate::mcp_install::{executable_file_names, MergeResult, DEFAULT_AGENT_URL};
use jsonc_parser::cst::CstRootNode;
use jsonc_parser::json;
use jsonc_parser::{parse_to_serde_value, ParseOptions};
use std::fs;
use std::path::{Path, PathBuf};
use std::time::Instant;

const CONNECTOR_ID: &str = "opencode";
const DISPLAY_NAME: &str = "OpenCode";

/// OpenCodeConnector 适配 OpenCode 的 JSONC MCP 配置与 Skill。
pub(super) struct OpenCodeConnector {
    descriptor: AgentConnectorDescriptor,
}

impl OpenCodeConnector {
    /// new 创建标准支持级别的 OpenCode 连接器。
    #[allow(dead_code)] // Task 7 注册 builtin 时消费
    pub(super) fn new() -> Self {
        Self {
            descriptor: common::descriptor(
                CONNECTOR_ID,
                DISPLAY_NAME,
                SupportMode::Manual,
                None,
            ),
        }
    }
}

/// config_path 解析 OpenCode 配置文件路径。
fn config_path(ctx: &ConnectorRuntimeContext) -> PathBuf {
    ctx.environment()
        .opencode_config()
        .map(Path::to_path_buf)
        .unwrap_or_else(|| {
            ctx.home_dir()
                .join(".config")
                .join("opencode")
                .join("opencode.json")
        })
}

/// skill_path 返回 owned Skill 目录。
fn skill_path(ctx: &ConnectorRuntimeContext) -> PathBuf {
    ctx.home_dir()
        .join(".config")
        .join("opencode")
        .join("skills")
        .join("superdev")
}

/// data_root 返回默认数据/配置根（用于检测）。
fn data_root(ctx: &ConnectorRuntimeContext) -> PathBuf {
    ctx.home_dir().join(".config").join("opencode")
}

fn find_cli(ctx: &ConnectorRuntimeContext) -> Option<PathBuf> {
    ctx.command_dirs().iter().find_map(|directory| {
        executable_file_names("opencode")
            .into_iter()
            .map(|name| directory.join(name))
            .find(|path| path.is_file())
    })
}

/// superdev_mcp_value 构造 OpenCode 本地 MCP 条目的 CST 输入值。
fn superdev_mcp_value(ctx: &ConnectorRuntimeContext) -> jsonc_parser::cst::CstInputValue {
    let command = ctx.mcp_binary().to_string_lossy().into_owned();
    json!({
        "type": "local",
        "command": [command],
        "enabled": true,
        "environment": {
            "SUPERDEV_AGENT_URL": DEFAULT_AGENT_URL
        }
    })
}

/// expected_superdev_json 返回用于 status 比较的 serde 值。
fn expected_superdev_json(ctx: &ConnectorRuntimeContext) -> serde_json::Value {
    serde_json::json!({
        "type": "local",
        "command": [ctx.mcp_binary().to_string_lossy()],
        "enabled": true,
        "environment": {
            "SUPERDEV_AGENT_URL": DEFAULT_AGENT_URL
        }
    })
}

/// merge_opencode_jsonc 通过 CST 写入 mcp.superdev，保留无关文本切片。
///
/// 必须用 CST 而非 serde_json 往返：OpenCode 配置常含注释与 trailing commas，
/// 序列化会静默丢弃这些用户拥有的格式信息。
fn merge_opencode_jsonc(
    existing: Option<&str>,
    ctx: &ConnectorRuntimeContext,
) -> Result<MergeResult, ConnectorError> {
    let source = match existing {
        Some(text) if !text.trim().is_empty() => text,
        _ => "{\n}\n",
    };
    let root = CstRootNode::parse(source, &ParseOptions::default()).map_err(|error| {
        ConnectorError::new("invalid_config", format!("OpenCode JSONC 无法解析: {error}"))
    })?;
    let before = root.to_string();
    let root_obj = root.object_value_or_set();
    let mcp = root_obj.object_value_or_set("mcp");
    let value = superdev_mcp_value(ctx);
    if let Some(prop) = mcp.get("superdev") {
        prop.set_value(value);
    } else {
        mcp.append("superdev", value);
    }
    let after = root.to_string();
    Ok(MergeResult {
        content: after.clone(),
        changed: before != after,
    })
}

/// remove_opencode_superdev 仅删除 mcp.superdev，保留空的用户 mcp 对象。
fn remove_opencode_superdev(existing: Option<&str>) -> Result<MergeResult, ConnectorError> {
    let Some(source) = existing.filter(|text| !text.trim().is_empty()) else {
        return Ok(MergeResult {
            content: String::new(),
            changed: false,
        });
    };
    let root = CstRootNode::parse(source, &ParseOptions::default()).map_err(|error| {
        ConnectorError::new("invalid_config", format!("OpenCode JSONC 无法解析: {error}"))
    })?;
    let before = root.to_string();
    if let Some(root_obj) = root.object_value() {
        if let Some(mcp_prop) = root_obj.get("mcp") {
            if let Some(mcp_obj) = mcp_prop.object_value() {
                if let Some(superdev) = mcp_obj.get("superdev") {
                    superdev.remove();
                }
            }
        }
    }
    let after = root.to_string();
    Ok(MergeResult {
        content: after.clone(),
        changed: before != after,
    })
}

/// mcp_configured 用 parse_to_serde_value 做只读比较。
fn mcp_configured(ctx: &ConnectorRuntimeContext, content: &str) -> Result<bool, ConnectorError> {
    let value = parse_to_serde_value(content, &ParseOptions::default())
        .map_err(|error| {
            ConnectorError::new("invalid_config", format!("OpenCode JSONC 无法解析: {error}"))
        })?
        .unwrap_or(serde_json::Value::Null);
    let Some(superdev) = value.get("mcp").and_then(|mcp| mcp.get("superdev")) else {
        return Ok(false);
    };
    Ok(superdev == &expected_superdev_json(ctx))
}

fn pasteable_mcp_config(ctx: &ConnectorRuntimeContext) -> String {
    serde_json::to_string_pretty(&serde_json::json!({
        "mcp": {
            "superdev": expected_superdev_json(ctx)
        }
    }))
    .unwrap_or_else(|_| "{}".into())
}

impl AgentConnector for OpenCodeConnector {
    fn descriptor(&self) -> &AgentConnectorDescriptor {
        &self.descriptor
    }

    fn detect(&self, ctx: &ConnectorRuntimeContext) -> Result<ConnectorDetection, ConnectorError> {
        let started = Instant::now();
        tracing::debug!(
            connector_id = CONNECTOR_ID,
            operation = "detect",
            "opencode detect started"
        );
        let cli = find_cli(ctx);
        let root = data_root(ctx);
        let config = config_path(ctx);
        let hit = cli
            .or_else(|| root.is_dir().then_some(root))
            .or_else(|| config.exists().then_some(config));
        let result = ConnectorDetection {
            detected: hit.is_some(),
            detection_path: hit,
            message: Some("OpenCode 检测完成".into()),
        };
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = "detect",
            detected = result.detected,
            duration_ms = started.elapsed().as_millis() as u64,
            "opencode detect finished"
        );
        Ok(result)
    }

    fn status(&self, ctx: &ConnectorRuntimeContext) -> Result<ConnectorStatus, ConnectorError> {
        let started = Instant::now();
        tracing::debug!(
            connector_id = CONNECTOR_ID,
            operation = "status",
            "opencode status started"
        );
        let config = config_path(ctx);
        let skill = skill_path(ctx);
        let (mcp_status, mcp_message) = match fs::read_to_string(&config) {
            Ok(content) => match mcp_configured(ctx, &content) {
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
                    } else if parse_to_serde_value(&content, &ParseOptions::default()).is_err() {
                        (
                            IntegrationStateStatus::Error,
                            Some("配置 JSONC 无法解析".into()),
                        )
                    } else if content.contains("superdev") {
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
                        "opencode status parse failed"
                    );
                    return Err(error);
                }
            },
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => (
                IntegrationStateStatus::Missing,
                Some("MCP 配置文件不存在".into()),
            ),
            Err(error) => {
                tracing::error!(
                    connector_id = CONNECTOR_ID,
                    operation = "status",
                    error_code = "config_read_failed",
                    duration_ms = started.elapsed().as_millis() as u64,
                    "opencode status read failed"
                );
                return Err(ConnectorError::new(
                    "config_read_failed",
                    format!("读取 OpenCode 配置失败: {error}"),
                ));
            }
        };
        let skill_state = common::skill_status(ctx, &skill);
        let result = ConnectorStatus {
            integrations: vec![
                IntegrationState {
                    capability: IntegrationCapability::Mcp,
                    status: mcp_status,
                    target_path: Some(common::path_string(&config)),
                    message: mcp_message,
                },
                skill_state,
                IntegrationState {
                    capability: IntegrationCapability::SessionHook,
                    status: IntegrationStateStatus::Missing,
                    target_path: None,
                    message: Some("Session Hook 需手动配置（OpenCode plugin/startup）".into()),
                },
            ],
            requires_restart: mcp_status == IntegrationStateStatus::Configured,
            message: Some("OpenCode 状态已读取".into()),
        };
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = "status",
            mcp_status = ?mcp_status,
            duration_ms = started.elapsed().as_millis() as u64,
            "opencode status finished"
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
            "opencode install started"
        );
        let config = config_path(ctx);
        let skill = skill_path(ctx);

        let (mcp_result, mcp_backup, mcp_message) =
            if request.capabilities.contains(&IntegrationCapability::Mcp) {
                match common::mutate_config(CONNECTOR_ID, &config, |existing| {
                    merge_opencode_jsonc(existing, ctx)
                }) {
                    Ok(outcome) => {
                        tracing::info!(
                            connector_id = CONNECTOR_ID,
                            capability = "mcp",
                            changed = outcome.changed,
                            "opencode mcp mutation finished"
                        );
                        (
                            if outcome.changed {
                                IntegrationResult::Installed
                            } else {
                                IntegrationResult::AlreadyPresent
                            },
                            outcome.backup_path,
                            Some("MCP 配置已处理".into()),
                        )
                    }
                    Err(error) => {
                        tracing::error!(
                            connector_id = CONNECTOR_ID,
                            operation = ?request.operation,
                            capability = "mcp",
                            error_code = error.code(),
                            duration_ms = started.elapsed().as_millis() as u64,
                            "opencode mcp install failed"
                        );
                        return aggregate_connector_result(
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
                            Some("OpenCode MCP 配置失败".into()),
                        )
                        .map_err(|_| ConnectorError::new("aggregate_failed", "结果聚合失败"));
                    }
                }
            } else {
                let status = self.status(ctx)?;
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

        let status_after = self.status(ctx)?;
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
                    Some(common::path_string(&config)),
                    mcp_backup,
                    mcp_message,
                ),
                skill_result,
                common::manual_hook_result(None),
            ],
            Some(self.manual_instructions(ctx)?),
            true,
            Some("OpenCode 安装完成".into()),
        )
        .map_err(|_| ConnectorError::new("aggregate_failed", "结果聚合失败"))?;

        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = ?request.operation,
            result = ?outcome.result,
            duration_ms = started.elapsed().as_millis() as u64,
            "opencode install finished"
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
            "opencode uninstall started"
        );
        let config = config_path(ctx);
        let skill = skill_path(ctx);
        let mcp_outcome =
            match common::remove_config(CONNECTOR_ID, &config, remove_opencode_superdev) {
                Ok(outcome) => outcome,
                Err(error) if error.code() == "invalid_config" => {
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
                        message: Some("OpenCode 卸载遇到无效配置".into()),
                    });
                }
                Err(error) => {
                    tracing::error!(
                        connector_id = CONNECTOR_ID,
                        operation = "uninstall",
                        error_code = error.code(),
                        duration_ms = started.elapsed().as_millis() as u64,
                        "opencode uninstall failed"
                    );
                    return Err(error);
                }
            };

        let skill_result = common::uninstall_skill(&skill);
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
                    Some("已移除 mcp.superdev".into()),
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
            message: Some("OpenCode 卸载完成".into()),
        };
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = "uninstall",
            result = ?outcome.result,
            duration_ms = started.elapsed().as_millis() as u64,
            "opencode uninstall finished"
        );
        Ok(outcome)
    }

    fn manual_instructions(
        &self,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorManualInstructions, ConnectorError> {
        let config = config_path(ctx);
        let skill = skill_path(ctx);
        Ok(ConnectorManualInstructions {
            summary: "手动将 SuperDev 接入 OpenCode（本地 MCP schema）".into(),
            steps: vec![
                format!("编辑 OpenCode 配置：{}", config.display()),
                "写入 mcp.superdev（type=local, command 数组, enabled, environment）".into(),
                format!("确认 Skill 目录：{}", skill.display()),
                "重启 OpenCode；如使用 plugin/startup 扩展，按需手动注册 Session Hook".into(),
                "验证 MCP 列表中出现 superdev".into(),
            ],
            config_path: Some(common::path_string(&config)),
            manual_config: Some(pasteable_mcp_config(ctx)),
            verification_prompt: Some(
                "重启 OpenCode 后确认本地 MCP superdev 已启用并可连接".into(),
            ),
        })
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
            "opencode-{}-{}-{}",
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

    fn jsonc_fixture() -> &'static str {
        r#"{
  // keep this comment
  "theme": "dim",
  "mcp": {
    "other": {
      "type": "local",
      "command": ["other"],
      "enabled": true,
    },
  },
}
"#
    }

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
        let ctx = context_at(test_dir("home")).with_environment(ConnectorEnvironment::new(
            Some(override_path.clone()),
            None,
            None,
        ));
        assert_eq!(config_path(&ctx), override_path);
    }

    #[test]
    fn default_paths_use_native_pathbuf_segments() {
        let home = test_dir("default-paths");
        let ctx = context_at(home.clone());
        assert_eq!(
            config_path(&ctx),
            home.join(".config")
                .join("opencode")
                .join("opencode.json")
        );
        assert_eq!(
            skill_path(&ctx),
            home.join(".config")
                .join("opencode")
                .join("skills")
                .join("superdev")
        );
        let _ = fs::remove_dir_all(home);
    }

    #[test]
    fn jsonc_install_uninstall_preserves_comments_and_unrelated_slices() {
        let home = test_dir("jsonc-roundtrip");
        let config_dir = home.join(".config").join("opencode");
        fs::create_dir_all(&config_dir).unwrap();
        let config = config_dir.join("opencode.json");
        let fixture = jsonc_fixture();
        fs::write(&config, fixture).unwrap();
        let ctx = context_at(home.clone());
        let connector = OpenCodeConnector::new();

        let first = connector.install(&ctx, install_request()).unwrap();
        assert_eq!(first.result, ConnectorResult::Partial);
        let after_install = fs::read_to_string(&config).unwrap();
        assert!(
            after_install.contains("// keep this comment"),
            "comments must survive: {after_install}"
        );
        assert!(after_install.contains("\"theme\": \"dim\""));
        assert!(after_install.contains("\"other\""));
        // trailing comma style around other entries should still parse
        assert!(parse_to_serde_value(&after_install, &ParseOptions::default()).is_ok());

        // 字节级：无关切片（注释行与 theme 行）仍存在于输出中。
        assert!(after_install.contains("// keep this comment"));
        assert!(after_install.contains(r#""theme": "dim""#));

        let second = connector.install(&ctx, install_request()).unwrap();
        assert!(matches!(
            second.integrations[0].result,
            IntegrationResult::AlreadyPresent | IntegrationResult::Installed
        ));

        let removed = connector.uninstall(&ctx).unwrap();
        assert_eq!(removed.result, ConnectorResult::Success);
        let after_remove = fs::read_to_string(&config).unwrap();
        assert!(after_remove.contains("// keep this comment"));
        assert!(after_remove.contains(r#""theme": "dim""#));
        assert!(after_remove.contains("\"other\""));
        assert!(!after_remove.contains("\"superdev\""));
        // 空的用户 mcp 对象应被保留（至少仍有 mcp 键）。
        assert!(after_remove.contains("\"mcp\""));
        let _ = fs::remove_dir_all(home);
    }

    #[test]
    fn malformed_jsonc_is_fail_closed_without_backup() {
        let home = test_dir("malformed");
        let config_dir = home.join(".config").join("opencode");
        fs::create_dir_all(&config_dir).unwrap();
        let config = config_dir.join("opencode.json");
        fs::write(&config, "{broken,,").unwrap();
        let before = fs::read(&config).unwrap();
        let ctx = context_at(home.clone());
        let outcome = OpenCodeConnector::new()
            .install(&ctx, install_request())
            .unwrap();
        assert_eq!(outcome.result, ConnectorResult::Failed);
        assert_eq!(fs::read(&config).unwrap(), before);
        let backups: Vec<_> = fs::read_dir(&config_dir)
            .unwrap()
            .filter_map(Result::ok)
            .map(|e| e.file_name().to_string_lossy().into_owned())
            .filter(|n| n.contains("superdev-bak"))
            .collect();
        assert!(backups.is_empty());
        let _ = fs::remove_dir_all(home);
    }
}
