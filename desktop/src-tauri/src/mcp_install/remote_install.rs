// remote_install.rs 提供「只装了 agent 的远端机器」上编程智能体接入的编排。
//
// 职责：
//   - 用全部内置连接器 `cli_commands()` 的去重合集问一次目标机的
//     `/api/integrations/detect`，拿到目标机 HOME、CLI 存在性表与该机 agent 的
//     MCP 启动规格（command/args/url）
//   - 据此构造「远端 ConnectorRuntimeContext」：home 取目标机的、command_dirs/
//     app_dirs 留空（远端不做本地目录扫描）、mcp_launch 取 detect 的 agent 三元组、
//     skill_source 仍是桌面端本机的打包路径
//   - 经 `ConnectorFs` 端口（生产传 `RemoteAgentFs`）复用与本机完全相同的状态读取、
//     安装与卸载编排，产出与本机同构的 `ConnectorOperationOutcome`
//
// 边界：
//   - 不含任何智能体方言：配置怎么合并、skill 装到哪、hook 怎么写全在 mcp_install
//     与各 connector 里，本模块只负责编排与上下文构造
//   - 不直接发文件操作 HTTP：那是 `RemoteAgentFs` 的职责；本模块只为 detect 这一个
//     非文件端点保留一个可替换的 `RemoteIntegrationDetector`
//   - 不走 connector 自己的 detect()：那套判断基于本机目录扫描，远端一律以 detect
//     端点返回的存在性表为准
//   - 不做 UI：返回值形状由前端契约固定，展示逻辑在 Vue 侧

use super::connectors;
use super::contracts::{ConnectorOperation, ConnectorOperationOutcome};
use super::fs_port::ConnectorFs;
use super::registry::{AgentConnector, ConnectorRuntimeContext};
use super::remote_fs::RemoteAgentFs;
use super::{AgentKind, McpLaunchSpec};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::path::PathBuf;
#[cfg(test)]
use std::path::Path;
use std::sync::Arc;
use std::time::Duration;

/// MAX_DETECT_COMMANDS 是 detect 端点单次请求允许的命令数上限（Task 3 契约）。
const MAX_DETECT_COMMANDS: usize = 32;

/// DETECT_TIMEOUT 是 detect 调用的总预算，与 `RemoteAgentFs` 的文件操作预算一致。
const DETECT_TIMEOUT: Duration = Duration::from_secs(15);

/// RemoteAgentLaunch 是 detect 响应里 `agent` 字段：目标机上怎么启动 SuperDev MCP。
#[derive(Clone, Debug, Deserialize, PartialEq, Eq)]
pub struct RemoteAgentLaunch {
    /// command 是目标机上 `superdev-agent` 的绝对路径。
    pub command: String,
    /// args 是启动参数，目标机固定回 `["mcp"]`；缺席按空处理。
    #[serde(default)]
    pub args: Vec<String>,
    /// url 是目标机上 agent 实际监听的 HTTP 地址（写进 SUPERDEV_AGENT_URL）。
    pub url: String,
}

/// RemoteDetectResponse 是 `/api/integrations/detect` 的响应体。
#[derive(Clone, Debug, Deserialize, PartialEq, Eq)]
pub struct RemoteDetectResponse {
    /// home 是目标机上当前用户的 HOME 绝对路径。
    pub home: String,
    /// commands 是「命令名 → 目标机上是否存在」的存在性表。
    pub commands: HashMap<String, bool>,
    /// agent 是目标机 agent 自报的 MCP 启动规格。
    pub agent: RemoteAgentLaunch,
}

/// RemoteAgentStatus 是前端展示用的单个远端连接器状态。
///
/// 注意：`mcp_installed` / `skill_installed` / `hook_installed` 只有在
/// `remote_supported == true` 时才有意义——见该字段的文档。
#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct RemoteAgentStatus {
    /// connector_id 是开放字符串连接器 ID（与本机一致）。
    pub connector_id: String,
    /// display_name 是展示名称。
    pub display_name: String,
    /// cli_present 表示目标机上是否存在该智能体的 CLI（来自 detect 端点，不是本机扫描）。
    pub cli_present: bool,
    /// mcp_installed 表示目标机配置里是否已有 superdev 这项 MCP server。
    pub mcp_installed: bool,
    /// skill_installed 表示目标机上 superdev skill 目录是否已存在。
    pub skill_installed: bool,
    /// hook_installed 表示目标机配置里是否已有 SuperDev 的 SessionStart hook。
    pub hook_installed: bool,
    /// remote_supported 表示该连接器能否在**只有 agent 文件端点**的远端机器上完成接入。
    ///
    /// 为 false 的连接器（当前是 opencode / openclaw / hermes / kimi-code / grok）有两类
    /// 结构性阻碍，都不是本模块能绕开的：
    ///   - openclaw / grok 通过目标机上的**自身 CLI 进程**（`openclaw mcp set`、
    ///     `grok mcp add`）写配置，远端只有受限文件端点、没有远程执行原语
    ///   - opencode / hermes / kimi-code 的配置读写走 `connectors/common.rs` 的
    ///     `mutate_config`，那条路径直调 `std::fs`（含 symlink 安全检查，端口里没有
    ///     对应原语），尚未端口化
    ///
    /// 之所以显式暴露这个布尔量而不是让三个状态位默默为 false：后者会让前端把
    /// 「查不到」渲染成「没装」，正是本任务要消灭的那类静默错误。
    pub remote_supported: bool,
}

