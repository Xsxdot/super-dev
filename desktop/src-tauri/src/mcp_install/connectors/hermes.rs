// hermes.rs 实现 Hermes 内置 Agent Connector（无损 YAML + owned Hook）。
//
// 职责：
//   - 解析 ~/.hermes/config.yaml 与 skills/superdev
//   - 通过 yaml-edit 仅写入 mcp_servers.superdev 与标记过的 pre_llm_call hook
//   - MCP → Skill → Hook 顺序安装；卸载仅移除 SuperDev 拥有的节点
//
// 边界：
//   - 不把二进制路径拼进 YAML 字符串模板（走 typed MappingBuilder API）
//   - 保留用户注释、无关配置与用户自己的 hook 条目
//   - 日志不记录路径、命令或配置正文

use super::common;
use crate::mcp_install::contracts::*;
use crate::mcp_install::fs_port::{ConnectorFs, LocalFs};
use crate::mcp_install::registry::*;
use crate::mcp_install::{executable_file_names, MergeResult};
#[cfg(test)]
use std::fs;
use std::path::{Path, PathBuf};
use std::str::FromStr;
use std::time::Instant;
use yaml_edit::{Document, Mapping, MappingBuilder, SequenceBuilder, YamlNode};

const CONNECTOR_ID: &str = "hermes";
const DISPLAY_NAME: &str = "Hermes";
/// CLI_COMMAND 是 find_cli 探测的命令名，同时也是 cli_commands() 对外汇报的值——
/// 两处共用同一个常量，避免各写一份字符串导致命令名漂移。
const CLI_COMMAND: &str = "hermes";
/// HOOK_MARKER 用于在 Hermes hooks 中精确识别 SuperDev 拥有的条目。
/// 使用稳定子串，避免依赖列表下标，并与 skill 路径结构对齐。
const HOOK_MARKER: &str = "skills/superdev/hooks/run-hook.cmd";
const HOOK_NAME: &str = "superdev-session-context";
const HOOK_EVENT: &str = "pre_llm_call";
const HOOK_SCRIPT: &str = "hermes-session-context";
const LEGACY_HOOK_NAME: &str = "superdev-session-start";
const LEGACY_HOOK_EVENT: &str = "on_session_start";
const LEGACY_HOOK_SCRIPT: &str = "session-start";

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

fn allowlist_path(ctx: &ConnectorRuntimeContext) -> PathBuf {
    data_root(ctx).join("shell-hooks-allowlist.json")
}

fn find_cli(ctx: &ConnectorRuntimeContext) -> Option<PathBuf> {
    ctx.command_dirs().iter().find_map(|directory| {
        executable_file_names(CLI_COMMAND)
            .into_iter()
            .map(|name| directory.join(name))
            .find(|path| path.is_file())
    })
}

/// hook_command 生成绝对路径的 run-hook 调用，统一正斜杠以便标记匹配。
fn hook_command(skill_dir: &Path) -> String {
    let runner = skill_dir.join("hooks").join("run-hook.cmd");
    let runner = runner.to_string_lossy().replace('\\', "/");
    format!("\"{runner}\" {HOOK_SCRIPT}")
}

fn scalar_string(node: &YamlNode) -> Option<String> {
    node.as_scalar().map(|s| s.as_string())
}

/// entry_has_marker 判断 hook 条目是否由 SuperDev 拥有。
fn entry_has_marker(entry: &YamlNode) -> bool {
    if let Some(mapping) = entry.as_mapping() {
        if let Some(name) = mapping.get("name").and_then(|n| scalar_string(&n)) {
            if name == HOOK_NAME || name == LEGACY_HOOK_NAME {
                return true;
            }
        }
        if let Some(command) = mapping.get("command").and_then(|n| scalar_string(&n)) {
            if command.contains(HOOK_MARKER)
                && (command.contains(HOOK_SCRIPT) || command.contains(LEGACY_HOOK_SCRIPT))
            {
                return true;
            }
        }
    }
    if let Some(command) = scalar_string(entry) {
        return command.contains(HOOK_MARKER)
            && (command.contains(HOOK_SCRIPT) || command.contains(LEGACY_HOOK_SCRIPT));
    }
    false
}

fn entry_matches_current_hook(entry: &YamlNode, skill_dir: &Path) -> bool {
    let Some(mapping) = entry.as_mapping() else {
        return false;
    };
    let name = mapping
        .get("name")
        .and_then(|node| scalar_string(&node))
        .unwrap_or_default();
    let command = mapping
        .get("command")
        .and_then(|node| scalar_string(&node))
        .unwrap_or_default();
    name == HOOK_NAME && command == hook_command(skill_dir)
}

fn mcp_entry_matches(ctx: &ConnectorRuntimeContext, entry: &Mapping) -> bool {
    let launch = ctx.mcp_launch();
    let expected = launch.command.to_string_lossy();
    let command = entry
        .get("command")
        .and_then(|n| scalar_string(&n))
        .unwrap_or_default();
    // args 键缺席等价于空列表（本机场景恒如此，判断结果与改造前一致）；远端场景
    // 下不比对 args，会把「命令对了但缺 mcp 子命令」的坏配置误判成已配置。
    let args: Vec<String> = entry
        .get_sequence("args")
        .map(|sequence| {
            sequence
                .values()
                .filter_map(|item| scalar_string(&item))
                .collect()
        })
        .unwrap_or_default();
    let agent_url = entry
        .get_mapping("env")
        .and_then(|env| env.get("SUPERDEV_AGENT_URL"))
        .and_then(|n| scalar_string(&n))
        .unwrap_or_default();
    command == expected.as_ref() && args == launch.args && agent_url == launch.agent_url
}

