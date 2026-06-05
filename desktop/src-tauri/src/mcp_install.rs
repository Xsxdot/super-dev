// mcp_install.rs 提供 SuperDev MCP 配置安装能力。
//
// 职责：
//   - 解析 Claude Code、Codex、Cursor 的 MCP 配置文件
//   - 只合并 mcpServers/mcp_servers.superdev 这一项
//   - 在写入前备份原文件并用临时文件原子替换
//   - 随 MCP 配置安装 SuperDev 使用指南 skill
//   - 检测 Claude Code、Codex、Cursor 是否已安装
//
// 边界：
//   - 不启动或探测 SuperDev agent
//   - 不启动编程智能体进程
//   - 不调用智能体 CLI
//   - 不渲染前端状态

use serde::Serialize;
use serde_json::json;
use std::ffi::OsStr;
use std::fs;
use std::path::{Path, PathBuf};
use tauri::{AppHandle, Manager};

const DEFAULT_AGENT_URL: &str = "http://127.0.0.1:57017";

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct McpEntry {
    pub command: String,
    pub agent_url: String,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct MergeResult {
    pub content: String,
    pub changed: bool,
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct ConfigInstallOutcome {
    installed: bool,
    already_present: bool,
    agent: String,
    config_path: String,
    backup_path: Option<String>,
    manual_config: String,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct SkillInstallOutcome {
    pub installed: bool,
    pub already_present: bool,
    pub target_path: String,
    pub backup_path: Option<String>,
    pub error: Option<String>,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct InstallOutcome {
    pub installed: bool,
    pub already_present: bool,
    pub agent: String,
    pub config_path: String,
    pub backup_path: Option<String>,
    pub manual_config: String,
    pub skill: SkillInstallOutcome,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct InstallHint {
    pub agent: String,
    pub config_path: String,
    pub manual_config: String,
    pub skill_target_path: String,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct McpStatus {
    pub agent: String,
    pub agent_installed: bool,
    pub detection_path: Option<String>,
    pub config_path: String,
    pub config_exists: bool,
    pub mcp_configured: bool,
    pub mcp_command: Option<String>,
    pub agent_url: Option<String>,
    pub config_error: Option<String>,
    pub skill_path: String,
    pub skill_installed: bool,
    pub skill_matches_bundled: Option<bool>,
    pub skill_error: Option<String>,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct UninstallOutcome {
    pub agent: String,
    pub config_path: String,
    pub removed_config: bool,
    pub config_backup_path: Option<String>,
    pub skill_path: String,
    pub removed_skill: bool,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct McpCapabilityTool {
    pub name: String,
    pub purpose: String,
    pub access: String,
    pub reference: String,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct McpCapabilitySection {
    pub id: String,
    pub title: String,
    pub description: String,
    pub tools: Vec<McpCapabilityTool>,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct McpDocument {
    pub id: String,
    pub title: String,
    pub path: String,
    pub content: String,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct McpDocs {
    pub summary_sections: Vec<McpCapabilitySection>,
    pub documents: Vec<McpDocument>,
}

impl SkillInstallOutcome {
    fn failed(target_path: &Path, error: String) -> Self {
        Self {
            installed: false,
            already_present: false,
            target_path: target_path.to_string_lossy().to_string(),
            backup_path: None,
            error: Some(error),
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct CodingAgentAvailability {
    pub agent: String,
    pub installed: bool,
    pub detection_path: Option<String>,
}

#[derive(Clone, Copy)]
enum AgentKind {
    ClaudeCode,
    Codex,
    Cursor,
}

impl AgentKind {
    fn parse(input: &str) -> Result<Self, String> {
        match input {
            "claude-code" => Ok(Self::ClaudeCode),
            "codex" => Ok(Self::Codex),
            "cursor" => Ok(Self::Cursor),
            other => Err(format!("不支持的智能体类型: {other}")),
        }
    }

    fn label(&self) -> &'static str {
        match self {
            Self::ClaudeCode => "claude-code",
            Self::Codex => "codex",
            Self::Cursor => "cursor",
        }
    }

    fn detect_installation(
        &self,
        command_dirs: &[PathBuf],
        app_dirs: &[PathBuf],
    ) -> Option<PathBuf> {
        match self {
            Self::ClaudeCode => find_command_in_dirs(command_dirs, &["claude"]),
            Self::Codex => find_command_in_dirs(command_dirs, &["codex"]),
            Self::Cursor => find_command_in_dirs(command_dirs, &["cursor"])
                .or_else(|| find_app_bundle(app_dirs, "Cursor.app")),
        }
    }

    fn config_path(&self, home: &Path) -> PathBuf {
        match self {
            Self::ClaudeCode => home.join(".claude.json"),
            Self::Codex => home.join(".codex").join("config.toml"),
            Self::Cursor => home.join(".cursor").join("mcp.json"),
        }
    }

    fn skill_dir(&self, home: &Path) -> PathBuf {
        match self {
            Self::ClaudeCode => home.join(".claude").join("skills").join("superdev"),
            Self::Codex => home.join(".codex").join("skills").join("superdev"),
            Self::Cursor => home.join(".cursor").join("skills").join("superdev"),
        }
    }

    fn manual_config(&self, entry: &McpEntry) -> String {
        match self {
            Self::ClaudeCode | Self::Cursor => serde_json::to_string_pretty(&json!({
                "mcpServers": {
                    "superdev": {
                        "command": entry.command,
                        "env": { "SUPERDEV_AGENT_URL": entry.agent_url }
                    }
                }
            }))
            .expect("manual json"),
            Self::Codex => format!(
                "[mcp_servers.superdev]\ncommand = {:?}\n[mcp_servers.superdev.env]\nSUPERDEV_AGENT_URL = {:?}\n",
                entry.command, entry.agent_url
            ),
        }
    }
}

pub fn install_mcp_for_paths_with_skill(
    agent: &str,
    home: &Path,
    mcp_path: &Path,
    skill_source: Option<&Path>,
    skill_source_error: Option<String>,
) -> Result<InstallOutcome, String> {
    let kind = AgentKind::parse(agent)?;
    let entry = McpEntry {
        command: mcp_path.to_string_lossy().to_string(),
        agent_url: DEFAULT_AGENT_URL.to_string(),
    };
    let config_path = kind.config_path(home);
    let config_outcome = match kind {
        AgentKind::ClaudeCode | AgentKind::Cursor => {
            install_json_kind_to_path(&config_path, &entry, agent)
        }
        AgentKind::Codex => install_toml_kind_to_path(&config_path, &entry, agent),
    }?;
    let skill_target = kind.skill_dir(home);
    let skill = match skill_source {
        Some(source) => install_skill_dir(source, &skill_target)
            .unwrap_or_else(|err| SkillInstallOutcome::failed(&skill_target, err)),
        None => SkillInstallOutcome::failed(
            &skill_target,
            skill_source_error.unwrap_or_else(|| "找不到 SuperDev skill 源目录".to_string()),
        ),
    };
    Ok(InstallOutcome {
        installed: config_outcome.installed,
        already_present: config_outcome.already_present,
        agent: config_outcome.agent,
        config_path: config_outcome.config_path,
        backup_path: config_outcome.backup_path,
        manual_config: config_outcome.manual_config,
        skill,
    })
}

pub fn install_hint_for_paths(
    agent: &str,
    home: &Path,
    mcp_path: &Path,
) -> Result<InstallHint, String> {
    let kind = AgentKind::parse(agent)?;
    let entry = McpEntry {
        command: mcp_path.to_string_lossy().to_string(),
        agent_url: DEFAULT_AGENT_URL.to_string(),
    };
    Ok(InstallHint {
        agent: kind.label().to_string(),
        config_path: kind.config_path(home).to_string_lossy().to_string(),
        manual_config: kind.manual_config(&entry),
        skill_target_path: kind.skill_dir(home).to_string_lossy().to_string(),
    })
}

pub fn detect_coding_agents_for_paths(
    home: &Path,
    path_value: Option<&OsStr>,
    app_dirs: &[PathBuf],
) -> Vec<CodingAgentAvailability> {
    let command_dirs = command_search_dirs(home, path_value);
    detect_coding_agents_for_search_dirs(&command_dirs, app_dirs)
}

fn detect_coding_agents_for_search_dirs(
    command_dirs: &[PathBuf],
    app_dirs: &[PathBuf],
) -> Vec<CodingAgentAvailability> {
    [AgentKind::ClaudeCode, AgentKind::Codex, AgentKind::Cursor]
        .into_iter()
        .map(|kind| {
            let detection_path = kind.detect_installation(command_dirs, app_dirs);
            CodingAgentAvailability {
                agent: kind.label().to_string(),
                installed: detection_path.is_some(),
                detection_path: detection_path.map(|path| path.to_string_lossy().to_string()),
            }
        })
        .collect()
}

fn find_command_in_dirs(command_dirs: &[PathBuf], commands: &[&str]) -> Option<PathBuf> {
    for dir in command_dirs {
        for command in commands {
            for file_name in executable_file_names(command) {
                let candidate = dir.join(file_name);
                if candidate.is_file() {
                    return Some(candidate);
                }
            }
        }
    }
    None
}

fn command_search_dirs(home: &Path, path_value: Option<&OsStr>) -> Vec<PathBuf> {
    let mut dirs = Vec::new();
    if let Some(value) = path_value {
        for dir in std::env::split_paths(value) {
            push_unique_path(&mut dirs, dir);
        }
    }
    for dir in [
        home.join(".local").join("bin"),
        home.join(".npm-global").join("bin"),
        home.join(".bun").join("bin"),
        home.join(".cargo").join("bin"),
        PathBuf::from("/opt/homebrew/bin"),
        PathBuf::from("/usr/local/bin"),
        PathBuf::from("/usr/bin"),
    ] {
        push_unique_path(&mut dirs, dir);
    }
    dirs
}

fn executable_file_names(command: &str) -> Vec<String> {
    if cfg!(windows) {
        vec![
            command.to_string(),
            format!("{command}.exe"),
            format!("{command}.cmd"),
            format!("{command}.bat"),
        ]
    } else {
        vec![command.to_string()]
    }
}

fn find_app_bundle(app_dirs: &[PathBuf], bundle_name: &str) -> Option<PathBuf> {
    app_dirs
        .iter()
        .map(|dir| dir.join(bundle_name))
        .find(|candidate| candidate.exists())
}

fn coding_agent_app_dirs(home: &Path) -> Vec<PathBuf> {
    let mut dirs = Vec::new();
    push_unique_path(&mut dirs, home.join("Applications"));
    if cfg!(target_os = "macos") {
        push_unique_path(&mut dirs, PathBuf::from("/Applications"));
    }
    dirs
}

fn push_unique_path(paths: &mut Vec<PathBuf>, path: PathBuf) {
    if !paths.iter().any(|existing| existing == &path) {
        paths.push(path);
    }
}

fn merge_json_config(existing: Option<&str>, entry: &McpEntry) -> Result<MergeResult, String> {
    let mut root = match existing {
        Some(content) if !content.trim().is_empty() => {
            serde_json::from_str::<serde_json::Value>(content)
                .map_err(|err| format!("配置文件格式异常(JSON): {err}"))?
        }
        _ => json!({}),
    };
    let server = json!({
        "command": entry.command,
        "env": { "SUPERDEV_AGENT_URL": entry.agent_url }
    });
    let obj = root
        .as_object_mut()
        .ok_or_else(|| "配置文件格式异常(JSON): 根节点必须是对象".to_string())?;
    let servers = obj
        .entry("mcpServers")
        .or_insert_with(|| json!({}))
        .as_object_mut()
        .ok_or_else(|| "配置文件格式异常(JSON): mcpServers 必须是对象".to_string())?;
    let changed = servers.get("superdev") != Some(&server);
    servers.insert("superdev".to_string(), server);
    let content = serde_json::to_string_pretty(&root)
        .map_err(|err| format!("序列化配置失败(JSON): {err}"))?
        + "\n";
    Ok(MergeResult { content, changed })
}

fn merge_codex_config(existing: Option<&str>, entry: &McpEntry) -> Result<MergeResult, String> {
    let mut root = match existing {
        Some(content) if !content.trim().is_empty() => toml::from_str::<toml::Value>(content)
            .map_err(|err| format!("配置文件格式异常(TOML): {err}"))?,
        _ => toml::Value::Table(toml::value::Table::new()),
    };
    let root_table = root
        .as_table_mut()
        .ok_or_else(|| "配置文件格式异常(TOML): 根节点必须是 table".to_string())?;
    let servers = root_table
        .entry("mcp_servers".to_string())
        .or_insert_with(|| toml::Value::Table(toml::value::Table::new()))
        .as_table_mut()
        .ok_or_else(|| "配置文件格式异常(TOML): mcp_servers 必须是 table".to_string())?;
    let mut env = toml::value::Table::new();
    env.insert(
        "SUPERDEV_AGENT_URL".to_string(),
        toml::Value::String(entry.agent_url.clone()),
    );
    let mut server = toml::value::Table::new();
    server.insert(
        "command".to_string(),
        toml::Value::String(entry.command.clone()),
    );
    server.insert("env".to_string(), toml::Value::Table(env));
    let next = toml::Value::Table(server);
    let changed = servers.get("superdev") != Some(&next);
    servers.insert("superdev".to_string(), next);
    let content =
        toml::to_string_pretty(&root).map_err(|err| format!("序列化配置失败(TOML): {err}"))?;
    Ok(MergeResult { content, changed })
}

fn read_config_status(
    kind: AgentKind,
    path: &Path,
) -> (bool, bool, Option<String>, Option<String>, Option<String>) {
    let existing = match fs::read_to_string(path) {
        Ok(content) => content,
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => {
            return (false, false, None, None, None);
        }
        Err(err) => {
            return (
                path.exists(),
                false,
                None,
                None,
                Some(format!("读取配置文件失败: {err}")),
            );
        }
    };
    match kind {
        AgentKind::ClaudeCode | AgentKind::Cursor => read_json_config_status(&existing),
        AgentKind::Codex => read_codex_config_status(&existing),
    }
}

fn read_json_config_status(
    content: &str,
) -> (bool, bool, Option<String>, Option<String>, Option<String>) {
    let root = match serde_json::from_str::<serde_json::Value>(content) {
        Ok(value) => value,
        Err(err) => {
            return (
                true,
                false,
                None,
                None,
                Some(format!("配置文件格式异常(JSON): {err}")),
            );
        }
    };
    let Some(server) = root
        .get("mcpServers")
        .and_then(|servers| servers.get("superdev"))
        .and_then(|server| server.as_object())
    else {
        return (true, false, None, None, None);
    };
    let command = server
        .get("command")
        .and_then(|value| value.as_str())
        .map(|value| value.to_string());
    let agent_url = server
        .get("env")
        .and_then(|env| env.get("SUPERDEV_AGENT_URL"))
        .and_then(|value| value.as_str())
        .map(|value| value.to_string());
    (true, true, command, agent_url, None)
}

fn read_codex_config_status(
    content: &str,
) -> (bool, bool, Option<String>, Option<String>, Option<String>) {
    let root = match toml::from_str::<toml::Value>(content) {
        Ok(value) => value,
        Err(err) => {
            return (
                true,
                false,
                None,
                None,
                Some(format!("配置文件格式异常(TOML): {err}")),
            );
        }
    };
    let Some(server) = root
        .get("mcp_servers")
        .and_then(|servers| servers.get("superdev"))
        .and_then(|server| server.as_table())
    else {
        return (true, false, None, None, None);
    };
    let command = server
        .get("command")
        .and_then(|value| value.as_str())
        .map(|value| value.to_string());
    let agent_url = server
        .get("env")
        .and_then(|env| env.get("SUPERDEV_AGENT_URL"))
        .and_then(|value| value.as_str())
        .map(|value| value.to_string());
    (true, true, command, agent_url, None)
}

fn remove_json_superdev_config(existing: Option<&str>) -> Result<MergeResult, String> {
    let mut root = match existing {
        Some(content) if !content.trim().is_empty() => {
            serde_json::from_str::<serde_json::Value>(content)
                .map_err(|err| format!("配置文件格式异常(JSON): {err}"))?
        }
        _ => json!({}),
    };
    let obj = root
        .as_object_mut()
        .ok_or_else(|| "配置文件格式异常(JSON): 根节点必须是对象".to_string())?;
    let changed = match obj.get_mut("mcpServers") {
        Some(servers) => {
            let servers = servers
                .as_object_mut()
                .ok_or_else(|| "配置文件格式异常(JSON): mcpServers 必须是对象".to_string())?;
            servers.remove("superdev").is_some()
        }
        None => false,
    };
    let content = if changed {
        serde_json::to_string_pretty(&root).map_err(|err| format!("序列化配置失败(JSON): {err}"))?
            + "\n"
    } else {
        existing.unwrap_or("").trim_end().to_string() + "\n"
    };
    Ok(MergeResult { content, changed })
}

fn remove_codex_superdev_config(existing: Option<&str>) -> Result<MergeResult, String> {
    let mut root = match existing {
        Some(content) if !content.trim().is_empty() => toml::from_str::<toml::Value>(content)
            .map_err(|err| format!("配置文件格式异常(TOML): {err}"))?,
        _ => toml::Value::Table(toml::value::Table::new()),
    };
    let root_table = root
        .as_table_mut()
        .ok_or_else(|| "配置文件格式异常(TOML): 根节点必须是 table".to_string())?;
    let changed = match root_table.get_mut("mcp_servers") {
        Some(servers) => {
            let servers = servers
                .as_table_mut()
                .ok_or_else(|| "配置文件格式异常(TOML): mcp_servers 必须是 table".to_string())?;
            servers.remove("superdev").is_some()
        }
        None => false,
    };
    let content = if changed {
        toml::to_string_pretty(&root).map_err(|err| format!("序列化配置失败(TOML): {err}"))?
    } else {
        existing.unwrap_or("").trim_end().to_string() + "\n"
    };
    Ok(MergeResult { content, changed })
}

fn uninstall_from_path(
    path: &Path,
    remove: fn(Option<&str>) -> Result<MergeResult, String>,
) -> Result<(bool, Option<String>), String> {
    let existing = match fs::read_to_string(path) {
        Ok(content) => Some(content),
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => return Ok((false, None)),
        Err(err) => return Err(format!("读取配置文件失败: {err}")),
    };
    let removed = remove(existing.as_deref())?;
    if !removed.changed {
        return Ok((false, None));
    }
    let backup = backup_path(path);
    fs::copy(path, &backup).map_err(|err| format!("备份配置文件失败: {err}"))?;
    let tmp = path.with_extension("superdev-tmp");
    fs::write(&tmp, removed.content).map_err(|err| format!("写入临时配置失败: {err}"))?;
    fs::rename(&tmp, path).map_err(|err| format!("替换配置文件失败: {err}"))?;
    Ok((true, Some(backup.to_string_lossy().to_string())))
}

fn install_json_kind_to_path(
    path: &Path,
    entry: &McpEntry,
    agent: &str,
) -> Result<ConfigInstallOutcome, String> {
    install_to_path(
        path,
        entry,
        merge_json_config,
        agent.to_string(),
        AgentKind::ClaudeCode.manual_config(entry),
    )
}

fn install_toml_kind_to_path(
    path: &Path,
    entry: &McpEntry,
    agent: &str,
) -> Result<ConfigInstallOutcome, String> {
    install_to_path(
        path,
        entry,
        merge_codex_config,
        agent.to_string(),
        AgentKind::Codex.manual_config(entry),
    )
}

fn install_to_path(
    path: &Path,
    entry: &McpEntry,
    merge: fn(Option<&str>, &McpEntry) -> Result<MergeResult, String>,
    agent: String,
    manual_config: String,
) -> Result<ConfigInstallOutcome, String> {
    let existing = match fs::read_to_string(path) {
        Ok(content) => Some(content),
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => None,
        Err(err) => return Err(format!("读取配置文件失败: {err}")),
    };
    let merged = merge(existing.as_deref(), entry)?;
    if !merged.changed {
        return Ok(ConfigInstallOutcome {
            installed: false,
            already_present: true,
            agent,
            config_path: path.to_string_lossy().to_string(),
            backup_path: None,
            manual_config,
        });
    }
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|err| format!("创建配置目录失败: {err}"))?;
    }
    let backup_path = if path.exists() {
        let backup = backup_path(path);
        fs::copy(path, &backup).map_err(|err| format!("备份配置文件失败: {err}"))?;
        Some(backup.to_string_lossy().to_string())
    } else {
        None
    };
    let tmp = path.with_extension("superdev-tmp");
    fs::write(&tmp, merged.content).map_err(|err| format!("写入临时配置失败: {err}"))?;
    fs::rename(&tmp, path).map_err(|err| format!("替换配置文件失败: {err}"))?;
    Ok(ConfigInstallOutcome {
        installed: true,
        already_present: false,
        agent,
        config_path: path.to_string_lossy().to_string(),
        backup_path,
        manual_config,
    })
}

fn install_skill_dir(source: &Path, target: &Path) -> Result<SkillInstallOutcome, String> {
    let source_files =
        collect_relative_files(source).map_err(|err| format!("读取 skill 源目录失败: {err}"))?;
    if source_files.is_empty() {
        return Err("skill 源目录为空".to_string());
    }
    if target.exists() && directories_equal(source, target, &source_files)? {
        return Ok(SkillInstallOutcome {
            installed: false,
            already_present: true,
            target_path: target.to_string_lossy().to_string(),
            backup_path: None,
            error: None,
        });
    }
    if let Some(parent) = target.parent() {
        fs::create_dir_all(parent).map_err(|err| format!("创建 skill 目录失败: {err}"))?;
    }
    let tmp = target.with_extension("superdev-tmp");
    if tmp.exists() {
        fs::remove_dir_all(&tmp).map_err(|err| format!("清理临时 skill 目录失败: {err}"))?;
    }
    copy_dir_recursive(source, &tmp)?;
    let backup_path = if target.exists() {
        let backup = backup_dir_path(target);
        fs::rename(target, &backup).map_err(|err| format!("备份旧 skill 目录失败: {err}"))?;
        Some(backup.to_string_lossy().to_string())
    } else {
        None
    };
    fs::rename(&tmp, target).map_err(|err| format!("替换 skill 目录失败: {err}"))?;
    Ok(SkillInstallOutcome {
        installed: true,
        already_present: false,
        target_path: target.to_string_lossy().to_string(),
        backup_path,
        error: None,
    })
}

fn skill_status_for_target(
    source: Option<&Path>,
    source_error: Option<String>,
    target: &Path,
) -> (bool, Option<bool>, Option<String>) {
    if !target.exists() {
        return (false, Some(false), source_error);
    }
    let Some(source) = source else {
        return (true, None, source_error);
    };
    let source_files = match collect_relative_files(source) {
        Ok(files) => files,
        Err(err) => {
            return (
                true,
                None,
                Some(format!("读取 bundled skill 源目录失败: {err}")),
            );
        }
    };
    match directories_equal(source, target, &source_files) {
        Ok(equal) => (true, Some(equal), None),
        Err(err) => (true, None, Some(err)),
    }
}

fn remove_skill_dir(target: &Path) -> Result<bool, String> {
    if !target.exists() {
        return Ok(false);
    }
    fs::remove_dir_all(target).map_err(|err| format!("删除 skill 目录失败: {err}"))?;
    Ok(true)
}

fn capability_tool(name: &str, purpose: &str, access: &str, reference: &str) -> McpCapabilityTool {
    McpCapabilityTool {
        name: name.to_string(),
        purpose: purpose.to_string(),
        access: access.to_string(),
        reference: reference.to_string(),
    }
}

fn default_capability_sections() -> Vec<McpCapabilitySection> {
    vec![
        McpCapabilitySection {
            id: "runtime".to_string(),
            title: "运行态与全局视野".to_string(),
            description: "查看本地项目、服务、deployment 和整体运行状态。".to_string(),
            tools: vec![
                capability_tool(
                    "list_projects",
                    "列出本地 agent 已登记项目",
                    "读",
                    "SKILL.md",
                ),
                capability_tool("get_project", "按 ID 或名称读取项目详情", "读", "SKILL.md"),
                capability_tool(
                    "get_runtime_snapshot",
                    "获取 SuperDev 全局运行态快照",
                    "读",
                    "SKILL.md",
                ),
                capability_tool(
                    "list_services",
                    "读取项目服务与 deployment 状态",
                    "读",
                    "references/debugging-workflow.md",
                ),
            ],
        },
        McpCapabilitySection {
            id: "logs".to_string(),
            title: "日志与诊断".to_string(),
            description: "读取日志、搜索错误、采集诊断证据和 trace 线索。".to_string(),
            tools: vec![
                capability_tool(
                    "tail_logs",
                    "查看近期日志或持续跟随某个 deployment",
                    "读",
                    "references/log-tools.md",
                ),
                capability_tool(
                    "search_logs",
                    "按关键词跨项目或 deployment 搜索历史日志",
                    "读",
                    "references/log-tools.md",
                ),
                capability_tool(
                    "get_log_context",
                    "围绕某条日志 ID 获取前后上下文",
                    "读",
                    "references/log-tools.md",
                ),
                capability_tool(
                    "diagnose_service",
                    "采集单个 deployment 的状态和近期日志证据",
                    "读",
                    "references/debugging-workflow.md",
                ),
                capability_tool(
                    "analyze_trace_logs",
                    "采集 trace/request 链路证据",
                    "读",
                    "references/debugging-workflow.md",
                ),
                capability_tool(
                    "summarize_error_window",
                    "聚合某时间窗错误信号",
                    "读",
                    "references/debugging-workflow.md",
                ),
            ],
        },
        McpCapabilitySection {
            id: "safety".to_string(),
            title: "配置变更与安全操作".to_string(),
            description: "配置写入走 preview/apply，服务启停重启走审批 token。".to_string(),
            tools: vec![
                capability_tool(
                    "preview_config_change",
                    "预览项目、服务、pipeline 配置变更",
                    "读",
                    "references/safe-operations.md",
                ),
                capability_tool(
                    "apply_config_change",
                    "应用已确认的配置变更",
                    "写",
                    "references/safe-operations.md",
                ),
                capability_tool(
                    "preview_operation",
                    "为启动、停止、重启等操作生成安全预检",
                    "读",
                    "references/safe-operations.md",
                ),
                capability_tool(
                    "get_operation_approval",
                    "读取审批并在批准后返回 one-time token",
                    "读",
                    "references/safe-operations.md",
                ),
                capability_tool(
                    "start_service",
                    "启动 deployment",
                    "写，需审批纪律",
                    "references/safe-operations.md",
                ),
                capability_tool(
                    "stop_service",
                    "停止 deployment",
                    "写，需审批纪律",
                    "references/safe-operations.md",
                ),
                capability_tool(
                    "restart_service",
                    "重启 deployment",
                    "写，需审批纪律",
                    "references/safe-operations.md",
                ),
            ],
        },
        McpCapabilitySection {
            id: "pipeline".to_string(),
            title: "Pipeline".to_string(),
            description: "管理 pipeline 模板、部署/回滚执行、运行日志和产物历史。".to_string(),
            tools: vec![
                capability_tool(
                    "preview_pipeline_template",
                    "校验 pipeline 模板 YAML",
                    "读",
                    "references/pipeline.md",
                ),
                capability_tool(
                    "import_pipeline_template",
                    "导入 pipeline 模板到本地模板库",
                    "写",
                    "references/pipeline.md",
                ),
                capability_tool(
                    "deploy_project_pipeline",
                    "执行项目级 pipeline deploy 或 rollback",
                    "写",
                    "references/pipeline.md",
                ),
                capability_tool(
                    "list_pipeline_runs",
                    "列出 pipeline 运行历史",
                    "读",
                    "references/pipeline.md",
                ),
                capability_tool(
                    "read_pipeline_run_logs",
                    "读取 pipeline run 日志",
                    "读",
                    "references/pipeline.md",
                ),
                capability_tool(
                    "list_pipeline_artifacts",
                    "查看 pipeline 产物历史",
                    "读",
                    "references/pipeline.md",
                ),
            ],
        },
    ]
}

fn collect_relative_files(root: &Path) -> Result<Vec<PathBuf>, std::io::Error> {
    let mut files = Vec::new();
    collect_relative_files_inner(root, root, &mut files)?;
    files.sort();
    Ok(files)
}

fn collect_relative_files_inner(
    root: &Path,
    current: &Path,
    files: &mut Vec<PathBuf>,
) -> Result<(), std::io::Error> {
    for entry in fs::read_dir(current)? {
        let entry = entry?;
        let path = entry.path();
        if path.is_dir() {
            collect_relative_files_inner(root, &path, files)?;
        } else if path.is_file() {
            files.push(path.strip_prefix(root).unwrap_or(&path).to_path_buf());
        }
    }
    Ok(())
}

fn directories_equal(
    source: &Path,
    target: &Path,
    source_files: &[PathBuf],
) -> Result<bool, String> {
    let target_files = match collect_relative_files(target) {
        Ok(files) => files,
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => return Ok(false),
        Err(err) => return Err(format!("读取目标 skill 目录失败: {err}")),
    };
    if source_files != target_files {
        return Ok(false);
    }
    for relative in source_files {
        let source_content = fs::read(source.join(relative))
            .map_err(|err| format!("读取源 skill 文件失败 {}: {err}", relative.display()))?;
        let target_content = fs::read(target.join(relative))
            .map_err(|err| format!("读取目标 skill 文件失败 {}: {err}", relative.display()))?;
        if source_content != target_content {
            return Ok(false);
        }
    }
    Ok(true)
}

fn copy_dir_recursive(source: &Path, target: &Path) -> Result<(), String> {
    fs::create_dir_all(target).map_err(|err| format!("创建 skill 临时目录失败: {err}"))?;
    for entry in fs::read_dir(source).map_err(|err| format!("读取 skill 源目录失败: {err}"))?
    {
        let entry = entry.map_err(|err| format!("读取 skill 目录项失败: {err}"))?;
        let source_path = entry.path();
        let target_path = target.join(entry.file_name());
        if source_path.is_dir() {
            copy_dir_recursive(&source_path, &target_path)?;
        } else if source_path.is_file() {
            fs::copy(&source_path, &target_path).map_err(|err| {
                format!(
                    "复制 skill 文件失败 {} -> {}: {err}",
                    source_path.display(),
                    target_path.display()
                )
            })?;
        }
    }
    Ok(())
}

fn backup_dir_path(path: &Path) -> PathBuf {
    let name = path
        .file_name()
        .and_then(|value| value.to_str())
        .unwrap_or("skill");
    let nanos = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|duration| duration.as_nanos())
        .unwrap_or(0);
    path.with_file_name(format!("{name}.superdev-bak-{nanos}"))
}

fn backup_path(path: &Path) -> PathBuf {
    let ext = path.extension().and_then(|s| s.to_str()).unwrap_or("");
    if ext.is_empty() {
        path.with_extension("superdev-bak")
    } else {
        path.with_extension(format!("{ext}.superdev-bak"))
    }
}

pub fn find_sidecar_binary_in_dir(dir: &Path, name: &str) -> Option<PathBuf> {
    let exact = dir.join(name);
    if exact.is_file() {
        return Some(exact);
    }
    let prefix = format!("{name}-");
    let entries = fs::read_dir(dir).ok()?;
    entries
        .filter_map(Result::ok)
        .map(|entry| entry.path())
        .find(|path| {
            path.file_name()
                .and_then(|value| value.to_str())
                .is_some_and(|file| file.starts_with(&prefix))
                && path.is_file()
        })
}

pub fn find_skill_source_dir_in_dir(dir: &Path) -> Option<PathBuf> {
    let candidate = dir.join("skills").join("superdev");
    if candidate.join("SKILL.md").is_file() {
        return Some(candidate);
    }
    None
}

pub fn resolve_sidecar_binary(app: &AppHandle, name: &str) -> Result<PathBuf, String> {
    let exe = std::env::current_exe().map_err(|err| format!("解析当前进程路径失败: {err}"))?;
    if let Some(dir) = exe.parent() {
        if let Some(path) = find_sidecar_binary_in_dir(dir, name) {
            return Ok(path);
        }
    }
    if let Ok(resource_dir) = app.path().resource_dir() {
        if let Some(path) = find_sidecar_binary_in_dir(&resource_dir, name) {
            return Ok(path);
        }
    }
    if cfg!(debug_assertions) {
        let dev_dir = std::env::current_dir()
            .map_err(|err| format!("解析开发目录失败: {err}"))?
            .join("src-tauri")
            .join("binaries");
        if let Some(path) = find_sidecar_binary_in_dir(&dev_dir, name) {
            return Ok(path);
        }
    }
    Err(format!(
        "找不到 {name} sidecar 二进制，请检查桌面端打包配置"
    ))
}

pub fn resolve_skill_source_dir(app: &AppHandle) -> Result<PathBuf, String> {
    if let Ok(resource_dir) = app.path().resource_dir() {
        if let Some(path) = find_skill_source_dir_in_dir(&resource_dir) {
            return Ok(path);
        }
    }
    if cfg!(debug_assertions) {
        let dev_dir = std::env::current_dir()
            .map_err(|err| format!("解析开发目录失败: {err}"))?
            .join("src-tauri")
            .join("resources");
        if let Some(path) = find_skill_source_dir_in_dir(&dev_dir) {
            return Ok(path);
        }
    }
    Err("找不到 SuperDev skill 资源目录，请检查桌面端打包配置".to_string())
}

/// mcp_status_for_paths 读取每个编程智能体的 SuperDev MCP/skill 安装状态。
///
/// 参数：
///   - home: 用于定位各 Agent 配置和 skill 目录的用户 HOME
///   - path_value: 用于检测 CLI 是否安装的 PATH 值
///   - app_dirs: 用于检测桌面应用安装状态的目录列表
///   - skill_source: bundled superdev skill 源目录
///   - skill_source_error: skill 源目录不可用时的错误说明
///
/// 返回：
///   - Claude Code、Codex、Cursor 三类 Agent 的 MCP 状态
///
/// 注意：
///   - 该函数只读配置文件和 skill 文件，不写入任何内容
pub fn mcp_status_for_paths(
    home: &Path,
    path_value: Option<&OsStr>,
    app_dirs: &[PathBuf],
    skill_source: Option<&Path>,
    skill_source_error: Option<String>,
) -> Vec<McpStatus> {
    let command_dirs = command_search_dirs(home, path_value);
    let detected = detect_coding_agents_for_search_dirs(&command_dirs, app_dirs);
    [AgentKind::ClaudeCode, AgentKind::Codex, AgentKind::Cursor]
        .into_iter()
        .map(|kind| {
            let agent = kind.label().to_string();
            let availability = detected
                .iter()
                .find(|status| status.agent == agent)
                .expect("known agent availability");
            let config_path = kind.config_path(home);
            let skill_path = kind.skill_dir(home);
            let (config_exists, mcp_configured, mcp_command, agent_url, config_error) =
                read_config_status(kind, &config_path);
            let (skill_installed, skill_matches_bundled, skill_error) =
                skill_status_for_target(skill_source, skill_source_error.clone(), &skill_path);
            McpStatus {
                agent,
                agent_installed: availability.installed,
                detection_path: availability.detection_path.clone(),
                config_path: config_path.to_string_lossy().to_string(),
                config_exists,
                mcp_configured,
                mcp_command,
                agent_url,
                config_error,
                skill_path: skill_path.to_string_lossy().to_string(),
                skill_installed,
                skill_matches_bundled,
                skill_error,
            }
        })
        .collect()
}

/// uninstall_mcp_for_paths 移除指定 Agent 的 SuperDev MCP 配置和 superdev skill。
///
/// 参数：
///   - agent: Agent 标识，支持 claude-code、codex、cursor
///   - home: 用于定位配置文件和 skill 目录的用户 HOME
///
/// 返回：
///   - 配置项和 skill 目录是否被删除，以及配置备份路径
///
/// 注意：
///   - 只删除 superdev 这一项 MCP server，不删除其他 MCP server
///   - 配置文件变更前会先备份原文件
pub fn uninstall_mcp_for_paths(agent: &str, home: &Path) -> Result<UninstallOutcome, String> {
    let kind = AgentKind::parse(agent)?;
    let config_path = kind.config_path(home);
    let (removed_config, config_backup_path) = match kind {
        AgentKind::ClaudeCode | AgentKind::Cursor => {
            uninstall_from_path(&config_path, remove_json_superdev_config)?
        }
        AgentKind::Codex => uninstall_from_path(&config_path, remove_codex_superdev_config)?,
    };
    let skill_path = kind.skill_dir(home);
    let removed_skill = remove_skill_dir(&skill_path)?;
    Ok(UninstallOutcome {
        agent: kind.label().to_string(),
        config_path: config_path.to_string_lossy().to_string(),
        removed_config,
        config_backup_path,
        skill_path: skill_path.to_string_lossy().to_string(),
        removed_skill,
    })
}

/// mcp_docs_for_skill_source 读取 bundled superdev skill 文档和 MCP 功能摘要。
///
/// 参数：
///   - skill_source: bundled superdev skill 源目录
///
/// 返回：
///   - 结构化 MCP 功能摘要和可查看的 skill/reference 文档
///
/// 注意：
///   - 文档内容来自打包资源目录，前端只负责展示
pub fn mcp_docs_for_skill_source(skill_source: &Path) -> Result<McpDocs, String> {
    let mut documents = Vec::new();
    let main = skill_source.join("SKILL.md");
    documents.push(McpDocument {
        id: "skill".to_string(),
        title: "SKILL.md".to_string(),
        path: main.to_string_lossy().to_string(),
        content: fs::read_to_string(&main)
            .map_err(|err| format!("读取 skill 主文档失败: {err}"))?,
    });
    for file in [
        "debugging-workflow.md",
        "log-tools.md",
        "safe-operations.md",
        "pipeline.md",
    ] {
        let path = skill_source.join("references").join(file);
        documents.push(McpDocument {
            id: format!("references/{file}"),
            title: file.to_string(),
            path: path.to_string_lossy().to_string(),
            content: fs::read_to_string(&path)
                .map_err(|err| format!("读取 skill reference {file} 失败: {err}"))?,
        });
    }
    Ok(McpDocs {
        summary_sections: default_capability_sections(),
        documents,
    })
}

#[tauri::command]
/// mcp_status 为设置页返回所有支持 Agent 的 MCP/skill 状态。
///
/// 参数：
///   - app: Tauri AppHandle，用于定位打包资源目录
///
/// 返回：
///   - 每个 Agent 的配置文件、MCP server 和 skill 安装状态
///
/// 注意：
///   - 该 command 只读本地配置和打包资源，不修改文件
pub fn mcp_status(app: AppHandle) -> Result<Vec<McpStatus>, String> {
    let home = std::env::var_os("HOME")
        .map(PathBuf::from)
        .ok_or_else(|| "无法解析 HOME 目录".to_string())?;
    let path_value = std::env::var_os("PATH");
    let skill_source = resolve_skill_source_dir(&app);
    let (skill_source_path, skill_source_error) = match &skill_source {
        Ok(path) => (Some(path.as_path()), None),
        Err(err) => (None, Some(err.clone())),
    };
    Ok(mcp_status_for_paths(
        &home,
        path_value.as_deref(),
        &coding_agent_app_dirs(&home),
        skill_source_path,
        skill_source_error,
    ))
}

#[tauri::command]
/// uninstall_mcp 从指定 Agent 移除 SuperDev MCP 配置和 skill。
///
/// 参数：
///   - agent: Agent 标识，支持 claude-code、codex、cursor
///
/// 返回：
///   - 配置项和 skill 目录的删除结果
///
/// 注意：
///   - 只删除 superdev 这一项，其他 MCP server 保持不变
pub fn uninstall_mcp(agent: String) -> Result<UninstallOutcome, String> {
    let home = std::env::var_os("HOME")
        .map(PathBuf::from)
        .ok_or_else(|| "无法解析 HOME 目录".to_string())?;
    uninstall_mcp_for_paths(&agent, &home)
}

#[tauri::command]
/// mcp_docs 返回设置页展示用的 MCP 能力摘要和 skill 文档内容。
///
/// 参数：
///   - app: Tauri AppHandle，用于定位 bundled superdev skill
///
/// 返回：
///   - 结构化工具说明和 skill/reference 文档列表
///
/// 注意：
///   - 文档读取失败时返回错误给前端展示
pub fn mcp_docs(app: AppHandle) -> Result<McpDocs, String> {
    let skill_source = resolve_skill_source_dir(&app)?;
    mcp_docs_for_skill_source(&skill_source)
}

#[tauri::command]
pub fn install_mcp(app: AppHandle, agent: String) -> Result<InstallOutcome, String> {
    let home = std::env::var_os("HOME")
        .map(PathBuf::from)
        .ok_or_else(|| "无法解析 HOME 目录".to_string())?;
    let mcp = resolve_sidecar_binary(&app, "superdev-mcp")?;
    let skill_source = resolve_skill_source_dir(&app);
    let (skill_source_path, skill_source_error) = match &skill_source {
        Ok(path) => (Some(path.as_path()), None),
        Err(err) => (None, Some(err.clone())),
    };
    install_mcp_for_paths_with_skill(&agent, &home, &mcp, skill_source_path, skill_source_error)
}

#[tauri::command]
pub fn mcp_install_hint(app: AppHandle, agent: String) -> Result<InstallHint, String> {
    let home = std::env::var_os("HOME")
        .map(PathBuf::from)
        .ok_or_else(|| "无法解析 HOME 目录".to_string())?;
    let mcp = resolve_sidecar_binary(&app, "superdev-mcp")?;
    install_hint_for_paths(&agent, &home, &mcp)
}

#[tauri::command]
pub fn detect_coding_agents() -> Result<Vec<CodingAgentAvailability>, String> {
    let home = std::env::var_os("HOME")
        .map(PathBuf::from)
        .ok_or_else(|| "无法解析 HOME 目录".to_string())?;
    let path_value = std::env::var_os("PATH");
    Ok(detect_coding_agents_for_paths(
        &home,
        path_value.as_deref(),
        &coding_agent_app_dirs(&home),
    ))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;

    fn entry() -> McpEntry {
        McpEntry {
            command: "/Applications/SuperDev/superdev-mcp".to_string(),
            agent_url: "http://127.0.0.1:57017".to_string(),
        }
    }

    #[test]
    fn merge_json_keeps_other_servers() {
        let existing = r#"{"mcpServers":{"github":{"command":"gh"}},"theme":"dark"}"#;

        let merged = merge_json_config(Some(existing), &entry()).expect("merge json");
        let parsed: serde_json::Value = serde_json::from_str(&merged.content).expect("json");

        assert!(merged.changed);
        assert_eq!(parsed["theme"], "dark");
        assert_eq!(parsed["mcpServers"]["github"]["command"], "gh");
        assert_eq!(
            parsed["mcpServers"]["superdev"]["command"],
            "/Applications/SuperDev/superdev-mcp"
        );
        assert_eq!(
            parsed["mcpServers"]["superdev"]["env"]["SUPERDEV_AGENT_URL"],
            "http://127.0.0.1:57017"
        );
    }

    #[test]
    fn merge_json_is_idempotent() {
        let first = merge_json_config(None, &entry()).expect("first");
        let second = merge_json_config(Some(&first.content), &entry()).expect("second");

        assert!(!second.changed);
        assert_eq!(first.content, second.content);
    }

    #[test]
    fn merge_json_rejects_bad_json() {
        let err = merge_json_config(Some("{broken"), &entry()).expect_err("bad json");
        assert!(err.contains("配置文件格式异常"));
    }

    #[test]
    fn merge_codex_toml_keeps_other_keys() {
        let existing = r#"
model = "gpt-5"

[mcp_servers.github]
command = "gh"
"#;

        let merged = merge_codex_config(Some(existing), &entry()).expect("merge toml");
        let parsed: toml::Value = toml::from_str(&merged.content).expect("toml");

        assert_eq!(parsed["model"].as_str(), Some("gpt-5"));
        assert_eq!(
            parsed["mcp_servers"]["github"]["command"].as_str(),
            Some("gh")
        );
        assert_eq!(
            parsed["mcp_servers"]["superdev"]["command"].as_str(),
            Some("/Applications/SuperDev/superdev-mcp")
        );
        assert_eq!(
            parsed["mcp_servers"]["superdev"]["env"]["SUPERDEV_AGENT_URL"].as_str(),
            Some("http://127.0.0.1:57017")
        );
    }

    #[test]
    fn install_to_path_backs_up_existing_file_once() {
        let dir = tempfile_dir();
        let config = dir.join("mcp.json");
        fs::write(&config, r#"{"mcpServers":{"github":{"command":"gh"}}}"#).expect("seed config");

        let outcome = install_json_kind_to_path(&config, &entry(), "claude-code").expect("install");

        assert!(outcome.installed);
        assert!(!outcome.already_present);
        assert_eq!(outcome.config_path, config.to_string_lossy());
        let backup = config.with_extension("json.superdev-bak");
        assert_eq!(
            outcome.backup_path.as_deref(),
            Some(backup.to_string_lossy().as_ref())
        );
        assert!(backup.exists());
        assert!(fs::read_to_string(config)
            .expect("read")
            .contains("superdev"));
    }

    fn seed_skill_source(root: &Path) {
        let refs = root.join("references");
        fs::create_dir_all(&refs).expect("mkdir refs");
        fs::write(
            root.join("SKILL.md"),
            "---\nname: superdev\n---\n# SuperDev\n",
        )
        .expect("write skill");
        fs::write(refs.join("debugging-workflow.md"), "# Debugging\n").expect("write debugging");
        fs::write(refs.join("log-tools.md"), "# Logs\n").expect("write logs");
        fs::write(refs.join("safe-operations.md"), "# Safe\n").expect("write safe");
        fs::write(refs.join("pipeline.md"), "# Pipeline\n").expect("write pipeline");
    }

    #[test]
    fn agent_kind_returns_skill_target_dirs() {
        let home = PathBuf::from("/home/alice");

        assert_eq!(
            AgentKind::ClaudeCode.skill_dir(&home),
            PathBuf::from("/home/alice/.claude/skills/superdev")
        );
        assert_eq!(
            AgentKind::Codex.skill_dir(&home),
            PathBuf::from("/home/alice/.codex/skills/superdev")
        );
        assert_eq!(
            AgentKind::Cursor.skill_dir(&home),
            PathBuf::from("/home/alice/.cursor/skills/superdev")
        );
    }

    #[test]
    fn install_skill_dir_copies_directory_and_is_idempotent() {
        let dir = tempfile_dir();
        let source = dir.join("source");
        let target = dir.join("target").join("superdev");
        seed_skill_source(&source);

        let first = install_skill_dir(&source, &target).expect("install first");
        assert!(first.installed);
        assert!(!first.already_present);
        assert_eq!(first.target_path, target.to_string_lossy());
        assert!(target.join("SKILL.md").exists());
        assert!(target.join("references").join("pipeline.md").exists());

        let second = install_skill_dir(&source, &target).expect("install second");
        assert!(!second.installed);
        assert!(second.already_present);
        assert_eq!(second.backup_path, None);
    }

    #[test]
    fn install_skill_dir_backs_up_existing_different_directory() {
        let dir = tempfile_dir();
        let source = dir.join("source");
        let target = dir.join("target").join("superdev");
        seed_skill_source(&source);
        fs::create_dir_all(&target).expect("mkdir target");
        fs::write(target.join("SKILL.md"), "old").expect("write old skill");

        let outcome = install_skill_dir(&source, &target).expect("install replacement");

        assert!(outcome.installed);
        let backup = outcome.backup_path.expect("backup path");
        assert!(PathBuf::from(&backup).join("SKILL.md").exists());
        assert_eq!(
            fs::read_to_string(target.join("SKILL.md")).expect("read target"),
            "---\nname: superdev\n---\n# SuperDev\n"
        );
    }

    #[test]
    fn install_mcp_reports_skill_error_without_blocking_config_install() {
        let dir = tempfile_dir();
        let home = dir.join("home");
        let mcp = dir.join("superdev-mcp");
        fs::write(&mcp, b"bin").expect("write mcp");

        let outcome = install_mcp_for_paths_with_skill(
            "claude-code",
            &home,
            &mcp,
            Some(&dir.join("missing-skill")),
            None,
        )
        .expect("install mcp");

        assert!(outcome.installed);
        assert!(home.join(".claude.json").exists());
        assert!(!outcome.skill.installed);
        assert!(outcome
            .skill
            .error
            .as_deref()
            .unwrap_or("")
            .contains("读取 skill 源目录失败"));
    }

    #[test]
    fn uninstall_json_removes_only_superdev_server() {
        let existing = r#"{"mcpServers":{"github":{"command":"gh"},"superdev":{"command":"/bin/superdev-mcp","env":{"SUPERDEV_AGENT_URL":"http://127.0.0.1:57017"}}},"theme":"dark"}"#;

        let removed = remove_json_superdev_config(Some(existing)).expect("remove json");
        let parsed: serde_json::Value = serde_json::from_str(&removed.content).expect("json");

        assert!(removed.changed);
        assert_eq!(parsed["theme"], "dark");
        assert_eq!(parsed["mcpServers"]["github"]["command"], "gh");
        assert!(parsed["mcpServers"].get("superdev").is_none());
    }

    #[test]
    fn uninstall_codex_removes_only_superdev_server() {
        let existing = r#"
model = "gpt-5"

[mcp_servers.github]
command = "gh"

[mcp_servers.superdev]
command = "/bin/superdev-mcp"

[mcp_servers.superdev.env]
SUPERDEV_AGENT_URL = "http://127.0.0.1:57017"
"#;

        let removed = remove_codex_superdev_config(Some(existing)).expect("remove toml");
        let parsed: toml::Value = toml::from_str(&removed.content).expect("toml");

        assert!(removed.changed);
        assert_eq!(parsed["model"].as_str(), Some("gpt-5"));
        assert_eq!(
            parsed["mcp_servers"]["github"]["command"].as_str(),
            Some("gh")
        );
        assert!(parsed["mcp_servers"].get("superdev").is_none());
    }

    #[test]
    fn uninstall_config_is_idempotent_without_superdev_entry() {
        let existing = r#"{"mcpServers":{"github":{"command":"gh"}}}"#;

        let removed = remove_json_superdev_config(Some(existing)).expect("remove json");

        assert!(!removed.changed);
        assert_eq!(removed.content, format!("{existing}\n"));
    }

    #[test]
    fn uninstall_skill_dir_removes_target_and_is_idempotent() {
        let dir = tempfile_dir();
        let target = dir.join("skills").join("superdev");
        fs::create_dir_all(&target).expect("mkdir target");
        fs::write(target.join("SKILL.md"), "# Local Skill\n").expect("write target");

        assert!(remove_skill_dir(&target).expect("remove first"));
        assert!(!target.exists());
        assert!(!remove_skill_dir(&target).expect("remove second"));
    }

    #[test]
    fn mcp_status_reports_config_and_skill_state() {
        let dir = tempfile_dir();
        let home = dir.join("home");
        let source = dir.join("source");
        let target = home.join(".claude").join("skills").join("superdev");
        seed_skill_source(&source);
        fs::create_dir_all(home.join(".local").join("bin")).expect("mkdir bin");
        fs::write(home.join(".local").join("bin").join("claude"), b"bin").expect("write claude");
        fs::create_dir_all(home.join(".claude")).expect("mkdir claude");
        fs::write(
            home.join(".claude.json"),
            r#"{"mcpServers":{"superdev":{"command":"/bin/superdev-mcp","env":{"SUPERDEV_AGENT_URL":"http://127.0.0.1:57017"}}}}"#,
        )
        .expect("write config");
        install_skill_dir(&source, &target).expect("install skill");

        let statuses = mcp_status_for_paths(&home, None, &[], Some(&source), None);
        let claude = statuses
            .iter()
            .find(|status| status.agent == "claude-code")
            .expect("claude status");

        assert!(claude.agent_installed);
        assert!(claude.config_exists);
        assert!(claude.mcp_configured);
        assert_eq!(claude.mcp_command.as_deref(), Some("/bin/superdev-mcp"));
        assert_eq!(claude.agent_url.as_deref(), Some("http://127.0.0.1:57017"));
        assert!(claude.skill_installed);
        assert_eq!(claude.skill_matches_bundled, Some(true));
        assert_eq!(claude.config_error, None);
        assert_eq!(claude.skill_error, None);
    }

    #[test]
    fn mcp_docs_for_skill_source_reads_main_and_references() {
        let dir = tempfile_dir();
        let source = dir.join("source");
        seed_skill_source(&source);

        let docs = mcp_docs_for_skill_source(&source).expect("docs");

        assert!(docs
            .summary_sections
            .iter()
            .any(|section| section.id == "runtime" && section.title.contains("运行")));
        assert!(docs.documents.iter().any(|doc| {
            doc.id == "skill" && doc.title == "SKILL.md" && doc.content.contains("# SuperDev")
        }));
        assert!(docs.documents.iter().any(|doc| {
            doc.id == "references/pipeline.md"
                && doc.title == "pipeline.md"
                && doc.content.contains("# Pipeline")
        }));
    }

    fn tempfile_dir() -> std::path::PathBuf {
        let mut dir = std::env::temp_dir();
        dir.push(format!(
            "superdev-mcp-install-test-{}",
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .expect("time")
                .as_nanos()
        ));
        fs::create_dir_all(&dir).expect("mkdir temp");
        dir
    }
}

#[cfg(test)]
mod path_tests {
    use super::*;
    use std::fs;

    #[test]
    fn find_sidecar_binary_prefers_exact_name() {
        let dir = tempfile_dir();
        let exact = dir.join("superdev-mcp");
        fs::write(&exact, b"bin").expect("write exact");
        let suffixed = dir.join("superdev-mcp-aarch64-apple-darwin");
        fs::write(&suffixed, b"bin").expect("write suffixed");

        assert_eq!(
            find_sidecar_binary_in_dir(&dir, "superdev-mcp"),
            Some(exact)
        );
    }

    #[test]
    fn find_sidecar_binary_accepts_target_suffix() {
        let dir = tempfile_dir();
        let suffixed = dir.join("superdev-mcp-aarch64-apple-darwin");
        fs::write(&suffixed, b"bin").expect("write suffixed");

        assert_eq!(
            find_sidecar_binary_in_dir(&dir, "superdev-mcp"),
            Some(suffixed)
        );
    }

    #[test]
    fn find_skill_source_dir_accepts_resource_layout() {
        let dir = tempfile_dir();
        let skill = dir.join("skills").join("superdev");
        fs::create_dir_all(skill.join("references")).expect("mkdir skill");
        fs::write(skill.join("SKILL.md"), "# SuperDev").expect("write skill");

        assert_eq!(find_skill_source_dir_in_dir(&dir), Some(skill));
    }

    #[test]
    fn find_skill_source_dir_rejects_directory_without_skill_file() {
        let dir = tempfile_dir();
        fs::create_dir_all(dir.join("skills").join("superdev")).expect("mkdir skill dir");

        assert_eq!(find_skill_source_dir_in_dir(&dir), None);
    }

    #[test]
    fn bundled_skill_assets_cover_required_workflows() {
        let root = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("resources")
            .join("skills")
            .join("superdev");
        let skill = fs::read_to_string(root.join("SKILL.md")).expect("read SKILL.md");

        for phrase in [
            "工具给证据，AI 下根因",
            "读写分离 + 双层安全门",
            "references/debugging-workflow.md",
            "references/log-tools.md",
            "references/safe-operations.md",
            "references/pipeline.md",
            "preview_operation → get_operation_approval",
            "deploy_project_pipeline",
        ] {
            assert!(skill.contains(phrase), "SKILL.md missing phrase: {phrase}");
        }

        let required_refs = [
            ("debugging-workflow.md", "create_debug_session"),
            ("log-tools.md", "get_log_context"),
            ("safe-operations.md", "apply_config_change"),
            ("pipeline.md", "read_pipeline_run_logs"),
        ];
        for (file, phrase) in required_refs {
            let content = fs::read_to_string(root.join("references").join(file))
                .unwrap_or_else(|err| panic!("read reference {file}: {err}"));
            assert!(content.contains(phrase), "{file} missing phrase: {phrase}");
        }
    }

    #[test]
    fn detect_coding_agents_finds_cli_and_app_bundle() {
        let home = tempfile_dir();
        let bin = home.join("bin");
        fs::create_dir_all(&bin).expect("mkdir bin");
        fs::write(bin.join("claude"), b"bin").expect("write claude");
        fs::write(bin.join("codex"), b"bin").expect("write codex");
        let apps = home.join("Applications");
        fs::create_dir_all(apps.join("Cursor.app")).expect("mkdir cursor app");

        let statuses = detect_coding_agents_for_paths(&home, Some(bin.as_os_str()), &[apps]);

        assert!(agent_status(&statuses, "claude-code").installed);
        assert!(agent_status(&statuses, "codex").installed);
        assert!(agent_status(&statuses, "cursor").installed);
    }

    #[test]
    fn detect_coding_agents_marks_missing_agents_uninstalled() {
        let statuses = detect_coding_agents_for_search_dirs(&[], &[]);

        assert!(!agent_status(&statuses, "claude-code").installed);
        assert!(!agent_status(&statuses, "codex").installed);
        assert!(!agent_status(&statuses, "cursor").installed);
    }

    fn agent_status<'a>(
        statuses: &'a [CodingAgentAvailability],
        agent: &str,
    ) -> &'a CodingAgentAvailability {
        statuses
            .iter()
            .find(|status| status.agent == agent)
            .expect("agent status")
    }

    fn tempfile_dir() -> std::path::PathBuf {
        let mut dir = std::env::temp_dir();
        dir.push(format!(
            "superdev-sidecar-path-test-{}",
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .expect("time")
                .as_nanos()
        ));
        fs::create_dir_all(&dir).expect("mkdir temp");
        dir
    }
}