/// RemoteIntegrationDetector 抽象「问一次目标机哪些 CLI 存在」这个非文件端点调用。
///
/// 抽出 trait 只为让编排逻辑可以在不起 HTTP 的前提下被测试；生产实现是
/// [`AgentProxyDetector`]，同样经本机 agent 的 integrations 代理转发。
pub trait RemoteIntegrationDetector {
    /// detect 用给定命令名清单询问目标机。
    ///
    /// 参数：
    ///   - commands: 待探测的命令名（调用方已去重、已校验数量与命名规则）
    ///
    /// 返回：
    ///   - 目标机 HOME、命令存在性表与 agent 启动规格
    fn detect(&self, commands: &[String]) -> Result<RemoteDetectResponse, String>;
}

/// AgentProxyDetector 经本机 agent 代理调用目标机的 detect 端点。
pub struct AgentProxyDetector {
    /// local_agent_base 是本机 agent 的 origin（如 `http://127.0.0.1:57017`）。
    local_agent_base: String,
    /// local_token 是本机 agent 的 local access token，只出现在 Authorization 头里，
    /// **绝不能**进日志或错误串。
    local_token: String,
    /// host_id 是目标机器在本机 agent 里的注册 ID。
    host_id: String,
    /// agent 是配置了统一超时的 ureq 客户端。
    agent: ureq::Agent,
}

impl AgentProxyDetector {
    /// new 构造一个指向 host_id 的 detect 客户端。
    ///
    /// 参数：
    ///   - local_agent_base: 本机 agent 的完整 origin（本机回环，明文 HTTP 即可）
    ///   - local_token: 本机 agent 的 local access token（不是目标机凭据）
    ///   - host_id: 目标机器在本机 agent 里的注册 ID
    pub fn new(local_agent_base: String, local_token: String, host_id: String) -> Self {
        Self {
            local_agent_base,
            local_token,
            host_id,
            agent: ureq::AgentBuilder::new().timeout(DETECT_TIMEOUT).build(),
        }
    }
}

impl RemoteIntegrationDetector for AgentProxyDetector {
    fn detect(&self, commands: &[String]) -> Result<RemoteDetectResponse, String> {
        let url = format!(
            "{}/api/agents/{}/integrations/detect",
            self.local_agent_base.trim_end_matches('/'),
            self.host_id
        );
        let body = serde_json::json!({ "commands": commands });
        tracing::debug!(
            host_id = %self.host_id,
            command_count = commands.len(),
            "remote integration detect started"
        );
        let response = self
            .agent
            .post(&url)
            .set("Authorization", &format!("Bearer {}", self.local_token))
            .send_json(body);
        let text = match response {
            Ok(ok) => ok.into_string().map_err(|error| {
                format!("远端机器 {} 的 detect 响应无法读取: {error}", self.host_id)
            })?,
            Err(ureq::Error::Status(status, response)) => {
                // 502 是本机 agent 转发失败（目标机不可达），与其它非 2xx 分开措辞，
                // 让用户知道该去查目标机还是查这条集成本身。
                let detail = response.into_string().unwrap_or_default();
                if status == 502 {
                    return Err(format!(
                        "远端机器 {} 不可达（detect 失败）: {detail}",
                        self.host_id
                    ));
                }
                return Err(format!(
                    "远端机器 {} 的 detect 失败（HTTP {status}）: {detail}",
                    self.host_id
                ));
            }
            Err(error) => {
                return Err(format!(
                    "远端机器 {} 不可达（detect 无法连接本机 agent）: {error}",
                    self.host_id
                ));
            }
        };
        serde_json::from_str(&text)
            .map_err(|error| format!("远端机器 {} 的 detect 响应无法解析: {error}", self.host_id))
    }
}

/// remote_supported_kind 返回该连接器在远端可复用的内置方言。
///
/// 参数：
///   - connector_id: 开放字符串连接器 ID
///
/// 返回：
///   - 能在远端完成接入时返回对应 `AgentKind`，否则 None
///
/// 注意：
///   - 判据是「这家连接器的读写是否全程经 `ConnectorFs` 端口」。当前只有
///     claude-code / codex / cursor 满足；其余五家的结构性阻碍见
///     [`RemoteAgentStatus::remote_supported`] 的文档
fn remote_supported_kind(connector_id: &str) -> Option<AgentKind> {
    AgentKind::parse(connector_id).ok()
}

