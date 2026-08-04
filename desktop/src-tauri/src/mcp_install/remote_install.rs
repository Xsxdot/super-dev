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
use super::contracts::{
    ConnectorOperation, ConnectorOperationOutcome, IntegrationCapability, IntegrationStateStatus,
};
use super::fs_port::ConnectorFs;
use super::registry::{
    AgentConnector, ConnectorInstallRequest, ConnectorRuntimeContext, ConnectorStatus,
};
use super::remote_fs::RemoteAgentFs;
use super::{AgentKind, McpLaunchSpec};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
#[cfg(test)]
use std::path::Path;
use std::path::PathBuf;
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
    /// mcp_installed 表示目标机的 superdev MCP 条目**是否指向这台机器自己的 agent**。
    ///
    /// 判据是 command / args / SUPERDEV_AGENT_URL 三者与 detect 返回的启动规格完全一致，
    /// 而**不是**「有没有 superdev 这一项」。差别在远端是真实会发生的：目标机可能残留
    /// 一条曾作为本地桌面机装出来的旧条目（`command` 指向 `superdev-mcp`、URL 是
    /// `http://127.0.0.1:57017`）。只判「有没有」会给出绿灯，用户不会去点安装，
    /// 那台机器上的智能体永远连不上。
    pub mcp_installed: bool,
    /// mcp_command 是目标机配置里读到的可执行文件路径（没有该条目时为 None）。
    ///
    /// 与 `agent_url` 一起暴露，是为了让前端能区分「没装」和「装了但指向别处」——
    /// 本机 `McpStatus` 一直有这两个字段，远端面板缺了它们就只剩红绿两态。
    pub mcp_command: Option<String>,
    /// agent_url 是目标机配置里读到的 SUPERDEV_AGENT_URL（没有该条目时为 None）。
    pub agent_url: Option<String>,
    /// skill_installed 表示目标机上 superdev skill 目录是否已存在。
    pub skill_installed: bool,
    /// hook_installed 表示目标机配置里是否已有 SuperDev 的 SessionStart hook。
    pub hook_installed: bool,
    /// remote_supported 表示该连接器能否在**只有 agent 文件端点**的远端机器上完成接入。
    ///
    /// 为 false 的连接器（当前是 openclaw / grok）有一类结构性阻碍，不是本模块
    /// 能绕开的：它们通过目标机上的**自身 CLI 进程**（`openclaw mcp set`、
    /// `grok mcp add`）写配置，而远端只提供受限文件端点、没有远程执行原语。
    /// 在远端机器上执行任意命令是一个独立的安全面（需要单独的威胁建模与审批
    /// 门设计），已明确排除在本计划之外。
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

/// built_in_remote_kind 返回该连接器在远端可复用的**内置方言**。
///
/// 参数：
///   - connector_id: 开放字符串连接器 ID
///
/// 返回：
///   - 走 mcp_install 内置方言机器（`install_mcp_for_paths_with_fs`）就能远端安装
///     时返回对应 `AgentKind`，否则 None
///
/// 注意：
///   - 这里刻意**逐个列出** ID，而不是写 `AgentKind::parse(id).ok()`：后者是拿
///     「有没有内置方言」代理「安装是否全程经端口」，两者今天恰好重合，但将来若
///     新增一个 `AgentKind` 变体而它的安装路径没有全程端口化，代理判据会**静默**
///     把它算进远端支持集合。列举式判据下，新增变体默认落到 `_ => None`，要支持
///     远端必须有人显式加一行——同时会被支持集合的正面断言逼着更新
///   - 第二波连接器（opencode / hermes / kimi-code）没有 `AgentKind` 变体，方言
///     在 `connectors/*.rs` 里；它们走 [`RemoteInstallPlan::Ported`] 那条分支
fn built_in_remote_kind(connector_id: &str) -> Option<AgentKind> {
    match connector_id {
        "claude-code" => Some(AgentKind::ClaudeCode),
        "codex" => Some(AgentKind::Codex),
        "cursor" => Some(AgentKind::Cursor),
        _ => None,
    }
}

/// RemoteInstallPlan 描述一家连接器在远端要走哪条安装路径。
///
/// 两条路径都全程经 `ConnectorFs` 端口，差别只在方言逻辑住在哪：
enum RemoteInstallPlan {
    /// BuiltInKind 走 mcp_install 的内置方言机器（claude-code / codex / cursor）。
    ///
    /// 不复用连接器自己的 `install()`：远端编排要给出指向目标机的手动指引
    /// （`remote_manual_instructions`），与本机版本不同，走连接器会拿到本机文案。
    BuiltInKind(AgentKind),
    /// Ported 走连接器自身实现的 [`PortedConnectorOps`]（opencode / hermes /
    /// kimi-code）：方言在 `connectors/*.rs`，本机与远端是同一份实现，
    /// 只有端口不同。
    Ported,
}

/// remote_plan 判定一家连接器能否远端接入、以及走哪条路径。
///
/// 参数：
///   - connector: 连接器（只读它的 descriptor 与 port_ops，不触发任何 I/O）
///
/// 返回：
///   - 能远端接入时返回具体计划，否则 None
///
/// 注意：
///   - `port_ops()` 返回 Some 的前提是那家连接器真的实现了 `PortedConnectorOps`
///     （即它的 status/install/uninstall 全部接受端口）。这条判据不需要维护
///     一份 ID 清单，因此不可能与代码事实漂移
fn remote_plan(connector: &dyn AgentConnector) -> Option<RemoteInstallPlan> {
    if let Some(kind) = built_in_remote_kind(connector.descriptor().id()) {
        return Some(RemoteInstallPlan::BuiltInKind(kind));
    }
    if connector.port_ops().is_some() {
        return Some(RemoteInstallPlan::Ported);
    }
    None
}

/// integration_status 取出 ConnectorStatus 里某一项能力的状态。
fn integration_status(
    status: &ConnectorStatus,
    capability: IntegrationCapability,
) -> Option<IntegrationStateStatus> {
    status
        .integrations
        .iter()
        .find(|item| item.capability == capability)
        .map(|item| item.status)
}

