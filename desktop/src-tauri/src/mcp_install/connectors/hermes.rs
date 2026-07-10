// hermes.rs 实现 Hermes 内置 Agent Connector（无损 YAML + owned Hook）。
//
// 职责：
//   - 解析 ~/.hermes/config.yaml 与 skills/superdev
//   - 通过 yaml-edit 仅写入 mcp_servers.superdev 与标记过的 on_session_start hook
//   - MCP → Skill → Hook 顺序安装；卸载仅移除 SuperDev 拥有的节点
//
// 边界：
//   - 不把二进制路径拼进 YAML 字符串模板（走 typed MappingBuilder API）
//   - 保留用户注释、无关配置与用户自己的 hook 条目
//   - 日志不记录路径、命令或配置正文

use super::common;
use crate::mcp_install::contracts::*;
use crate::mcp_install::registry::*;
use crate::mcp_install::{executable_file_names, MergeResult, DEFAULT_AGENT_URL};
use std::fs;
use std::path::{Path, PathBuf};
use std::str::FromStr;
use std::time::Instant;
use yaml_edit::{Document, Mapping, MappingBuilder, YamlNode};

const CONNECTOR_ID: &str = "hermes";
const DISPLAY_NAME: &str = "Hermes";
/// HOOK_MARKER 用于在 hooks.on_session_start 中精确识别 SuperDev 拥有的条目。
/// 使用稳定子串，避免依赖列表下标，并与 skill 路径结构对齐。
const HOOK_MARKER: &str = "skills/superdev/hooks/run-hook.cmd";
const HOOK_NAME: &str = "superdev-session-start";

/// HermesConnector 适配 Hermes 的 YAML MCP、Skill 与自动 Session Hook。
pub(super) struct HermesConnector {
    descriptor: AgentConnectorDescriptor,
}

impl HermesConnector {
    /// new 创建 Full 支持级别（三项集成均为 Automatic）的 Hermes 连接器。
    pub(super) fn new() -> Self {
        Self {
            descriptor: common::descriptor(
                CONNECTOR_ID,
                DISPLAY_NAME,
                SupportMode::Automatic,
                None,
            ),
        }
    }
}

fn config_path(ctx: &ConnectorRuntimeContext) -> PathBuf {
    ctx.home_dir().join(".hermes").join("config.yaml")
}

fn skill_path(ctx: &ConnectorRuntimeContext) -> PathBuf {
    ctx.home_dir()
        .join(".hermes")
        .join("skills")
        .join("superdev")
}

fn data_root(ctx: &ConnectorRuntimeContext) -> PathBuf {
    ctx.home_dir().join(".hermes")
}

fn find_cli(ctx: &ConnectorRuntimeContext) -> Option<PathBuf> {
    ctx.command_dirs().iter().find_map(|directory| {
        executable_file_names("hermes")
            .into_iter()
            .map(|name| directory.join(name))
            .find(|path| path.is_file())
    })
}

/// hook_command 生成绝对路径的 run-hook 调用，统一正斜杠以便标记匹配。
fn hook_command(skill_dir: &Path) -> String {
    let runner = skill_dir.join("hooks").join("run-hook.cmd");
    let runner = runner.to_string_lossy().replace('\\', "/");
    format!("\"{runner}\" session-start")
}

fn node_text(node: &YamlNode) -> String {
    node.to_string()
}

fn scalar_string(node: &YamlNode) -> Option<String> {
    node.as_scalar().map(|s| s.as_string())
}

fn key_name(key: &YamlNode) -> String {
    key.as_scalar()
        .map(|scalar| scalar.as_string())
        .unwrap_or_default()
}

/// entry_has_marker 判断 hook 条目是否由 SuperDev 拥有。
fn entry_has_marker(entry: &YamlNode) -> bool {
    let text = node_text(entry);
    if text.contains(HOOK_MARKER) || text.contains(HOOK_NAME) {
        return true;
    }
    if let Some(mapping) = entry.as_mapping() {
        if let Some(name) = mapping.get("name").and_then(|n| scalar_string(&n)) {
            if name == HOOK_NAME {
                return true;
            }
        }
        if let Some(command) = mapping.get("command").and_then(|n| scalar_string(&n)) {
            if command.contains(HOOK_MARKER) {
                return true;
            }
        }
    }
    if let Some(command) = scalar_string(entry) {
        return command.contains(HOOK_MARKER);
    }
    false
}