/// detect_command_union 汇总全部连接器要探测的 CLI 命令名。
///
/// 参数：
///   - connectors: 按注册顺序排列的连接器列表
///
/// 返回：
///   - 保持首次出现顺序的去重命令名列表
///
/// 注意：
///   - 同时校验 Task 3 契约的两条硬约束（≤32 个、命名匹配
///     `^[a-z0-9][a-z0-9-]{0,63}$`）。校验放在本地是为了在发请求前就给出可读错误，
///     而不是让用户收到一个 400 状态码
fn detect_command_union(connectors: &[Arc<dyn AgentConnector>]) -> Result<Vec<String>, String> {
    let mut union: Vec<String> = Vec::new();
    for connector in connectors {
        for command in connector.cli_commands() {
            if !is_valid_detect_command(&command) {
                return Err(format!(
                    "连接器 {} 上报的 CLI 命令名不符合 detect 端点约定: {command}",
                    connector.descriptor().id()
                ));
            }
            if !union.iter().any(|existing| existing == &command) {
                union.push(command);
            }
        }
    }
    if union.len() > MAX_DETECT_COMMANDS {
        return Err(format!(
            "待探测的 CLI 命令共 {} 个，超过 detect 端点上限 {MAX_DETECT_COMMANDS}",
            union.len()
        ));
    }
    Ok(union)
}

/// is_valid_detect_command 校验命令名是否匹配 `^[a-z0-9][a-z0-9-]{0,63}$`。
fn is_valid_detect_command(command: &str) -> bool {
    if command.is_empty() || command.len() > 64 {
        return false;
    }
    let mut chars = command.chars();
    let first = chars.next().expect("non-empty");
    if !(first.is_ascii_lowercase() || first.is_ascii_digit()) {
        return false;
    }
    chars.all(|value| value.is_ascii_lowercase() || value.is_ascii_digit() || value == '-')
}

/// build_remote_context 用 detect 结果构造远端连接器运行上下文。
///
/// 参数：
///   - detect: detect 端点响应（提供目标机 HOME 与 agent 启动规格）
///   - skill_source: **桌面端本机**的 bundled skill 源目录
///   - skill_source_error: skill 源不可用时的说明
///
/// 返回：
///   - 指向目标机的运行上下文
///
/// 注意：
///   - `command_dirs` / `app_dirs` 刻意留空：这两个字段驱动的是本机目录扫描
///     （`path.is_file()`），在远端场景下扫的会是桌面机自己的磁盘，得出的结论是错的。
///     远端的 CLI 存在性一律以 detect 端点的存在性表为准
///   - `skill_source` 仍是本机路径：skill 内容随桌面端打包，安装时先在本地物化成
///     文件集，再经端口（`write_batch`）写到目标机，源目录从不需要存在于目标机
pub fn build_remote_context(
    detect: &RemoteDetectResponse,
    skill_source: Option<PathBuf>,
    skill_source_error: Option<String>,
) -> ConnectorRuntimeContext {
    let launch = McpLaunchSpec {
        command: PathBuf::from(&detect.agent.command),
        args: detect.agent.args.clone(),
        agent_url: detect.agent.url.clone(),
    };
    ConnectorRuntimeContext::new(
        PathBuf::from(&detect.home),
        Vec::new(),
        Vec::new(),
        launch.command.clone(),
        skill_source,
        skill_source_error,
    )
    .with_mcp_launch(launch)
}

/// remote_status_for 读取单个连接器在目标机上的接入状态。
///
/// 参数：
///   - connector: 连接器（只取 descriptor 的 id/display_name，不调它的 detect）
///   - ctx: 远端运行上下文
///   - fs_port: 目标机文件操作端口
///   - cli_present: detect 端点回报的 CLI 存在性
///
/// 返回：
///   - 该连接器的远端状态
///
/// 注意：
///   - `cli_present == false` 或该连接器不支持远端接入时**一次文件操作都不发**：
///     远端每次 stat/read 都是一趟经隧道的往返，为一台根本没装该智能体的机器
///     发三次请求既慢又会在目标机日志里留下无意义的白名单命中记录
pub fn remote_status_for(
    connector: &dyn AgentConnector,
    ctx: &ConnectorRuntimeContext,
    fs_port: &dyn ConnectorFs,
    cli_present: bool,
) -> RemoteAgentStatus {
    let descriptor = connector.descriptor();
    let kind = remote_supported_kind(descriptor.id());
    let base = RemoteAgentStatus {
        connector_id: descriptor.id().to_string(),
        display_name: descriptor.display_name().to_string(),
        cli_present,
        mcp_installed: false,
        skill_installed: false,
        hook_installed: false,
        remote_supported: kind.is_some(),
    };
    let Some(kind) = kind else {
        return base;
    };
    if !cli_present {
        return base;
    }
    let home = ctx.home_dir();
    let (_, mcp_installed, _, _, config_error) =
        super::read_config_status(fs_port, kind, &kind.config_path(home));
    if let Some(error) = config_error.as_ref() {
        // 配置读不出来（格式坏了 / 远端拒绝）时不把它伪装成"未安装"——记一条 warn，
        // 让排障能在桌面端日志里找到线索；返回值仍是 false，但用户看到的是
        // 「远端读取失败」而不是「配置没了」这类误导，因为 install 会立刻复报同一错误。
        tracing::warn!(
            connector_id = descriptor.id(),
            error = %error,
            "remote connector config status unavailable"
        );
    }
    let (skill_installed, _, _) = super::skill_status_for_target(
        fs_port,
        ctx.skill_source(),
        ctx.skill_source_error().map(str::to_string),
        &kind.skill_dir(home),
    );
    let hook_installed =
        super::session_hook_status_with_fs(fs_port, kind, &kind.session_hook_path(home));
    RemoteAgentStatus {
        mcp_installed,
        skill_installed,
        hook_installed,
        ..base
    }
}