/// superdev_server_fields 往 `superdev:` 这层 mapping 里写入 command/args/env。
///
/// 三处生成点（fragment 替换、空配置自建、根节点追加）共用这一个函数，避免各写一份
/// 之后悄悄分叉。**args 为空时完全不写 `args` 键**——不是写一个空列表：本机 args 恒
/// 为空，这条保证桌面端升级后本地 Hermes 配置与升级前逐字节相同。
fn superdev_server_fields(
    server: MappingBuilder,
    launch: &crate::mcp_install::McpLaunchSpec,
) -> MappingBuilder {
    let server = server.pair("command", launch.command.to_string_lossy().into_owned());
    let server = if launch.args.is_empty() {
        server
    } else {
        let args = launch.args.clone();
        server.sequence("args", |sequence| {
            args.into_iter()
                .fold(sequence, |sequence, arg| sequence.item(arg))
        })
    };
    let agent_url = launch.agent_url.clone();
    server.mapping("env", |env| env.pair("SUPERDEV_AGENT_URL", agent_url))
}

fn desired_mcp_server_fragment(ctx: &ConnectorRuntimeContext) -> String {
    let launch = ctx.mcp_launch();
    MappingBuilder::new()
        .mapping("superdev", |server| superdev_server_fields(server, launch))
        .build_document()
        .to_string()
}

fn desired_hook_event_fragment(skill_dir: &Path) -> String {
    let command = hook_command(skill_dir);
    MappingBuilder::new()
        .sequence(HOOK_EVENT, |sequence| {
            sequence.mapping(|entry| entry.pair("name", HOOK_NAME).pair("command", command))
        })
        .build_document()
        .to_string()
}

fn desired_hook_item_fragment(skill_dir: &Path) -> String {
    let command = hook_command(skill_dir);
    SequenceBuilder::new()
        .mapping(|entry| entry.pair("name", HOOK_NAME).pair("command", command))
        .build_document()
        .to_string()
}

fn indent_fragment(fragment: &str, spaces: usize) -> String {
    let indent = " ".repeat(spaces);
    fragment
        .lines()
        .map(|line| format!("{indent}{line}"))
        .collect::<Vec<_>>()
        .join("\n")
}

fn node_indent(
    source: &str,
    needle: &str,
    label: &str,
    default: usize,
) -> Result<usize, ConnectorError> {
    let (start, _) = unique_span(source, needle, label)?;
    let line_start = source[..start].rfind('\n').map_or(0, |index| index + 1);
    let prefix = &source[line_start..start];
    Ok(if prefix.chars().all(|character| character == ' ') {
        prefix.len()
    } else {
        default
    })
}

fn parse_hermes_document(existing: Option<&str>) -> Result<(Document, String), ConnectorError> {
    let source = existing
        .filter(|text| !text.trim().is_empty())
        .unwrap_or_default()
        .to_string();
    let doc = if source.is_empty() {
        MappingBuilder::new().build_document()
    } else {
        Document::from_str(&source).map_err(|error| {
            ConnectorError::new("invalid_config", format!("Hermes YAML 无法解析: {error}"))
        })?
    };
    let root = doc
        .as_mapping()
        .ok_or_else(|| ConnectorError::new("invalid_config", "Hermes 配置根节点必须是 mapping"))?;
    // yaml-edit 不能安全地向 flow-style mapping 里追加 block-style 子节点。
    // 对这类罕见形态 fail closed，避免为了自动安装破坏用户文件。
    if root.is_flow_style() {
        return Err(ConnectorError::new(
            "unsupported_config_shape",
            "Hermes 配置根节点不支持 flow-style mapping",
        ));
    }
    if source.trim_end().ends_with("...") {
        return Err(ConnectorError::new(
            "unsupported_config_shape",
            "Hermes 配置不支持在显式文档结束标记后自动追加节点",
        ));
    }
    Ok((doc, source))
}

fn unique_span(source: &str, needle: &str, label: &str) -> Result<(usize, usize), ConnectorError> {
    if needle.is_empty() {
        return Err(ConnectorError::new(
            "config_transform_failed",
            format!("Hermes {label} 节点为空，无法安全定位"),
        ));
    }
    let matches = source
        .match_indices(needle)
        .map(|(start, _)| start)
        .collect::<Vec<_>>();
    if matches.len() != 1 {
        return Err(ConnectorError::new(
            "config_transform_failed",
            format!("Hermes {label} 节点无法唯一定位"),
        ));
    }
    Ok((matches[0], matches[0] + needle.len()))
}

fn validate_generated_yaml(content: String) -> Result<String, ConnectorError> {
    Document::from_str(&content).map_err(|error| {
        ConnectorError::new(
            "config_transform_failed",
            format!("Hermes YAML 变更后无法解析: {error}"),
        )
    })?;
    Ok(content)
}

fn block_end(source: &str, start: usize, indent: usize) -> usize {
    let mut cursor = source[start..]
        .find('\n')
        .map_or(source.len(), |offset| start + offset + 1);
    while cursor < source.len() {
        let next = source[cursor..]
            .find('\n')
            .map_or(source.len(), |offset| cursor + offset + 1);
        let line = source[cursor..next].trim_end_matches(['\r', '\n']);
        if !line.trim().is_empty() {
            let current_indent = line.len() - line.trim_start_matches(' ').len();
            if current_indent <= indent {
                return cursor;
            }
        }
        cursor = next;
    }
    source.len()
}

