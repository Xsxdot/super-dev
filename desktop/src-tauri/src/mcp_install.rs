// mcp_install.rs 提供 SuperDev MCP 配置安装能力。
//
// 职责：
//   - 解析 Claude Code、Codex、Cursor 的 MCP 配置文件
//   - 只合并 mcpServers/mcp_servers.superdev 这一项
//   - 在写入前备份原文件并用临时文件原子替换
//   - 随 MCP 配置安装 SuperDev 使用指南 skill
//   - 检测 Claude Code、Codex、Cursor 是否已安装
//   - 为其他本机 stdio MCP Agent 生成不带配置路径假设的标准连接材料
//
// 边界：
//   - 不启动或探测 SuperDev agent
//   - 不启动编程智能体进程
//   - 不调用智能体 CLI
//   - 不渲染前端状态
//   - 不猜测未知 Agent 的私有配置位置、schema 方言、Skill 或 Hook

pub mod compat;
pub mod connectors;
pub mod contracts;
pub mod fs_port;
pub mod registry;

use fs_port::{BatchFile, ConnectorFs, LocalFs};
use serde::Serialize;
use serde_json::json;
use std::ffi::{OsStr, OsString};
use std::fs;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use tauri::{AppHandle, Manager, State};

const DEFAULT_AGENT_URL: &str = "http://127.0.0.1:57017";

/// CONFIG_WRITE_LABELS 是 MCP 配置文件写入失败时的文案词汇。
///
/// 这类词汇是调用方的（端口不知道自己在写哪一类业务文件），经
/// `ConnectorFs::write_atomic` 传下去，让「备份配置文件失败」「写入临时配置文件失败」
/// 这些改造前就有的文案逐字节保持不变。
/// 第二波 connector 的 `connectors/common.rs` 直调 atomic_write_file 时用的是
/// 同一个词，两族措辞必须一致，改这里请一并核对那边。
const CONFIG_WRITE_LABELS: fs_port::WriteLabels<'static> = fs_port::WriteLabels {
    write_object: "配置文件",
    backup_failure: "备份配置文件失败",
};

/// HOOK_WRITE_LABELS 是 SessionStart hook 配置写入失败时的文案词汇。
///
/// 注意两个字段的名词**故意不一致**（写入用「hook 配置文件」、备份用「hook 配置」，
/// 且「备份」后带一个空格）：这是改造前两处手写文案的原样，不是笔误，不要"统一"掉。
const HOOK_WRITE_LABELS: fs_port::WriteLabels<'static> = fs_port::WriteLabels {
    write_object: "hook 配置文件",
    backup_failure: "备份 hook 配置失败",
};