/// detect_remote_agents 完成一次「探测 + 状态读取」的完整编排。
///
/// 参数：
///   - detector: detect 端点客户端
///   - fs_port: 目标机文件操作端口
///   - connectors: 按注册顺序排列的连接器列表
///   - skill_source / skill_source_error: 桌面端 bundled skill 源
///
/// 返回：
///   - 与 `connectors` 同序的远端状态列表
///
/// 注意：
///   - detect 只调用**一次**，带全部连接器命令名的去重合集；不是每家各问一次
pub fn detect_remote_agents(
    detector: &dyn RemoteIntegrationDetector,
    fs_port: &dyn ConnectorFs,
    connectors: &[Arc<dyn AgentConnector>],
    skill_source: Option<PathBuf>,
    skill_source_error: Option<String>,
) -> Result<Vec<RemoteAgentStatus>, String> {
    let commands = detect_command_union(connectors)?;
    let detected = detector.detect(&commands)?;
    let ctx = build_remote_context(&detected, skill_source, skill_source_error);
    Ok(connectors
        .iter()
        .map(|connector| {
            // 一家连接器可能上报多个命令名，任一存在即认为该智能体在目标机上可用。
            let cli_present = connector
                .cli_commands()
                .iter()
                .any(|command| detected.commands.get(command).copied().unwrap_or(false));
            remote_status_for(connector.as_ref(), &ctx, fs_port, cli_present)
        })
        .collect())
}

/// unsupported_remote_error 生成「这家连接器不支持远端接入」的用户可读错误。
///
/// 错误串必须同时点名**哪台机器**与**哪个连接器**——远端操作失败时用户手上通常
/// 同时开着多台机器和多个智能体，缺任一维度都要靠猜。
fn unsupported_remote_error(host_id: &str, connector_id: &str) -> String {
    format!(
        "远端机器 {host_id} 上的 {connector_id} 暂不支持远程接入：\
         该连接器需要在目标机上运行自身 CLI 或直接读写本地文件，\
         而远端只提供受限文件端点。请在目标机本地完成该智能体的接入。"
    )
}

/// install_remote_connector 在目标机上安装单个连接器的 MCP + skill + hook。
///
/// 参数：
///   - detector: detect 端点客户端（用于取目标机 HOME 与 agent 启动规格）
///   - fs_port: 目标机文件操作端口
///   - connectors: 连接器列表（用于校验 connector_id 合法）
///   - host_id: 目标机器 ID，仅用于错误文案
///   - connector_id: 待安装的连接器 ID
///   - skill_source / skill_source_error: 桌面端 bundled skill 源
///
/// 返回：
///   - 与本机同构的连接器操作结果
///
/// 注意：
///   - 方言与编排完全复用 `install_mcp_for_paths_with_fs`，差别只有 fs_port 与
///     launch 两个入参；结果映射复用 `built_in_install_outcome`
pub fn install_remote_connector(
    detector: &dyn RemoteIntegrationDetector,
    fs_port: &dyn ConnectorFs,
    connectors: &[Arc<dyn AgentConnector>],
    host_id: &str,
    connector_id: &str,
    skill_source: Option<PathBuf>,
    skill_source_error: Option<String>,
) -> Result<ConnectorOperationOutcome, String> {
    let kind = require_remote_kind(connectors, host_id, connector_id)?;
    let commands = detect_command_union(connectors)?;
    let detected = detector.detect(&commands)?;
    let ctx = build_remote_context(&detected, skill_source, skill_source_error);
    let outcome = super::install_mcp_for_paths_with_fs(
        fs_port,
        kind.label(),
        ctx.home_dir(),
        ctx.mcp_launch(),
        ctx.skill_source(),
        ctx.skill_source_error().map(str::to_string),
    )
    .map_err(|error| format!("远端机器 {host_id} 安装 {connector_id} 失败: {error}"))?;
    let manual = remote_manual_instructions(&ctx, kind, host_id, connector_id)?;
    connectors::built_in_install_outcome(connector_id, ConnectorOperation::Install, outcome, manual)
        .map_err(|error| format!("远端机器 {host_id} 安装 {connector_id} 结果聚合失败: {error}"))
}

/// uninstall_remote_connector 在目标机上移除单个连接器的 SuperDev 接入。
///
/// 参数与返回语义同 [`install_remote_connector`]；只删除 SuperDev 自己写入的那部分。
pub fn uninstall_remote_connector(
    detector: &dyn RemoteIntegrationDetector,
    fs_port: &dyn ConnectorFs,
    connectors: &[Arc<dyn AgentConnector>],
    host_id: &str,
    connector_id: &str,
    skill_source: Option<PathBuf>,
    skill_source_error: Option<String>,
) -> Result<ConnectorOperationOutcome, String> {
    let kind = require_remote_kind(connectors, host_id, connector_id)?;
    let commands = detect_command_union(connectors)?;
    let detected = detector.detect(&commands)?;
    let ctx = build_remote_context(&detected, skill_source, skill_source_error);
    let outcome = super::uninstall_mcp_for_paths_with_fs(fs_port, kind.label(), ctx.home_dir())
        .map_err(|error| format!("远端机器 {host_id} 卸载 {connector_id} 失败: {error}"))?;
    Ok(connectors::built_in_uninstall_outcome(
        connector_id,
        outcome,
    ))
}