fn mapping_entry_span(
    source: &str,
    needle: &str,
    label: &str,
) -> Result<(usize, usize, usize), ConnectorError> {
    let (match_start, _) = unique_span(source, needle, label)?;
    let start = source[..match_start]
        .rfind('\n')
        .map_or(0, |index| index + 1);
    let indent = match_start - start;
    Ok((start, block_end(source, start, indent), indent))
}

fn key_line_matches(line: &str, key: &str) -> bool {
    let trimmed = line.trim_start_matches(' ');
    [
        format!("{key}:"),
        format!("'{key}':"),
        format!("\"{key}\":"),
    ]
    .iter()
    .any(|prefix| trimmed.starts_with(prefix))
}

fn mapping_key_span_within(
    source: &str,
    key: &str,
    range_start: usize,
    range_end: usize,
    minimum_indent: usize,
) -> Result<(usize, usize, usize), ConnectorError> {
    let mut candidates = Vec::new();
    let mut cursor = range_start;
    while cursor < range_end {
        let next = source[cursor..range_end]
            .find('\n')
            .map_or(range_end, |offset| cursor + offset + 1);
        let line = source[cursor..next].trim_end_matches(['\r', '\n']);
        let indent = line.len() - line.trim_start_matches(' ').len();
        if indent >= minimum_indent && key_line_matches(line, key) {
            candidates.push((
                cursor,
                block_end(source, cursor, indent).min(range_end),
                indent,
            ));
        }
        cursor = next;
    }
    if candidates.len() != 1 {
        return Err(ConnectorError::new(
            "config_transform_failed",
            format!("Hermes {key} 键无法唯一定位"),
        ));
    }
    Ok(candidates[0])
}

fn sequence_item_span(
    source: &str,
    needle: &str,
    label: &str,
) -> Result<(usize, usize), ConnectorError> {
    let (match_start, _) = unique_span(source, needle, label)?;
    let line_start = source[..match_start]
        .rfind('\n')
        .map_or(0, |index| index + 1);
    let prefix = &source[line_start..match_start];
    let mut start = line_start;
    if !prefix.contains('-') && line_start > 0 {
        let previous_end = line_start - 1;
        let previous_start = source[..previous_end]
            .rfind('\n')
            .map_or(0, |index| index + 1);
        if source[previous_start..previous_end].trim() == "-" {
            start = previous_start;
        }
    }
    let first_line_end = source[start..]
        .find('\n')
        .map_or(source.len(), |offset| start + offset);
    let first_line = &source[start..first_line_end];
    let indent = first_line.len() - first_line.trim_start_matches(' ').len();
    Ok((start, block_end(source, start, indent)))
}

fn remove_owned_hook_event(source: &str, event: &str) -> Result<String, ConnectorError> {
    let doc = Document::from_str(source).map_err(|error| {
        ConnectorError::new("invalid_config", format!("Hermes YAML 无法解析: {error}"))
    })?;
    let Some(hooks) = doc.as_mapping().and_then(|root| root.get_mapping("hooks")) else {
        return Ok(source.to_string());
    };
    let Some(sequence) = hooks.get_sequence(event) else {
        return Ok(source.to_string());
    };
    let mut owned = Vec::new();
    let mut user_count = 0usize;
    for index in 0..sequence.len() {
        if let Some(item) = sequence.get(index) {
            if entry_has_marker(&item) {
                owned.push(item.to_string());
            } else {
                user_count += 1;
            }
        }
    }
    if owned.is_empty() {
        return Ok(source.to_string());
    }
    if user_count == 0 {
        let entry = hooks
            .find_entry_by_key(event)
            .expect("validated event must have a mapping entry");
        let (start, end, _) = mapping_entry_span(source, &entry.to_string(), event)?;
        let mut after = source.to_string();
        after.replace_range(start..end, "");
        return validate_generated_yaml(after);
    }
    let mut spans = owned
        .iter()
        .map(|needle| sequence_item_span(source, needle, event))
        .collect::<Result<Vec<_>, _>>()?;
    spans.sort_unstable();
    spans.dedup();
    let mut after = source.to_string();
    for (start, end) in spans.into_iter().rev() {
        after.replace_range(start..end, "");
    }
    validate_generated_yaml(after)
}

fn append_fragment_at_node_end(
    source: &str,
    node_text: &str,
    fragment: &str,
    label: &str,
) -> Result<String, ConnectorError> {
    let (_, end) = unique_span(source, node_text, label)?;
    let mut insertion = String::new();
    if !node_text.ends_with('\n') {
        insertion.push('\n');
    }
    insertion.push_str(fragment);
    if !fragment.ends_with('\n') {
        insertion.push('\n');
    }
    let mut after = source.to_string();
    after.insert_str(end, &insertion);
    validate_generated_yaml(after)
}

fn append_root_fragment(source: &str, fragment: &str) -> Result<String, ConnectorError> {
    let mut after = source.to_string();
    if !after.is_empty() && !after.ends_with('\n') {
        after.push('\n');
    }
    after.push_str(fragment);
    if !fragment.ends_with('\n') {
        after.push('\n');
    }
    validate_generated_yaml(after)
}