/// SKILL_BATCH_WRITE_LABEL 是 skill 目录批量写入失败时的对象名词。
const SKILL_BATCH_WRITE_LABEL: &str = "skill 文件";

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
pub(crate) struct ConfigInstallOutcome {
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
pub struct SessionHookOutcome {
    pub installed: bool,
    pub already_present: bool,
    pub config_path: String,
    pub backup_path: Option<String>,
    // needs_trust 为 true 时表示该 Agent（目前仅 Codex）要求用户在 CLI 里
    // 手动 review/trust 后 hook 才会生效，前端应据此提示用户。
    pub needs_trust: bool,
    pub error: Option<String>,
}

impl SessionHookOutcome {
    fn failed(config_path: &Path, needs_trust: bool, error: String) -> Self {
        Self {
            installed: false,
            already_present: false,
            config_path: config_path.to_string_lossy().to_string(),
            backup_path: None,
            needs_trust,
            error: Some(error),
        }
    }
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
    pub session_hook: SessionHookOutcome,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct InstallHint {
    pub agent: String,
    pub config_path: String,
    pub manual_config: String,
    pub skill_target_path: String,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct GenericMcpConnectionMaterial {
    pub transport: String,
    pub command: String,
    pub agent_url: String,
    pub manual_config: String,
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
    pub hook_config_path: String,
    pub hook_installed: bool,
    pub hook_needs_trust: bool,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct UninstallOutcome {
    pub agent: String,
    pub config_path: String,
    pub removed_config: bool,
    pub config_backup_path: Option<String>,
    pub skill_path: String,
    pub removed_skill: bool,
    pub hook_config_path: String,
    pub removed_hook: bool,
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
pub(crate) enum AgentKind {
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
        home: &Path,
        command_dirs: &[PathBuf],
        app_dirs: &[PathBuf],
    ) -> Option<PathBuf> {
        match self {
            Self::ClaudeCode => find_command_in_dirs(command_dirs, &["claude"]),
            Self::Codex => find_command_in_dirs(command_dirs, &["codex"])
                .or_else(|| find_app_bundle(app_dirs, "Codex.app"))
                .or_else(|| find_existing_config_target(&self.config_path(home))),
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

    // hook_needs_trust 表示该 Agent 是否要求用户手动信任非托管 hook 后才生效。
    // Codex 的非托管 command hook 必须在 CLI 里 /hooks 审核信任，写入即生效不成立；
    // Claude Code 与 Cursor 写入用户 settings 后即生效，无此门槛。
    fn hook_needs_trust(&self) -> bool {
        matches!(self, Self::Codex)
    }

    // session_hook_path 返回各 Agent 存放 SessionStart hook 的配置文件路径。
    //
    // 注意：hook 与 MCP 的配置文件不一定是同一个——
    //   - Claude Code：MCP 在 ~/.claude.json，但 hooks 在 ~/.claude/settings.json。
    //   - Codex：用独立的 ~/.codex/hooks.json，刻意不并进 config.toml，避免 TOML 合并风险。
    //   - Cursor：hooks 在 ~/.cursor/hooks.json。
    fn session_hook_path(&self, home: &Path) -> PathBuf {
        match self {
            Self::ClaudeCode => home.join(".claude").join("settings.json"),
            Self::Codex => home.join(".codex").join("hooks.json"),
            Self::Cursor => home.join(".cursor").join("hooks.json"),
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

/// install_mcp_for_paths_with_skill 为单个 Agent 完成 MCP + skill + hook 安装。
///
/// 参数：
///   - agent: Agent 标识，支持 claude-code、codex、cursor
///   - home: 用于定位配置文件、skill 目录与 hook 配置的用户 HOME
///   - mcp_path: superdev-mcp 可执行文件的绝对路径
///   - skill_source: bundled superdev skill 源目录
///   - skill_source_error: skill 源目录不可用时的错误说明
///
/// 返回：
///   - MCP 配置、skill、session hook 三项的安装结果
///
/// 注意：
///   - 文件操作经 ConnectorFs 端口，本地/远端同一逻辑；本函数是本机入口，固定绑定 LocalFs
///   - skill 或 hook 失败不阻断 MCP 配置安装，失败原因回填到各自的结果里
pub fn install_mcp_for_paths_with_skill(
    agent: &str,
    home: &Path,
    mcp_path: &Path,
    skill_source: Option<&Path>,
    skill_source_error: Option<String>,
) -> Result<InstallOutcome, String> {
    // 本机安装：文件操作统一经 LocalFs 端口；远端安装（Task 9）传入 RemoteAgentFs
    // 复用下面完全相同的编排与方言逻辑。
    let fs_port = &LocalFs;
    let kind = AgentKind::parse(agent)?;
    let entry = McpEntry {
        command: mcp_path.to_string_lossy().to_string(),
        agent_url: DEFAULT_AGENT_URL.to_string(),
    };
    let config_path = kind.config_path(home);
    let merge: fn(Option<&str>, &McpEntry) -> Result<MergeResult, String> = match kind {
        AgentKind::ClaudeCode | AgentKind::Cursor => merge_json_config,
        AgentKind::Codex => merge_codex_config,
    };
    let config_outcome = install_to_path_with_fs(
        fs_port,
        &config_path,
        &entry,
        merge,
        agent.to_string(),
        kind.manual_config(&entry),
    )?;
    let skill_target = kind.skill_dir(home);
    let skill = match skill_source {
        // skill 源目录恒在桌面端 resources，先在本地物化成文件集，再经端口写到目标机器。
        Some(source) => install_skill_dir_from_source(fs_port, source, &skill_target)
            .unwrap_or_else(|err| SkillInstallOutcome::failed(&skill_target, err)),
        None => SkillInstallOutcome::failed(
            &skill_target,
            skill_source_error.unwrap_or_else(|| "找不到 SuperDev skill 源目录".to_string()),
        ),
    };
    // SessionStart hook 依赖 skill 目录里的 run-hook.cmd/session-start，
    // 因此仅在 skill 已就位（本次装好或先前已存在）时才注册 hook。
    // hook 注册失败不阻断整体安装：降级后仍有 skill description 兜底触发。
    let hook_path = kind.session_hook_path(home);
    // hook 脚本是否就位必须查**目标机器**：远端安装时 skill 装在远端，直连本地
    // fs 会读成桌面机的目录，从而对远端做出错误判断。
    let hook_script = skill_target.join("hooks").join("session-start");
    let session_hook = if port_path_is_file(fs_port, &hook_script) {
        install_session_hook_with_fs(fs_port, kind, &hook_path, &skill_target).unwrap_or_else(
            |err| SessionHookOutcome::failed(&hook_path, kind.hook_needs_trust(), err),
        )
    } else {
        SessionHookOutcome::failed(
            &hook_path,
            kind.hook_needs_trust(),
            "skill hook 脚本缺失，跳过 SessionStart hook 注册".to_string(),
        )
    };
    Ok(InstallOutcome {
        installed: config_outcome.installed,
        already_present: config_outcome.already_present,
        agent: config_outcome.agent,
        config_path: config_outcome.config_path,
        backup_path: config_outcome.backup_path,
        manual_config: config_outcome.manual_config,
        skill,
        session_hook,
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

fn generic_mcp_connection_material_for_path(mcp_path: &Path) -> GenericMcpConnectionMaterial {
    let command = mcp_path.to_string_lossy().to_string();
    let agent_url = DEFAULT_AGENT_URL.to_string();
    let manual_config = serde_json::to_string_pretty(&json!({
        "mcpServers": {
            "superdev": {
                "command": command,
                "env": { "SUPERDEV_AGENT_URL": agent_url }
            }
        }
    }))
    .expect("generic manual json");
    GenericMcpConnectionMaterial {
        transport: "stdio".to_string(),
        command,
        agent_url,
        manual_config,
    }
}

#[cfg(test)]
pub fn detect_coding_agents_for_paths(
    home: &Path,
    path_value: Option<&OsStr>,
    app_dirs: &[PathBuf],
) -> Vec<CodingAgentAvailability> {
    let command_dirs = command_search_dirs(home, path_value);
    detect_coding_agents_for_search_dirs(home, &command_dirs, app_dirs)
}

#[cfg(test)]
fn detect_coding_agents_for_search_dirs(
    home: &Path,
    command_dirs: &[PathBuf],
    app_dirs: &[PathBuf],
) -> Vec<CodingAgentAvailability> {
    [AgentKind::ClaudeCode, AgentKind::Codex, AgentKind::Cursor]
        .into_iter()
        .map(|kind| {
            let detection_path = kind.detect_installation(home, command_dirs, app_dirs);
            CodingAgentAvailability {
                agent: kind.label().to_string(),
                installed: detection_path.is_some(),
                detection_path: detection_path.map(|path| path.to_string_lossy().to_string()),
            }
        })
        .collect()
}

fn find_existing_config_target(config_path: &Path) -> Option<PathBuf> {
    if config_path.is_file() {
        return Some(config_path.to_path_buf());
    }
    config_path
        .parent()
        .filter(|dir| dir.is_dir())
        .map(Path::to_path_buf)
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

pub(crate) fn executable_file_names(command: &str) -> Vec<String> {
    executable_file_names_for_platform(command, cfg!(windows))
}

fn executable_file_names_for_platform(command: &str, is_windows: bool) -> Vec<String> {
    if is_windows {
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

/// resolve_user_home_dir 按当前平台解析桌面进程对应的真实用户目录。
pub(crate) fn resolve_user_home_dir() -> Result<PathBuf, String> {
    user_home_dir_from_env(|key| std::env::var_os(key), cfg!(windows)).ok_or_else(|| {
        "无法解析用户目录：请检查 HOME（macOS/Linux）或 USERPROFILE/HOMEDRIVE/HOMEPATH（Windows）"
            .to_string()
    })
}

fn user_home_dir_from_env(
    mut env_var: impl FnMut(&str) -> Option<OsString>,
    is_windows: bool,
) -> Option<PathBuf> {
    if let Some(home) = non_empty_env_value(env_var("HOME")) {
        return Some(PathBuf::from(home));
    }
    if !is_windows {
        return None;
    }
    // Windows 的普通桌面进程通常没有 HOME；优先使用 USERPROFILE，
    // 再回退到 HOMEDRIVE + HOMEPATH，避免启动向导在第一步就失败。
    if let Some(user_profile) = non_empty_env_value(env_var("USERPROFILE")) {
        return Some(PathBuf::from(user_profile));
    }
    let mut drive = non_empty_env_value(env_var("HOMEDRIVE"))?;
    let path = non_empty_env_value(env_var("HOMEPATH"))?;
    drive.push(path);
    Some(PathBuf::from(drive))
}

fn non_empty_env_value(value: Option<OsString>) -> Option<OsString> {
    value.filter(|value| !value.as_os_str().is_empty())
}

fn push_unique_path(paths: &mut Vec<PathBuf>, path: PathBuf) {
    if !paths.iter().any(|existing| existing == &path) {
        paths.push(path);
    }
}

/// port_path_exists 经端口判断路径是否存在。
///
/// 语义刻意与 `Path::exists()` 对齐：stat 失败（权限异常等）一律当作「不存在」，
/// 而不是把一次探测升级成错误——改造前所有 `path.exists()` 判断都是这个语义，
/// 收拢到端口后必须保持一致，否则本地安装会在边角场景里多出新的失败分支。
fn port_path_exists(fs_port: &dyn ConnectorFs, path: &Path) -> bool {
    fs_port.stat(path).map(|stat| stat.exists).unwrap_or(false)
}

/// port_path_is_file 经端口判断路径是否是普通文件。
///
/// 语义与 `Path::is_file()` 对齐：不存在、是目录、stat 失败一律为 false。
fn port_path_is_file(fs_port: &dyn ConnectorFs, path: &Path) -> bool {
    fs_port
        .stat(path)
        .map(|stat| stat.exists && !stat.is_dir)
        .unwrap_or(false)
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

/// read_config_status 只读地解析某个 Agent 配置文件里的 SuperDev MCP 状态。
///
/// 参数：
///   - fs_port: 文件操作端口，本地传 LocalFs、远端传 RemoteAgentFs
///   - kind: Agent 类型，决定按 JSON 还是 TOML 方言解析
///   - path: 配置文件路径
///
/// 返回：
///   - (配置文件是否存在, 是否已配置 superdev, command, agent_url, 错误说明)
///
/// 注意：
///   - 文件操作经 ConnectorFs 端口，本地/远端同一逻辑
fn read_config_status(
    fs_port: &dyn ConnectorFs,
    kind: AgentKind,
    path: &Path,
) -> (bool, bool, Option<String>, Option<String>, Option<String>) {
    let existing = match fs_port.read_optional(path) {
        Ok(Some(content)) => content,
        Ok(None) => {
            return (false, false, None, None, None);
        }
        Err(err) => {
            return (
                port_path_exists(fs_port, path),
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
        .map(|value| value.to_string())
        .or_else(|| Some(String::new()));
    let agent_url = server
        .get("env")
        .and_then(|env| env.get("SUPERDEV_AGENT_URL"))
        .and_then(|value| value.as_str())
        .map(|value| value.to_string())
        .or_else(|| Some(String::new()));
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
        .map(|value| value.to_string())
        .or_else(|| Some(String::new()));
    let agent_url = server
        .get("env")
        .and_then(|env| env.get("SUPERDEV_AGENT_URL"))
        .and_then(|value| value.as_str())
        .map(|value| value.to_string())
        .or_else(|| Some(String::new()));
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

const TEMP_ARTIFACT_MARKER: &str = ".superdev-tmp-";
static TEMP_ARTIFACT_COUNTER: AtomicU64 = AtomicU64::new(0);

// TempArtifactKind 只剩临时文件一种：skill 临时目录端口化之后由
// PortTempDirGuard 经 ConnectorFs 清理，不再走这里的本地 std::fs 清理。
#[derive(Clone, Copy)]
enum TempArtifactKind {
    File,
}

impl TempArtifactKind {
    fn label(self) -> &'static str {
        match self {
            Self::File => "file",
        }
    }
}

struct TempArtifactGuard {
    path: PathBuf,
    kind: TempArtifactKind,
}

impl TempArtifactGuard {
    fn new(path: PathBuf, kind: TempArtifactKind) -> Self {
        Self { path, kind }
    }
}

impl Drop for TempArtifactGuard {
    fn drop(&mut self) {
        let result = match self.kind {
            TempArtifactKind::File => fs::remove_file(&self.path),
        };
        if let Err(error) = result {
            if error.kind() != std::io::ErrorKind::NotFound {
                tracing::warn!(
                    artifact_kind = self.kind.label(),
                    error_kind = ?error.kind(),
                    "failed to clean SuperDev temporary artifact"
                );
            }
        }
    }
}

fn target_parent(path: &Path) -> &Path {
    path.parent()
        .filter(|parent| !parent.as_os_str().is_empty())
        .unwrap_or_else(|| Path::new("."))
}

fn unique_temp_candidate(target: &Path) -> PathBuf {
    let counter = TEMP_ARTIFACT_COUNTER.fetch_add(1, Ordering::Relaxed);
    let nanos = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|duration| duration.as_nanos())
        .unwrap_or(0);
    let mut name = OsString::from(".");
    name.push(target.file_name().unwrap_or_else(|| OsStr::new("superdev")));
    name.push(format!(
        "{TEMP_ARTIFACT_MARKER}{}-{nanos}-{counter}",
        std::process::id()
    ));
    target.with_file_name(name)
}

fn create_unique_temp_file(target: &Path) -> Result<(PathBuf, fs::File), std::io::Error> {
    for _ in 0..64 {
        let temp = unique_temp_candidate(target);
        match fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&temp)
        {
            Ok(file) => return Ok((temp, file)),
            Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => continue,
            Err(error) => return Err(error),
        }
    }
    Err(std::io::Error::new(
        std::io::ErrorKind::AlreadyExists,
        "无法分配唯一临时文件",
    ))
}

#[cfg(unix)]
fn apply_atomic_target_permissions(target: &Path, file: &fs::File) -> Result<(), std::io::Error> {
    use std::os::unix::fs::PermissionsExt;

    let permissions = match fs::metadata(target) {
        Ok(metadata) => metadata.permissions(),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            // 新建 Agent 配置可能包含 token/env，不能继承常见 umask=022 的 0644。
            fs::Permissions::from_mode(0o600)
        }
        Err(error) => return Err(error),
    };
    file.set_permissions(permissions)
}

#[cfg(not(unix))]
fn apply_atomic_target_permissions(_target: &Path, _file: &fs::File) -> Result<(), std::io::Error> {
    Ok(())
}

#[cfg(unix)]
fn sync_parent_directory(path: &Path) -> Result<(), std::io::Error> {
    fs::File::open(target_parent(path))?.sync_all()
}

#[cfg(not(unix))]
fn sync_parent_directory(_path: &Path) -> Result<(), std::io::Error> {
    Ok(())
}

fn atomic_write_file_with_replace<F>(
    target: &Path,
    content: &[u8],
    write_kind: &str,
    replace: F,
) -> Result<(), String>
where
    F: FnOnce(&Path, &Path) -> Result<(), std::io::Error>,
{
    let (temp, mut file) = create_unique_temp_file(target)
        .map_err(|error| format!("创建{write_kind}临时文件失败: {error}"))?;
    let _guard = TempArtifactGuard::new(temp.clone(), TempArtifactKind::File);
    // rename 会继承临时文件权限；写入敏感正文前先复制旧 mode 或使用安全默认值。
    apply_atomic_target_permissions(target, &file)
        .map_err(|error| format!("设置临时{write_kind}权限失败: {error}"))?;
    file.write_all(content)
        .map_err(|error| format!("写入临时{write_kind}失败: {error}"))?;
    file.flush()
        .map_err(|error| format!("刷新临时{write_kind}失败: {error}"))?;
    file.sync_all()
        .map_err(|error| format!("同步临时{write_kind}失败: {error}"))?;
    // Windows 不允许在打开的临时文件句柄上完成替换，因此必须先显式关闭文件。
    drop(file);
    replace(&temp, target).map_err(|error| format!("替换{write_kind}失败: {error}"))?;
    sync_parent_directory(target)
        .map_err(|error| format!("同步{write_kind}所在目录失败: {error}"))?;
    Ok(())
}

fn atomic_write_file(target: &Path, content: &[u8], write_kind: &str) -> Result<(), String> {
    tracing::debug!(write_kind, "atomic file write started");
    let started = std::time::Instant::now();
    let result = atomic_write_file_with_replace(target, content, write_kind, |temp, target| {
        fs::rename(temp, target)
    });
    match &result {
        Ok(()) => tracing::info!(
            write_kind,
            duration_ms = started.elapsed().as_millis() as u64,
            "atomic file write finished"
        ),
        Err(error) => tracing::error!(
            write_kind,
            error,
            duration_ms = started.elapsed().as_millis() as u64,
            "atomic file write failed"
        ),
    }
    result
}

/// uninstall_from_path 是 `uninstall_from_path_with_fs(&LocalFs, ..)` 的旧签名封装。
///
/// **仅测试可见**：生产代码一律显式传端口，避免远端卸载误调本地绑定版本而静默
/// 改到桌面机；保留它只是为了让既有测试与 connectors 的 cfg(test) 夹具零改动。
#[cfg(test)]
fn uninstall_from_path(
    path: &Path,
    remove: fn(Option<&str>) -> Result<MergeResult, String>,
) -> Result<(bool, Option<String>), String> {
    uninstall_from_path_with_fs(&LocalFs, path, remove)
}

/// uninstall_from_path_with_fs 经端口从配置文件里摘除 SuperDev 条目。
///
/// 参数：
///   - fs_port: 文件操作端口，本地传 LocalFs、远端传 RemoteAgentFs
///   - path: 配置文件路径
///   - remove: 方言相关的删除变换（JSON / TOML）
///
/// 返回：
///   - (是否确实发生删除, 备份文件路径)
///
/// 注意：
///   - 文件操作经 ConnectorFs 端口，本地/远端同一逻辑
///   - 文件不存在或没有 superdev 条目时不写盘，返回 (false, None)
fn uninstall_from_path_with_fs(
    fs_port: &dyn ConnectorFs,
    path: &Path,
    remove: fn(Option<&str>) -> Result<MergeResult, String>,
) -> Result<(bool, Option<String>), String> {
    let existing = match fs_port.read_optional(path) {
        Ok(Some(content)) => Some(content),
        Ok(None) => return Ok((false, None)),
        Err(err) => return Err(format!("读取配置文件失败: {err}")),
    };
    let removed = remove(existing.as_deref())?;
    if !removed.changed {
        return Ok((false, None));
    }
    // 走到这里说明文件刚被读到过，write_atomic 必然备份并返回 Some。
    let backup = fs_port.write_atomic(path, &removed.content, true, CONFIG_WRITE_LABELS)?;
    Ok((true, backup))
}

/// install_json_kind_to_path_with_fs 经端口写入 JSON 方言（Claude Code / Cursor）的 MCP 配置。
///
/// 参数：
///   - fs_port: 文件操作端口，本地传 LocalFs、远端传 RemoteAgentFs
///   - path / entry / agent: 配置路径、MCP 条目、Agent 标识
///
/// 返回：
///   - MCP 配置安装结果摘要
///
/// 注意：
///   - 文件操作经 ConnectorFs 端口，本地/远端同一逻辑
pub(crate) fn install_json_kind_to_path_with_fs(
    fs_port: &dyn ConnectorFs,
    path: &Path,
    entry: &McpEntry,
    agent: &str,
) -> Result<ConfigInstallOutcome, String> {
    install_to_path_with_fs(
        fs_port,
        path,
        entry,
        merge_json_config,
        agent.to_string(),
        AgentKind::ClaudeCode.manual_config(entry),
    )
}

/// install_toml_kind_to_path_with_fs 经端口写入 TOML 方言（Codex）的 MCP 配置。
///
/// 参数与返回同 `install_json_kind_to_path_with_fs`，只是换成 Codex 的合并变换。
///
/// 注意：
///   - 文件操作经 ConnectorFs 端口，本地/远端同一逻辑
pub(crate) fn install_toml_kind_to_path_with_fs(
    fs_port: &dyn ConnectorFs,
    path: &Path,
    entry: &McpEntry,
    agent: &str,
) -> Result<ConfigInstallOutcome, String> {
    install_to_path_with_fs(
        fs_port,
        path,
        entry,
        merge_codex_config,
        agent.to_string(),
        AgentKind::Codex.manual_config(entry),
    )
}

/// install_json_kind_to_path 是 `install_json_kind_to_path_with_fs(&LocalFs, ..)` 的旧签名封装。
///
/// **仅测试可见**：生产代码一律显式传端口，避免远端安装误调本地绑定版本而静默
/// 写到桌面机；保留它只是为了让既有测试零改动。
#[cfg(test)]
pub(crate) fn install_json_kind_to_path(
    path: &Path,
    entry: &McpEntry,
    agent: &str,
) -> Result<ConfigInstallOutcome, String> {
    install_json_kind_to_path_with_fs(&LocalFs, path, entry, agent)
}

/// install_to_path 是 `install_to_path_with_fs(&LocalFs, ..)` 的旧签名封装。
///
/// **仅测试可见**：理由同 `install_json_kind_to_path`；调用方是 connectors.rs 里
/// 同样 `#[cfg(test)]` 的 StandardJsonConnector 夹具。
#[cfg(test)]
fn install_to_path(
    path: &Path,
    entry: &McpEntry,
    merge: fn(Option<&str>, &McpEntry) -> Result<MergeResult, String>,
    agent: String,
    manual_config: String,
) -> Result<ConfigInstallOutcome, String> {
    install_to_path_with_fs(&LocalFs, path, entry, merge, agent, manual_config)
}

/// install_to_path_with_fs 经端口把 SuperDev MCP 条目合并进配置文件。
///
/// 参数：
///   - fs_port: 文件操作端口，本地传 LocalFs、远端传 RemoteAgentFs
///   - path: 配置文件路径
///   - entry: 要写入的 MCP 条目（命令 + Agent URL）
///   - merge: 方言相关的合并变换（JSON / TOML）
///   - agent: Agent 标识，仅用于回填结果
///   - manual_config: 手动配置示例，仅用于回填结果
///
/// 返回：
///   - 安装结果摘要（是否写入、是否已存在、备份路径等）
///
/// 注意：
///   - 文件操作经 ConnectorFs 端口，本地/远端同一逻辑
///   - 合并结果无变化时不写盘，也不产生备份
fn install_to_path_with_fs(
    fs_port: &dyn ConnectorFs,
    path: &Path,
    entry: &McpEntry,
    merge: fn(Option<&str>, &McpEntry) -> Result<MergeResult, String>,
    agent: String,
    manual_config: String,
) -> Result<ConfigInstallOutcome, String> {
    let existing = fs_port
        .read_optional(path)
        .map_err(|err| format!("读取配置文件失败: {err}"))?;
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
        fs_port
            .mkdir_all(parent)
            .map_err(|err| format!("创建配置目录失败: {err}"))?;
    }
    let backup_path = fs_port.write_atomic(path, &merged.content, true, CONFIG_WRITE_LABELS)?;
    Ok(ConfigInstallOutcome {
        installed: true,
        already_present: false,
        agent,
        config_path: path.to_string_lossy().to_string(),
        backup_path,
        manual_config,
    })
}

/// install_skill_dir_from_source 把 bundled skill 源目录经端口安装到目标目录。
///
/// 参数：
///   - fs_port: 目标端文件操作端口，本地传 LocalFs、远端传 RemoteAgentFs
///   - source: bundled skill 源目录（恒在桌面端本机，读取不经端口）
///   - target: 目标 skill 目录（可能在远端机器上）
///
/// 返回：
///   - 安装结果摘要
///
/// 注意：
///   - 文件操作经 ConnectorFs 端口，本地/远端同一逻辑
///   - 这是 connector 侧安装 skill 的常规入口：先本地物化，再经端口批量写
fn install_skill_dir_from_source(
    fs_port: &dyn ConnectorFs,
    source: &Path,
    target: &Path,
) -> Result<SkillInstallOutcome, String> {
    let source_files =
        materialize_skill_source(source).map_err(|err| format!("读取 skill 源目录失败: {err}"))?;
    install_skill_dir_with_fs(fs_port, &source_files, target)
}

/// install_skill_dir 是 `install_skill_dir_from_source(&LocalFs, ..)` 的旧签名封装。
///
/// **仅测试可见**：生产代码一律显式传端口，避免远端安装误调本地绑定版本而静默
/// 写到桌面机；保留它只是为了让既有测试零改动。
#[cfg(test)]
fn install_skill_dir(source: &Path, target: &Path) -> Result<SkillInstallOutcome, String> {
    install_skill_dir_from_source(&LocalFs, source, target)
}

/// install_skill_dir_with_fs 经端口把已物化的 skill 文件集安装到目标目录。
///
/// 参数：
///   - fs_port: 文件操作端口，本地传 LocalFs、远端传 RemoteAgentFs
///   - source_files: 已在本地物化好的 skill 文件集（源永远在桌面端 resources）
///   - target: 目标 skill 目录
///
/// 返回：
///   - 安装结果摘要（是否写入、是否已一致、旧目录备份路径）
///
/// 注意：
///   - 文件操作经 ConnectorFs 端口，本地/远端同一逻辑
///   - 安装顺序为「临时目录 → 备份旧目录 → 改名替换」，替换失败会恢复旧目录
fn install_skill_dir_with_fs(
    fs_port: &dyn ConnectorFs,
    source_files: &[BatchFile],
    target: &Path,
) -> Result<SkillInstallOutcome, String> {
    install_skill_dir_core(
        fs_port,
        source_files,
        target,
        |temp| fs_port.write_batch(temp, source_files, SKILL_BATCH_WRITE_LABEL),
        |from, to| fs_port.rename(from, to),
    )
}

/// install_skill_dir_with_ops 保留「注入 copy/replace」的旧安装入口。
///
/// 这个缝在端口化之前就存在，唯一用途是让测试注入一个必然失败的 replace，
/// 验证「备份 → 替换失败 → 恢复旧目录 → 清理临时目录」这条恢复链。端口化之后
/// 生产路径已改走 install_skill_dir_with_fs，因此本函数只对测试可见；它与生产
/// 路径共用同一个 install_skill_dir_core，注入的只是最外层两步文件动作。
#[cfg(test)]
fn install_skill_dir_with_ops<C, R>(
    source: &Path,
    target: &Path,
    copy: C,
    replace: R,
) -> Result<SkillInstallOutcome, String>
where
    C: FnOnce(&Path, &Path) -> Result<(), String>,
    R: FnOnce(&Path, &Path) -> Result<(), std::io::Error>,
{
    let source_files =
        materialize_skill_source(source).map_err(|err| format!("读取 skill 源目录失败: {err}"))?;
    install_skill_dir_core(
        &LocalFs,
        &source_files,
        target,
        |temp| copy(source, temp),
        |from, to| replace(from, to).map_err(|error| error.to_string()),
    )
}

/// install_skill_dir_core 是 skill 目录安装的唯一实现。
///
/// 参数：
///   - fs_port: 文件操作端口
///   - source_files: 已物化的源文件集
///   - target: 目标 skill 目录
///   - copy_into_temp: 把源文件集写进临时目录（生产路径即 fs_port.write_batch）
///   - replace: 把临时目录改名成目标目录（生产路径即 fs_port.rename）
///
/// 返回：
///   - 安装结果摘要
///
/// 注意：
///   - 文件操作经 ConnectorFs 端口，本地/远端同一逻辑
///   - copy/replace 单独抽成参数只为保留既有的失败注入测试缝，生产路径两者都是端口调用
fn install_skill_dir_core<C, R>(
    fs_port: &dyn ConnectorFs,
    source_files: &[BatchFile],
    target: &Path,
    copy_into_temp: C,
    replace: R,
) -> Result<SkillInstallOutcome, String>
where
    C: FnOnce(&Path) -> Result<(), String>,
    R: FnOnce(&Path, &Path) -> Result<(), String>,
{
    if source_files.is_empty() {
        return Err("skill 源目录为空".to_string());
    }
    if port_path_exists(fs_port, target) && skill_dir_matches(fs_port, source_files, target)? {
        return Ok(SkillInstallOutcome {
            installed: false,
            already_present: true,
            target_path: target.to_string_lossy().to_string(),
            backup_path: None,
            error: None,
        });
    }
    if let Some(parent) = target.parent() {
        fs_port
            .mkdir_all(parent)
            .map_err(|err| format!("创建 skill 目录失败: {err}"))?;
    }
    // 临时目录名由客户端生成（进程号 + 纳秒 + 进程内自增计数器），两端通用；
    // 端口只有 mkdir_all，没有「独占创建」原语，唯一性靠这三段组合保证。
    let temp = unique_temp_candidate(target);
    fs_port
        .mkdir_all(&temp)
        .map_err(|error| format!("创建唯一 skill 临时目录失败: {error}"))?;
    let mut guard = PortTempDirGuard::new(fs_port, temp.clone());
    copy_into_temp(&temp)?;
    let backup = if port_path_exists(fs_port, target) {
        let backup = backup_dir_path(target);
        fs_port
            .rename(target, &backup)
            .map_err(|err| format!("备份旧 skill 目录失败: {err}"))?;
        Some(backup)
    } else {
        None
    };
    if let Err(error) = replace(&temp, target) {
        // 旧目录已经移动到备份位置；最终替换失败时必须先恢复它，避免用户看到半完成状态。
        if let Some(backup) = &backup {
            if let Err(restore_error) = fs_port.rename(backup, target) {
                return Err(format!(
                    "替换 skill 目录失败: {error}; 恢复旧 skill 目录失败: {restore_error}"
                ));
            }
        }
        return Err(format!("替换 skill 目录失败: {error}"));
    }
    // 临时目录已被改名成目标目录，不再需要清理。
    guard.disarm();
    let backup_path = backup.map(|path| path.to_string_lossy().to_string());
    Ok(SkillInstallOutcome {
        installed: true,
        already_present: false,
        target_path: target.to_string_lossy().to_string(),
        backup_path,
        error: None,
    })
}

/// PortTempDirGuard 在出错路径上经端口清理 skill 临时目录。
///
/// 端口化之前这里用的是直接调 std::fs 的 TempArtifactGuard；远端安装时临时目录
/// 在远端机器上，只有经端口删除才有意义，因此清理动作必须跟着端口走。
struct PortTempDirGuard<'a> {
    fs_port: &'a dyn ConnectorFs,
    path: PathBuf,
    armed: bool,
}

impl<'a> PortTempDirGuard<'a> {
    fn new(fs_port: &'a dyn ConnectorFs, path: PathBuf) -> Self {
        Self {
            fs_port,
            path,
            armed: true,
        }
    }

    /// disarm 交出临时目录所有权（替换成功后调用），此后不再清理。
    fn disarm(&mut self) {
        self.armed = false;
    }
}

impl Drop for PortTempDirGuard<'_> {
    fn drop(&mut self) {
        if !self.armed {
            return;
        }
        if let Err(error) = self.fs_port.remove_dir_all(&self.path) {
            tracing::warn!(
                error = %error,
                "failed to clean SuperDev temporary skill directory"
            );
        }
    }
}

/// skill_status_for_target 只读地判断目标 skill 目录相对 bundled 源的状态。
///
/// 参数：
///   - fs_port: 文件操作端口，本地传 LocalFs、远端传 RemoteAgentFs
///   - source: bundled skill 源目录（始终在桌面端本机）
///   - source_error: 源目录不可用时的错误说明
///   - target: 目标 skill 目录
///
/// 返回：
///   - (目标是否存在, 是否与 bundled 源一致, 错误说明)
///
/// 注意：
///   - 文件操作经 ConnectorFs 端口，本地/远端同一逻辑（源目录读取恒为本地）
fn skill_status_for_target(
    fs_port: &dyn ConnectorFs,
    source: Option<&Path>,
    source_error: Option<String>,
    target: &Path,
) -> (bool, Option<bool>, Option<String>) {
    if !port_path_exists(fs_port, target) {
        return (false, Some(false), source_error);
    }
    let Some(source) = source else {
        return (true, None, source_error);
    };
    let source_files = match materialize_skill_source(source) {
        Ok(files) => files,
        Err(err) => {
            return (
                true,
                None,
                Some(format!("读取 bundled skill 源目录失败: {err}")),
            );
        }
    };
    match skill_dir_matches(fs_port, &source_files, target) {
        Ok(equal) => (true, Some(equal), None),
        Err(err) => (true, None, Some(err)),
    }
}

/// remove_skill_dir 是 `remove_skill_dir_with_fs(&LocalFs, ..)` 的旧签名封装。
///
/// **仅测试可见**：理由同 `install_skill_dir`。
#[cfg(test)]
fn remove_skill_dir(target: &Path) -> Result<bool, String> {
    remove_skill_dir_with_fs(&LocalFs, target)
}

/// remove_skill_dir_with_fs 经端口删除目标 skill 目录。
///
/// 参数：
///   - fs_port: 文件操作端口，本地传 LocalFs、远端传 RemoteAgentFs
///   - target: 目标 skill 目录
///
/// 返回：
///   - 目录本来就不存在时为 false，确实删除时为 true
///
/// 注意：
///   - 文件操作经 ConnectorFs 端口，本地/远端同一逻辑
fn remove_skill_dir_with_fs(fs_port: &dyn ConnectorFs, target: &Path) -> Result<bool, String> {
    if !port_path_exists(fs_port, target) {
        return Ok(false);
    }
    fs_port
        .remove_dir_all(target)
        .map_err(|err| format!("删除 skill 目录失败: {err}"))?;
    Ok(true)
}

// SuperDev hook 的稳定标识：所有 Agent 的 hook command 都包含这段路径片段，
// 据此在用户 settings 里幂等识别「我们装的那条」，避免重复追加、并支持精确卸载。
const HOOK_MARKER: &str = "skills/superdev/hooks";

// hook_command_for 生成各 Agent 写入 settings 的 hook 调用命令。
// 统一走 skill 目录下的 run-hook.cmd（跨平台 polyglot wrapper），由它再调 session-start。
// 用绝对路径，使 hook 不依赖会话 cwd；run-hook.cmd 含 HOOK_MARKER，是幂等与卸载的锚点。
fn hook_command_for(skill_dir: &Path) -> String {
    let runner = skill_dir.join("hooks").join("run-hook.cmd");
    // 统一用正斜杠，保证 HOOK_MARKER（含正斜杠）在 Windows 上也能匹配命中。
    let runner = runner.to_string_lossy().replace('\\', "/");
    format!("\"{runner}\" session-start")
}

// session_hook_event 返回各 Agent 的 SessionStart 事件键名与是否带 matcher。
// Cursor 用小写 sessionStart 且不需要 matcher；Claude Code / Codex 用 SessionStart 且带 startup 类 matcher。
fn session_hook_event(kind: AgentKind) -> (&'static str, Option<&'static str>) {
    match kind {
        AgentKind::ClaudeCode => ("SessionStart", Some("startup|clear|compact")),
        AgentKind::Codex => ("SessionStart", Some("startup|resume|clear|compact")),
        AgentKind::Cursor => ("sessionStart", None),
    }
}

// hook_already_present 判断给定 hooks 数组里是否已存在带 HOOK_MARKER 的条目。
// 同时兼容 Claude/Codex 的嵌套结构（matcher 组 -> hooks[].command）与 Cursor 的扁平结构（command）。
fn hook_array_contains_marker(arr: &[serde_json::Value]) -> bool {
    arr.iter().any(|group| {
        // 扁平结构：{ "command": "..." }
        if group
            .get("command")
            .and_then(|v| v.as_str())
            .is_some_and(|cmd| cmd.contains(HOOK_MARKER))
        {
            return true;
        }
        // 嵌套结构：{ "hooks": [ { "command": "..." } ] }
        group
            .get("hooks")
            .and_then(|v| v.as_array())
            .is_some_and(|inner| {
                inner.iter().any(|h| {
                    h.get("command")
                        .and_then(|v| v.as_str())
                        .is_some_and(|cmd| cmd.contains(HOOK_MARKER))
                })
            })
    })
}

// build_hook_entry 按 Agent 结构构造要追加的单个 hook 条目。
fn build_hook_entry(kind: AgentKind, command: &str) -> serde_json::Value {
    match kind {
        AgentKind::Cursor => json!({ "command": command }),
        AgentKind::ClaudeCode | AgentKind::Codex => {
            let (_, matcher) = session_hook_event(kind);
            let mut group = serde_json::Map::new();
            if let Some(m) = matcher {
                group.insert("matcher".to_string(), json!(m));
            }
            group.insert(
                "hooks".to_string(),
                json!([{ "type": "command", "command": command }]),
            );
            serde_json::Value::Object(group)
        }
    }
}

// merge_session_hook 把 SuperDev 的 SessionStart hook 幂等合并进现有 settings JSON。
// 已存在带 HOOK_MARKER 的条目则不改动（changed=false）；否则在对应事件数组末尾追加。
// 严格只追加、不重写用户的其它 hooks 与配置，保证对用户 settings 的最小侵入。
fn merge_session_hook(
    existing: Option<&str>,
    kind: AgentKind,
    command: &str,
) -> Result<MergeResult, String> {
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
    let hooks = obj
        .entry("hooks")
        .or_insert_with(|| json!({}))
        .as_object_mut()
        .ok_or_else(|| "配置文件格式异常(JSON): hooks 必须是对象".to_string())?;
    let (event, _) = session_hook_event(kind);
    let arr = hooks
        .entry(event.to_string())
        .or_insert_with(|| json!([]))
        .as_array_mut()
        .ok_or_else(|| format!("配置文件格式异常(JSON): hooks.{event} 必须是数组"))?;
    if hook_array_contains_marker(arr) {
        let content = serde_json::to_string_pretty(&root)
            .map_err(|err| format!("序列化配置失败(JSON): {err}"))?
            + "\n";
        return Ok(MergeResult {
            content,
            changed: false,
        });
    }
    arr.push(build_hook_entry(kind, command));
    let content = serde_json::to_string_pretty(&root)
        .map_err(|err| format!("序列化配置失败(JSON): {err}"))?
        + "\n";
    Ok(MergeResult {
        content,
        changed: true,
    })
}

/// install_session_hook 是 `install_session_hook_with_fs(&LocalFs, ..)` 的旧签名封装。
///
/// **仅测试可见**：理由同 `install_skill_dir`。
#[cfg(test)]
fn install_session_hook(
    kind: AgentKind,
    hook_path: &Path,
    skill_dir: &Path,
) -> Result<SessionHookOutcome, String> {
    install_session_hook_with_fs(&LocalFs, kind, hook_path, skill_dir)
}

/// install_session_hook_with_fs 经端口把 SuperDev SessionStart hook 写入 settings。
///
/// 参数：
///   - fs_port: 文件操作端口，本地传 LocalFs、远端传 RemoteAgentFs
///   - kind: Agent 类型，决定事件名、matcher 与结构形态
///   - hook_path: 该 Agent 存放 hook 的配置文件
///   - skill_dir: 已安装的 skill 目录，用于生成 hook 命令绝对路径
///
/// 返回：
///   - hook 安装结果（是否写入、是否已存在、备份路径、是否需用户手动信任）
///
/// 注意：
///   - 文件操作经 ConnectorFs 端口，本地/远端同一逻辑
///   - 沿用与 MCP 配置一致的「备份 + 临时文件原子替换」策略；幂等：已存在则 already_present
fn install_session_hook_with_fs(
    fs_port: &dyn ConnectorFs,
    kind: AgentKind,
    hook_path: &Path,
    skill_dir: &Path,
) -> Result<SessionHookOutcome, String> {
    let command = hook_command_for(skill_dir);
    let existing = fs_port
        .read_optional(hook_path)
        .map_err(|err| format!("读取 hook 配置失败: {err}"))?;
    let merged = merge_session_hook(existing.as_deref(), kind, &command)?;
    if !merged.changed {
        return Ok(SessionHookOutcome {
            installed: false,
            already_present: true,
            config_path: hook_path.to_string_lossy().to_string(),
            backup_path: None,
            needs_trust: kind.hook_needs_trust(),
            error: None,
        });
    }
    if let Some(parent) = hook_path.parent() {
        fs_port
            .mkdir_all(parent)
            .map_err(|err| format!("创建 hook 配置目录失败: {err}"))?;
    }
    let backup_path = fs_port.write_atomic(hook_path, &merged.content, true, HOOK_WRITE_LABELS)?;
    Ok(SessionHookOutcome {
        installed: true,
        already_present: false,
        config_path: hook_path.to_string_lossy().to_string(),
        backup_path,
        needs_trust: kind.hook_needs_trust(),
        error: None,
    })
}

/// remove_session_hook 是 `remove_session_hook_with_fs(&LocalFs, ..)` 的旧签名封装。
///
/// **仅测试可见**：理由同 `install_skill_dir`。
#[cfg(test)]
fn remove_session_hook(kind: AgentKind, hook_path: &Path) -> Result<bool, String> {
    remove_session_hook_with_fs(&LocalFs, kind, hook_path)
}

/// remove_session_hook_with_fs 经端口精确摘除带 HOOK_MARKER 的 SuperDev hook 条目。
///
/// 参数：
///   - fs_port: 文件操作端口，本地传 LocalFs、远端传 RemoteAgentFs
///   - kind: Agent 类型，决定读哪个事件数组
///   - hook_path: 该 Agent 存放 hook 的配置文件
///
/// 返回：
///   - 是否确实移除了条目
///
/// 注意：
///   - 文件操作经 ConnectorFs 端口，本地/远端同一逻辑
///   - 只摘除带 HOOK_MARKER 的条目，不动用户的其它 hooks
fn remove_session_hook_with_fs(
    fs_port: &dyn ConnectorFs,
    kind: AgentKind,
    hook_path: &Path,
) -> Result<bool, String> {
    let Some(content) = fs_port
        .read_optional(hook_path)
        .map_err(|err| format!("读取 hook 配置失败: {err}"))?
    else {
        return Ok(false);
    };
    if content.trim().is_empty() {
        return Ok(false);
    }
    let mut root = serde_json::from_str::<serde_json::Value>(&content)
        .map_err(|err| format!("配置文件格式异常(JSON): {err}"))?;
    let (event, _) = session_hook_event(kind);
    let Some(arr) = root
        .as_object_mut()
        .and_then(|obj| obj.get_mut("hooks"))
        .and_then(|hooks| hooks.as_object_mut())
        .and_then(|hooks| hooks.get_mut(event))
        .and_then(|ev| ev.as_array_mut())
    else {
        return Ok(false);
    };
    let before = arr.len();
    arr.retain(|group| {
        let flat = group
            .get("command")
            .and_then(|v| v.as_str())
            .is_some_and(|cmd| cmd.contains(HOOK_MARKER));
        let nested = group
            .get("hooks")
            .and_then(|v| v.as_array())
            .is_some_and(|inner| {
                inner.iter().any(|h| {
                    h.get("command")
                        .and_then(|v| v.as_str())
                        .is_some_and(|cmd| cmd.contains(HOOK_MARKER))
                })
            });
        !(flat || nested)
    });
    if arr.len() == before {
        return Ok(false);
    }
    let out = serde_json::to_string_pretty(&root)
        .map_err(|err| format!("序列化配置失败(JSON): {err}"))?
        + "\n";
    fs_port.write_atomic(hook_path, &out, true, HOOK_WRITE_LABELS)?;
    Ok(true)
}

/// session_hook_status 是 `session_hook_status_with_fs(&LocalFs, ..)` 的旧签名封装。
///
/// **仅测试可见**：理由同 `install_skill_dir`。
#[cfg(test)]
fn session_hook_status(kind: AgentKind, hook_path: &Path) -> bool {
    session_hook_status_with_fs(&LocalFs, kind, hook_path)
}

/// session_hook_status_with_fs 经端口只读判断是否已装 SuperDev hook。
///
/// 参数：
///   - fs_port: 文件操作端口，本地传 LocalFs、远端传 RemoteAgentFs
///   - kind: Agent 类型，决定读哪个事件数组
///   - hook_path: 该 Agent 存放 hook 的配置文件
///
/// 返回：
///   - 已存在带 HOOK_MARKER 的条目时为 true；读不到或解析失败一律为 false
///
/// 注意：
///   - 文件操作经 ConnectorFs 端口，本地/远端同一逻辑
fn session_hook_status_with_fs(
    fs_port: &dyn ConnectorFs,
    kind: AgentKind,
    hook_path: &Path,
) -> bool {
    let Ok(Some(content)) = fs_port.read_optional(hook_path) else {
        return false;
    };
    if content.trim().is_empty() {
        return false;
    }
    let Ok(root) = serde_json::from_str::<serde_json::Value>(&content) else {
        return false;
    };
    let (event, _) = session_hook_event(kind);
    root.get("hooks")
        .and_then(|h| h.get(event))
        .and_then(|ev| ev.as_array())
        .is_some_and(|arr| hook_array_contains_marker(arr))
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
                    "list_hosts",
                    "列出可选择主机；配置 host_ids 时只使用非本机 hosts[].id",
                    "读",
                    "references/safe-operations.md",
                ),
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
                capability_tool(
                    "get_debug_credentials",
                    "取项目/服务的调试凭据明文，用于 AI 合法登录或鉴权",
                    "读",
                    "SKILL.md",
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

/// materialize_skill_source 把本地 skill 源目录读成可经端口批量写入的文件集。
///
/// 参数：
///   - source: bundled skill 源目录（永远在桌面端本机 resources 里）
///
/// 返回：
///   - 按相对路径稳定排序的文件集；错误为「裸原因串」，由调用方补业务前缀
///
/// 注意：
///   - 源目录读取恒为本地操作，不经端口——远端安装时源仍在桌面端，只有目标在远端
///   - executable 只保留「有没有执行位」，与远端 write-batch 端点的语义一致
fn materialize_skill_source(source: &Path) -> Result<Vec<BatchFile>, String> {
    let relatives = collect_relative_files(source).map_err(|err| err.to_string())?;
    let mut files = Vec::with_capacity(relatives.len());
    for rel_path in relatives {
        let absolute = source.join(&rel_path);
        let content =
            fs::read(&absolute).map_err(|err| format!("{}: {err}", rel_path.display()))?;
        files.push(BatchFile {
            rel_path,
            content,
            executable: is_executable_file(&absolute),
        });
    }
    Ok(files)
}

/// is_executable_file 判断本地文件是否带执行位（非 unix 平台恒为 false）。
#[cfg(unix)]
fn is_executable_file(path: &Path) -> bool {
    use std::os::unix::fs::PermissionsExt;

    fs::metadata(path)
        .map(|metadata| metadata.permissions().mode() & 0o111 != 0)
        .unwrap_or(false)
}

#[cfg(not(unix))]
fn is_executable_file(_path: &Path) -> bool {
    false
}

/// skill_dir_matches 判断目标 skill 目录内容是否与源文件集完全一致。
///
/// 参数：
///   - fs_port: 文件操作端口，本地传 LocalFs、远端传 RemoteAgentFs
///   - source_files: 已物化的源文件集
///   - target: 目标 skill 目录
///
/// 返回：
///   - 文件清单与逐文件内容都一致时为 true
///
/// 注意：
///   - 文件操作经 ConnectorFs 端口，本地/远端同一逻辑
///   - 刻意用「逐文件读原文比对」而不是哈希：skill 是低频操作的小目录（十几个
///     文本文件），逐文件读的成本可以忽略；而一旦引入哈希，本地 Rust 与远端 Go
///     就各有一份哈希实现，任何一端的算法/换行/编码处理漂移都会让「已安装且一致」
///     被误判成「需要重装」，反复覆盖用户目录。比对原文没有这个漂移面。
fn skill_dir_matches(
    fs_port: &dyn ConnectorFs,
    source_files: &[BatchFile],
    target: &Path,
) -> Result<bool, String> {
    if !port_path_exists(fs_port, target) {
        return Ok(false);
    }
    let target_files = fs_port
        .list_relative_files(target)
        .map_err(|err| format!("读取目标 skill 目录失败: {err}"))?;
    let source_relatives = source_files
        .iter()
        .map(|file| file.rel_path.clone())
        .collect::<Vec<_>>();
    if source_relatives != target_files {
        return Ok(false);
    }
    for file in source_files {
        let target_content = fs_port
            .read_optional(&target.join(&file.rel_path))
            .map_err(|err| format!("读取目标 skill 文件失败 {}: {err}", file.rel_path.display()))?;
        // 列表里刚出现过却读不到，说明目标目录正在被其它进程改动：按「不一致」
        // 处理即可触发重装，不需要把一次竞态升级成安装失败。
        let Some(target_content) = target_content else {
            return Ok(false);
        };
        if target_content.as_bytes() != file.content.as_slice() {
            return Ok(false);
        }
    }
    Ok(true)
}

/// copy_dir_recursive 递归复制目录。
///
/// 端口化之后生产路径的 skill 安装改走 `ConnectorFs::write_batch`，本函数只剩
/// 既有失败注入测试在用（它需要一个「真的会复制」的 copy 实现）。
#[cfg(test)]
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

fn find_sidecar_binary_in_dir_with_executable_suffix(
    dir: &Path,
    name: &str,
    executable_suffix: &str,
) -> Option<PathBuf> {
    let exact = dir.join(format!("{name}{executable_suffix}"));
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

/// find_sidecar_binary_in_dir 按当前平台的可执行文件扩展名查找 sidecar。
///
/// 参数：
///   - dir: Tauri 安装目录或资源目录
///   - name: 不带平台扩展名的 sidecar 名称
///
/// 返回：存在的精确平台文件或 target-suffixed 文件路径；未找到返回 None。
pub fn find_sidecar_binary_in_dir(dir: &Path, name: &str) -> Option<PathBuf> {
    // Tauri 会把 Windows sidecar 安装成 `<name>.exe`；按当前目标平台补扩展名，
    // 避免把已正确打包的 MCP 误判为缺失。
    find_sidecar_binary_in_dir_with_executable_suffix(dir, name, std::env::consts::EXE_SUFFIX)
}

pub fn find_skill_source_dir_in_dir(dir: &Path) -> Option<PathBuf> {
    let candidate = dir.join("skills").join("superdev");
    if candidate.join("SKILL.md").is_file() {
        return Some(candidate);
    }
    None
}

pub fn resolve_sidecar_binary(app: &AppHandle, name: &str) -> Result<PathBuf, String> {
    tracing::debug!(sidecar = name, "sidecar resolution started");
    let exe = std::env::current_exe().map_err(|err| format!("解析当前进程路径失败: {err}"))?;
    if let Some(dir) = exe.parent() {
        if let Some(path) = find_sidecar_binary_in_dir(dir, name) {
            tracing::info!(
                sidecar = name,
                source = "executable_dir",
                "sidecar resolved"
            );
            return Ok(path);
        }
    }
    if let Ok(resource_dir) = app.path().resource_dir() {
        if let Some(path) = find_sidecar_binary_in_dir(&resource_dir, name) {
            tracing::info!(sidecar = name, source = "resource_dir", "sidecar resolved");
            return Ok(path);
        }
    }
    if cfg!(debug_assertions) {
        let dev_dir = std::env::current_dir()
            .map_err(|err| format!("解析开发目录失败: {err}"))?
            .join("src-tauri")
            .join("binaries");
        if let Some(path) = find_sidecar_binary_in_dir(&dev_dir, name) {
            tracing::info!(
                sidecar = name,
                source = "development_dir",
                "sidecar resolved"
            );
            return Ok(path);
        }
    }
    tracing::error!(sidecar = name, "sidecar resolution failed");
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

/// connector_runtime_context 统一解析 Registry 所需的本机资源。
///
/// 解析失败只保留安全错误说明，调用方不得将路径或配置内容写入日志。
/// 已知环境覆盖只在此边界读取一次，连接器通过上下文 getter 消费。
pub fn connector_runtime_context(
    app: &AppHandle,
) -> Result<registry::ConnectorRuntimeContext, String> {
    let home = resolve_user_home_dir()?;
    let command_dirs = command_search_dirs(&home, std::env::var_os("PATH").as_deref());
    let app_dirs = coding_agent_app_dirs(&home);
    let mcp_binary = resolve_sidecar_binary(app, "superdev-mcp")?;
    let (skill_source, skill_source_error) = match resolve_skill_source_dir(app) {
        Ok(path) => (Some(path), None),
        Err(error) => (None, Some(error)),
    };
    // 只解析已批准的三个路径覆盖，不透传任意环境变量。
    let environment = registry::ConnectorEnvironment::new(
        std::env::var_os("OPENCODE_CONFIG").map(PathBuf::from),
        std::env::var_os("OPENCLAW_CONFIG_PATH").map(PathBuf::from),
        std::env::var_os("KIMI_CODE_HOME").map(PathBuf::from),
    );
    // 只记录是否存在覆盖，不记录路径值，避免把用户目录写进日志。
    tracing::debug!(
        has_opencode_config = environment.opencode_config().is_some(),
        has_openclaw_config_path = environment.openclaw_config_path().is_some(),
        has_kimi_code_home = environment.kimi_code_home().is_some(),
        "connector runtime environment resolved"
    );
    Ok(registry::ConnectorRuntimeContext::new(
        home,
        command_dirs,
        app_dirs,
        mcp_binary,
        skill_source,
        skill_source_error,
    )
    .with_environment(environment))
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
#[cfg(test)]
pub fn mcp_status_for_paths(
    home: &Path,
    path_value: Option<&OsStr>,
    app_dirs: &[PathBuf],
    skill_source: Option<&Path>,
    skill_source_error: Option<String>,
) -> Vec<McpStatus> {
    let command_dirs = command_search_dirs(home, path_value);
    [AgentKind::ClaudeCode, AgentKind::Codex, AgentKind::Cursor]
        .into_iter()
        .map(|kind| {
            mcp_status_for_kind(
                home,
                &command_dirs,
                app_dirs,
                skill_source,
                skill_source_error.clone(),
                kind,
            )
        })
        .collect()
}

/// mcp_status_for_kind 读取单个连接器的状态，供 Registry 适配器使用。
pub fn mcp_status_for_kind(
    home: &Path,
    command_dirs: &[PathBuf],
    app_dirs: &[PathBuf],
    skill_source: Option<&Path>,
    skill_source_error: Option<String>,
    kind: AgentKind,
) -> McpStatus {
    let availability = kind.detect_installation(home, command_dirs, app_dirs);
    let agent = kind.label().to_string();
    let config_path = kind.config_path(home);
    let skill_path = kind.skill_dir(home);
    let hook_path = kind.session_hook_path(home);
    // 本机状态读取：文件操作统一经 LocalFs 端口；远端状态由 Task 9 传入
    // RemoteAgentFs 复用同一套解析逻辑。
    let fs_port = &LocalFs;
    let (config_exists, mcp_configured, mcp_command, agent_url, config_error) =
        read_config_status(fs_port, kind, &config_path);
    let (skill_installed, skill_matches_bundled, skill_error) = skill_status_for_target(
        fs_port,
        skill_source,
        skill_source_error.clone(),
        &skill_path,
    );
    let hook_installed = session_hook_status_with_fs(fs_port, kind, &hook_path);
    McpStatus {
        agent,
        agent_installed: availability.is_some(),
        detection_path: availability.map(|path| path.to_string_lossy().into_owned()),
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
        hook_config_path: hook_path.to_string_lossy().to_string(),
        hook_installed,
        hook_needs_trust: kind.hook_needs_trust(),
    }
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
    // 本机卸载：文件操作统一经 LocalFs 端口；远端卸载（Task 9）传入 RemoteAgentFs
    // 复用下面完全相同的编排。
    let fs_port = &LocalFs;
    let kind = AgentKind::parse(agent)?;
    let config_path = kind.config_path(home);
    let (removed_config, config_backup_path) = match kind {
        AgentKind::ClaudeCode | AgentKind::Cursor => {
            uninstall_from_path_with_fs(fs_port, &config_path, remove_json_superdev_config)?
        }
        AgentKind::Codex => {
            uninstall_from_path_with_fs(fs_port, &config_path, remove_codex_superdev_config)?
        }
    };
    let skill_path = kind.skill_dir(home);
    let removed_skill = remove_skill_dir_with_fs(fs_port, &skill_path)?;
    let hook_path = kind.session_hook_path(home);
    let removed_hook = remove_session_hook_with_fs(fs_port, kind, &hook_path)?;
    Ok(UninstallOutcome {
        agent: kind.label().to_string(),
        config_path: config_path.to_string_lossy().to_string(),
        removed_config,
        config_backup_path,
        skill_path: skill_path.to_string_lossy().to_string(),
        removed_skill,
        hook_config_path: hook_path.to_string_lossy().to_string(),
        removed_hook,
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
pub fn mcp_status(
    app: AppHandle,
    registry: State<'_, registry::ConnectorRegistry>,
) -> Result<Vec<McpStatus>, String> {
    let context = connector_runtime_context(&app)?;
    Ok(compat::statuses(registry.list(&context)))
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
pub fn uninstall_mcp(
    app: AppHandle,
    registry: State<'_, registry::ConnectorRegistry>,
    agent: String,
) -> Result<UninstallOutcome, String> {
    let context = connector_runtime_context(&app)?;
    registry
        .uninstall(&agent, &context)
        .map(compat::uninstall_outcome)
        .map_err(|error| error.to_string())
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
pub fn install_mcp(
    app: AppHandle,
    registry: State<'_, registry::ConnectorRegistry>,
    agent: String,
) -> Result<InstallOutcome, String> {
    let context = connector_runtime_context(&app)?;
    registry
        .install(&agent, &context, None)
        .map(compat::install_outcome)
        .map_err(|error| error.to_string())
}

#[tauri::command]
pub fn mcp_install_hint(
    app: AppHandle,
    registry: State<'_, registry::ConnectorRegistry>,
    agent: String,
) -> Result<InstallHint, String> {
    let context = connector_runtime_context(&app)?;
    registry
        .manual_instructions(&agent, &context)
        .map(|instructions| compat::hint(&agent, instructions))
        .map_err(|error| error.to_string())
}

#[tauri::command]
/// generic_mcp_connection_material 返回未知本机 Agent 可参考的标准 stdio MCP 连接材料。
///
/// 参数：
///   - app: Tauri AppHandle，用于解析随桌面端打包的 superdev-mcp 绝对路径
///
/// 返回：
///   - transport、sidecar 绝对命令、Agent URL 与标准 mcpServers JSON 示例
///
/// 注意：
///   - 该命令只提供传输材料，不猜测未知 Agent 的配置路径、schema 方言、Skill 或 Hook
///   - 返回的 127.0.0.1 地址只适用于本机或同一执行环境，不适用于云端或隔离沙箱
pub fn generic_mcp_connection_material(
    app: AppHandle,
) -> Result<GenericMcpConnectionMaterial, String> {
    let mcp = resolve_sidecar_binary(&app, "superdev-mcp")?;
    Ok(generic_mcp_connection_material_for_path(&mcp))
}

#[tauri::command]
pub fn detect_coding_agents(
    app: AppHandle,
    registry: State<'_, registry::ConnectorRegistry>,
) -> Result<Vec<CodingAgentAvailability>, String> {
    let context = connector_runtime_context(&app)?;
    Ok(compat::availability(registry.list(&context)))
}

/// skill_port_tests 覆盖 skill 安装端口化之后最容易静默失守的等价性属性。
///
/// 端口化之前 skill 目录是 `fs::copy` 逐文件复制（权限位随源文件继承）；改成
/// 「批量写 + 按 executable 落权限」之后，一旦执行位丢失，SessionStart hook 会
/// 静默不再触发——既有测试只比对内容，覆盖不到这一点，因此单独锁一条。
#[cfg(test)]
mod skill_port_tests {
    use super::*;

    #[test]
    fn install_skill_dir_keeps_hook_scripts_executable_and_docs_readable() {
        let dir = create_unique_test_dir("superdev-skill-port-test");
        let source = dir.join("source");
        let target = dir.join("target").join("superdev");
        fs::create_dir_all(source.join("hooks")).expect("mkdir source hooks");
        fs::write(source.join("SKILL.md"), "# SuperDev\n").expect("write skill doc");
        let runner = source.join("hooks").join("run-hook.cmd");
        fs::write(&runner, "#!/bin/sh\n").expect("write hook runner");
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(&runner, fs::Permissions::from_mode(0o755))
                .expect("chmod hook runner");
        }

        let outcome = install_skill_dir(&source, &target).expect("install skill");

        assert!(outcome.installed);
        assert_eq!(
            fs::read_to_string(target.join("hooks").join("run-hook.cmd")).expect("read hook"),
            "#!/bin/sh\n"
        );
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mode =
                |path: PathBuf| fs::metadata(path).expect("metadata").permissions().mode() & 0o777;
            assert_eq!(
                mode(target.join("hooks").join("run-hook.cmd")),
                0o755,
                "hook 脚本必须保持可执行，否则 SessionStart hook 会静默失效"
            );
            assert_eq!(mode(target.join("SKILL.md")), 0o644);
        }

        // 装完立即复检必须判为「已一致」，否则每次状态查询都会触发重装。
        let again = install_skill_dir(&source, &target).expect("install skill again");
        assert!(again.already_present);
    }

    /// bundled skill 资产必须落在端口能无损搬运的范围内。
    ///
    /// 这条是**守卫**，不是等价性证明：`BatchFile` 只带 executable 一个布尔量
    /// （远端 write-batch 端点同样只收这一个量），所以安装产物的权限恒为
    /// 0644/0755。只要 bundled 资产本身也只有这两种权限，产物就与改造前
    /// `fs::copy` 继承源权限的结果逐位相同；一旦有人塞进 0600（会被放宽成
    /// 0644）、0700（会被判成可执行并放宽成 0755）或 0444（会被放宽成 0644）
    /// 的文件，等价性就断了——那时红的必须是这条测试，而不是用户的权限位。
    ///
    /// 同理，等价性检查经 `ConnectorFs::read_optional` 读 UTF-8 文本；一旦
    /// bundled skill 里出现非 UTF-8 资产，首次安装之后每次状态查询与重装都会
    /// 报「读取目标 skill 文件失败」。这里一并守住。
    #[test]
    fn bundled_skill_assets_stay_within_what_the_port_can_carry() {
        let root = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("resources")
            .join("skills")
            .join("superdev");
        let files = collect_relative_files(&root).expect("collect bundled skill files");
        assert!(!files.is_empty(), "bundled skill 目录不应为空");

        for relative in &files {
            let absolute = root.join(relative);
            let bytes = fs::read(&absolute)
                .unwrap_or_else(|err| panic!("read {}: {err}", relative.display()));
            assert!(
                std::str::from_utf8(&bytes).is_ok(),
                "bundled skill 资产必须是 UTF-8，否则安装后每次状态查询都会报读取失败: {}",
                relative.display()
            );

            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;

                let mode = fs::metadata(&absolute)
                    .unwrap_or_else(|err| panic!("stat {}: {err}", relative.display()))
                    .permissions()
                    .mode()
                    & 0o777;
                assert!(
                    mode == 0o644 || mode == 0o755,
                    "bundled skill 资产权限必须是 0644 或 0755（端口只搬运 executable 一个布尔量，\
                     其它权限会在安装时被改写）: {} 当前 {mode:o}",
                    relative.display()
                );
            }
        }
    }
}

#[cfg(test)]
static TEST_DIR_COUNTER: AtomicU64 = AtomicU64::new(0);

/// create_unique_test_dir 为单个测试独占创建一个空的临时目录。
///
/// 参数：
///   - prefix: 目录名前缀，用于区分不同测试模块的产物
///
/// 返回：
///   - 独占创建成功的目录路径
///
/// 注意：
///   - 仅靠 SystemTime 纳秒值无法保证唯一：macOS 上 CLOCK_REALTIME 实际只有
///     1 微秒粒度（纳秒值末三位恒为 0），`cargo test` 并行启动的多个测试线程
///     极易落在同一微秒。因此这里叠加进程 id 与进程内自增计数器。
///   - 必须用 `create_dir` 而不是 `create_dir_all`：后者对已存在目录返回 Ok，
///     会把"目录名撞车"静默降级成"两个测试共用一个目录"，让一个测试看见另一个
///     测试在飞的临时文件。独占创建保证撞车必然暴露为 AlreadyExists 并重试。
#[cfg(test)]
fn create_unique_test_dir(prefix: &str) -> PathBuf {
    for _ in 0..64 {
        let counter = TEST_DIR_COUNTER.fetch_add(1, Ordering::Relaxed);
        let nanos = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .expect("time")
            .as_nanos();
        let dir =
            std::env::temp_dir().join(format!("{prefix}-{}-{nanos}-{counter}", std::process::id()));
        match fs::create_dir(&dir) {
            Ok(()) => return dir,
            Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => continue,
            Err(error) => panic!("mkdir temp {}: {error}", dir.display()),
        }
    }
    panic!("无法为测试分配唯一临时目录(prefix={prefix})");
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;

    fn unique_temp_artifacts(dir: &Path) -> Vec<PathBuf> {
        let mut artifacts = fs::read_dir(dir)
            .expect("read temp parent")
            .filter_map(Result::ok)
            .map(|entry| entry.path())
            .filter(|path| {
                path.file_name()
                    .is_some_and(|name| name.to_string_lossy().contains(".superdev-tmp-"))
            })
            .collect::<Vec<_>>();
        artifacts.sort();
        artifacts
    }

    fn entry() -> McpEntry {
        McpEntry {
            command: "/Applications/SuperDev/superdev-mcp".to_string(),
            agent_url: "http://127.0.0.1:57017".to_string(),
        }
    }

    #[test]
    fn generic_mcp_connection_material_uses_standard_json() {
        let material = generic_mcp_connection_material_for_path(Path::new(
            "/Applications/SuperDev.app/Contents/MacOS/superdev-mcp",
        ));
        let parsed: serde_json::Value =
            serde_json::from_str(&material.manual_config).expect("generic manual json");

        assert_eq!(material.transport, "stdio");
        assert_eq!(
            material.command,
            "/Applications/SuperDev.app/Contents/MacOS/superdev-mcp"
        );
        assert_eq!(material.agent_url, DEFAULT_AGENT_URL);
        assert_eq!(
            parsed["mcpServers"]["superdev"]["command"],
            material.command
        );
        assert_eq!(
            parsed["mcpServers"]["superdev"]["env"]["SUPERDEV_AGENT_URL"],
            DEFAULT_AGENT_URL
        );
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
    fn merge_hook_claude_appends_nested_entry_with_matcher() {
        let merged = merge_session_hook(
            None,
            AgentKind::ClaudeCode,
            "\"/skills/superdev/hooks/run-hook.cmd\" session-start",
        )
        .expect("merge");
        let parsed: serde_json::Value = serde_json::from_str(&merged.content).expect("json");

        assert!(merged.changed);
        let arr = parsed["hooks"]["SessionStart"].as_array().expect("array");
        assert_eq!(arr.len(), 1);
        assert_eq!(arr[0]["matcher"], "startup|clear|compact");
        assert!(arr[0]["hooks"][0]["command"]
            .as_str()
            .expect("cmd")
            .contains(HOOK_MARKER));
        assert_eq!(arr[0]["hooks"][0]["type"], "command");
    }

    #[test]
    fn merge_hook_cursor_appends_flat_entry_lowercase_event() {
        let merged = merge_session_hook(
            None,
            AgentKind::Cursor,
            "\"/skills/superdev/hooks/run-hook.cmd\" session-start",
        )
        .expect("merge");
        let parsed: serde_json::Value = serde_json::from_str(&merged.content).expect("json");

        // Cursor 用小写 sessionStart 且扁平结构（无 matcher 包裹）。
        let arr = parsed["hooks"]["sessionStart"].as_array().expect("array");
        assert_eq!(arr.len(), 1);
        assert!(arr[0]["command"]
            .as_str()
            .expect("cmd")
            .contains(HOOK_MARKER));
        assert!(arr[0].get("matcher").is_none());
    }

    #[test]
    fn merge_hook_keeps_user_other_hooks() {
        // 用户已有自己的 SessionStart hook 和一个 PreToolUse hook，合并不能动它们。
        let existing = r#"{
  "hooks": {
    "SessionStart": [ { "matcher": "startup", "hooks": [ { "type": "command", "command": "echo mine" } ] } ],
    "PreToolUse": [ { "matcher": "Bash", "hooks": [ { "type": "command", "command": "echo guard" } ] } ]
  },
  "theme": "dark"
}"#;
        let merged = merge_session_hook(
            Some(existing),
            AgentKind::ClaudeCode,
            "\"/x/skills/superdev/hooks/run-hook.cmd\" session-start",
        )
        .expect("merge");
        let parsed: serde_json::Value = serde_json::from_str(&merged.content).expect("json");

        assert!(merged.changed);
        assert_eq!(parsed["theme"], "dark");
        // 用户原有的两类 hook 都还在
        assert_eq!(
            parsed["hooks"]["PreToolUse"][0]["hooks"][0]["command"],
            "echo guard"
        );
        let ss = parsed["hooks"]["SessionStart"].as_array().expect("array");
        assert_eq!(ss.len(), 2, "应在保留用户原条目的基础上追加");
        assert_eq!(ss[0]["hooks"][0]["command"], "echo mine");
        assert!(ss[1]["hooks"][0]["command"]
            .as_str()
            .expect("cmd")
            .contains(HOOK_MARKER));
    }

    #[test]
    fn merge_hook_is_idempotent() {
        let cmd = "\"/x/skills/superdev/hooks/run-hook.cmd\" session-start";
        let first = merge_session_hook(None, AgentKind::ClaudeCode, cmd).expect("first");
        let second =
            merge_session_hook(Some(&first.content), AgentKind::ClaudeCode, cmd).expect("second");

        assert!(!second.changed, "已存在 SuperDev hook 时不应再次追加");
        assert_eq!(first.content, second.content);
    }

    #[test]
    fn merge_hook_rejects_bad_json() {
        let err = merge_session_hook(Some("{broken"), AgentKind::ClaudeCode, "x").expect_err("bad");
        assert!(err.contains("配置文件格式异常"));
    }

    #[test]
    fn install_and_remove_session_hook_round_trip() {
        let dir = tempfile_dir();
        let skill_dir = dir.join("skills").join("superdev");
        fs::create_dir_all(skill_dir.join("hooks")).expect("mkdir skill hooks");
        let hook_path = dir.join("settings.json");
        // 预置用户已有的无关 hook，验证安装/卸载都不动它
        fs::write(&hook_path, r#"{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo keep"}]}]}}"#)
            .expect("seed");

        // 安装
        let installed =
            install_session_hook(AgentKind::ClaudeCode, &hook_path, &skill_dir).expect("install");
        assert!(installed.installed);
        assert!(!installed.already_present);
        assert!(!installed.needs_trust);
        assert!(installed.backup_path.is_some(), "已有文件应先备份");
        assert!(session_hook_status(AgentKind::ClaudeCode, &hook_path));

        // 再装一次 -> 幂等
        let again =
            install_session_hook(AgentKind::ClaudeCode, &hook_path, &skill_dir).expect("install2");
        assert!(!again.installed);
        assert!(again.already_present);

        // 卸载 -> 精确摘除 SuperDev 条目，保留用户的 PreToolUse
        let removed = remove_session_hook(AgentKind::ClaudeCode, &hook_path).expect("remove");
        assert!(removed);
        assert!(!session_hook_status(AgentKind::ClaudeCode, &hook_path));
        let after: serde_json::Value =
            serde_json::from_str(&fs::read_to_string(&hook_path).expect("read")).expect("json");
        assert_eq!(
            after["hooks"]["PreToolUse"][0]["hooks"][0]["command"],
            "echo keep"
        );

        // 再卸载 -> 没有可移除项，返回 false
        let removed_again =
            remove_session_hook(AgentKind::ClaudeCode, &hook_path).expect("remove2");
        assert!(!removed_again);
    }

    #[test]
    fn codex_hook_flags_needs_trust() {
        let dir = tempfile_dir();
        let skill_dir = dir.join("skills").join("superdev");
        fs::create_dir_all(skill_dir.join("hooks")).expect("mkdir");
        let hook_path = dir.join("hooks.json");

        let installed =
            install_session_hook(AgentKind::Codex, &hook_path, &skill_dir).expect("install");
        assert!(installed.installed);
        assert!(installed.needs_trust, "Codex 需要用户手动信任 hook");
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

    #[test]
    fn atomic_write_file_concurrent_writers_use_unique_temps_without_residue() {
        let dir = tempfile_dir();
        let target = dir.join("concurrent.json");
        fs::write(&target, "original").expect("seed target");
        let barrier = std::sync::Arc::new(std::sync::Barrier::new(8));
        let expected = (0..8)
            .map(|index| format!("payload-{index}"))
            .collect::<Vec<_>>();

        let handles = expected
            .iter()
            .cloned()
            .map(|payload| {
                let target = target.clone();
                let barrier = barrier.clone();
                std::thread::spawn(move || {
                    barrier.wait();
                    atomic_write_file(&target, payload.as_bytes(), "test_config")
                })
            })
            .collect::<Vec<_>>();
        for handle in handles {
            handle.join().expect("join writer").expect("atomic write");
        }

        let final_content = fs::read_to_string(&target).expect("read final target");
        assert!(expected.contains(&final_content));
        assert_eq!(unique_temp_artifacts(&dir), Vec::<PathBuf>::new());
    }

    #[test]
    fn atomic_write_file_failure_preserves_original_and_cleans_temp() {
        let dir = tempfile_dir();
        let target = dir.join("protected.json");
        fs::write(&target, "original").expect("seed target");

        let error = atomic_write_file_with_replace(
            &target,
            b"replacement",
            "test_config",
            |_temp, _target| {
                Err(std::io::Error::new(
                    std::io::ErrorKind::PermissionDenied,
                    "injected replace failure",
                ))
            },
        )
        .expect_err("replace must fail");

        assert!(error.contains("替换"));
        assert_eq!(
            fs::read_to_string(&target).expect("read target"),
            "original"
        );
        assert_eq!(unique_temp_artifacts(&dir), Vec::<PathBuf>::new());
    }

    #[test]
    fn config_and_hook_mutations_do_not_reuse_fixed_legacy_temp_names() {
        let dir = tempfile_dir();
        let config = dir.join("mcp.json");
        fs::write(&config, r#"{"mcpServers":{"github":{"command":"gh"}}}"#).expect("seed config");
        let legacy_config_temp = config.with_extension("superdev-tmp");
        fs::create_dir_all(&legacy_config_temp).expect("reserve legacy config temp");
        fs::write(legacy_config_temp.join("marker"), "keep").expect("seed marker");

        install_json_kind_to_path(&config, &entry(), "claude-code").expect("install config");
        uninstall_from_path(&config, remove_json_superdev_config).expect("uninstall config");
        assert_eq!(
            fs::read_to_string(legacy_config_temp.join("marker")).expect("read config marker"),
            "keep"
        );

        let skill_dir = dir.join("skills").join("superdev");
        fs::create_dir_all(skill_dir.join("hooks")).expect("mkdir skill hooks");
        let hook_path = dir.join("settings.json");
        fs::write(&hook_path, r#"{"theme":"dark"}"#).expect("seed hook config");
        let legacy_hook_temp = hook_path.with_extension("superdev-hook-tmp");
        fs::create_dir_all(&legacy_hook_temp).expect("reserve legacy hook temp");
        fs::write(legacy_hook_temp.join("marker"), "keep").expect("seed hook marker");

        install_session_hook(AgentKind::ClaudeCode, &hook_path, &skill_dir).expect("install hook");
        remove_session_hook(AgentKind::ClaudeCode, &hook_path).expect("remove hook");
        assert_eq!(
            fs::read_to_string(legacy_hook_temp.join("marker")).expect("read hook marker"),
            "keep"
        );
        assert_eq!(unique_temp_artifacts(&dir), Vec::<PathBuf>::new());
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
    fn skill_replace_failure_restores_original_and_cleans_each_unique_temp() {
        let dir = tempfile_dir();
        let source = dir.join("source");
        let target = dir.join("target").join("superdev");
        seed_skill_source(&source);
        fs::create_dir_all(&target).expect("mkdir original target");
        fs::write(target.join("SKILL.md"), "original").expect("seed original skill");
        let seen_temps = std::sync::Arc::new(std::sync::Mutex::new(Vec::<PathBuf>::new()));

        for _ in 0..2 {
            let seen_temps_for_copy = seen_temps.clone();
            let error = install_skill_dir_with_ops(
                &source,
                &target,
                move |source, temp| {
                    seen_temps_for_copy
                        .lock()
                        .expect("lock seen temps")
                        .push(temp.to_path_buf());
                    copy_dir_recursive(source, temp)
                },
                |_temp, _target| {
                    Err(std::io::Error::new(
                        std::io::ErrorKind::PermissionDenied,
                        "injected skill replace failure",
                    ))
                },
            )
            .expect_err("skill replace must fail");

            assert!(error.contains("替换 skill 目录失败"));
            assert_eq!(
                fs::read_to_string(target.join("SKILL.md")).expect("read restored skill"),
                "original"
            );
        }

        let seen_temps = seen_temps.lock().expect("lock final seen temps");
        assert_eq!(seen_temps.len(), 2);
        assert_ne!(seen_temps[0], seen_temps[1]);
        assert!(seen_temps
            .iter()
            .all(|temp| temp.parent() == target.parent()));
        assert!(seen_temps.iter().all(|temp| !temp.exists()));
        assert_eq!(
            unique_temp_artifacts(target.parent().expect("target parent")),
            Vec::<PathBuf>::new()
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
        assert!(docs.summary_sections.iter().any(|section| {
            section
                .tools
                .iter()
                .any(|tool| tool.name == "get_debug_credentials" && tool.reference == "SKILL.md")
        }));
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
        create_unique_test_dir("superdev-mcp-install-test")
    }

    /// 测试目录工厂在并行下必须给出互不相同的目录。
    ///
    /// 回归背景：旧实现只用 SystemTime 纳秒值命名并用 create_dir_all 创建，
    /// 而 macOS 的 CLOCK_REALTIME 只有微秒粒度，多个测试线程会拿到同一目录，
    /// 导致 atomic_write_file_failure_preserves_original_and_cleans_temp
    /// 扫到别的测试在飞的 .superdev-tmp- 文件而偶发失败。
    #[test]
    fn tempfile_dir_is_unique_across_parallel_tests() {
        const THREADS: usize = 16;
        const PER_THREAD: usize = 8;

        let barrier = std::sync::Arc::new(std::sync::Barrier::new(THREADS));
        let handles = (0..THREADS)
            .map(|_| {
                let barrier = barrier.clone();
                std::thread::spawn(move || {
                    barrier.wait();
                    (0..PER_THREAD).map(|_| tempfile_dir()).collect::<Vec<_>>()
                })
            })
            .collect::<Vec<_>>();

        let dirs = handles
            .into_iter()
            .flat_map(|handle| handle.join().expect("join temp dir allocator"))
            .collect::<Vec<_>>();

        let unique = dirs.iter().collect::<std::collections::HashSet<_>>();
        assert_eq!(
            unique.len(),
            THREADS * PER_THREAD,
            "并行分配的测试目录出现重名，测试之间会互相看到对方的临时文件"
        );
    }
}

#[cfg(test)]
mod path_tests {
    use super::*;
    use std::collections::HashMap;
    use std::ffi::OsString;
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
    fn find_sidecar_binary_accepts_windows_executable_name() {
        let dir = tempfile_dir();
        let executable = dir.join("superdev-mcp.exe");
        fs::write(&executable, b"bin").expect("write Windows executable");

        assert_eq!(
            find_sidecar_binary_in_dir_with_executable_suffix(&dir, "superdev-mcp", ".exe"),
            Some(executable)
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
            "list_hosts",
            "hosts[].id",
            "is_self=false",
            "preview_operation → get_operation_approval",
            "deploy_project_pipeline",
            "get_debug_credentials",
            "AI 调试凭据纪律",
        ] {
            assert!(skill.contains(phrase), "SKILL.md missing phrase: {phrase}");
        }

        let required_refs = [
            ("debugging-workflow.md", "create_debug_session"),
            ("log-tools.md", "get_log_context"),
            ("safe-operations.md", "apply_config_change"),
            ("safe-operations.md", "start_on_boot"),
            ("safe-operations.md", "depends_on"),
            ("safe-operations.md", "readiness"),
            ("safe-operations.md", "service ID"),
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
    fn detect_coding_agents_accepts_existing_codex_config_without_cli() {
        let home = tempfile_dir();
        fs::create_dir_all(home.join(".codex")).expect("mkdir codex config dir");
        fs::write(home.join(".codex").join("config.toml"), b"").expect("write codex config");
        let config_path = home
            .join(".codex")
            .join("config.toml")
            .to_string_lossy()
            .to_string();

        let detection_path = AgentKind::Codex
            .detect_installation(&home, &[], &[])
            .map(|path| path.to_string_lossy().to_string());

        assert_eq!(detection_path.as_deref(), Some(config_path.as_str()));
    }

    #[test]
    fn detect_coding_agents_marks_missing_agents_uninstalled() {
        let home = tempfile_dir().join("missing-home");
        fs::create_dir_all(&home).expect("mkdir missing home");
        let statuses = detect_coding_agents_for_search_dirs(&home, &[], &[]);

        assert!(!agent_status(&statuses, "claude-code").installed);
        assert!(!agent_status(&statuses, "codex").installed);
        assert!(!agent_status(&statuses, "cursor").installed);
    }

    #[test]
    fn user_home_dir_from_env_uses_userprofile_on_windows_when_home_missing() {
        let vars = HashMap::from([("USERPROFILE", OsString::from(r"C:\Users\superdev-user"))]);

        let home =
            user_home_dir_from_env(|key| vars.get(key).cloned(), true).expect("windows home");

        assert_eq!(home, PathBuf::from(r"C:\Users\superdev-user"));
    }

    #[test]
    fn executable_name_matrix_covers_windows_and_unix_connectors() {
        assert_eq!(
            executable_file_names_for_platform("fixture-json-agent", false),
            vec!["fixture-json-agent"]
        );
        assert_eq!(
            executable_file_names_for_platform("fixture-json-agent", true),
            vec![
                "fixture-json-agent",
                "fixture-json-agent.exe",
                "fixture-json-agent.cmd",
                "fixture-json-agent.bat",
            ]
        );
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
        create_unique_test_dir("superdev-sidecar-path-test")
    }
}