/// require_remote_kind 校验 connector_id 已注册且支持远端接入。
fn require_remote_kind(
    connectors: &[Arc<dyn AgentConnector>],
    host_id: &str,
    connector_id: &str,
) -> Result<AgentKind, String> {
    if !connectors
        .iter()
        .any(|connector| connector.descriptor().id() == connector_id)
    {
        return Err(format!(
            "远端机器 {host_id} 请求的连接器不存在: {connector_id}"
        ));
    }
    remote_supported_kind(connector_id)
        .ok_or_else(|| unsupported_remote_error(host_id, connector_id))
}

/// remote_manual_instructions 生成指向**目标机**路径与启动规格的手动配置指引。
fn remote_manual_instructions(
    ctx: &ConnectorRuntimeContext,
    kind: AgentKind,
    host_id: &str,
    connector_id: &str,
) -> Result<super::contracts::ConnectorManualInstructions, String> {
    let hint = super::install_hint_for_launch(kind.label(), ctx.home_dir(), ctx.mcp_launch())
        .map_err(|error| format!("远端机器 {host_id} 生成 {connector_id} 手动指引失败: {error}"))?;
    Ok(super::contracts::ConnectorManualInstructions {
        summary: format!("在远端机器 {host_id} 上配置 SuperDev MCP 与 skill"),
        steps: vec!["写入 MCP 配置".into(), "安装 superdev skill".into()],
        config_path: Some(hint.config_path),
        manual_config: Some(hint.manual_config),
        verification_prompt: Some("验证 SuperDev MCP 可用".into()),
    })
}

/// remote_connectors 返回参与远端编排的连接器列表（与本机注册表同一份工厂）。
///
/// 用生产工厂而不是另起一份清单：连接器增减时两边不会漂移。
pub fn remote_connectors() -> Vec<Arc<dyn AgentConnector>> {
    connectors::builtin()
}

/// remote_agent_fs 构造指向目标机的文件操作端口。
///
/// 单独包一层是为了让 main.rs 的 Tauri command 不必直接依赖 `remote_fs` 模块，
/// 远端接入的全部入口收敛在本模块。
pub fn remote_agent_fs(local_agent_base: &str, local_token: &str, host_id: &str) -> RemoteAgentFs {
    RemoteAgentFs::new(
        local_agent_base.to_string(),
        local_token.to_string(),
        host_id.to_string(),
    )
}