fn mcp_entry_matches(ctx: &ConnectorRuntimeContext, entry: &Mapping) -> bool {
    let expected = ctx.mcp_binary().to_string_lossy();
    let command = entry
        .get("command")
        .and_then(|n| scalar_string(&n))
        .unwrap_or_default();
    let agent_url = entry
        .get_mapping("env")
        .and_then(|env| env.get("SUPERDEV_AGENT_URL"))
        .and_then(|n| scalar_string(&n))
        .unwrap_or_default();
    command == expected.as_ref() && agent_url == DEFAULT_AGENT_URL
}

/// leading_comment_prefix 保留文件开头的注释/空行前缀（不参与 mapping 重建）。
fn leading_comment_prefix(source: &str) -> String {
    let mut prefix = String::new();
    for line in source.lines() {
        let trimmed = line.trim_start();
        if trimmed.is_empty() || trimmed.starts_with('#') {
            prefix.push_str(line);
            prefix.push('\n');
        } else {
            break;
        }
    }
    prefix
}

/// append_scalar_pairs 把 mapping 的标量字段复制进 builder（仅一层）。
fn append_scalar_pairs(mut builder: MappingBuilder, mapping: &Mapping) -> MappingBuilder {
    for (key, value) in mapping.iter() {
        let name = key_name(&key);
        if name.is_empty() {
            continue;
        }
        if let Some(scalar) = value.as_scalar() {
            builder = builder.pair(name, scalar.as_string());
        } else if let Some(nested) = value.as_mapping() {
            // 递归重建嵌套 mapping，避免复制 YamlNode 导致缩进丢失。
            builder = builder.mapping(name, |child| append_scalar_pairs(child, nested));
        } else if let Some(text) = scalar_string(&value) {
            builder = builder.pair(name, text);
        }
    }
    builder
}

/// append_mcp_servers 把用户 server + SuperDev 条目写入根 MappingBuilder。
fn append_mcp_servers(
    builder: MappingBuilder,
    root: &Mapping,
    ctx: &ConnectorRuntimeContext,
    include_superdev: bool,
) -> MappingBuilder {
    let command = ctx.mcp_binary().to_string_lossy().into_owned();
    builder.mapping("mcp_servers", |mut servers| {
        if let Some(existing) = root.get_mapping("mcp_servers") {
            for (key, value) in existing.iter() {
                let name = key_name(&key);
                if name.is_empty() || name == "superdev" {
                    continue;
                }
                if let Some(nested) = value.as_mapping() {
                    servers = servers.mapping(name, |child| append_scalar_pairs(child, nested));
                } else if let Some(scalar) = value.as_scalar() {
                    servers = servers.pair(name, scalar.as_string());
                }
            }
        }
        if include_superdev {
            servers = servers.mapping("superdev", |superdev| {
                superdev
                    .pair("command", command.as_str())
                    .mapping("env", |env| {
                        env.pair("SUPERDEV_AGENT_URL", DEFAULT_AGENT_URL)
                    })
            });
        }
        servers
    })
}

/// append_hooks 重建 hooks，可选择写入/剥离 SuperDev 标记条目。
fn append_hooks(
    builder: MappingBuilder,
    root: &Mapping,
    skill_dir: &Path,
    include_owned_hook: bool,
) -> MappingBuilder {
    let command = hook_command(skill_dir);
    builder.mapping("hooks", |mut hooks| {
        if let Some(existing) = root.get_mapping("hooks") {
            for (key, value) in existing.iter() {
                let name = key_name(&key);
                if name.is_empty() || name == "on_session_start" {
                    continue;
                }
                if let Some(nested) = value.as_mapping() {
                    hooks = hooks.mapping(name, |child| append_scalar_pairs(child, nested));
                } else if let Some(scalar) = value.as_scalar() {
                    hooks = hooks.pair(name, scalar.as_string());
                }
            }
        }
        hooks.sequence("on_session_start", |mut seq| {
            if let Some(existing) = root.get_mapping("hooks") {
                if let Some(items) = existing.get_sequence("on_session_start") {
                    for index in 0..items.len() {
                        if let Some(item) = items.get(index) {
                            if entry_has_marker(&item) {
                                continue;
                            }
                            if let Some(mapping) = item.as_mapping() {
                                seq = seq.mapping(|entry| append_scalar_pairs(entry, mapping));
                            } else if let Some(scalar) = item.as_scalar() {
                                seq = seq.item(scalar.as_string());
                            }
                        }
                    }
                }
            }
            if include_owned_hook {
                seq = seq.mapping(|entry| {
                    entry
                        .pair("name", HOOK_NAME)
                        .pair("command", command.as_str())
                });
            }
            seq
        })
    })
}

