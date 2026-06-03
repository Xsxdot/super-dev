// mcp_install.rs 提供 SuperDev MCP 配置安装能力。
//
// 职责：
//   - 解析 Claude Code、Codex、Cursor 的 MCP 配置文件
//   - 只合并 mcpServers/mcp_servers.superdev 这一项
//   - 在写入前备份原文件并用临时文件原子替换
//
// 边界：
//   - 不启动或探测 agent
//   - 不调用智能体 CLI
//   - 不渲染前端状态

use serde::Serialize;
use serde_json::json;
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

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct InstallOutcome {
    pub installed: bool,
    pub already_present: bool,
    pub agent: String,
    pub config_path: String,
    pub backup_path: Option<String>,
    pub manual_config: String,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct InstallHint {
    pub agent: String,
    pub config_path: String,
    pub manual_config: String,
}

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

    fn config_path(&self, home: &Path) -> PathBuf {
        match self {
            Self::ClaudeCode => home.join(".claude.json"),
            Self::Codex => home.join(".codex").join("config.toml"),
            Self::Cursor => home.join(".cursor").join("mcp.json"),
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

pub fn install_mcp_for_paths(
    agent: &str,
    home: &Path,
    mcp_path: &Path,
) -> Result<InstallOutcome, String> {
    let kind = AgentKind::parse(agent)?;
    let entry = McpEntry {
        command: mcp_path.to_string_lossy().to_string(),
        agent_url: DEFAULT_AGENT_URL.to_string(),
    };
    let config_path = kind.config_path(home);
    match kind {
        AgentKind::ClaudeCode | AgentKind::Cursor => {
            install_json_kind_to_path(&config_path, &entry, agent)
        }
        AgentKind::Codex => install_toml_kind_to_path(&config_path, &entry, agent),
    }
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
    })
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

fn install_json_to_path(path: &Path, entry: &McpEntry) -> Result<InstallOutcome, String> {
    install_json_kind_to_path(path, entry, "claude-code")
}

fn install_json_kind_to_path(
    path: &Path,
    entry: &McpEntry,
    agent: &str,
) -> Result<InstallOutcome, String> {
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
) -> Result<InstallOutcome, String> {
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
) -> Result<InstallOutcome, String> {
    let existing = match fs::read_to_string(path) {
        Ok(content) => Some(content),
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => None,
        Err(err) => return Err(format!("读取配置文件失败: {err}")),
    };
    let merged = merge(existing.as_deref(), entry)?;
    if !merged.changed {
        return Ok(InstallOutcome {
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
    Ok(InstallOutcome {
        installed: true,
        already_present: false,
        agent,
        config_path: path.to_string_lossy().to_string(),
        backup_path,
        manual_config,
    })
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
    Err(format!("找不到 {name} sidecar 二进制，请检查桌面端打包配置"))
}

#[tauri::command]
pub fn install_mcp(app: AppHandle, agent: String) -> Result<InstallOutcome, String> {
    let home = std::env::var_os("HOME")
        .map(PathBuf::from)
        .ok_or_else(|| "无法解析 HOME 目录".to_string())?;
    let mcp = resolve_sidecar_binary(&app, "superdev-mcp")?;
    install_mcp_for_paths(&agent, &home, &mcp)
}

#[tauri::command]
pub fn mcp_install_hint(app: AppHandle, agent: String) -> Result<InstallHint, String> {
    let home = std::env::var_os("HOME")
        .map(PathBuf::from)
        .ok_or_else(|| "无法解析 HOME 目录".to_string())?;
    let mcp = resolve_sidecar_binary(&app, "superdev-mcp")?;
    install_hint_for_paths(&agent, &home, &mcp)
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
        fs::write(&config, r#"{"mcpServers":{"github":{"command":"gh"}}}"#)
            .expect("seed config");

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
        assert!(fs::read_to_string(config).expect("read").contains("superdev"));
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

        assert_eq!(find_sidecar_binary_in_dir(&dir, "superdev-mcp"), Some(exact));
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
