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

fn install_json_to_path(path: &Path, entry: &McpEntry) -> Result<ConfigInstallOutcome, String> {
    install_json_kind_to_path(path, entry, "claude-code")
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

        let outcome = install_json_to_path(&config, &entry()).expect("install");

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