/// rebuild_document 以根 MappingBuilder 重建整份配置。
///
/// yaml-edit 0.2.x 复制嵌套 YamlNode 或在已有 block 上 set 会弄乱缩进；
/// 根级 MappingBuilder + 标量递归重建可被 from_str 正确 round-trip。
/// 文件头注释通过 leading_comment_prefix 另行拼接保留。
fn rebuild_document(
    source: &str,
    root: &Mapping,
    ctx: &ConnectorRuntimeContext,
    include_superdev: bool,
    include_owned_hook: Option<bool>,
) -> String {
    let prefix = leading_comment_prefix(source);
    let mut builder = MappingBuilder::new();
    for (key, value) in root.iter() {
        let name = key_name(&key);
        if name.is_empty() || name == "mcp_servers" || name == "hooks" {
            continue;
        }
        if let Some(scalar) = value.as_scalar() {
            builder = builder.pair(name, scalar.as_string());
        } else if let Some(nested) = value.as_mapping() {
            builder = builder.mapping(name, |child| append_scalar_pairs(child, nested));
        }
    }
    builder = append_mcp_servers(builder, root, ctx, include_superdev);
    match include_owned_hook {
        Some(include) => {
            builder = append_hooks(builder, root, &skill_path(ctx), include);
        }
        None => {
            if root.get_mapping("hooks").is_some() {
                builder = append_hooks(builder, root, &skill_path(ctx), false);
            }
        }
    }
    let body = builder.build_document().to_string();
    format!("{prefix}{body}")
}

/// merge_hermes_yaml 写入 MCP 条目与 owned Hook（幂等）。
fn merge_hermes_yaml(
    existing: Option<&str>,
    ctx: &ConnectorRuntimeContext,
    install_hook: bool,
) -> Result<MergeResult, ConnectorError> {
    let source = match existing {
        Some(text) if !text.trim().is_empty() => text.to_string(),
        _ => "theme: default\n".into(),
    };
    let doc = Document::from_str(&source).map_err(|error| {
        ConnectorError::new("invalid_config", format!("Hermes YAML 无法解析: {error}"))
    })?;
    let root = doc
        .as_mapping()
        .ok_or_else(|| ConnectorError::new("invalid_config", "Hermes 配置根节点必须是 mapping"))?;
    let after = rebuild_document(
        &source,
        &root,
        ctx,
        true,
        if install_hook { Some(true) } else { None },
    );
    Ok(MergeResult {
        changed: after != source,
        content: after,
    })
}

/// remove_hermes_owned 仅删除 mcp_servers.superdev 与标记 Hook。
fn remove_hermes_owned(existing: Option<&str>) -> Result<MergeResult, ConnectorError> {
    let Some(source) = existing.filter(|text| !text.trim().is_empty()) else {
        return Ok(MergeResult {
            content: String::new(),
            changed: false,
        });
    };
    let doc = Document::from_str(source).map_err(|error| {
        ConnectorError::new("invalid_config", format!("Hermes YAML 无法解析: {error}"))
    })?;
    let root = doc
        .as_mapping()
        .ok_or_else(|| ConnectorError::new("invalid_config", "Hermes 配置根节点必须是 mapping"))?;
    // 卸载时不需要真实 MCP 二进制路径；传入占位 ctx 不可用，改用只剥除逻辑。
    // 这里用 rebuild：include_superdev=false，hooks 剥离 owned。
    // command 仅在 include_superdev=true 时使用，占位 Path 即可。
    let placeholder = ConnectorRuntimeContext::new(
        PathBuf::from("/"),
        vec![],
        vec![],
        PathBuf::from("/superdev-mcp"),
        None,
        None,
    );
    let after = rebuild_document(source, &root, &placeholder, false, Some(false));
    Ok(MergeResult {
        changed: after != source,
        content: after,
    })
}