/// integration_present 判断某一项集成「在目标机上是否已就位」。
///
/// Configured 与 NeedsAction 都算就位：后者的含义是"东西在，但内容与本次要装的
/// 不一致"（skill 版本旧、hook 已写但尚未被 Hermes 信任），前端要显示的是
/// 「已安装、可更新」而不是「没装」。Missing/Error/Unknown 一律为 false——
/// 「读不出来」不能渲染成「装好了」。
fn integration_present(status: Option<IntegrationStateStatus>) -> bool {
    matches!(
        status,
        Some(IntegrationStateStatus::Configured) | Some(IntegrationStateStatus::NeedsAction)
    )
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

/// validate_detect_response 校验 detect 响应里编排真正依赖的三个字段非空。
///
/// 参数：
///   - host_id: 目标机器 ID，仅用于错误文案
///   - detect: detect 端点响应
///
/// 返回：
///   - 校验通过返回 Ok(())
///
/// 注意：
///   - Go 侧不会返回空值（解析不出 HOME/agent 路径时直接 500），但空串能被 serde
///     无声接受，然后一路写成 `"command": ""` 落到目标机配置里——那份配置永远启动
///     不了，且错误发生的位置离根因很远。宁可在这里就明确失败
fn validate_detect_response(host_id: &str, detect: &RemoteDetectResponse) -> Result<(), String> {
    for (field, value) in [
        ("home", detect.home.as_str()),
        ("agent.command", detect.agent.command.as_str()),
        ("agent.url", detect.agent.url.as_str()),
    ] {
        if value.trim().is_empty() {
            return Err(format!(
                "远端机器 {host_id} 的 detect 响应缺少 {field}，无法构造远端安装上下文"
            ));
        }
    }
    Ok(())
}

/// detect_once 完成一次「算合集 → 调 detect → 校验响应」的完整前置。
///
/// 参数：
///   - detector: detect 端点客户端
///   - connectors: 按注册顺序排列的连接器列表
///   - host_id: 目标机器 ID，仅用于错误文案
///
/// 返回：
///   - 已校验非空的 detect 响应
///
/// 注意：
///   - 三个对外入口（detect / install / uninstall）都经这一条路径，保证「一次调用、
///     带去重合集、响应必校验」这三件事不会在某个入口上被漏掉
fn detect_once(
    detector: &dyn RemoteIntegrationDetector,
    connectors: &[Arc<dyn AgentConnector>],
    host_id: &str,
) -> Result<RemoteDetectResponse, String> {
    let commands = detect_command_union(connectors)?;
    let detected = detector.detect(&commands)?;
    validate_detect_response(host_id, &detected)?;
    Ok(detected)
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
    let plan = remote_plan(connector);
    let base = RemoteAgentStatus {
        connector_id: descriptor.id().to_string(),
        display_name: descriptor.display_name().to_string(),
        cli_present,
        mcp_installed: false,
        mcp_command: None,
        agent_url: None,
        skill_installed: false,
        hook_installed: false,
        remote_supported: plan.is_some(),
    };
    let Some(plan) = plan else {
        return base;
    };
    if !cli_present {
        return base;
    }
    let kind = match plan {
        RemoteInstallPlan::BuiltInKind(kind) => kind,
        RemoteInstallPlan::Ported => {
            return ported_remote_status(connector, ctx, fs_port, base);
        }
    };
    let home = ctx.home_dir();
    let config = super::read_config_status(fs_port, kind, &kind.config_path(home));
    if let Some(error) = config.error.as_ref() {
        // 配置读不出来（格式坏了 / 远端拒绝）时不把它伪装成"未安装"——记一条 warn，
        // 让排障能在桌面端日志里找到线索；返回值仍是 false，但用户看到的是
        // 「远端读取失败」而不是「配置没了」这类误导，因为 install 会立刻复报同一错误。
        tracing::warn!(
            connector_id = descriptor.id(),
            error = %error,
            "remote connector config status unavailable"
        );
    }
    // 判「是否指向这台机器自己的 agent」，不是判「有没有 superdev 这一项」——
    // 理由见 RemoteAgentStatus::mcp_installed 的文档。
    let mcp_installed = config.matches_launch(ctx.mcp_launch());
    if config.configured && !mcp_installed {
        // 有条目但不匹配是最需要留痕的一类：面板会显示红灯，用户却可能觉得"明明装过"。
        // 只记结论不记路径与 URL 值，避免把用户环境写进日志。
        tracing::info!(
            connector_id = descriptor.id(),
            "remote connector has a superdev entry that points elsewhere"
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
        mcp_command: config.command,
        agent_url: config.agent_url,
        skill_installed,
        hook_installed,
        ..base
    }
}

/// ported_remote_status 用连接器自身的端口化 status 读出远端状态。
///
/// 参数：
///   - connector: 已确认 `port_ops()` 为 Some 的连接器
///   - ctx: 远端运行上下文（home / mcp_launch 都取自 detect）
///   - fs_port: 目标机文件操作端口
///   - base: 已填好 id/display_name/cli_present/remote_supported 的基线值
///
/// 返回：
///   - 填好三项状态与运行时字段的远端状态；status 整体失败时退回 base 并记 warn
///
/// 注意：
///   - 这里**不**重新实现任何方言判断：`mcp_installed` 直接用连接器自己的
///     `Configured`，而连接器的判据（如 opencode 的 `expected_superdev_json`）
///     比对的是 `ctx.mcp_launch()`，在远端上下文里就是目标机自己的 agent 三元组
///     ——与内置方言那条分支 `matches_launch` 的语义一致
fn ported_remote_status(
    connector: &dyn AgentConnector,
    ctx: &ConnectorRuntimeContext,
    fs_port: &dyn ConnectorFs,
    base: RemoteAgentStatus,
) -> RemoteAgentStatus {
    let ops = match connector.port_ops() {
        Some(ops) => ops,
        None => return base,
    };
    let status = match ops.status_with_fs(ctx, fs_port) {
        Ok(status) => status,
        Err(error) => {
            // 与内置方言分支同一纪律：读不出来时记 warn 留痕，返回值仍是全 false，
            // 但用户随后点安装会立刻收到同一条错误，不会停留在"配置没了"的误解上。
            tracing::warn!(
                connector_id = connector.descriptor().id(),
                error_code = error.code(),
                "remote connector status unavailable"
            );
            return base;
        }
    };
    let mcp_status = integration_status(&status, IntegrationCapability::Mcp);
    let mcp_installed = mcp_status == Some(IntegrationStateStatus::Configured);
    if !mcp_installed && integration_present(mcp_status) {
        // 有条目但不匹配：面板会显示红灯，用户却可能觉得"明明装过"。
        // 只记结论不记路径与 URL 值，避免把用户环境写进日志。
        tracing::info!(
            connector_id = connector.descriptor().id(),
            "remote connector has a superdev entry that points elsewhere"
        );
    }
    let skill_installed =
        integration_present(integration_status(&status, IntegrationCapability::Skill));
    let hook_installed = integration_present(integration_status(
        &status,
        IntegrationCapability::SessionHook,
    ));
    RemoteAgentStatus {
        mcp_installed,
        mcp_command: status.mcp_command,
        agent_url: status.agent_url,
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
    host_id: &str,
    skill_source: Option<PathBuf>,
    skill_source_error: Option<String>,
) -> Result<Vec<RemoteAgentStatus>, String> {
    let detected = detect_once(detector, connectors, host_id)?;
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
         该连接器需要在目标机上运行自身 CLI 才能写配置，\
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
    let (connector, plan) = resolve_remote_plan(connectors, host_id, connector_id)?;
    let detected = detect_once(detector, connectors, host_id)?;
    let ctx = build_remote_context(&detected, skill_source, skill_source_error);
    let kind = match plan {
        RemoteInstallPlan::BuiltInKind(kind) => kind,
        RemoteInstallPlan::Ported => {
            // 方言与编排完全复用连接器自身的实现，差别只有 fs_port 与 ctx 两个入参；
            // 能力集合给全三项，与本机「安装」按钮的语义一致（不支持的能力由连接器
            // 自己降级成 NeedsAction/Skipped，不是编排在这里挑）。
            let ops = connector
                .port_ops()
                .ok_or_else(|| unsupported_remote_error(host_id, connector_id))?;
            let request = ConnectorInstallRequest {
                operation: ConnectorOperation::Install,
                capabilities: vec![
                    IntegrationCapability::Mcp,
                    IntegrationCapability::Skill,
                    IntegrationCapability::SessionHook,
                ],
            };
            return ops
                .install_with_fs(&ctx, request, fs_port)
                .map_err(|error| format!("远端机器 {host_id} 安装 {connector_id} 失败: {error}"));
        }
    };
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
    let (connector, plan) = resolve_remote_plan(connectors, host_id, connector_id)?;
    let detected = detect_once(detector, connectors, host_id)?;
    let ctx = build_remote_context(&detected, skill_source, skill_source_error);
    let kind = match plan {
        RemoteInstallPlan::BuiltInKind(kind) => kind,
        RemoteInstallPlan::Ported => {
            let ops = connector
                .port_ops()
                .ok_or_else(|| unsupported_remote_error(host_id, connector_id))?;
            return ops
                .uninstall_with_fs(&ctx, fs_port)
                .map_err(|error| format!("远端机器 {host_id} 卸载 {connector_id} 失败: {error}"));
        }
    };
    let outcome = super::uninstall_mcp_for_paths_with_fs(fs_port, kind.label(), ctx.home_dir())
        .map_err(|error| format!("远端机器 {host_id} 卸载 {connector_id} 失败: {error}"))?;
    Ok(connectors::built_in_uninstall_outcome(
        connector_id,
        outcome,
    ))
}

/// resolve_remote_plan 校验 connector_id 已注册且支持远端接入，并返回安装计划。
///
/// 参数：
///   - connectors: 已注册的连接器列表
///   - host_id / connector_id: 仅用于错误文案与查找
///
/// 返回：
///   - (连接器本身, 该连接器的远端安装计划)
///
/// 注意：
///   - 校验必须先于任何远端调用：拒绝一家不支持的连接器不该先去 detect 一次目标机
fn resolve_remote_plan<'a>(
    connectors: &'a [Arc<dyn AgentConnector>],
    host_id: &str,
    connector_id: &str,
) -> Result<(&'a dyn AgentConnector, RemoteInstallPlan), String> {
    let connector = connectors
        .iter()
        .find(|connector| connector.descriptor().id() == connector_id)
        .ok_or_else(|| format!("远端机器 {host_id} 请求的连接器不存在: {connector_id}"))?;
    let plan = remote_plan(connector.as_ref())
        .ok_or_else(|| unsupported_remote_error(host_id, connector_id))?;
    Ok((connector.as_ref(), plan))
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
    use crate::mcp_install::fs_port::{BatchFile, FsStat, WriteLabels, WritePolicy};
    use std::cell::RefCell;
    use std::collections::BTreeMap;

    /// RecordingFs 是一个内存 `ConnectorFs`：记录调用次数、保存写入内容，
    /// 用于断言「不该发文件请求时一次都没发」与「写出的配置内容是什么」。
    struct RecordingFs {
        files: RefCell<BTreeMap<PathBuf, String>>,
        dirs: RefCell<Vec<PathBuf>>,
        calls: RefCell<usize>,
        /// touched 记录每一次端口调用碰到的 (操作类别, 【目标机】路径)。
        ///
        /// 用途只有一个：给跨栈一致性测试提供「桌面端**实际**会在目标机上读写
        /// 哪些路径、以哪种操作」这份清单，而不是靠测试作者记得。见
        /// `desktop_connector_paths_fixture_matches_what_the_connectors_actually_touch`。
        ///
        /// 操作类别必须区分出 `delete`：Go 侧对删除走的是**另一条**更窄的白名单
        /// （`integrationDeleteAllowed`），只断言 `integrationPathAllowed` 会漏掉
        /// 删除专属的缺口——本任务就漏过一次（临时目录的前导点）。
        touched: RefCell<Vec<(&'static str, PathBuf)>>,
        /// policies 记录每次原子写收到的 (路径, 策略)。
        ///
        /// **必须记，且 RecordingFs 必须覆写 `write_atomic_with_policy`**：策略
        /// 是从调用方一路传到远端 write 端点的，`LocalFs` 对它恒为 no-op、
        /// trait 默认实现直接把它丢掉，于是「调用方忘了传策略」在本机与内存
        /// fake 上都毫无症状——远端却会因此丢掉服务端符号链接守卫、新建配置
        /// 从 0600 退回 0644。这份记录是那条传递链唯一的观测点。
        policies: RefCell<Vec<(PathBuf, WritePolicy)>>,
    }

    impl RecordingFs {
        fn new() -> Self {
            Self {
                files: RefCell::new(BTreeMap::new()),
                dirs: RefCell::new(Vec::new()),
                calls: RefCell::new(0),
                touched: RefCell::new(Vec::new()),
                policies: RefCell::new(Vec::new()),
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

        /// policy_for 返回某个路径最后一次写入时收到的策略。
        fn policy_for(&self, path: &str) -> Option<WritePolicy> {
            let target = PathBuf::from(path);
            self.policies
                .borrow()
                .iter()
                .rev()
                .find(|(written, _)| written == &target)
                .map(|(_, policy)| *policy)
        }

        fn hit(&self) {
            *self.calls.borrow_mut() += 1;
        }

        /// touch 记录一次端口调用碰到的路径（hit 之外单独一份，因为 hit 只计数）。
        ///
        /// operation 取 `PATH_OP`（走写/读白名单的一切操作）或 `DELETE_OP`
        /// （`remove_dir_all`，走窄删除白名单）。
        fn touch(&self, operation: &'static str, path: &Path) {
            self.touched
                .borrow_mut()
                .push((operation, path.to_path_buf()));
        }

        fn touched_paths(&self) -> Vec<(&'static str, PathBuf)> {
            self.touched.borrow().clone()
        }
    }

    impl ConnectorFs for RecordingFs {
        fn stat(&self, path: &Path) -> Result<FsStat, String> {
            self.hit();
            self.touch(PATH_OP, path);
            let is_dir = self.dirs.borrow().iter().any(|dir| dir == path);
            let exists = is_dir || self.files.borrow().contains_key(path);
            // 内存 fake 里不存在符号链接这种东西，恒为 false。
            Ok(FsStat {
                exists,
                is_dir,
                is_symlink: false,
            })
        }

        fn read_optional(&self, path: &Path) -> Result<Option<String>, String> {
            self.hit();
            self.touch(PATH_OP, path);
            Ok(self.files.borrow().get(path).cloned())
        }

        fn write_atomic(
            &self,
            path: &Path,
            content: &str,
            backup: bool,
            labels: WriteLabels<'_>,
        ) -> Result<Option<String>, String> {
            // 与 RemoteAgentFs 同一结构：无策略版是「默认策略」的特例，落地只有
            // 一份，免得两条路径记录的东西不一致。
            self.write_atomic_with_policy(path, content, backup, labels, WritePolicy::default())
        }

        fn write_atomic_with_policy(
            &self,
            path: &Path,
            content: &str,
            _backup: bool,
            _labels: WriteLabels<'_>,
            policy: WritePolicy,
        ) -> Result<Option<String>, String> {
            self.hit();
            self.touch(PATH_OP, path);
            self.policies
                .borrow_mut()
                .push((path.to_path_buf(), policy));
            self.files
                .borrow_mut()
                .insert(path.to_path_buf(), content.to_string());
            Ok(None)
        }

        fn mkdir_all(&self, path: &Path) -> Result<(), String> {
            self.hit();
            self.touch(PATH_OP, path);
            self.dirs.borrow_mut().push(path.to_path_buf());
            Ok(())
        }

        fn write_batch(&self, dir: &Path, files: &[BatchFile], _label: &str) -> Result<(), String> {
            self.hit();
            self.touch(PATH_OP, dir);
            for file in files {
                self.touch(PATH_OP, &dir.join(&file.rel_path));
            }
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
            self.touch(PATH_OP, from);
            self.touch(PATH_OP, to);
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
            self.touch(PATH_OP, dir);
            Ok(self
                .files
                .borrow()
                .keys()
                .filter_map(|path| path.strip_prefix(dir).ok().map(Path::to_path_buf))
                .collect())
        }

        fn remove_dir_all(&self, path: &Path) -> Result<(), String> {
            self.hit();
            self.touch(DELETE_OP, path);
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
        for id in ["openclaw", "grok"] {
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

    /// claude_code_connector 取生产工厂里的 claude-code，避免测试里另造一个假连接器。
    fn claude_code_connector() -> Arc<dyn AgentConnector> {
        connectors::builtin()
            .into_iter()
            .find(|connector| connector.descriptor().id() == "claude-code")
            .expect("claude-code connector")
    }

    #[test]
    fn a_stale_superdev_entry_pointing_at_another_agent_is_not_reported_as_installed() {
        // 目标机曾作为本地桌面机装过：command 指向 superdev-mcp、URL 是本机默认端口。
        // 而 detect 返回的是 /opt/superdev/superdev-agent + ["mcp"] + 该机实际端口。
        let fs_port = RecordingFs::new().with_file(
            "/home/remote/.claude.json",
            r#"{"mcpServers":{"superdev":{"command":"/Applications/SuperDev.app/Contents/MacOS/superdev-mcp","env":{"SUPERDEV_AGENT_URL":"http://127.0.0.1:57017"}}}}"#,
        );
        let ctx = build_remote_context(&detect_fixture(), None, None);

        let status = remote_status_for(claude_code_connector().as_ref(), &ctx, &fs_port, true);

        assert!(
            !status.mcp_installed,
            "残留的旧 superdev 条目指向别的 agent，报成「已安装」会让用户永远不去点安装，\
             那台机器上的 claude-code 永远连不上"
        );
        assert_eq!(
            status.mcp_command.as_deref(),
            Some("/Applications/SuperDev.app/Contents/MacOS/superdev-mcp"),
            "读到的 command 要如实回给前端，前端才能显示「装了但指向别处」"
        );
        assert_eq!(status.agent_url.as_deref(), Some("http://127.0.0.1:57017"));
    }

    #[test]
    fn a_superdev_entry_missing_the_mcp_subcommand_is_not_reported_as_installed() {
        // command 与 URL 都对，唯独缺 args：目标机上的 superdev-agent 不会进入 MCP
        // 模式。只比 command+URL 的判据会放过这一种。
        let fs_port = RecordingFs::new().with_file(
            "/home/remote/.claude.json",
            r#"{"mcpServers":{"superdev":{"command":"/opt/superdev/superdev-agent","env":{"SUPERDEV_AGENT_URL":"http://10.1.2.3:57117"}}}}"#,
        );
        let ctx = build_remote_context(&detect_fixture(), None, None);

        let status = remote_status_for(claude_code_connector().as_ref(), &ctx, &fs_port, true);

        assert!(
            !status.mcp_installed,
            "缺 args 的条目启动不了 MCP，不能报成已安装"
        );
    }

    #[test]
    fn a_codex_entry_pointing_elsewhere_is_not_reported_as_installed() {
        // TOML 方言走的是另一条解析分支，同一条性质要单独钉一次。
        let fs_port = RecordingFs::new().with_file(
            "/home/remote/.codex/config.toml",
            "[mcp_servers.superdev]\ncommand = \"/usr/local/bin/superdev-mcp\"\n\
             [mcp_servers.superdev.env]\nSUPERDEV_AGENT_URL = \"http://127.0.0.1:57017\"\n",
        );
        let ctx = build_remote_context(&detect_fixture(), None, None);
        let codex = connectors::builtin()
            .into_iter()
            .find(|connector| connector.descriptor().id() == "codex")
            .expect("codex connector");

        let status = remote_status_for(codex.as_ref(), &ctx, &fs_port, true);

        assert!(!status.mcp_installed);
        assert_eq!(
            status.mcp_command.as_deref(),
            Some("/usr/local/bin/superdev-mcp")
        );
    }

    #[test]
    fn remote_support_is_pinned_to_the_six_fully_ported_connectors() {
        // 正面钉死支持集合。两条判据各管一半，都不是可以"顺手"扩大的：
        //   - 内置方言三家走 `built_in_remote_kind` 的列举式 match
        //   - 第二波三家走 `AgentConnector::port_ops()`——返回 Some 的前提是这家
        //     连接器真的实现了 PortedConnectorOps（即它的 status/install/uninstall
        //     全部接受端口），没端口化就根本写不出那个 Some
        let supported: Vec<String> = connectors::builtin()
            .iter()
            .filter(|connector| remote_plan(connector.as_ref()).is_some())
            .map(|connector| connector.descriptor().id().to_string())
            .collect();

        assert_eq!(
            supported,
            vec![
                "claude-code".to_string(),
                "codex".to_string(),
                "cursor".to_string(),
                "opencode".to_string(),
                "hermes".to_string(),
                "kimi-code".to_string(),
            ],
            "远端支持集合只能是「安装/卸载/状态读取全程经 ConnectorFs 端口」的那几家"
        );
        assert!(built_in_remote_kind("not-a-connector").is_none());
    }

    /// openclaw / grok 的负例必须被单独钉住：它们靠在目标机上运行**自身 CLI**
    /// 写配置，而远端只有受限文件端点、没有远程执行原语。把它们顺手加进支持集合
    /// 的后果不是"报个错"，而是在桌面机自己的磁盘上按目标机路径写文件然后报成功。
    #[test]
    fn openclaw_and_grok_stay_out_of_the_remote_support_set() {
        for id in ["openclaw", "grok"] {
            let connector = connectors::builtin()
                .into_iter()
                .find(|connector| connector.descriptor().id() == id)
                .unwrap_or_else(|| panic!("{id} connector"));
            assert!(
                connector.port_ops().is_none(),
                "{id} 不能实现 PortedConnectorOps：它需要在目标机上执行任意命令"
            );
            assert!(
                remote_plan(connector.as_ref()).is_none(),
                "{id} 不能进入远端支持集合"
            );
        }

        // 并且 install 入口必须显式失败、不产生任何副作用（不只是"状态位为 false"）。
        let fs_port = RecordingFs::new();
        let detector = FakeDetector::new(&["openclaw", "grok"]);
        let connectors = connectors::builtin();
        for id in ["openclaw", "grok"] {
            let error = install_remote_connector(
                &detector,
                &fs_port,
                &connectors,
                "host-42",
                id,
                None,
                None,
            )
            .expect_err("不支持的连接器必须显式失败，而不是静默写出半套配置");
            assert!(error.contains("host-42") && error.contains(id), "{error}");
        }
        assert_eq!(fs_port.calls(), 0, "拒绝时不得对目标机产生任何副作用");
        assert_eq!(detector.call_count(), 0, "拒绝应先于任何远端调用");
    }

    #[test]
    fn detect_response_with_blank_required_fields_is_rejected_before_anything_is_written() {
        let blank_home = RemoteDetectResponse {
            home: "  ".to_string(),
            ..detect_fixture()
        };
        let blank_command = RemoteDetectResponse {
            agent: RemoteAgentLaunch {
                command: String::new(),
                ..detect_fixture().agent
            },
            ..detect_fixture()
        };
        let blank_url = RemoteDetectResponse {
            agent: RemoteAgentLaunch {
                url: String::new(),
                ..detect_fixture().agent
            },
            ..detect_fixture()
        };

        for (label, response) in [
            ("home", blank_home),
            ("agent.command", blank_command),
            ("agent.url", blank_url),
        ] {
            let error = validate_detect_response("host-42", &response)
                .expect_err("空字段必须被拒绝，否则会写出一份永远启动不了的配置");
            assert!(
                error.contains("host-42") && error.contains(label),
                "{error}"
            );
        }
        assert!(validate_detect_response("host-42", &detect_fixture()).is_ok());
    }

    /// BlankFieldDetector 回一份 `agent.command` 为空串的响应（Go 侧不会这样回，
    /// 但 serde 会无声接受，所以编排必须自己挡住）。
    struct BlankFieldDetector;

    impl RemoteIntegrationDetector for BlankFieldDetector {
        fn detect(&self, commands: &[String]) -> Result<RemoteDetectResponse, String> {
            Ok(RemoteDetectResponse {
                commands: commands.iter().map(|name| (name.clone(), true)).collect(),
                agent: RemoteAgentLaunch {
                    command: String::new(),
                    ..detect_fixture().agent
                },
                ..detect_fixture()
            })
        }
    }

    #[test]
    fn every_entry_point_rejects_a_blank_detect_response_before_writing_anything() {
        // 单测 validate_detect_response 只证明「校验函数本身对」，证明不了「三个入口
        // 真的调了它」。这条测试驱动全部三个对外入口，把接线本身钉住。
        let fs_port = RecordingFs::new();
        let connectors = connectors::builtin();

        let detect_error = detect_remote_agents(
            &BlankFieldDetector,
            &fs_port,
            &connectors,
            "host-42",
            None,
            None,
        )
        .expect_err("detect 入口必须挡住空字段");
        let install_error = install_remote_connector(
            &BlankFieldDetector,
            &fs_port,
            &connectors,
            "host-42",
            "claude-code",
            None,
            None,
        )
        .expect_err("install 入口必须挡住空字段");
        let uninstall_error = uninstall_remote_connector(
            &BlankFieldDetector,
            &fs_port,
            &connectors,
            "host-42",
            "claude-code",
            None,
            None,
        )
        .expect_err("uninstall 入口必须挡住空字段");

        for error in [&detect_error, &install_error, &uninstall_error] {
            assert!(
                error.contains("host-42") && error.contains("agent.command"),
                "{error}"
            );
        }
        assert_eq!(fs_port.calls(), 0, "校验失败时不得对目标机产生任何副作用");
    }

    #[test]
    fn detect_is_called_once_with_the_deduplicated_union_of_all_connector_commands() {
        let fs_port = RecordingFs::new();
        let detector = FakeDetector::new(&["claude"]);
        let connectors = connectors::builtin();

        let statuses =
            detect_remote_agents(&detector, &fs_port, &connectors, "host-42", None, None)
                .expect("detect");

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

    /// FIXTURE_HOME 是跨栈路径清单里冒充「目标机 HOME」的固定前缀。
    const FIXTURE_HOME: &str = "/superdev-fixture-home";

    /// PATH_OP 标注「走 Go 侧 integrationPathAllowed 那条白名单」的操作。
    const PATH_OP: &str = "path";

    /// DELETE_OP 标注「走 Go 侧 integrationDeleteAllowed 那条**更窄**白名单」的操作。
    ///
    /// 必须与 PATH_OP 分开：删除白名单额外要求 basename 是 superdev / superdev.*
    /// 且落在 `<root>/skills/` 之下，只断言 integrationPathAllowed 照不出删除
    /// 专属的缺口——skill 临时目录的前导点就是这么漏掉的。
    const DELETE_OP: &str = "delete";

    /// DESKTOP_PATHS_FIXTURE 是跨栈路径清单的落盘位置。
    ///
    /// 放在 agent 的 testdata 下，是因为消费方是 Go 测试；Rust 这边只负责生成
    /// 与校验它是否还与代码事实一致。
    const DESKTOP_PATHS_FIXTURE: &str = "../../agent/api/testdata/desktop-connector-paths.txt";

    /// FixtureDetector 回一份 home 指向 FIXTURE_HOME、且**所有 CLI 都存在**的
    /// detect 响应——要让六家都真的走完文件操作，才能收集到全部路径。
    struct FixtureDetector;

    impl RemoteIntegrationDetector for FixtureDetector {
        fn detect(&self, commands: &[String]) -> Result<RemoteDetectResponse, String> {
            Ok(RemoteDetectResponse {
                home: FIXTURE_HOME.to_string(),
                commands: commands.iter().map(|name| (name.clone(), true)).collect(),
                agent: detect_fixture().agent,
            })
        }
    }

    /// FailingRenameFs 把 rename 变成必然失败，其余方法原样委托给内层 RecordingFs。
    ///
    /// 用来驱动 skill 安装的**失败**路径——那是唯一会真的对目标机发「删除临时
    /// 目录」请求的路径：成功路径上临时目录是被 rename 成目标目录的
    /// （`PortTempDirGuard::disarm`，根本不发 delete）。跨栈清单必须覆盖它，
    /// 否则删除白名单对临时目录名的缺口照不出来——本任务就漏过一次。
    struct FailingRenameFs<'a> {
        inner: &'a RecordingFs,
    }

    impl ConnectorFs for FailingRenameFs<'_> {
        fn stat(&self, path: &Path) -> Result<FsStat, String> {
            self.inner.stat(path)
        }

        fn read_optional(&self, path: &Path) -> Result<Option<String>, String> {
            self.inner.read_optional(path)
        }

        fn write_atomic(
            &self,
            path: &Path,
            content: &str,
            backup: bool,
            labels: WriteLabels<'_>,
        ) -> Result<Option<String>, String> {
            self.inner.write_atomic(path, content, backup, labels)
        }

        fn write_atomic_with_policy(
            &self,
            path: &Path,
            content: &str,
            backup: bool,
            labels: WriteLabels<'_>,
            policy: WritePolicy,
        ) -> Result<Option<String>, String> {
            self.inner
                .write_atomic_with_policy(path, content, backup, labels, policy)
        }

        fn mkdir_all(&self, path: &Path) -> Result<(), String> {
            self.inner.mkdir_all(path)
        }

        fn write_batch(&self, dir: &Path, files: &[BatchFile], label: &str) -> Result<(), String> {
            self.inner.write_batch(dir, files, label)
        }

        fn rename(&self, _from: &Path, _to: &Path) -> Result<(), String> {
            Err("注入的 rename 失败（用于驱动临时目录清理路径）".to_string())
        }

        fn list_relative_files(&self, dir: &Path) -> Result<Vec<PathBuf>, String> {
            self.inner.list_relative_files(dir)
        }

        fn remove_dir_all(&self, path: &Path) -> Result<(), String> {
            self.inner.remove_dir_all(path)
        }
    }

    /// normalize_fixture_path 把绝对路径转成 home 相对、正斜杠、且**去掉易变段**
    /// 的形态。
    ///
    /// 临时目录名里带进程号与纳秒时间戳（`unique_temp_candidate`），逐次都不同，
    /// 直接落进清单会让这条测试每跑一次就红一次。归一成固定 token 不削弱判据：
    /// 白名单对它的裁决只取决于「在哪个根下 + basename 前缀」，与那串数字无关。
    fn normalize_fixture_path(path: &Path) -> Option<String> {
        let text = path.to_string_lossy().replace('\\', "/");
        let rel = text.strip_prefix(FIXTURE_HOME)?.trim_start_matches('/');
        if rel.is_empty() {
            return None;
        }
        let normalized = rel
            .split('/')
            .map(|segment| match segment.find(".superdev-tmp-") {
                Some(index) => format!("{}.superdev-tmp-PID-NANOS-N", &segment[..index]),
                None => segment.to_string(),
            })
            .collect::<Vec<_>>()
            .join("/");
        Some(normalized)
    }

    /// collect_target_machine_paths 收集「桌面端会在目标机上碰到的全部路径」，
    /// 每条形如 `<操作类别> <home 相对路径>`。
    ///
    /// 两个来源，都不是手写清单：
    ///   1. 真的跑一遍六家 remote-supported 连接器的 **install + status + uninstall**，
    ///      把端口收到的每个路径记下来——这一条会自动带上手写清单绝对想不起来的
    ///      东西（skill 的唯一临时目录、备份目录、hermes 的 hook 信任文件）
    ///   2. 八家（含 openclaw / grok）各自 `status()` 上报的 target_path 与
    ///      `manual_instructions()` 的 config_path——那两家今天不走远端，但将来
    ///      若接入，路径漂移问题一样存在，先纳入判据
    fn collect_target_machine_paths() -> Vec<String> {
        let skill_source = bundled_skill_dir("cross-stack-paths");
        let fs_port = RecordingFs::new();
        let connectors = connectors::builtin();
        let detector = FixtureDetector;

        for connector in &connectors {
            let id = connector.descriptor().id();
            if remote_plan(connector.as_ref()).is_none() {
                continue;
            }
            install_remote_connector(
                &detector,
                &fs_port,
                &connectors,
                "fixture-host",
                id,
                Some(skill_source.clone()),
                None,
            )
            .unwrap_or_else(|error| panic!("{id} fixture install: {error}"));
            uninstall_remote_connector(
                &detector,
                &fs_port,
                &connectors,
                "fixture-host",
                id,
                Some(skill_source.clone()),
                None,
            )
            .unwrap_or_else(|error| panic!("{id} fixture uninstall: {error}"));
        }
        detect_remote_agents(
            &detector,
            &fs_port,
            &connectors,
            "fixture-host",
            Some(skill_source.clone()),
            None,
        )
        .expect("fixture detect");

        // 再跑一遍安装，但把 rename 注入成必然失败：走 skill 安装的失败路径，
        // 逼出「删除唯一临时目录」这次调用（成功路径上不会有它）。
        let failing_records = RecordingFs::new();
        let failing = FailingRenameFs {
            inner: &failing_records,
        };
        for connector in &connectors {
            let id = connector.descriptor().id();
            if remote_plan(connector.as_ref()).is_none() {
                continue;
            }
            let _ = install_remote_connector(
                &detector,
                &failing,
                &connectors,
                "fixture-host",
                id,
                Some(skill_source.clone()),
                None,
            );
        }

        let mut paths: Vec<String> = fs_port
            .touched_paths()
            .iter()
            .chain(failing_records.touched_paths().iter())
            .filter_map(|(operation, path)| {
                normalize_fixture_path(path).map(|rel| format!("{operation} {rel}"))
            })
            .collect();

        // 第二个来源：连接器自报的路径，覆盖 openclaw / grok 这两家不走远端的。
        let ctx = ConnectorRuntimeContext::new(
            PathBuf::from(FIXTURE_HOME),
            Vec::new(),
            Vec::new(),
            PathBuf::from("/opt/superdev/superdev-agent"),
            Some(skill_source.clone()),
            None,
        );
        for connector in &connectors {
            if let Ok(status) = connector.status(&ctx) {
                for item in &status.integrations {
                    if let Some(target) = item.target_path.as_ref() {
                        if let Some(rel) = normalize_fixture_path(Path::new(target)) {
                            paths.push(format!("{PATH_OP} {rel}"));
                        }
                    }
                }
            }
            if let Ok(manual) = connector.manual_instructions(&ctx) {
                if let Some(config) = manual.config_path.as_ref() {
                    if let Some(rel) = normalize_fixture_path(Path::new(config)) {
                        paths.push(format!("{PATH_OP} {rel}"));
                    }
                }
            }
        }

        let _ = std::fs::remove_dir_all(skill_source.parent().and_then(Path::parent).unwrap());
        paths.sort();
        paths.dedup();
        paths
    }

    /// 跨栈一致性：桌面端**实际**会在目标机上碰到的路径清单必须与落盘 fixture 一致。
    ///
    /// 这条测试是「白名单数据同步义务」的执行机制的**上半段**：下半段是 Go 侧的
    /// `TestIntegrationPathAllowedCoversEveryDesktopConnectorPath`，它读同一份
    /// fixture 并断言每一条都能过 `integrationPathAllowed`。
    ///
    /// 为什么要落盘一个 fixture 而不是让 Go 直接调 Rust：两栈没有共同的运行时。
    /// 落盘 + 双向校验的效果是——connector 改了默认路径 → 本条测试先红（fixture
    /// 过期）→ 开发者更新 fixture → Go 侧那条紧接着红（新路径不在白名单里）。
    /// 单靠 `integrationConfigRoots` 头注释那句「与桌面端一一对应」已经漏过一次
    /// （`~/.claude.json`），注释不是机制。
    #[test]
    fn desktop_connector_paths_fixture_matches_what_the_connectors_actually_touch() {
        let actual = collect_target_machine_paths();
        let fixture = Path::new(env!("CARGO_MANIFEST_DIR")).join(DESKTOP_PATHS_FIXTURE);
        let recorded = std::fs::read_to_string(&fixture).unwrap_or_default();
        let expected: Vec<String> = recorded
            .lines()
            .map(str::trim)
            .filter(|line| !line.is_empty() && !line.starts_with('#'))
            .map(str::to_string)
            .collect();

        assert_eq!(
            actual,
            expected,
            "桌面端在目标机上会碰到的路径变了。请把下面这份清单更新进 {}\n{}",
            fixture.display(),
            actual.join("\n")
        );
    }

    /// ported_connector 从生产工厂里取一家第二波连接器。
    fn ported_connector(id: &str) -> Arc<dyn AgentConnector> {
        connectors::builtin()
            .into_iter()
            .find(|connector| connector.descriptor().id() == id)
            .unwrap_or_else(|| panic!("{id} connector"))
    }

    #[test]
    fn ported_connector_remote_status_reads_state_from_the_target_machine() {
        let skill_source = bundled_skill_dir("ported-status");
        let fs_port = RecordingFs::new()
            .with_file(
                "/home/remote/.kimi-code/mcp.json",
                r#"{"mcpServers":{"superdev":{"command":"/opt/superdev/superdev-agent","args":["mcp"],"env":{"SUPERDEV_AGENT_URL":"http://10.1.2.3:57117"}}}}"#,
            )
            .with_dir("/home/remote/.kimi-code/skills/superdev")
            .with_file("/home/remote/.kimi-code/skills/superdev/SKILL.md", "# SuperDev\n");
        let ctx = build_remote_context(&detect_fixture(), Some(skill_source.clone()), None);

        let status =
            remote_status_for(ported_connector("kimi-code").as_ref(), &ctx, &fs_port, true);

        assert!(
            status.remote_supported,
            "kimi-code 已全程端口化，必须支持远端"
        );
        assert!(
            status.mcp_installed,
            "配置里的 command/args/URL 与目标机 agent 完全一致，应报已安装"
        );
        assert_eq!(
            status.mcp_command.as_deref(),
            Some("/opt/superdev/superdev-agent")
        );
        assert_eq!(status.agent_url.as_deref(), Some("http://10.1.2.3:57117"));
        assert!(status.skill_installed, "目标机上的 skill 目录已存在");
        assert!(
            !status.hook_installed,
            "kimi-code 的 Session Hook 是手动能力，不该报成已安装"
        );
        assert!(fs_port.calls() >= 2, "状态读取必须真的问过目标机");
        let _ = std::fs::remove_dir_all(skill_source.parent().and_then(Path::parent).unwrap());
    }

    #[test]
    fn a_ported_connector_entry_pointing_elsewhere_is_not_reported_as_installed() {
        // 与内置方言那三家同一条性质：目标机上残留一条曾作为本机装出来的旧条目
        // （指向 superdev-mcp + 本机默认端口）时必须报未安装，否则用户不会去点
        // 安装，那台机器上的 kimi 永远连不上。
        let fs_port = RecordingFs::new().with_file(
            "/home/remote/.kimi-code/mcp.json",
            r#"{"mcpServers":{"superdev":{"command":"/Applications/SuperDev.app/Contents/MacOS/superdev-mcp","env":{"SUPERDEV_AGENT_URL":"http://127.0.0.1:57017"}}}}"#,
        );
        let ctx = build_remote_context(&detect_fixture(), None, None);

        let status =
            remote_status_for(ported_connector("kimi-code").as_ref(), &ctx, &fs_port, true);

        assert!(!status.mcp_installed);
        assert_eq!(
            status.mcp_command.as_deref(),
            Some("/Applications/SuperDev.app/Contents/MacOS/superdev-mcp"),
            "读到的 command 要如实回给前端，前端才能显示「装了但指向别处」"
        );
        assert_eq!(status.agent_url.as_deref(), Some("http://127.0.0.1:57017"));
    }

    /// HomedFakeDetector 与 FakeDetector 同义，但 home 指向调用方给定的**真实
    /// 存在的空目录**——用来把「有没有绕过端口直连 std::fs」变成一条可观测的
    /// 断言：任何一步走了本机文件系统，文件就会真的落在这个目录里。
    struct HomedFakeDetector {
        present: Vec<String>,
        home: String,
    }

    impl HomedFakeDetector {
        fn new(present: &[&str], home: &Path) -> Self {
            Self {
                present: present.iter().map(|value| value.to_string()).collect(),
                home: home.to_string_lossy().into_owned(),
            }
        }
    }

    impl RemoteIntegrationDetector for HomedFakeDetector {
        fn detect(&self, commands: &[String]) -> Result<RemoteDetectResponse, String> {
            Ok(RemoteDetectResponse {
                home: self.home.clone(),
                commands: commands
                    .iter()
                    .map(|command| (command.clone(), self.present.contains(command)))
                    .collect(),
                agent: detect_fixture().agent,
            })
        }
    }

    /// empty_remote_home 造一个真实存在的空目录，冒充目标机 HOME。
    fn empty_remote_home(label: &str) -> PathBuf {
        let root = std::env::temp_dir().join(format!(
            "remote-home-{}-{}-{}",
            label,
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .map(|value| value.as_nanos())
                .unwrap_or(0)
        ));
        std::fs::create_dir_all(&root).expect("create remote home");
        root
    }

    #[test]
    fn ported_connectors_remote_install_writes_remote_values_only_through_the_port() {
        // 三家第二波连接器（方言在 connectors/*.rs，经 PortedConnectorOps 端口化）
        // 各跑一遍完整远端安装，同时钉住两件事：
        //   1. 写出的 command / args / SUPERDEV_AGENT_URL 全是**目标机**的值
        //   2. 一次都没有绕过端口——目标机 HOME 在桌面机磁盘上是个真实存在的空
        //      目录，任何一次 std::fs 写入都会让它不再为空
        for (connector_id, cli, config_rel, skill_rel) in [
            (
                "opencode",
                "opencode",
                ".config/opencode/opencode.json",
                ".config/opencode/skills/superdev/SKILL.md",
            ),
            (
                "hermes",
                "hermes",
                ".hermes/config.yaml",
                ".hermes/skills/superdev/SKILL.md",
            ),
            (
                "kimi-code",
                "kimi",
                ".kimi-code/mcp.json",
                ".kimi-code/skills/superdev/SKILL.md",
            ),
        ] {
            let skill_source = bundled_skill_dir(connector_id);
            let remote_home = empty_remote_home(connector_id);
            let fs_port = RecordingFs::new();
            let detector = HomedFakeDetector::new(&[cli], &remote_home);
            let connectors = connectors::builtin();

            let outcome = install_remote_connector(
                &detector,
                &fs_port,
                &connectors,
                "host-42",
                connector_id,
                Some(skill_source.clone()),
                None,
            )
            .unwrap_or_else(|error| panic!("{connector_id} 远端安装失败: {error}"));
            assert_eq!(outcome.connector_id, connector_id);

            let config_path = remote_home.join(config_rel);
            let written = fs_port
                .read(&config_path.to_string_lossy())
                .unwrap_or_else(|| panic!("{connector_id} 的配置必须被写到目标机 HOME 下"));
            assert!(
                written.contains("/opt/superdev/superdev-agent"),
                "{connector_id} 的 command 必须是目标机 agent 的绝对路径: {written}"
            );
            assert!(
                written.contains("http://10.1.2.3:57117"),
                "{connector_id} 的 SUPERDEV_AGENT_URL 必须指向目标机，不是桌面机默认端口: {written}"
            );
            assert!(
                mentions_mcp_subcommand(connector_id, &written),
                "{connector_id} 缺 mcp 子命令的话目标机 agent 不会进入 MCP 模式，\
                 用户会看到'安装成功'却永远连不上: {written}"
            );
            assert!(
                fs_port
                    .read(&remote_home.join(skill_rel).to_string_lossy())
                    .is_some(),
                "{connector_id} 的 skill 必须落到目标机的 skill 目录"
            );
            // 策略必须一路传到端口：`connectors/common.rs` 里那一处
            // WritePolicy::CONFIG_FILE 是生产代码里唯一的传递点，丢了它远端就
            // 同时失去服务端符号链接守卫（require_regular_file）与「新建配置
            // 0600」（restrict_new_file_mode），而本机侧毫无症状。
            assert_eq!(
                fs_port.policy_for(&config_path.to_string_lossy()),
                Some(WritePolicy::CONFIG_FILE),
                "{connector_id} 的配置写入必须带上 CONFIG_FILE 策略"
            );

            // 卸载走的是同一条 PortedConnectorOps 分支，同样必须只碰目标机：
            // 只摘掉 superdev 条目、配置文件本身保留、skill 目录被删。
            let removed = uninstall_remote_connector(
                &detector,
                &fs_port,
                &connectors,
                "host-42",
                connector_id,
                Some(skill_source.clone()),
                None,
            )
            .unwrap_or_else(|error| panic!("{connector_id} 远端卸载失败: {error}"));
            assert_eq!(removed.operation, ConnectorOperation::Uninstall);
            let after = fs_port
                .read(&config_path.to_string_lossy())
                .unwrap_or_else(|| panic!("{connector_id} 的配置文件本身必须保留"));
            assert!(
                !after.contains("/opt/superdev/superdev-agent"),
                "{connector_id} 的 superdev 条目必须被移除: {after}"
            );
            assert!(
                fs_port
                    .read(&remote_home.join(skill_rel).to_string_lossy())
                    .is_none(),
                "{connector_id} 目标机上的 skill 目录必须被删除"
            );

            let leaked: Vec<String> = std::fs::read_dir(&remote_home)
                .expect("read remote home")
                .filter_map(Result::ok)
                .map(|entry| entry.file_name().to_string_lossy().into_owned())
                .collect();
            assert!(
                leaked.is_empty(),
                "{connector_id} 远端安装把文件写到了【桌面机】自己的磁盘上: {leaked:?}"
            );

            let _ = std::fs::remove_dir_all(&remote_home);
            let _ = std::fs::remove_dir_all(skill_source.parent().and_then(Path::parent).unwrap());
        }
    }

    /// mentions_mcp_subcommand 按各家 schema 判断 `mcp` 子命令是否真的写进了配置。
    ///
    /// 不能统一用 `written.contains("mcp")`：opencode 的 schema 根键就叫 `mcp`，
    /// kimi-code 的根键叫 `mcpServers`，那样断言恒真、测不出任何东西。
    fn mentions_mcp_subcommand(connector_id: &str, written: &str) -> bool {
        match connector_id {
            "opencode" => {
                let value: serde_json::Value =
                    serde_json::from_str(written).expect("opencode json");
                value["mcp"]["superdev"]["command"]
                    == serde_json::json!(["/opt/superdev/superdev-agent", "mcp"])
            }
            "kimi-code" => {
                let value: serde_json::Value =
                    serde_json::from_str(written).expect("kimi-code json");
                value["mcpServers"]["superdev"]["args"] == serde_json::json!(["mcp"])
            }
            // Hermes 是无损 YAML，没有现成的解析依赖；它的 args 写法固定为
            // `args:` 后跟一个 `- mcp` 列表项（见 superdev_server_fields）。
            "hermes" => written.contains("args:") && written.contains("- mcp"),
            other => panic!("未覆盖的连接器: {other}"),
        }
    }

    #[test]
    fn built_in_remote_install_restricts_new_config_file_mode() {
        // 内置方言三家（claude-code / codex / cursor）在本机新建配置是 0600
        // （atomic_write_file），而远端 agent 的 write 端点默认是 0644——这条
        // 分叉只能靠 restrict_new_file_mode 送到服务端消除。MCP 配置与 hook
        // 配置两条写入路径都要带上，漏一条就有一半的配置仍是 0644。
        //
        // 刻意断言 RESTRICTED_NEW_FILE 而不是 CONFIG_FILE：这两条路径在**本机侧
        // 从来没有**符号链接守卫，加上它会让远端比本机更严，把 dotfiles 仓库用
        // 符号链接管理 ~/.claude.json 的用户挡在门外（理由见 WritePolicy 的 doc）。
        let skill_source = bundled_skill_dir("builtin-policy");
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

        for path in [
            "/home/remote/.claude.json",
            "/home/remote/.claude/settings.json",
        ] {
            assert_eq!(
                fs_port.policy_for(path),
                Some(WritePolicy::RESTRICTED_NEW_FILE),
                "{path} 的远端写入必须要求新建落 0600，且不得附带本机没有的符号链接守卫"
            );
        }
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