/// merge_hermes_mcp 仅写入 MCP owned 节点，为 Skill 安装保留明确的顺序边界。
fn merge_hermes_mcp(
    existing: Option<&str>,
    ctx: &ConnectorRuntimeContext,
) -> Result<MergeResult, ConnectorError> {
    let (doc, source) = parse_hermes_document(existing)?;
    if source.is_empty() {
        let launch = ctx.mcp_launch();
        let content = MappingBuilder::new()
            .mapping("mcp_servers", |servers| {
                servers.mapping("superdev", |server| superdev_server_fields(server, launch))
            })
            .build_document()
            .to_string();
        return Ok(MergeResult {
            changed: true,
            content,
        });
    }
    let root = doc
        .as_mapping()
        .ok_or_else(|| ConnectorError::new("invalid_config", "Hermes 配置根节点必须是 mapping"))?;
    let after = if let Some(servers) = root.get_mapping("mcp_servers") {
        if servers.is_flow_style() {
            return Err(ConnectorError::new(
                "unsupported_config_shape",
                "Hermes mcp_servers 不支持 flow-style mapping",
            ));
        }
        if servers.find_entry_by_key("superdev").is_some() {
            if servers
                .get_mapping("superdev")
                .is_some_and(|entry| mcp_entry_matches(ctx, &entry))
            {
                return Ok(MergeResult {
                    changed: false,
                    content: source,
                });
            }
            let (mcp_start, mcp_end, _) =
                mapping_key_span_within(&source, "mcp_servers", 0, source.len(), 0)?;
            let (start, end, indent) =
                mapping_key_span_within(&source, "superdev", mcp_start, mcp_end, 1)?;
            let mut after = source.clone();
            let fragment = format!(
                "{}\n",
                indent_fragment(&desired_mcp_server_fragment(ctx), indent)
            );
            after.replace_range(start..end, &fragment);
            validate_generated_yaml(after)?
        } else {
            let servers_text = servers.to_string();
            let fragment = indent_fragment(
                &desired_mcp_server_fragment(ctx),
                node_indent(&source, &servers_text, "mcp_servers", 2)?,
            );
            append_fragment_at_node_end(&source, &servers_text, &fragment, "mcp_servers")?
        }
    } else if root.contains_key("mcp_servers") {
        return Err(ConnectorError::new(
            "unsupported_config_shape",
            "Hermes mcp_servers 必须是 mapping",
        ));
    } else {
        let launch = ctx.mcp_launch();
        let fragment = MappingBuilder::new()
            .mapping("mcp_servers", |servers| {
                servers.mapping("superdev", |server| superdev_server_fields(server, launch))
            })
            .build_document()
            .to_string();
        append_root_fragment(&source, &fragment)?
    };
    Ok(MergeResult {
        changed: after != source,
        content: after,
    })
}

/// merge_hermes_hook 在 Skill 就绪后写入 pre_llm_call，并迁移旧版 owned hook。
fn merge_hermes_hook(
    existing: Option<&str>,
    ctx: &ConnectorRuntimeContext,
) -> Result<MergeResult, ConnectorError> {
    let (doc, source) = parse_hermes_document(existing)?;
    let root = doc
        .as_mapping()
        .ok_or_else(|| ConnectorError::new("invalid_config", "Hermes 配置根节点必须是 mapping"))?;
    let skill = skill_path(ctx);
    if root.contains_key("hooks") && root.get_mapping("hooks").is_none() {
        return Err(ConnectorError::new(
            "unsupported_config_shape",
            "Hermes hooks 必须是 mapping",
        ));
    }
    if let Some(hooks) = root.get_mapping("hooks") {
        if hooks.is_flow_style() {
            return Err(ConnectorError::new(
                "unsupported_config_shape",
                "Hermes hooks 不支持 flow-style mapping",
            ));
        }
        if let Some(current) = hooks.get_sequence(HOOK_EVENT) {
            if current.is_flow_style() {
                return Err(ConnectorError::new(
                    "unsupported_config_shape",
                    "Hermes hooks.pre_llm_call 不支持 flow-style sequence",
                ));
            }
        } else if hooks.contains_key(HOOK_EVENT) {
            return Err(ConnectorError::new(
                "unsupported_config_shape",
                "Hermes hooks.pre_llm_call 必须是 sequence",
            ));
        }
    }

    // 先以文本块精确移除旧版/重复 owned 条目，避免 yaml-edit remove 吞掉相邻换行。
    let base = remove_owned_hook_event(&source, LEGACY_HOOK_EVENT)?;
    let base = remove_owned_hook_event(&base, HOOK_EVENT)?;
    let base_doc = Document::from_str(&base).map_err(|error| {
        ConnectorError::new(
            "config_transform_failed",
            format!("Hermes Hook 迁移后无法解析: {error}"),
        )
    })?;
    let base_root = base_doc
        .as_mapping()
        .ok_or_else(|| ConnectorError::new("invalid_config", "Hermes 配置根节点必须是 mapping"))?;
    let after = if let Some(hooks) = base_root.get_mapping("hooks") {
        if let Some(current) = hooks.get_sequence(HOOK_EVENT) {
            let sequence_text = current.to_string();
            let fragment = indent_fragment(
                &desired_hook_item_fragment(&skill),
                node_indent(&base, &sequence_text, "hooks.pre_llm_call", 4)?,
            );
            append_fragment_at_node_end(&base, &sequence_text, &fragment, "hooks.pre_llm_call")?
        } else if hooks.contains_key(HOOK_EVENT) {
            return Err(ConnectorError::new(
                "unsupported_config_shape",
                "Hermes hooks.pre_llm_call 必须是 sequence",
            ));
        } else if hooks.is_empty() {
            let entry = base_root
                .find_entry_by_key("hooks")
                .expect("existing hooks mapping must have an entry");
            let needle = entry.to_string();
            let (start, end, _) = mapping_entry_span(&base, &needle, "hooks")?;
            let command = hook_command(&skill);
            let fragment = MappingBuilder::new()
                .mapping("hooks", |hooks| {
                    hooks.sequence(HOOK_EVENT, |sequence| {
                        sequence
                            .mapping(|entry| entry.pair("name", HOOK_NAME).pair("command", command))
                    })
                })
                .build_document()
                .to_string();
            let mut after = base.clone();
            after.replace_range(start..end, &format!("{fragment}\n"));
            validate_generated_yaml(after)?
        } else {
            let hooks_text = hooks.to_string();
            let fragment = indent_fragment(
                &desired_hook_event_fragment(&skill),
                node_indent(&base, &hooks_text, "hooks", 2)?,
            );
            append_fragment_at_node_end(&base, &hooks_text, &fragment, "hooks")?
        }
    } else {
        let command = hook_command(&skill);
        let fragment = MappingBuilder::new()
            .mapping("hooks", |hooks| {
                hooks.sequence(HOOK_EVENT, |sequence| {
                    sequence.mapping(|entry| entry.pair("name", HOOK_NAME).pair("command", command))
                })
            })
            .build_document()
            .to_string();
        append_root_fragment(&base, &fragment)?
    };
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
    let mut after = source.to_string();
    if let Some(servers) = root.get_mapping("mcp_servers") {
        if servers.find_entry_by_key("superdev").is_some() {
            let (mcp_start, mcp_end, _) =
                mapping_key_span_within(&after, "mcp_servers", 0, after.len(), 0)?;
            let (start, end, _) = if servers.len() == 1 {
                (mcp_start, mcp_end, 0)
            } else {
                mapping_key_span_within(&after, "superdev", mcp_start, mcp_end, 1)?
            };
            after.replace_range(start..end, "");
        }
    }
    after = remove_owned_hook_event(&after, HOOK_EVENT)?;
    after = remove_owned_hook_event(&after, LEGACY_HOOK_EVENT)?;
    let after = validate_generated_yaml(after)?;
    Ok(MergeResult {
        changed: after != source,
        content: after,
    })
}