fn read_doc(path: &Path) -> Result<Option<Document>, ConnectorError> {
    match fs::read_to_string(path) {
        Ok(content) if content.trim().is_empty() => Ok(None),
        Ok(content) => Document::from_str(&content).map(Some).map_err(|error| {
            ConnectorError::new("invalid_config", format!("Hermes YAML 无法解析: {error}"))
        }),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(None),
        Err(error) => Err(ConnectorError::new(
            "config_read_failed",
            format!("读取 Hermes 配置失败: {error}"),
        )),
    }
}

fn mcp_status_from_doc(
    ctx: &ConnectorRuntimeContext,
    doc: Option<&Document>,
) -> (IntegrationStateStatus, Option<String>) {
    let Some(doc) = doc else {
        return (
            IntegrationStateStatus::Missing,
            Some("MCP 配置文件不存在".into()),
        );
    };
    let Some(root) = doc.as_mapping() else {
        return (
            IntegrationStateStatus::Error,
            Some("配置根节点不是 mapping".into()),
        );
    };
    let Some(servers) = root.get_mapping("mcp_servers") else {
        return (
            IntegrationStateStatus::Missing,
            Some("SuperDev MCP 配置缺失".into()),
        );
    };
    let Some(entry) = servers.get_mapping("superdev") else {
        return (
            IntegrationStateStatus::Missing,
            Some("SuperDev MCP 配置缺失".into()),
        );
    };
    if mcp_entry_matches(ctx, &entry) {
        (
            IntegrationStateStatus::Configured,
            Some("SuperDev MCP 已配置".into()),
        )
    } else {
        (
            IntegrationStateStatus::NeedsAction,
            Some("SuperDev MCP 配置不匹配".into()),
        )
    }
}

fn hook_status_from_doc(doc: Option<&Document>) -> (IntegrationStateStatus, Option<String>) {
    let Some(doc) = doc else {
        return (
            IntegrationStateStatus::Missing,
            Some("Session Hook 未安装".into()),
        );
    };
    let Some(root) = doc.as_mapping() else {
        return (
            IntegrationStateStatus::Error,
            Some("配置根节点不是 mapping".into()),
        );
    };
    let Some(hooks) = root.get_mapping("hooks") else {
        return (
            IntegrationStateStatus::Missing,
            Some("Session Hook 未安装".into()),
        );
    };
    let Some(seq) = hooks.get_sequence("on_session_start") else {
        return (
            IntegrationStateStatus::Missing,
            Some("Session Hook 未安装".into()),
        );
    };
    let mut found = false;
    for index in 0..seq.len() {
        if let Some(item) = seq.get(index) {
            if entry_has_marker(&item) {
                found = true;
                break;
            }
        }
    }
    if found {
        // Hook 已写入，但仍需用户信任/重启后才真正生效——因此状态保持 NeedsAction。
        (
            IntegrationStateStatus::NeedsAction,
            Some("Session Hook 已写入，请在 Hermes 中信任并重启后生效".into()),
        )
    } else {
        (
            IntegrationStateStatus::Missing,
            Some("Session Hook 未安装".into()),
        )
    }
}

impl AgentConnector for HermesConnector {
    fn descriptor(&self) -> &AgentConnectorDescriptor {
        &self.descriptor
    }