/// skill_source_pair 把 skill 源解析结果拆成上下文需要的两个字段。
pub fn skill_source_pair(resolved: Result<PathBuf, String>) -> (Option<PathBuf>, Option<String>) {
    match resolved {
        Ok(path) => (Some(path), None),
        Err(error) => (None, Some(error)),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mcp_install::fs_port::{BatchFile, FsStat, WriteLabels};
    use std::cell::RefCell;
    use std::collections::BTreeMap;

    /// RecordingFs 是一个内存 `ConnectorFs`：记录调用次数、保存写入内容，
    /// 用于断言「不该发文件请求时一次都没发」与「写出的配置内容是什么」。
    struct RecordingFs {
        files: RefCell<BTreeMap<PathBuf, String>>,
        dirs: RefCell<Vec<PathBuf>>,
        calls: RefCell<usize>,
    }

    impl RecordingFs {
        fn new() -> Self {
            Self {
                files: RefCell::new(BTreeMap::new()),
                dirs: RefCell::new(Vec::new()),
                calls: RefCell::new(0),
            }
        }

        fn with_file(self, path: &str, content: &str) -> Self {
            self.files
                .borrow_mut()
                .insert(PathBuf::from(path), content.to_string());
            self
        }

        fn with_dir(self, path: &str) -> Self {
            self.dirs.borrow_mut().push(PathBuf::from(path));
            self
        }

        fn calls(&self) -> usize {
            *self.calls.borrow()
        }

        fn read(&self, path: &str) -> Option<String> {
            self.files.borrow().get(&PathBuf::from(path)).cloned()
        }

        fn hit(&self) {
            *self.calls.borrow_mut() += 1;
        }
    }

    impl ConnectorFs for RecordingFs {
        fn stat(&self, path: &Path) -> Result<FsStat, String> {
            self.hit();
            let is_dir = self.dirs.borrow().iter().any(|dir| dir == path);
            let exists = is_dir || self.files.borrow().contains_key(path);
            Ok(FsStat { exists, is_dir })
        }

        fn read_optional(&self, path: &Path) -> Result<Option<String>, String> {
            self.hit();
            Ok(self.files.borrow().get(path).cloned())
        }

        fn write_atomic(
            &self,
            path: &Path,
            content: &str,
            _backup: bool,
            _labels: WriteLabels<'_>,
        ) -> Result<Option<String>, String> {
            self.hit();
            self.files
                .borrow_mut()
                .insert(path.to_path_buf(), content.to_string());
            Ok(None)
        }

        fn mkdir_all(&self, path: &Path) -> Result<(), String> {
            self.hit();
            self.dirs.borrow_mut().push(path.to_path_buf());
            Ok(())
        }

        fn write_batch(&self, dir: &Path, files: &[BatchFile], _label: &str) -> Result<(), String> {
            self.hit();
            self.dirs.borrow_mut().push(dir.to_path_buf());
            for file in files {
                self.files.borrow_mut().insert(
                    dir.join(&file.rel_path),
                    String::from_utf8_lossy(&file.content).into_owned(),
                );
            }
            Ok(())
        }

        fn rename(&self, from: &Path, to: &Path) -> Result<(), String> {
            self.hit();
            let moved: Vec<(PathBuf, String)> = self
                .files
                .borrow()
                .iter()
                .filter(|(path, _)| path.starts_with(from))
                .map(|(path, content)| {
                    let rel = path.strip_prefix(from).expect("prefix").to_path_buf();
                    (to.join(rel), content.clone())
                })
                .collect();
            self.files
                .borrow_mut()
                .retain(|path, _| !path.starts_with(from));
            self.dirs.borrow_mut().retain(|dir| !dir.starts_with(from));
            self.dirs.borrow_mut().push(to.to_path_buf());
            for (path, content) in moved {
                self.files.borrow_mut().insert(path, content);
            }
            Ok(())
        }

        fn list_relative_files(&self, dir: &Path) -> Result<Vec<PathBuf>, String> {
            self.hit();
            Ok(self
                .files
                .borrow()
                .keys()
                .filter_map(|path| path.strip_prefix(dir).ok().map(Path::to_path_buf))
                .collect())
        }

        fn remove_dir_all(&self, path: &Path) -> Result<(), String> {
            self.hit();
            self.files
                .borrow_mut()
                .retain(|file, _| !file.starts_with(path));
            self.dirs.borrow_mut().retain(|dir| !dir.starts_with(path));
            Ok(())
        }
    }

    /// FakeDetector 记录收到的命令名清单，并回一份固定的 detect 响应。
    struct FakeDetector {
        present: Vec<String>,
        seen: RefCell<Vec<Vec<String>>>,
    }

    impl FakeDetector {
        fn new(present: &[&str]) -> Self {
            Self {
                present: present.iter().map(|value| value.to_string()).collect(),
                seen: RefCell::new(Vec::new()),
            }
        }

        fn last_request(&self) -> Vec<String> {
            self.seen.borrow().last().cloned().unwrap_or_default()
        }

        fn call_count(&self) -> usize {
            self.seen.borrow().len()
        }
    }

    impl RemoteIntegrationDetector for FakeDetector {
        fn detect(&self, commands: &[String]) -> Result<RemoteDetectResponse, String> {
            self.seen.borrow_mut().push(commands.to_vec());
            Ok(RemoteDetectResponse {
                home: "/home/remote".to_string(),
                commands: commands
                    .iter()
                    .map(|command| (command.clone(), self.present.contains(command)))
                    .collect(),
                agent: RemoteAgentLaunch {
                    command: "/opt/superdev/superdev-agent".to_string(),
                    args: vec!["mcp".to_string()],
                    url: "http://10.1.2.3:57117".to_string(),
                },
            })
        }
    }

    fn detect_fixture() -> RemoteDetectResponse {
        RemoteDetectResponse {
            home: "/home/remote".to_string(),
            commands: HashMap::new(),
            agent: RemoteAgentLaunch {
                command: "/opt/superdev/superdev-agent".to_string(),
                args: vec!["mcp".to_string()],
                url: "http://10.1.2.3:57117".to_string(),
            },
        }
    }

    /// bundled_skill_dir 在桌面端本机物化一份最小 skill 源（远端安装时它仍是本地路径）。
    fn bundled_skill_dir(label: &str) -> PathBuf {
        let root = std::env::temp_dir().join(format!(
            "remote-install-{}-{}-{}",
            label,
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .map(|value| value.as_nanos())
                .unwrap_or(0)
        ));
        let source = root.join("skills").join("superdev");
        std::fs::create_dir_all(source.join("hooks")).expect("create skill source");
        std::fs::write(source.join("SKILL.md"), "# SuperDev\n").expect("write skill doc");
        std::fs::write(source.join("hooks").join("session-start"), "#!/bin/sh\n")
            .expect("write hook script");
        std::fs::write(source.join("hooks").join("run-hook.cmd"), "#!/bin/sh\n")
            .expect("write hook runner");
        source
    }

    #[test]
    fn remote_context_takes_home_and_launch_from_detect_and_keeps_local_skill_source() {
        let skill_source = PathBuf::from("/Applications/SuperDev.app/skills/superdev");
        let ctx = build_remote_context(&detect_fixture(), Some(skill_source.clone()), None);

        assert_eq!(ctx.home_dir(), Path::new("/home/remote"));
        assert!(
            ctx.command_dirs().is_empty(),
            "远端不做本地目录扫描，command_dirs 必须为空，否则会扫到桌面机自己的磁盘"
        );
        assert!(ctx.app_dirs().is_empty());
        assert_eq!(
            ctx.mcp_launch(),
            &McpLaunchSpec {
                command: PathBuf::from("/opt/superdev/superdev-agent"),
                args: vec!["mcp".to_string()],
                agent_url: "http://10.1.2.3:57117".to_string(),
            }
        );
        assert_eq!(
            ctx.skill_source(),
            Some(skill_source.as_path()),
            "skill 源恒在桌面端本机：内容随桌面端打包，安装时先本地物化再经端口写到目标机"
        );
    }

    #[test]
    fn remote_context_carries_skill_source_error_when_bundle_is_missing() {
        let ctx = build_remote_context(&detect_fixture(), None, Some("找不到 skill 资源".into()));
        assert_eq!(ctx.skill_source(), None);
        assert_eq!(ctx.skill_source_error(), Some("找不到 skill 资源"));
    }

    #[test]
    fn absent_cli_reports_all_false_without_touching_the_remote_filesystem() {
        let fs_port = RecordingFs::new();
        let ctx = build_remote_context(&detect_fixture(), None, None);
        let connector = connectors::builtin()
            .into_iter()
            .find(|connector| connector.descriptor().id() == "claude-code")
            .expect("claude-code connector");

        let status = remote_status_for(connector.as_ref(), &ctx, &fs_port, false);

        assert_eq!(
            fs_port.calls(),
            0,
            "CLI 不在目标机上时不得发任何文件操作——每次远端 stat/read 都是一趟隧道往返"
        );
        assert!(!status.cli_present);
        assert!(!status.mcp_installed && !status.skill_installed && !status.hook_installed);
        assert!(status.remote_supported);
    }

    #[test]
    fn unsupported_connector_reports_no_remote_support_without_touching_the_filesystem() {
        let fs_port = RecordingFs::new();
        let ctx = build_remote_context(&detect_fixture(), None, None);
        for id in ["opencode", "openclaw", "hermes", "kimi-code", "grok"] {
            let connector = connectors::builtin()
                .into_iter()
                .find(|connector| connector.descriptor().id() == id)
                .unwrap_or_else(|| panic!("{id} connector"));

            let status = remote_status_for(connector.as_ref(), &ctx, &fs_port, true);

            assert!(
                !status.remote_supported,
                "{id} 的读写没有全程经 ConnectorFs 端口，不能声称支持远端接入"
            );
            assert!(status.cli_present, "{id} 的 CLI 存在性仍应如实上报");
            assert!(!status.mcp_installed && !status.skill_installed && !status.hook_installed);
        }
        assert_eq!(
            fs_port.calls(),
            0,
            "不支持远端接入的连接器不得对目标机发文件操作"
        );
    }

    #[test]
    fn present_cli_reads_status_from_the_remote_filesystem() {
        let fs_port = RecordingFs::new()
            .with_file(
                "/home/remote/.claude.json",
                r#"{"mcpServers":{"superdev":{"command":"/opt/superdev/superdev-agent","args":["mcp"],"env":{"SUPERDEV_AGENT_URL":"http://10.1.2.3:57117"}}}}"#,
            )
            .with_dir("/home/remote/.claude/skills/superdev")
            .with_file(
                "/home/remote/.claude/settings.json",
                r#"{"hooks":{"SessionStart":[{"hooks":[{"command":"\"/home/remote/.claude/skills/superdev/hooks/run-hook.cmd\" session-start"}]}]}}"#,
            );
        let ctx = build_remote_context(&detect_fixture(), None, None);
        let connector = connectors::builtin()
            .into_iter()
            .find(|connector| connector.descriptor().id() == "claude-code")
            .expect("claude-code connector");

        let status = remote_status_for(connector.as_ref(), &ctx, &fs_port, true);

        assert!(status.cli_present);
        assert!(status.mcp_installed, "配置里已有 superdev 条目");
        assert!(status.skill_installed, "skill 目录已存在于目标机");
        assert!(status.hook_installed, "hook 条目已存在于目标机");
        assert!(fs_port.calls() >= 3, "三项状态各自至少读一次目标机");
    }

    #[test]
    fn detect_is_called_once_with_the_deduplicated_union_of_all_connector_commands() {
        let fs_port = RecordingFs::new();
        let detector = FakeDetector::new(&["claude"]);
        let connectors = connectors::builtin();

        let statuses =
            detect_remote_agents(&detector, &fs_port, &connectors, None, None).expect("detect");

        assert_eq!(detector.call_count(), 1, "detect 只能调用一次");
        let requested = detector.last_request();
        assert_eq!(
            requested.len(),
            requested
                .iter()
                .collect::<std::collections::BTreeSet<_>>()
                .len(),
            "命令名必须去重: {requested:?}"
        );
        assert!(requested.len() <= MAX_DETECT_COMMANDS);
        for connector in &connectors {
            for command in connector.cli_commands() {
                assert!(
                    requested.contains(&command),
                    "{} 的命令 {command} 没进合集: {requested:?}",
                    connector.descriptor().id()
                );
            }
        }
        assert_eq!(statuses.len(), connectors.len(), "每家连接器都要有一行状态");
        let claude = statuses
            .iter()
            .find(|status| status.connector_id == "claude-code")
            .expect("claude-code status");
        assert!(claude.cli_present, "detect 说 claude 在目标机上");
        assert!(statuses
            .iter()
            .filter(|status| status.connector_id != "claude-code")
            .all(|status| !status.cli_present));
    }

    #[test]
    fn detect_command_union_rejects_names_the_endpoint_would_refuse() {
        assert!(is_valid_detect_command("claude"));
        assert!(is_valid_detect_command("kimi"));
        assert!(!is_valid_detect_command(""));
        assert!(!is_valid_detect_command("-claude"));
        assert!(!is_valid_detect_command("Claude"));
        assert!(!is_valid_detect_command("cla ude"));
        assert!(!is_valid_detect_command("../claude"));
    }

    #[test]
    fn remote_install_writes_the_remote_agent_command_args_and_url() {
        let skill_source = bundled_skill_dir("install");
        let fs_port = RecordingFs::new();
        let detector = FakeDetector::new(&["claude"]);
        let connectors = connectors::builtin();

        let outcome = install_remote_connector(
            &detector,
            &fs_port,
            &connectors,
            "host-42",
            "claude-code",
            Some(skill_source.clone()),
            None,
        )
        .expect("remote install");

        assert_eq!(outcome.connector_id, "claude-code");
        let written = fs_port
            .read("/home/remote/.claude.json")
            .expect("远端配置必须被写到目标机 HOME 下");
        let parsed: serde_json::Value = serde_json::from_str(&written).expect("written json");
        let server = &parsed["mcpServers"]["superdev"];
        assert_eq!(server["command"], "/opt/superdev/superdev-agent");
        assert_eq!(
            server["args"],
            serde_json::json!(["mcp"]),
            "缺 args 的话目标机上的 superdev-agent 不会进入 MCP 模式，用户会看到\
             '安装成功'却永远连不上"
        );
        assert_eq!(
            server["env"]["SUPERDEV_AGENT_URL"], "http://10.1.2.3:57117",
            "Agent URL 必须指向目标机，不是桌面机默认端口"
        );
        assert!(
            fs_port
                .read("/home/remote/.claude/skills/superdev/SKILL.md")
                .is_some(),
            "skill 必须落到目标机的 skill 目录"
        );
        let hook = fs_port
            .read("/home/remote/.claude/settings.json")
            .expect("hook 配置必须写到目标机");
        assert!(
            hook.contains("/home/remote/.claude/skills/superdev/hooks/run-hook.cmd"),
            "hook 命令必须指向目标机上的 skill 路径: {hook}"
        );
        let _ = std::fs::remove_dir_all(skill_source.parent().and_then(Path::parent).unwrap());
    }

    #[test]
    fn remote_uninstall_removes_only_superdev_entries() {
        let skill_source = bundled_skill_dir("uninstall");
        let fs_port = RecordingFs::new();
        let detector = FakeDetector::new(&["claude"]);
        let connectors = connectors::builtin();
        install_remote_connector(
            &detector,
            &fs_port,
            &connectors,
            "host-42",
            "claude-code",
            Some(skill_source.clone()),
            None,
        )
        .expect("remote install");

        let outcome = uninstall_remote_connector(
            &detector,
            &fs_port,
            &connectors,
            "host-42",
            "claude-code",
            Some(skill_source.clone()),
            None,
        )
        .expect("remote uninstall");

        assert_eq!(outcome.operation, ConnectorOperation::Uninstall);
        let written = fs_port
            .read("/home/remote/.claude.json")
            .expect("配置文件本身保留");
        let parsed: serde_json::Value = serde_json::from_str(&written).expect("written json");
        assert!(
            parsed["mcpServers"].get("superdev").is_none(),
            "superdev 条目必须被移除: {written}"
        );
        assert!(
            fs_port
                .read("/home/remote/.claude/skills/superdev/SKILL.md")
                .is_none(),
            "目标机上的 skill 目录必须被删除"
        );
        let _ = std::fs::remove_dir_all(skill_source.parent().and_then(Path::parent).unwrap());
    }

    #[test]
    fn remote_install_of_an_unsupported_connector_fails_loudly_with_host_and_connector() {
        let fs_port = RecordingFs::new();
        let detector = FakeDetector::new(&["grok"]);
        let connectors = connectors::builtin();

        let error = install_remote_connector(
            &detector,
            &fs_port,
            &connectors,
            "host-42",
            "grok",
            None,
            None,
        )
        .expect_err("不支持的连接器必须显式失败，而不是静默写出半套配置");

        assert!(error.contains("host-42"), "{error}");
        assert!(error.contains("grok"), "{error}");
        assert_eq!(fs_port.calls(), 0, "拒绝时不得对目标机产生任何副作用");
        assert_eq!(detector.call_count(), 0, "拒绝应先于任何远端调用");
    }

    #[test]
    fn remote_install_of_an_unknown_connector_id_is_rejected() {
        let fs_port = RecordingFs::new();
        let detector = FakeDetector::new(&[]);
        let connectors = connectors::builtin();

        let error = install_remote_connector(
            &detector,
            &fs_port,
            &connectors,
            "host-42",
            "not-a-connector",
            None,
            None,
        )
        .expect_err("未注册的连接器 ID 必须被拒绝");

        assert!(
            error.contains("host-42") && error.contains("not-a-connector"),
            "{error}"
        );
        assert_eq!(fs_port.calls(), 0);
    }
}