/// read_doc 读取并解析【目标机】上的 Hermes YAML 配置。
///
/// 参数：
///   - fs_port: 目标机文件操作端口
///   - path: 配置文件路径
///
/// 返回：
///   - 文件不存在或内容全为空白时 Ok(None)；解析失败时 invalid_config
fn read_doc(fs_port: &dyn ConnectorFs, path: &Path) -> Result<Option<Document>, ConnectorError> {
    match fs_port.read_optional(path) {
        Ok(Some(content)) if content.trim().is_empty() => Ok(None),
        Ok(Some(content)) => Document::from_str(&content).map(Some).map_err(|error| {
            ConnectorError::new("invalid_config", format!("Hermes YAML 无法解析: {error}"))
        }),
        Ok(None) => Ok(None),
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

fn hook_trust_status(
    fs_port: &dyn ConnectorFs,
    ctx: &ConnectorRuntimeContext,
) -> (IntegrationStateStatus, Option<String>) {
    let path = allowlist_path(ctx);
    let content = match fs_port.read_optional(&path) {
        Ok(Some(content)) => content,
        Ok(None) => {
            return (
                IntegrationStateStatus::NeedsAction,
                Some("Session Hook 已写入，请在 Hermes 中信任后重启".into()),
            );
        }
        Err(error) => {
            return (
                IntegrationStateStatus::Error,
                Some(format!("读取 Hermes Hook 信任状态失败: {error}")),
            );
        }
    };
    let allowlist: serde_json::Value = match serde_json::from_str(&content) {
        Ok(value) => value,
        Err(error) => {
            return (
                IntegrationStateStatus::Error,
                Some(format!("Hermes Hook 信任文件无法解析: {error}")),
            );
        }
    };
    let expected = hook_command(&skill_path(ctx));
    let approved = allowlist
        .get("approvals")
        .and_then(serde_json::Value::as_array)
        .is_some_and(|approvals| {
            approvals.iter().any(|approval| {
                approval.get("event").and_then(serde_json::Value::as_str) == Some(HOOK_EVENT)
                    && approval.get("command").and_then(serde_json::Value::as_str)
                        == Some(expected.as_str())
            })
        });
    if approved {
        (
            IntegrationStateStatus::Configured,
            Some("Session Hook 已配置并获得 Hermes 信任".into()),
        )
    } else {
        (
            IntegrationStateStatus::NeedsAction,
            Some("Session Hook 已写入，请在 Hermes 中信任后重启".into()),
        )
    }
}

fn hook_status_from_doc(
    fs_port: &dyn ConnectorFs,
    ctx: &ConnectorRuntimeContext,
    doc: Option<&Document>,
) -> (IntegrationStateStatus, Option<String>) {
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
    let Some(seq) = hooks.get_sequence(HOOK_EVENT) else {
        return (
            IntegrationStateStatus::Missing,
            Some("Session Hook 未安装".into()),
        );
    };
    let mut found = false;
    for index in 0..seq.len() {
        if let Some(item) = seq.get(index) {
            if entry_matches_current_hook(&item, &skill_path(ctx)) {
                found = true;
                break;
            }
        }
    }
    if found {
        hook_trust_status(fs_port, ctx)
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

    fn cli_commands(&self) -> Vec<String> {
        vec![CLI_COMMAND.to_string()]
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
        // detect 是【本机】操作，刻意保留直连 std::fs：远端场景下 CLI 存在性
        // 一律来自目标机的 `/api/integrations/detect` 端点，编排层从不调用连接器
        // 自己的 detect()。
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

    /// status 是 `status_with_fs(ctx, &LocalFs)` —— **恒绑定本机**。
    ///
    /// 拿一个远端 ctx（home 指向目标机）调它，会去读**桌面机自己**磁盘上那些
    /// 路径，把读到的内容当成目标机状态返回，且不会有任何报错。远端场景一律走
    /// `PortedConnectorOps::status_with_fs` 并显式传 `RemoteAgentFs`
    /// （`remote_install::ported_remote_status` 是唯一入口）。
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
        // 恢复指引与自动安装共用同一 YAML 生成路径，避免带空格路径的引用规则漂移。
        let mcp = merge_hermes_mcp(None, ctx)?.content;
        let manual = merge_hermes_hook(Some(&mcp), ctx)?.content;
        Ok(ConnectorManualInstructions {
            summary: "手动将 SuperDev 写入 Hermes YAML 并信任 Hook".into(),
            steps: vec![
                format!("编辑 {}", config.display()),
                format!("写入 mcp_servers.superdev 与 hooks.{HOOK_EVENT} 标记条目"),
                format!("确认 Skill 目录：{}", skill.display()),
                "运行 `hermes hooks list` 检查 Hook，在 Hermes 提示时信任并重启".into(),
                "若 Hook 未生效，运行 `hermes hooks doctor` 诊断".into(),
            ],
            config_path: Some(common::path_string(&config)),
            manual_config: Some(manual),
            verification_prompt: Some(
                "重启并信任后确认 superdev MCP 可用，Session Hook 不再提示信任".into(),
            ),
        })
    }
}

impl PortedConnectorOps for HermesConnector {
    fn status_with_fs(
        &self,
        ctx: &ConnectorRuntimeContext,
        fs_port: &dyn ConnectorFs,
    ) -> Result<ConnectorStatus, ConnectorError> {
        let started = Instant::now();
        tracing::debug!(
            connector_id = CONNECTOR_ID,
            operation = "status",
            "hermes status started"
        );
        let config = config_path(ctx);
        let skill = skill_path(ctx);
        let doc = read_doc(fs_port, &config)?;
        let (mcp_status, mcp_message) = mcp_status_from_doc(ctx, doc.as_ref());
        let (hook_status, hook_message) = hook_status_from_doc(fs_port, ctx, doc.as_ref());
        let skill_state = common::skill_status(fs_port, ctx, &skill);
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
            requires_restart: false,
            message: None,
            mcp_command,
            agent_url,
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
            match common::mutate_config_with_fs(fs_port, CONNECTOR_ID, &config, |existing| {
                merge_hermes_mcp(existing, ctx)
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
            let status = self.status_with_fs(ctx, fs_port)?;
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
        } else if want_skill {
            common::install_skill(fs_port, ctx, &skill)
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
        let skill_ready = matches!(
            skill_result.result,
            IntegrationResult::Installed | IntegrationResult::AlreadyPresent
        );

        let hook_result = if !mcp_ready {
            common::integration_result(
                IntegrationCapability::SessionHook,
                IntegrationResult::Skipped,
                Some(common::path_string(&config)),
                None,
                Some("MCP 未就绪，已跳过 Hook".into()),
            )
        } else if want_hook && !skill_ready {
            // Hook 依赖 Skill 内的可执行脚本，不允许留下指向空路径的活跃配置。
            common::integration_result(
                IntegrationCapability::SessionHook,
                IntegrationResult::Skipped,
                Some(common::path_string(&config)),
                None,
                Some("Skill 未就绪，已跳过 Hook".into()),
            )
        } else if want_hook {
            match common::mutate_config_with_fs(fs_port, CONNECTOR_ID, &config, |existing| {
                merge_hermes_hook(existing, ctx)
            }) {
                Ok(mutation) => {
                    let (status, message) =
                        hook_status_from_doc(fs_port, ctx, read_doc(fs_port, &config)?.as_ref());
                    let result = match status {
                        IntegrationStateStatus::Configured if mutation.changed => {
                            IntegrationResult::Installed
                        }
                        IntegrationStateStatus::Configured => IntegrationResult::AlreadyPresent,
                        IntegrationStateStatus::NeedsAction => IntegrationResult::NeedsAction,
                        IntegrationStateStatus::Missing
                        | IntegrationStateStatus::Error
                        | IntegrationStateStatus::Unknown => IntegrationResult::Failed,
                    };
                    common::integration_result(
                        IntegrationCapability::SessionHook,
                        result,
                        Some(common::path_string(&config)),
                        mutation.backup_path,
                        message,
                    )
                }
                Err(error) => common::integration_result(
                    IntegrationCapability::SessionHook,
                    IntegrationResult::Failed,
                    Some(common::path_string(&config)),
                    None,
                    Some(error.message().into()),
                ),
            }
        } else {
            let (status, message) =
                hook_status_from_doc(fs_port, ctx, read_doc(fs_port, &config)?.as_ref());
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

    fn uninstall_with_fs(
        &self,
        ctx: &ConnectorRuntimeContext,
        fs_port: &dyn ConnectorFs,
    ) -> Result<ConnectorOperationOutcome, ConnectorError> {
        let started = Instant::now();
        tracing::info!(
            connector_id = CONNECTOR_ID,
            operation = "uninstall",
            "hermes uninstall started"
        );
        let config = config_path(ctx);
        let skill = skill_path(ctx);
        let mcp_outcome = match common::remove_config_with_fs(
            fs_port,
            CONNECTOR_ID,
            &config,
            remove_hermes_owned,
        ) {
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
                        common::uninstall_skill(fs_port, &skill),
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
        let skill_result = common::uninstall_skill(fs_port, &skill);
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
        fs::write(
            skill_source.join("hooks/hermes-session-context"),
            "#!/bin/sh\nprintf '{}\\n'\n",
        )
        .unwrap();
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
    fn yaml_mutation_preserves_sequences_block_scalars_and_inline_comments() {
        let home = test_dir("yaml-lossless");
        let root = home.join(".hermes");
        fs::create_dir_all(&root).unwrap();
        let config = root.join("config.yaml");
        let fixture = r#"theme: dark # keep inline comment
toolsets:
  - hermes-cli
  - mcp-other
notes: |
  keep this block
  exactly as written
mcp_servers:
  other:
    command: npx
    args: ["-y", "other-mcp"] # keep args and comment
hooks:
  post_tool_call:
    - matcher: "write_file|patch"
      command: "echo user-hook"
"#;
        fs::write(&config, fixture).unwrap();
        let ctx = context_at(home.clone());
        let connector = HermesConnector::new();

        connector.install(&ctx, install_request()).unwrap();
        let installed = fs::read_to_string(&config).unwrap();
        for preserved in [
            "theme: dark # keep inline comment",
            "  - hermes-cli\n  - mcp-other",
            "notes: |\n  keep this block\n  exactly as written",
            "args: [\"-y\", \"other-mcp\"] # keep args and comment",
            "matcher: \"write_file|patch\"",
            "command: \"echo user-hook\"",
        ] {
            assert!(
                installed.contains(preserved),
                "install must preserve `{preserved}` byte-for-byte:\n{installed}"
            );
        }

        connector.uninstall(&ctx).unwrap();
        let removed = fs::read_to_string(&config).unwrap();
        for preserved in [
            "theme: dark # keep inline comment",
            "  - hermes-cli\n  - mcp-other",
            "notes: |\n  keep this block\n  exactly as written",
            "args: [\"-y\", \"other-mcp\"] # keep args and comment",
            "matcher: \"write_file|patch\"",
            "command: \"echo user-hook\"",
        ] {
            assert!(
                removed.contains(preserved),
                "uninstall must preserve `{preserved}` byte-for-byte:\n{removed}"
            );
        }
        let _ = fs::remove_dir_all(home);
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
    fn mismatched_existing_mcp_is_replaced_without_corrupting_adjacent_keys() {
        let home = test_dir("mcp-replace");
        let root = home.join(".hermes");
        fs::create_dir_all(&root).unwrap();
        let config = root.join("config.yaml");
        fs::write(
            &config,
            "mcp_servers:\n  superdev:\n    command: old-command\n    env:\n      SUPERDEV_AGENT_URL: http://old.invalid\ntheme: dark # preserve me\n",
        )
        .unwrap();
        let ctx = context_at(home.clone());
        let connector = HermesConnector::new();

        let outcome = connector.install(&ctx, install_request()).unwrap();
        assert_ne!(outcome.result, ConnectorResult::Failed);
        let content = fs::read_to_string(&config).unwrap();
        let parsed = Document::from_str(&content).expect("updated YAML must remain valid");
        let root = parsed.as_mapping().unwrap();
        assert_eq!(
            root.get("theme").and_then(|node| scalar_string(&node)),
            Some("dark".into())
        );
        assert!(content.contains("theme: dark # preserve me"), "{content}");
        let status = connector.status(&ctx).unwrap();
        assert_eq!(
            status
                .integrations
                .iter()
                .find(|item| item.capability == IntegrationCapability::Mcp)
                .unwrap()
                .status,
            IntegrationStateStatus::Configured
        );
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
    fn hook_is_not_written_when_skill_install_fails() {
        let home = test_dir("hook-skill-failure");
        let ctx = ConnectorRuntimeContext::new(
            home.clone(),
            vec![home.join("bin")],
            vec![],
            home.join("superdev-mcp"),
            None,
            Some("bundled skill unavailable".into()),
        );
        let connector = HermesConnector::new();

        let outcome = connector.install(&ctx, install_request()).unwrap();
        let config = fs::read_to_string(config_path(&ctx)).unwrap();
        assert!(
            !config.contains(HOOK_MARKER) && !config.contains(HOOK_NAME),
            "failed Skill must not leave an active hook:\n{config}"
        );
        let hook = outcome
            .integrations
            .iter()
            .find(|item| item.capability == IntegrationCapability::SessionHook)
            .unwrap();
        assert!(matches!(
            hook.result,
            IntegrationResult::Skipped | IntegrationResult::Failed
        ));
        let _ = fs::remove_dir_all(home);
    }

    #[test]
    fn hook_uses_hermes_context_protocol_and_becomes_configured_after_trust() {
        let home = test_dir("hook-protocol");
        let ctx = context_at(home.clone());
        let connector = HermesConnector::new();
        connector.install(&ctx, install_request()).unwrap();

        let config = fs::read_to_string(config_path(&ctx)).unwrap();
        assert!(config.contains("pre_llm_call"), "{config}");
        assert!(config.contains("hermes-session-context"), "{config}");
        assert!(
            !config.contains("on_session_start"),
            "legacy hook must be migrated:\n{config}"
        );
        assert!(skill_path(&ctx)
            .join("hooks")
            .join("hermes-session-context")
            .is_file());

        let before = connector.status(&ctx).unwrap();
        let before_hook = before
            .integrations
            .iter()
            .find(|item| item.capability == IntegrationCapability::SessionHook)
            .unwrap();
        assert_eq!(before_hook.status, IntegrationStateStatus::NeedsAction);

        let allowlist = home.join(".hermes").join("shell-hooks-allowlist.json");
        fs::write(
            allowlist,
            serde_json::to_vec_pretty(&serde_json::json!({
                "approvals": [{
                    "event": "pre_llm_call",
                    "command": hook_command(&skill_path(&ctx))
                }]
            }))
            .unwrap(),
        )
        .unwrap();
        let trusted = connector.status(&ctx).unwrap();
        let trusted_hook = trusted
            .integrations
            .iter()
            .find(|item| item.capability == IntegrationCapability::SessionHook)
            .unwrap();
        assert_eq!(trusted_hook.status, IntegrationStateStatus::Configured);
        let _ = fs::remove_dir_all(home);
    }

    #[test]
    fn current_event_user_hooks_survive_install_update_and_uninstall() {
        let home = test_dir("hook-user-current-event");
        let root = home.join(".hermes");
        fs::create_dir_all(&root).unwrap();
        let config = root.join("config.yaml");
        fs::write(
            &config,
            "hooks:\n  pre_llm_call:\n    - name: user-context\n      command: echo user-context # keep user hook\n",
        )
        .unwrap();
        let ctx = context_at(home.clone());
        let connector = HermesConnector::new();

        connector.install(&ctx, install_request()).unwrap();
        connector.install(&ctx, install_request()).unwrap();
        let installed = fs::read_to_string(&config).unwrap();
        Document::from_str(&installed).expect("installed YAML must remain valid");
        assert!(installed.contains("echo user-context # keep user hook"));
        assert_eq!(installed.matches(HOOK_NAME).count(), 1, "{installed}");

        connector.uninstall(&ctx).unwrap();
        let removed = fs::read_to_string(&config).unwrap();
        Document::from_str(&removed).expect("uninstalled YAML must remain valid");
        assert!(removed.contains("echo user-context # keep user hook"));
        assert!(!removed.contains(HOOK_NAME));
        let _ = fs::remove_dir_all(home);
    }

    #[test]
    fn manual_config_is_valid_yaml_with_the_exact_hook_command() {
        let home = test_dir("manual-yaml");
        let ctx = context_at(home.clone());
        let manual = HermesConnector::new().manual_instructions(&ctx).unwrap();
        let content = manual.manual_config.expect("manual config");
        let doc = Document::from_str(&content).expect("manual config must be valid YAML");
        let command = doc
            .as_mapping()
            .and_then(|root| root.get_mapping("hooks"))
            .and_then(|hooks| hooks.get_sequence(HOOK_EVENT))
            .and_then(|sequence| sequence.get(0))
            .and_then(|entry| entry.as_mapping().cloned())
            .and_then(|entry| entry.get("command"))
            .and_then(|node| scalar_string(&node));
        assert_eq!(command, Some(hook_command(&skill_path(&ctx))));
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
    fn remote_launch_spec_reaches_generated_mcp_server_fragment() {
        let home = test_dir("hermes-remote-launch");
        let ctx = context_at(home.clone()).with_mcp_launch(remote_launch_spec());

        let fragment = desired_mcp_server_fragment(&ctx);
        assert!(
            fragment.contains("/opt/superdev/superdev-agent"),
            "command 必须取自远端启动规格: {fragment}"
        );
        assert!(
            fragment.contains("- mcp"),
            "args 必须写进配置，否则目标机 agent 不会进入 mcp 模式: {fragment}"
        );
        assert!(
            fragment.contains("http://10.1.2.3:57117"),
            "SUPERDEV_AGENT_URL 必须指向目标机: {fragment}"
        );

        // 空配置分支（merge_hermes_mcp 自建整份文档）与替换分支必须是同一份形状。
        let merged = merge_hermes_mcp(None, &ctx).expect("merge from empty");
        assert!(merged.content.contains("- mcp"), "{}", merged.content);
        assert!(
            merged.content.contains("http://10.1.2.3:57117"),
            "{}",
            merged.content
        );
        let _ = fs::remove_dir_all(home);
    }

    #[test]
    fn local_launch_spec_keeps_the_args_key_absent() {
        let home = test_dir("hermes-local-launch");
        let ctx = context_at(home.clone());

        let fragment = desired_mcp_server_fragment(&ctx);
        assert!(
            !fragment.contains("args"),
            "本机 args 为空时不得写 args 键（字节等价约束）: {fragment}"
        );
        assert!(
            !merge_hermes_mcp(None, &ctx)
                .expect("merge from empty")
                .content
                .contains("args"),
            "空配置分支同样不得写 args 键"
        );
        let _ = fs::remove_dir_all(home);
    }
}