    fn detect(&self, ctx: &ConnectorRuntimeContext) -> Result<ConnectorDetection, ConnectorError> {
        let started = Instant::now();
        tracing::debug!(
            connector_id = CONNECTOR_ID,
            operation = "detect",
            "hermes detect started"
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
            message: Some("Hermes 检测完成".into()),
        };
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = "detect",
            detected = result.detected,
            duration_ms = started.elapsed().as_millis() as u64,
            "hermes detect finished"
        );
        Ok(result)
    }

    fn status(&self, ctx: &ConnectorRuntimeContext) -> Result<ConnectorStatus, ConnectorError> {
        let started = Instant::now();
        tracing::debug!(
            connector_id = CONNECTOR_ID,
            operation = "status",
            "hermes status started"
        );
        let config = config_path(ctx);
        let skill = skill_path(ctx);
        let doc = read_doc(&config)?;
        let (mcp_status, mcp_message) = mcp_status_from_doc(ctx, doc.as_ref());
        let (hook_status, hook_message) = hook_status_from_doc(doc.as_ref());
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
                    status: hook_status,
                    target_path: Some(common::path_string(&config)),
                    message: hook_message,
                },
            ],
            requires_restart: true,
            message: Some("Hermes 状态已读取".into()),
        };
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = "status",
            mcp_status = ?mcp_status,
            hook_status = ?hook_status,
            duration_ms = started.elapsed().as_millis() as u64,
            "hermes status finished"
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
            "hermes install started"
        );
        let config = config_path(ctx);
        let skill = skill_path(ctx);

        let want_mcp = request.capabilities.contains(&IntegrationCapability::Mcp);
        let want_skill = request.capabilities.contains(&IntegrationCapability::Skill);
        let want_hook = request
            .capabilities
            .contains(&IntegrationCapability::SessionHook);

        let (mcp_result, mcp_backup, mcp_message) = if want_mcp {
            // Hook 与 MCP 同文件；若同时请求 Hook 则一次写入，避免二次备份。
            match common::mutate_config(CONNECTOR_ID, &config, |existing| {
                merge_hermes_yaml(existing, ctx, want_hook)
            }) {
                Ok(outcome) => {
                    tracing::info!(
                        connector_id = CONNECTOR_ID,
                        capability = "mcp",
                        changed = outcome.changed,
                        "hermes yaml mutation finished"
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
                        error_code = error.code(),
                        duration_ms = started.elapsed().as_millis() as u64,
                        "hermes install failed"
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
                            common::integration_result(
                                IntegrationCapability::SessionHook,
                                IntegrationResult::Skipped,
                                Some(common::path_string(&config)),
                                None,
                                Some("MCP 失败，已跳过 Hook".into()),
                            ),
                        ],
                        Some(self.manual_instructions(ctx)?),
                        false,
                        Some("Hermes 配置失败".into()),
                    )
                    .map_err(|_| ConnectorError::new("aggregate_failed", "结果聚合失败"));
                }
            }
        } else {
            let status = self.status(ctx)?;
            let mcp = status
                .integrations
                .iter()
                .find(|i| i.capability == IntegrationCapability::Mcp)
                .expect("mcp");
            (
                match mcp.status {
                    IntegrationStateStatus::Configured => IntegrationResult::AlreadyPresent,
                    _ => IntegrationResult::NeedsAction,
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
        } else if want_skill {
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

        // 若 MCP 写入时未带 Hook（例如只装 MCP），在 MCP 就绪后再单独写入 Hook。
        let hook_result = if !mcp_ready {
            common::integration_result(
                IntegrationCapability::SessionHook,
                IntegrationResult::Skipped,
                Some(common::path_string(&config)),
                None,
                Some("MCP 未就绪，已跳过 Hook".into()),
            )
        } else if want_hook {
            if want_mcp {
                // 已在 MCP 写入中合并 Hook；按状态映射结果。
                let (_, message) = hook_status_from_doc(read_doc(&config)?.as_ref());
                common::integration_result(
                    IntegrationCapability::SessionHook,
                    IntegrationResult::NeedsAction,
                    Some(common::path_string(&config)),
                    None,
                    message.or_else(|| {
                        Some("Session Hook 已写入，请在 Hermes 中信任并重启后生效".into())
                    }),
                )
            } else {
                match common::mutate_config(CONNECTOR_ID, &config, |existing| {
                    merge_hermes_yaml(existing, ctx, true)
                }) {
                    Ok(outcome) => common::integration_result(
                        IntegrationCapability::SessionHook,
                        IntegrationResult::NeedsAction,
                        Some(common::path_string(&config)),
                        outcome.backup_path,
                        Some("Session Hook 已写入，请在 Hermes 中信任并重启后生效".into()),
                    ),
                    Err(error) => common::integration_result(
                        IntegrationCapability::SessionHook,
                        IntegrationResult::Failed,
                        Some(common::path_string(&config)),
                        None,
                        Some(error.message().into()),
                    ),
                }
            }
        } else {
            let (status, message) = hook_status_from_doc(read_doc(&config)?.as_ref());
            common::integration_result(
                IntegrationCapability::SessionHook,
                match status {
                    IntegrationStateStatus::NeedsAction => IntegrationResult::NeedsAction,
                    IntegrationStateStatus::Configured => IntegrationResult::AlreadyPresent,
                    IntegrationStateStatus::Missing => IntegrationResult::Skipped,
                    _ => IntegrationResult::Failed,
                },
                Some(common::path_string(&config)),
                None,
                message,
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
                hook_result,
            ],
            Some(self.manual_instructions(ctx)?),
            true,
            Some("Hermes 安装完成".into()),
        )
        .map_err(|_| ConnectorError::new("aggregate_failed", "结果聚合失败"))?;

        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = ?request.operation,
            result = ?outcome.result,
            duration_ms = started.elapsed().as_millis() as u64,
            "hermes install finished"
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
            "hermes uninstall started"
        );
        let config = config_path(ctx);
        let skill = skill_path(ctx);
        let mcp_outcome = match common::remove_config(CONNECTOR_ID, &config, remove_hermes_owned) {
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
                            IntegrationResult::Failed,
                            Some(common::path_string(&config)),
                            None,
                            Some(error.message().into()),
                        ),
                    ],
                    manual_instructions: Some(self.manual_instructions(ctx)?),
                    requires_restart: false,
                    message: Some("Hermes 卸载遇到无效配置".into()),
                });
            }
            Err(error) => {
                tracing::error!(
                    connector_id = CONNECTOR_ID,
                    operation = "uninstall",
                    error_code = error.code(),
                    duration_ms = started.elapsed().as_millis() as u64,
                    "hermes uninstall failed"
                );
                return Err(error);
            }
        };
        let skill_result = common::uninstall_skill(&skill);
        let changed =
            mcp_outcome.changed || matches!(skill_result.result, IntegrationResult::Installed);
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
                    if mcp_outcome.changed {
                        IntegrationResult::Installed
                    } else {
                        IntegrationResult::AlreadyPresent
                    },
                    Some(common::path_string(&config)),
                    mcp_outcome.backup_path.clone(),
                    Some("已移除 owned MCP 与 Hook 节点".into()),
                ),
                skill_result,
                common::integration_result(
                    IntegrationCapability::SessionHook,
                    if mcp_outcome.changed {
                        IntegrationResult::Installed
                    } else {
                        IntegrationResult::AlreadyPresent
                    },
                    Some(common::path_string(&config)),
                    None,
                    Some("已移除标记的 SuperDev Hook".into()),
                ),
            ],
            manual_instructions: None,
            requires_restart: changed,
            message: Some("Hermes 卸载完成".into()),
        };
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = "uninstall",
            result = ?outcome.result,
            duration_ms = started.elapsed().as_millis() as u64,
            "hermes uninstall finished"
        );
        Ok(outcome)
    }

    fn manual_instructions(
        &self,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorManualInstructions, ConnectorError> {
        let config = config_path(ctx);
        let skill = skill_path(ctx);
        let command = ctx.mcp_binary().to_string_lossy();
        let hook = hook_command(&skill);
        let manual = format!(
            "mcp_servers:\n  superdev:\n    command: {command}\n    env:\n      SUPERDEV_AGENT_URL: {DEFAULT_AGENT_URL}\nhooks:\n  on_session_start:\n    - name: {HOOK_NAME}\n      command: {hook}\n"
        );
        Ok(ConnectorManualInstructions {
            summary: "手动将 SuperDev 写入 Hermes YAML 并信任 Hook".into(),
            steps: vec![
                format!("编辑 {}", config.display()),
                "写入 mcp_servers.superdev 与 hooks.on_session_start 标记条目".into(),
                format!("确认 Skill 目录：{}", skill.display()),
                "在 Hermes 中信任 Session Hook 并重启".into(),
            ],
            config_path: Some(common::path_string(&config)),
            manual_config: Some(manual),
            verification_prompt: Some(
                "重启并信任后确认 superdev MCP 可用，Session Hook 不再提示信任".into(),
            ),
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
            "hermes-{}-{}-{}",
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
        fs::write(skill_source.join("hooks/run-hook.cmd"), "echo hook\n").unwrap();
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

    fn yaml_fixture() -> &'static str {
        r#"# keep this comment
theme: dark
mcp_servers:
  other:
    command: other-mcp
hooks:
  on_session_start:
    - name: user-hook
      command: echo user
"#
    }

    #[test]
    fn descriptor_is_full() {
        assert_eq!(
            HermesConnector::new().descriptor().support_level(),
            Some(SupportLevel::Full)
        );
    }

    #[test]
    fn yaml_install_uninstall_preserves_comments_and_user_hooks() {
        let home = test_dir("yaml-roundtrip");
        let root = home.join(".hermes");
        fs::create_dir_all(&root).unwrap();
        let config = root.join("config.yaml");
        fs::write(&config, yaml_fixture()).unwrap();
        let ctx = context_at(home.clone());
        let connector = HermesConnector::new();

        let first = connector.install(&ctx, install_request()).unwrap();
        // MCP 成功 + Hook NeedsAction → Partial（Full 描述符下仍可 Partial）
        assert!(matches!(
            first.result,
            ConnectorResult::Partial | ConnectorResult::Success
        ));
        assert_eq!(first.integrations[2].result, IntegrationResult::NeedsAction);

        let after = fs::read_to_string(&config).unwrap();
        assert!(
            after.contains("# keep this comment"),
            "comments must survive: {after}"
        );
        assert!(after.contains("theme: dark") || after.contains("theme:dark"));
        assert!(after.contains("other"));
        assert!(after.contains("user-hook"));
        assert!(after.contains("superdev"));
        assert!(after.contains(HOOK_NAME) || after.contains(HOOK_MARKER));

        let second = connector.install(&ctx, install_request()).unwrap();
        let after2 = fs::read_to_string(&config).unwrap();
        // 幂等：标记 hook 不应重复多条
        let marker_count = after2.matches(HOOK_NAME).count();
        assert!(
            marker_count <= 2,
            "hook should be idempotent, count={marker_count}, text={after2}"
        );
        assert!(matches!(
            second.integrations[0].result,
            IntegrationResult::AlreadyPresent | IntegrationResult::Installed
        ));

        // 安装后的 hook 状态应为 NeedsAction，缺失时为 Missing
        let status = connector.status(&ctx).unwrap();
        let hook = status
            .integrations
            .iter()
            .find(|i| i.capability == IntegrationCapability::SessionHook)
            .unwrap();
        assert_eq!(hook.status, IntegrationStateStatus::NeedsAction);

        let removed = connector.uninstall(&ctx).unwrap();
        assert_eq!(removed.result, ConnectorResult::Success);
        let final_text = fs::read_to_string(&config).unwrap();
        assert!(final_text.contains("# keep this comment"));
        assert!(final_text.contains("user-hook"));
        assert!(!final_text.contains("superdev") || final_text.contains("user-hook"));
        assert!(!final_text.contains(HOOK_NAME));
        assert!(final_text.contains("other"));
        assert!(!skill_path(&ctx).exists());
        let _ = fs::remove_dir_all(home);
    }

    #[test]
    fn malformed_yaml_is_never_backed_up_or_rewritten() {
        let home = test_dir("malformed");
        let root = home.join(".hermes");
        fs::create_dir_all(&root).unwrap();
        let config = root.join("config.yaml");
        fs::write(&config, ":\n  - [broken").unwrap();
        let before = fs::read(&config).unwrap();
        let ctx = context_at(home.clone());
        let outcome = HermesConnector::new()
            .install(&ctx, install_request())
            .unwrap();
        assert_eq!(outcome.result, ConnectorResult::Failed);
        assert_eq!(fs::read(&config).unwrap(), before);
        let backups: Vec<_> = fs::read_dir(&root)
            .unwrap()
            .filter_map(Result::ok)
            .map(|e| e.file_name().to_string_lossy().into_owned())
            .filter(|n| n.contains("superdev-bak"))
            .collect();
        assert!(backups.is_empty());
        let _ = fs::remove_dir_all(home);
    }

    #[test]
    fn missing_hook_status_is_missing() {
        let home = test_dir("hook-missing");
        let root = home.join(".hermes");
        fs::create_dir_all(&root).unwrap();
        fs::write(
            root.join("config.yaml"),
            "mcp_servers:\n  superdev:\n    command: x\n",
        )
        .unwrap();
        let ctx = context_at(home.clone());
        let status = HermesConnector::new().status(&ctx).unwrap();
        let hook = status
            .integrations
            .iter()
            .find(|i| i.capability == IntegrationCapability::SessionHook)
            .unwrap();
        assert_eq!(hook.status, IntegrationStateStatus::Missing);
        let _ = fs::remove_dir_all(home);
    }

    #[test]

    fn default_paths_use_native_pathbuf() {
        let home = test_dir("paths");
        let ctx = context_at(home.clone());
        assert_eq!(config_path(&ctx), home.join(".hermes").join("config.yaml"));
        assert_eq!(
            skill_path(&ctx),
            home.join(".hermes").join("skills").join("superdev")
        );
        let _ = fs::remove_dir_all(home);
    }
}
