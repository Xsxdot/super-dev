// registry.rs 管理 MCP Agent Connector 的稳定注册表与并发边界。
//
// 职责：
//   - 以开放字符串 ID 注册、查找并按注册顺序列出连接器
//   - 隔离单个连接器的检测错误，并串行化同一连接器的变更操作
//   - 根据描述符支持方式与上次结果选择初次执行或重试能力
//
// 边界：
//   - 不实现 Claude、Codex、Cursor 等具体配置方言
//   - 不注册 Tauri command，也不初始化 tracing subscriber
//   - 不读取真实用户配置或执行文件系统变更

use crate::mcp_install::contracts::{
    validate_descriptor, AgentConnectorDescriptor, AgentConnectorState, AgentConnectorSummary,
    ConnectorManualInstructions, ConnectorOperation, ConnectorOperationOutcome, ConnectorResult,
    ContractError, IntegrationCapability, IntegrationOperationResult, IntegrationResult,
    IntegrationState, IntegrationStateStatus, SupportMode,
};
use crate::mcp_install::fs_port::ConnectorFs;
use crate::mcp_install::{McpLaunchSpec, DEFAULT_AGENT_URL};
use std::collections::{HashMap, HashSet};
use std::fmt;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::Instant;

const ISOLATED_STATE_MESSAGE: &str = "连接器状态暂时不可用，请稍后重试";
const MANUAL_ACTION_MESSAGE: &str = "此操作需要按连接器指引手动完成";

/// ConnectorEnvironment 保存已批准的 Agent 配置路径覆盖。
///
/// 边界：不保存任意环境变量或秘密值，Connector 只能读取三个公开路径。
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ConnectorEnvironment {
    opencode_config: Option<PathBuf>,
    openclaw_config_path: Option<PathBuf>,
    kimi_code_home: Option<PathBuf>,
}

impl ConnectorEnvironment {
    /// new 构造仅包含已知路径覆盖的环境值对象。
    ///
    /// 参数：
    ///   - opencode_config: OpenCode 配置文件覆盖路径（对应 OPENCODE_CONFIG）
    ///   - openclaw_config_path: OpenClaw 配置路径覆盖（对应 OPENCLAW_CONFIG_PATH）
    ///   - kimi_code_home: Kimi Code 数据根目录覆盖（对应 KIMI_CODE_HOME）
    ///
    /// 返回：
    ///   - 只读路径覆盖集合；未设置的字段为 None
    pub fn new(
        opencode_config: Option<PathBuf>,
        openclaw_config_path: Option<PathBuf>,
        kimi_code_home: Option<PathBuf>,
    ) -> Self {
        Self {
            opencode_config,
            openclaw_config_path,
            kimi_code_home,
        }
    }

    /// opencode_config 返回 OpenCode 配置文件覆盖路径。
    pub fn opencode_config(&self) -> Option<&Path> {
        self.opencode_config.as_deref()
    }

    /// openclaw_config_path 返回 OpenClaw 配置路径覆盖。
    pub fn openclaw_config_path(&self) -> Option<&Path> {
        self.openclaw_config_path.as_deref()
    }

    /// kimi_code_home 返回 Kimi Code 数据根目录覆盖。
    pub fn kimi_code_home(&self) -> Option<&Path> {
        self.kimi_code_home.as_deref()
    }
}

/// ConnectorRuntimeContext 提供连接器运行所需的已解析本机资源。
///
/// 边界：
///   - 上下文只拥有路径、已知环境覆盖和预解析错误，不主动读取文件系统
///   - 环境变量仅在 Tauri 边界解析一次后注入，连接器不直接读 std::env
///   - getter 仅暴露借用，连接器不能改写 Registry 持有的调用上下文
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ConnectorRuntimeContext {
    home_dir: PathBuf,
    command_dirs: Vec<PathBuf>,
    app_dirs: Vec<PathBuf>,
    mcp_launch: McpLaunchSpec,
    skill_source: Option<PathBuf>,
    skill_source_error: Option<String>,
    environment: ConnectorEnvironment,
}

impl ConnectorRuntimeContext {
    /// new 构造一个完全拥有其输入的连接器运行上下文。
    ///
    /// 参数：
    ///   - home_dir: 当前用户目录
    ///   - command_dirs: 用于检测命令的目录列表
    ///   - app_dirs: 用于检测桌面应用的目录列表
    ///   - mcp_binary: 打包后的 SuperDev MCP 可执行文件路径
    ///   - skill_source: 可选的 SuperDev skill 源目录
    ///   - skill_source_error: skill 源解析失败时的结构化说明
    ///
    /// 返回：
    ///   - 可克隆、只读访问其字段的运行上下文；环境覆盖默认为空
    ///
    /// 注意：
    ///   - 签名刻意保留 `mcp_binary: PathBuf`（而非要求调用方直接构造 `McpLaunchSpec`），
    ///     避免这个已被数十处测试按位置参数调用的构造函数产生连锁改动。内部据此拼出
    ///     本机默认启动规格（空 args + 默认 Agent URL），与改造前行为逐字节一致；
    ///     远端场景（Task 9）改由专门的构造路径注入携带 args 的 `McpLaunchSpec`。
    pub fn new(
        home_dir: PathBuf,
        command_dirs: Vec<PathBuf>,
        app_dirs: Vec<PathBuf>,
        mcp_binary: PathBuf,
        skill_source: Option<PathBuf>,
        skill_source_error: Option<String>,
    ) -> Self {
        Self {
            home_dir,
            command_dirs,
            app_dirs,
            mcp_launch: McpLaunchSpec {
                command: mcp_binary,
                args: Vec::new(),
                agent_url: DEFAULT_AGENT_URL.to_string(),
            },
            skill_source,
            skill_source_error,
            environment: ConnectorEnvironment::default(),
        }
    }

    /// with_mcp_launch 覆盖 SuperDev MCP 的启动规格（command/args/agent_url 三元组）。
    ///
    /// 参数：
    ///   - mcp_launch: 目标机器上的启动规格；远端场景取自 detect 端点返回的 `agent`
    ///     字段（目标机 `superdev-agent` 绝对路径 + `["mcp"]` + 该机 agent URL）
    ///
    /// 返回：
    ///   - 带该启动规格的运行上下文
    ///
    /// 注意：
    ///   - `new()` 只会产出「本机默认」规格（独立 mcp 二进制 + 空 args + 默认 Agent URL），
    ///     远端上下文**必须**经这个消费型 builder 覆盖，否则会写出指向本机端口的配置。
    ///     刻意做成与 `with_environment` 对称的形态，而不是给 `new()` 加参数——后者会
    ///     波及数十处按位置参数调用它的既有测试。
    pub fn with_mcp_launch(mut self, mcp_launch: McpLaunchSpec) -> Self {
        self.mcp_launch = mcp_launch;
        self
    }

    /// with_environment 注入已在 Tauri 边界解析的已知路径覆盖。
    ///
    /// 参数：
    ///   - environment: 仅含三个公开路径覆盖的值对象
    ///
    /// 返回：
    ///   - 带环境覆盖的运行上下文
    pub fn with_environment(mut self, environment: ConnectorEnvironment) -> Self {
        self.environment = environment;
        self
    }

    /// home_dir 返回已解析的用户目录。
    pub fn home_dir(&self) -> &Path {
        &self.home_dir
    }

    /// command_dirs 返回命令检测目录的只读切片。
    pub fn command_dirs(&self) -> &[PathBuf] {
        &self.command_dirs
    }

    /// app_dirs 返回桌面应用检测目录的只读切片。
    pub fn app_dirs(&self) -> &[PathBuf] {
        &self.app_dirs
    }

    /// mcp_launch 返回 SuperDev MCP 启动规格（command/args/agent_url 三元组）。
    ///
    /// 本机连接器目前始终拿到「独立二进制 + 空 args + 默认 Agent URL」；远端连接器
    /// （Task 9）会传入指向 `superdev-agent` 的 `mcp` 子命令与目标机 Agent URL。
    pub fn mcp_launch(&self) -> &McpLaunchSpec {
        &self.mcp_launch
    }

    /// mcp_binary 返回 SuperDev MCP 可执行文件路径。
    ///
    /// 兼容包装：等价于 `mcp_launch().command`，供尚未迁移到 `mcp_launch()` 的调用方
    /// 过渡使用，计划逐步下线。
    pub fn mcp_binary(&self) -> &Path {
        &self.mcp_launch.command
    }

    /// skill_source 返回可用的 SuperDev skill 源目录。
    pub fn skill_source(&self) -> Option<&Path> {
        self.skill_source.as_deref()
    }

    /// skill_source_error 返回 skill 源解析失败时的说明。
    pub fn skill_source_error(&self) -> Option<&str> {
        self.skill_source_error.as_deref()
    }

    /// environment 返回已知 Agent 配置路径覆盖。
    pub fn environment(&self) -> &ConnectorEnvironment {
        &self.environment
    }
}

/// ConnectorDetection 描述 Agent 安装检测结果。
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ConnectorDetection {
    /// detected 表示当前上下文中是否检测到 Agent。
    pub detected: bool,
    /// detection_path 是可选的检测命中位置，仅用于返回调用方。
    pub detection_path: Option<PathBuf>,
    /// message 是连接器生成的可展示说明。
    pub message: Option<String>,
}

/// ConnectorStatus 描述连接器三项集成的动态状态。
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ConnectorStatus {
    /// integrations 是 MCP、Skill 与 Session Hook 的状态明细。
    pub integrations: Vec<IntegrationState>,
    /// requires_restart 表示配置是否需要重启 Agent 才能生效。
    ///
    /// 注意：状态读取不应因「已配置」永久为 true；写操作 outcome 才是重启提示的主来源。
    pub requires_restart: bool,
    /// message 是连接器生成的可展示整体说明；仅异常或待处理时填写，避免成功路径噪音。
    pub message: Option<String>,
    /// 当前配置中的 SuperDev MCP 可执行命令。
    pub mcp_command: Option<String>,
    /// 当前配置中的 SUPERDEV_AGENT_URL。
    pub agent_url: Option<String>,
}

/// ConnectorInstallRequest 描述一次自动安装或更新应执行的能力集合。
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ConnectorInstallRequest {
    /// operation 区分初次安装与更新驱动。
    pub operation: ConnectorOperation,
    /// capabilities 只包含本次应实际执行或重试的自动能力。
    pub capabilities: Vec<IntegrationCapability>,
}

/// ConnectorError 是具体连接器返回的结构化、可恢复错误。
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ConnectorError {
    code: String,
    message: String,
}

impl ConnectorError {
    /// new 构造连接器错误。
    ///
    /// 参数：
    ///   - code: 稳定、可分类的错误代码
    ///   - message: 面向调用方的错误说明；Registry 日志不会记录该原文
    pub fn new(code: impl Into<String>, message: impl Into<String>) -> Self {
        Self {
            code: code.into(),
            message: message.into(),
        }
    }

    /// code 返回稳定错误代码。
    pub fn code(&self) -> &str {
        &self.code
    }

    /// message 返回连接器错误说明。
    pub fn message(&self) -> &str {
        &self.message
    }
}

impl fmt::Display for ConnectorError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "{}: {}", self.code(), self.message())
    }
}

impl std::error::Error for ConnectorError {}

/// AgentConnector 定义具体 Agent 适配器必须实现的六项对象安全职责。
pub trait AgentConnector: Send + Sync {
    /// descriptor 返回连接器不可变的静态描述符。
    fn descriptor(&self) -> &AgentConnectorDescriptor;

    /// detect 检测 Agent 本体是否存在，不应执行配置变更。
    fn detect(&self, ctx: &ConnectorRuntimeContext) -> Result<ConnectorDetection, ConnectorError>;

    /// status 读取三项集成状态，不应执行配置变更。
    fn status(&self, ctx: &ConnectorRuntimeContext) -> Result<ConnectorStatus, ConnectorError>;

    /// install 执行安装或更新请求中的自动能力。
    ///
    /// 注意：
    ///   - request.operation 仅由 Registry 设置为 install 或 update
    ///   - 实现必须返回与 descriptor id 及 request.operation 一致的 outcome
    fn install(
        &self,
        ctx: &ConnectorRuntimeContext,
        request: ConnectorInstallRequest,
    ) -> Result<ConnectorOperationOutcome, ConnectorError>;

    /// uninstall 删除 SuperDev 拥有的连接器配置。
    fn uninstall(
        &self,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorOperationOutcome, ConnectorError>;

    /// manual_instructions 返回连接器特定的手动配置步骤，不执行变更。
    fn manual_instructions(
        &self,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorManualInstructions, ConnectorError>;

    /// cli_commands 返回该连接器在本机检测时会寻找的 CLI 命令名（不含路径/
    /// 平台可执行文件扩展名）。
    ///
    /// 默认返回空列表：并非所有连接器实现都需要向外暴露这份清单（测试用的
    /// fixture 连接器、fs_port 端口测试用的 fake 连接器都不关心它）。生产的
    /// 八个内置连接器各自覆盖这个方法，返回值与它们 detect() 里实际探测的
    /// 命令名同源（同一个常量/清单），不是另起一份容易漂移的字符串。
    ///
    /// 用途：远端安装场景下（Task 9 的 `RemoteAgentFs` 调用方）用这份清单去问
    /// 目标机 `/api/integrations/detect`（Task 3）「这些命令里哪个在远端存在」，
    /// 从而在目标机上复刻本机 detect() 的判断依据，不需要另外约定一套命令名。
    fn cli_commands(&self) -> Vec<String> {
        Vec::new()
    }

    /// port_ops 返回该连接器「文件操作全程经 [`ConnectorFs`] 端口」的那套实现。
    ///
    /// 返回 None（默认）表示这家连接器的读写没有全程端口化——要么它靠在目标机
    /// 上运行自身 CLI 写配置（openclaw / grok），要么它还直连 `std::fs`。这类
    /// 连接器**不能**在只有受限文件端点的远端机器上接入：那样的"安装"会在桌面机
    /// 自己的磁盘上按目标机的路径写文件，然后报告成功。
    ///
    /// 之所以做成「实现方主动返回 Some(self)」而不是在编排侧维护一份 ID 清单：
    /// 清单会与代码事实漂移（清单说支持、代码却没端口化，或反过来），而这个
    /// 方法的返回值本身就是那套实现的引用，指不到别处去。
    ///
    /// **但返回 Some(self) 不是通行证，它只证明签名收了端口，不证明函数体用了
    /// 端口**——一家连接器完全可以在 `install_with_fs` 里继续 `std::fs::write`，
    /// 编译器不会有意见。真正提供保证的是另外两道，新增端口化连接器时必须
    /// 一起做到：
    ///   1. 该文件顶部的 `use std::fs` 标成 `#[cfg(test)]`（生产代码里再出现
    ///      `fs::` 就是编译错误），本机专属的探测保持直连时要逐处注明理由
    ///   2. `remote_install.rs` 的
    ///      `ported_connectors_remote_install_writes_remote_values_only_through_the_port`
    ///      把「目标机 HOME」设成桌面机上一个真实存在的空目录，装完卸完断言它
    ///      仍为空——任何一次绕过端口的写入都会让它不再为空
    fn port_ops(&self) -> Option<&dyn PortedConnectorOps> {
        None
    }
}

/// PortedConnectorOps 是「文件操作全程经端口」的连接器操作三件套。
///
/// 与 [`AgentConnector`] 上同名的 status / install / uninstall 是同一份实现的
/// 两种绑定：`AgentConnector` 那三个方法恒绑定本机（`LocalFs`），本 trait 让
/// 调用方显式指定端口，从而把同一套方言逻辑跑到远端机器上。
///
/// **不要**在实现里另写一份逻辑：那正是本次端口化要消灭的东西——方言必须是
/// Rust 单源，`AgentConnector::install` 应当是 `install_with_fs(ctx, req, &LocalFs)`
/// 这样的一行委托。
pub trait PortedConnectorOps {
    /// status_with_fs 是 [`AgentConnector::status`] 的显式端口版本。
    fn status_with_fs(
        &self,
        ctx: &ConnectorRuntimeContext,
        fs_port: &dyn ConnectorFs,
    ) -> Result<ConnectorStatus, ConnectorError>;

    /// install_with_fs 是 [`AgentConnector::install`] 的显式端口版本。
    fn install_with_fs(
        &self,
        ctx: &ConnectorRuntimeContext,
        request: ConnectorInstallRequest,
        fs_port: &dyn ConnectorFs,
    ) -> Result<ConnectorOperationOutcome, ConnectorError>;

    /// uninstall_with_fs 是 [`AgentConnector::uninstall`] 的显式端口版本。
    fn uninstall_with_fs(
        &self,
        ctx: &ConnectorRuntimeContext,
        fs_port: &dyn ConnectorFs,
    ) -> Result<ConnectorOperationOutcome, ConnectorError>;
}

/// RegistryError 描述注册、查找、支持检查或操作结果校验失败。
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum RegistryError {
    /// InvalidDescriptor 表示注册时描述符未通过规范化校验。
    InvalidDescriptor {
        connector_id: String,
        source: ContractError,
    },
    /// DuplicateConnectorId 表示同一个开放 ID 被注册了两次。
    DuplicateConnectorId { connector_id: String },
    /// ConnectorNotFound 表示开放字符串 ID 尚未注册。
    ConnectorNotFound { connector_id: String },
    /// UnsupportedOperation 表示描述符明确声明该操作不受支持。
    UnsupportedOperation {
        connector_id: String,
        operation: ConnectorOperation,
    },
    /// UnsupportedCapability 表示操作虽可自动执行，但其基础 MCP 能力不受支持。
    UnsupportedCapability {
        connector_id: String,
        capability: IntegrationCapability,
        operation: ConnectorOperation,
    },
    /// MutationGatePoisoned 表示先前线程异常退出导致该 ID 的锁不可用。
    MutationGatePoisoned { connector_id: String },
    /// ConnectorOperationFailed 表示具体连接器未能形成规范化结果。
    ConnectorOperationFailed {
        connector_id: String,
        operation: ConnectorOperation,
        source: ConnectorError,
    },
    /// ManualInstructionsFailed 表示连接器无法形成手动操作说明。
    ManualInstructionsFailed {
        connector_id: String,
        source: ConnectorError,
    },
    /// OutcomeConnectorMismatch 表示 prior 或返回结果属于另一个连接器。
    OutcomeConnectorMismatch {
        expected_connector_id: String,
        actual_connector_id: String,
    },
    /// OutcomeOperationMismatch 表示 prior 或返回结果的操作类型不匹配。
    OutcomeOperationMismatch {
        expected_operation: ConnectorOperation,
        actual_operation: ConnectorOperation,
    },
}

impl RegistryError {
    fn kind(&self) -> &'static str {
        match self {
            Self::InvalidDescriptor { .. } => "invalid_descriptor",
            Self::DuplicateConnectorId { .. } => "duplicate_connector_id",
            Self::ConnectorNotFound { .. } => "connector_not_found",
            Self::UnsupportedOperation { .. } => "unsupported_operation",
            Self::UnsupportedCapability { .. } => "unsupported_capability",
            Self::MutationGatePoisoned { .. } => "mutation_gate_poisoned",
            Self::ConnectorOperationFailed { .. } => "connector_operation_failed",
            Self::ManualInstructionsFailed { .. } => "manual_instructions_failed",
            Self::OutcomeConnectorMismatch { .. } => "outcome_connector_mismatch",
            Self::OutcomeOperationMismatch { .. } => "outcome_operation_mismatch",
        }
    }
}

impl fmt::Display for RegistryError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::InvalidDescriptor {
                connector_id,
                source,
            } => write!(
                formatter,
                "connector {connector_id} has an invalid descriptor: {source}"
            ),
            Self::DuplicateConnectorId { connector_id } => {
                write!(formatter, "duplicate connector id: {connector_id}")
            }
            Self::ConnectorNotFound { connector_id } => {
                write!(formatter, "connector not found: {connector_id}")
            }
            Self::UnsupportedOperation {
                connector_id,
                operation,
            } => write!(
                formatter,
                "connector {connector_id} does not support {operation:?}"
            ),
            Self::UnsupportedCapability {
                connector_id,
                capability,
                operation,
            } => write!(
                formatter,
                "connector {connector_id} cannot run {operation:?}: unsupported capability {capability:?}"
            ),
            Self::MutationGatePoisoned { connector_id } => {
                write!(formatter, "connector {connector_id} mutation gate is poisoned")
            }
            Self::ConnectorOperationFailed {
                connector_id,
                operation,
                source,
            } => write!(
                formatter,
                "connector {connector_id} {operation:?} failed: {source}"
            ),
            Self::ManualInstructionsFailed {
                connector_id,
                source,
            } => write!(
                formatter,
                "connector {connector_id} manual instructions failed: {source}"
            ),
            Self::OutcomeConnectorMismatch {
                expected_connector_id,
                actual_connector_id,
            } => write!(
                formatter,
                "connector outcome id mismatch: expected={expected_connector_id}, actual={actual_connector_id}"
            ),
            Self::OutcomeOperationMismatch {
                expected_operation,
                actual_operation,
            } => write!(
                formatter,
                "connector outcome operation mismatch: expected={expected_operation:?}, actual={actual_operation:?}"
            ),
        }
    }
}

impl std::error::Error for RegistryError {}

/// ConnectorRegistry 保持连接器注册顺序、开放 ID 索引和每 ID 变更锁。
pub struct ConnectorRegistry {
    connectors: Vec<Arc<dyn AgentConnector>>,
    id_index: HashMap<String, usize>,
    mutation_gates: HashMap<String, Arc<Mutex<()>>>,
}

impl fmt::Debug for ConnectorRegistry {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        let connector_metadata = self
            .connectors
            .iter()
            .map(|connector| {
                let descriptor = connector.descriptor();
                (
                    descriptor.id(),
                    descriptor.display_name(),
                    descriptor.built_in(),
                    descriptor.platforms().len(),
                    descriptor.docs_url().is_some(),
                    descriptor.verified_versions().map_or(0, <[String]>::len),
                )
            })
            .collect::<Vec<_>>();
        formatter
            .debug_struct("ConnectorRegistry")
            .field("connector_metadata", &connector_metadata)
            .finish_non_exhaustive()
    }
}

impl ConnectorRegistry {
    /// builtin 构造生产环境内置连接器注册表，顺序稳定且不包含测试 fixture。
    pub fn builtin() -> Result<Self, RegistryError> {
        Self::new(crate::mcp_install::connectors::builtin())
    }

    /// new 校验连接器描述符、拒绝重复 ID，并保留传入顺序。
    ///
    /// 参数：
    ///   - connectors: 已构造的对象安全连接器列表
    ///
    /// 返回：
    ///   - 成功时返回带独立 per-id mutation gate 的 Registry
    ///   - 描述符不合法或 ID 重复时返回 RegistryError
    pub fn new(connectors: Vec<Arc<dyn AgentConnector>>) -> Result<Self, RegistryError> {
        let mut id_index = HashMap::with_capacity(connectors.len());
        let mut mutation_gates = HashMap::with_capacity(connectors.len());

        for (index, connector) in connectors.iter().enumerate() {
            let descriptor = connector.descriptor();
            let connector_id = descriptor.id().to_string();
            if let Err(source) = validate_descriptor(descriptor) {
                tracing::error!(
                    connector_id = connector_id.as_str(),
                    operation = "registry_new",
                    aggregate_result = "failed",
                    capability_results = "none",
                    reason = "invalid_descriptor"
                );
                return Err(RegistryError::InvalidDescriptor {
                    connector_id,
                    source,
                });
            }
            if id_index.insert(connector_id.clone(), index).is_some() {
                tracing::error!(
                    connector_id = connector_id.as_str(),
                    operation = "registry_new",
                    aggregate_result = "failed",
                    capability_results = "none",
                    reason = "duplicate_connector_id"
                );
                return Err(RegistryError::DuplicateConnectorId { connector_id });
            }
            mutation_gates.insert(connector_id, Arc::new(Mutex::new(())));
        }

        Ok(Self {
            connectors,
            id_index,
            mutation_gates,
        })
    }

    /// descriptor 按开放字符串 ID 返回连接器的只读描述符。
    ///
    /// 返回：
    ///   - 已注册时返回描述符借用；未知 ID 返回结构化 ConnectorNotFound
    ///
    /// 注意：
    ///   - 不暴露 trait object，避免调用方绕过 Registry 的 per-id mutation gate
    #[cfg(test)]
    pub fn descriptor(
        &self,
        connector_id: &str,
    ) -> Result<&AgentConnectorDescriptor, RegistryError> {
        self.connector(connector_id)
            .map(|connector| connector.descriptor())
    }

    fn connector(&self, connector_id: &str) -> Result<&dyn AgentConnector, RegistryError> {
        let index = self.id_index.get(connector_id).copied().ok_or_else(|| {
            RegistryError::ConnectorNotFound {
                connector_id: connector_id.to_string(),
            }
        })?;
        self.connectors.get(index).map(Arc::as_ref).ok_or_else(|| {
            RegistryError::ConnectorNotFound {
                connector_id: connector_id.to_string(),
            }
        })
    }

    /// list 按注册顺序组合所有连接器的静态描述符与动态状态。
    ///
    /// 注意：
    ///   - 单个 detect/status 错误只降级该项为 Unknown，不中止其余连接器
    ///   - 安全降级消息不包含连接器错误原文或本机路径
    pub fn list(&self, ctx: &ConnectorRuntimeContext) -> Vec<AgentConnectorSummary> {
        let started = Instant::now();
        tracing::info!(
            operation = "list",
            connector_count = self.connectors.len(),
            aggregate_result = "started"
        );

        let mut isolated_error_count = 0usize;
        let summaries = self
            .connectors
            .iter()
            .map(|connector| {
                let descriptor = connector.descriptor().clone();
                let connector_id = descriptor.id();
                let detection = connector.detect(ctx);
                let status = connector.status(ctx);

                if detection.is_err() {
                    isolated_error_count += 1;
                    tracing::warn!(
                        connector_id,
                        operation = "detect",
                        aggregate_result = "isolated_error",
                        capability_results = "unknown"
                    );
                }
                if status.is_err() {
                    isolated_error_count += 1;
                    tracing::warn!(
                        connector_id,
                        operation = "status",
                        aggregate_result = "isolated_error",
                        capability_results = "unknown"
                    );
                }

                let state = match (detection, status) {
                    (Ok(detection), Ok(status)) => AgentConnectorState {
                        detected: detection.detected,
                        detection_path: detection
                            .detection_path
                            .map(|path| path.to_string_lossy().into_owned()),
                        integrations: status.integrations,
                        requires_restart: status.requires_restart,
                        // 仅透传 status 侧真实说明；检测成功消息不升级为状态告警。
                        message: status.message,
                        mcp_command: status.mcp_command,
                        agent_url: status.agent_url,
                    },
                    // 任一读取失败都舍弃另一半快照，避免把不同时间点的局部成功拼成误导状态。
                    _ => unknown_state(&descriptor),
                };

                AgentConnectorSummary { descriptor, state }
            })
            .collect::<Vec<_>>();

        tracing::info!(
            operation = "list",
            connector_count = summaries.len(),
            isolated_error_count,
            aggregate_result = if isolated_error_count == 0 {
                "success"
            } else {
                "partial"
            },
            duration_ms = duration_ms(started)
        );
        summaries
    }

    /// install 执行初次安装，或根据同一 install prior outcome 选择重试能力。
    pub fn install(
        &self,
        connector_id: &str,
        ctx: &ConnectorRuntimeContext,
        previous_outcome: Option<&ConnectorOperationOutcome>,
    ) -> Result<ConnectorOperationOutcome, RegistryError> {
        self.run_install_operation(
            connector_id,
            ctx,
            ConnectorOperation::Install,
            previous_outcome,
        )
    }

    /// update 执行更新，或根据同一 update prior outcome 选择重试能力。
    pub fn update(
        &self,
        connector_id: &str,
        ctx: &ConnectorRuntimeContext,
        previous_outcome: Option<&ConnectorOperationOutcome>,
    ) -> Result<ConnectorOperationOutcome, RegistryError> {
        self.run_install_operation(
            connector_id,
            ctx,
            ConnectorOperation::Update,
            previous_outcome,
        )
    }

    /// uninstall 按描述符支持方式自动卸载、返回手动指引或拒绝不支持操作。
    pub fn uninstall(
        &self,
        connector_id: &str,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorOperationOutcome, RegistryError> {
        let operation = ConnectorOperation::Uninstall;
        let started = Instant::now();
        log_mutation_start(connector_id, operation);
        let result = self.run_uninstall_inner(connector_id, ctx);
        log_mutation_finish(connector_id, operation, started, &result);
        result
    }

    /// manual_instructions 只读获取连接器手动配置说明，不获取 mutation gate。
    pub fn manual_instructions(
        &self,
        connector_id: &str,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorManualInstructions, RegistryError> {
        let connector = self.connector(connector_id)?;
        connector.manual_instructions(ctx).map_err(|source| {
            RegistryError::ManualInstructionsFailed {
                connector_id: connector_id.to_string(),
                source,
            }
        })
    }

    /// verify 读取连接器状态并形成只读 Verify outcome，不执行配置变更。
    ///
    /// 注意：
    ///   - automatic Verify 调用 status，不获取 mutation gate
    ///   - manual Verify 返回手动指引；unsupported Verify 返回结构化操作错误
    pub fn verify(
        &self,
        connector_id: &str,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorOperationOutcome, RegistryError> {
        let started = Instant::now();
        let operation = ConnectorOperation::Verify;
        log_mutation_start(connector_id, operation);
        let result = self.run_verify_inner(connector_id, ctx);
        log_mutation_finish(connector_id, operation, started, &result);
        result
    }

    fn run_verify_inner(
        &self,
        connector_id: &str,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorOperationOutcome, RegistryError> {
        let connector = self.connector(connector_id)?;
        match operation_support(connector.descriptor(), ConnectorOperation::Verify)? {
            SupportMode::Unsupported => Err(RegistryError::UnsupportedOperation {
                connector_id: connector_id.to_string(),
                operation: ConnectorOperation::Verify,
            }),
            SupportMode::Manual => {
                mcp_automation_gate(connector.descriptor(), ConnectorOperation::Verify)?;
                self.manual_outcome(connector, ctx, ConnectorOperation::Verify)
            }
            SupportMode::Automatic
                if mcp_automation_gate(connector.descriptor(), ConnectorOperation::Verify)?
                    == SupportMode::Manual =>
            {
                self.manual_outcome(connector, ctx, ConnectorOperation::Verify)
            }
            SupportMode::Automatic => {
                let status = connector.status(ctx).map_err(|source| {
                    RegistryError::ConnectorOperationFailed {
                        connector_id: connector_id.to_string(),
                        operation: ConnectorOperation::Verify,
                        source,
                    }
                })?;
                Ok(verify_outcome(connector.descriptor(), status))
            }
        }
    }

    fn run_install_operation(
        &self,
        connector_id: &str,
        ctx: &ConnectorRuntimeContext,
        operation: ConnectorOperation,
        previous_outcome: Option<&ConnectorOperationOutcome>,
    ) -> Result<ConnectorOperationOutcome, RegistryError> {
        let started = Instant::now();
        log_mutation_start(connector_id, operation);
        let result = self.run_install_inner(connector_id, ctx, operation, previous_outcome);
        log_mutation_finish(connector_id, operation, started, &result);
        result
    }

    fn run_install_inner(
        &self,
        connector_id: &str,
        ctx: &ConnectorRuntimeContext,
        operation: ConnectorOperation,
        previous_outcome: Option<&ConnectorOperationOutcome>,
    ) -> Result<ConnectorOperationOutcome, RegistryError> {
        let connector = self.connector(connector_id)?;
        if let Some(previous) = previous_outcome {
            validate_outcome_identity(connector_id, operation, previous)?;
        }

        match operation_support(connector.descriptor(), operation)? {
            SupportMode::Unsupported => Err(RegistryError::UnsupportedOperation {
                connector_id: connector_id.to_string(),
                operation,
            }),
            SupportMode::Manual => {
                // 手动 operation 也不能把 unsupported MCP 包装成可继续的 NeedsAction。
                mcp_automation_gate(connector.descriptor(), operation)?;
                self.manual_outcome(connector, ctx, operation)
            }
            SupportMode::Automatic
                if mcp_automation_gate(connector.descriptor(), operation)?
                    == SupportMode::Manual =>
            {
                self.manual_outcome(connector, ctx, operation)
            }
            SupportMode::Automatic => {
                let capabilities = match previous_outcome {
                    Some(previous) => retry_capabilities(connector.descriptor(), previous),
                    None => automatic_capabilities(connector.descriptor()),
                };
                let request = ConnectorInstallRequest {
                    operation,
                    capabilities,
                };
                let gate = self.mutation_gate(connector_id)?;
                // 锁覆盖 connector.install 的完整 MCP→Skill→Hook 调用，避免同一 Agent 出现交错写入。
                let _guard = gate
                    .lock()
                    .map_err(|_| RegistryError::MutationGatePoisoned {
                        connector_id: connector_id.to_string(),
                    })?;
                let outcome = connector.install(ctx, request).map_err(|source| {
                    RegistryError::ConnectorOperationFailed {
                        connector_id: connector_id.to_string(),
                        operation,
                        source,
                    }
                })?;
                validate_outcome_identity(connector_id, operation, &outcome)?;
                Ok(outcome)
            }
        }
    }

    fn run_uninstall_inner(
        &self,
        connector_id: &str,
        ctx: &ConnectorRuntimeContext,
    ) -> Result<ConnectorOperationOutcome, RegistryError> {
        let connector = self.connector(connector_id)?;
        let operation = ConnectorOperation::Uninstall;
        match operation_support(connector.descriptor(), operation)? {
            SupportMode::Unsupported => Err(RegistryError::UnsupportedOperation {
                connector_id: connector_id.to_string(),
                operation,
            }),
            SupportMode::Manual => {
                // uninstall 的手动说明同样以 Connector 具备 MCP 能力为前提。
                mcp_automation_gate(connector.descriptor(), operation)?;
                self.manual_outcome(connector, ctx, operation)
            }
            SupportMode::Automatic
                if mcp_automation_gate(connector.descriptor(), operation)?
                    == SupportMode::Manual =>
            {
                self.manual_outcome(connector, ctx, operation)
            }
            SupportMode::Automatic => {
                let gate = self.mutation_gate(connector_id)?;
                // uninstall 与 install/update 共享同一 gate，保证所有配置变更按 ID 串行。
                let _guard = gate
                    .lock()
                    .map_err(|_| RegistryError::MutationGatePoisoned {
                        connector_id: connector_id.to_string(),
                    })?;
                let outcome = connector.uninstall(ctx).map_err(|source| {
                    RegistryError::ConnectorOperationFailed {
                        connector_id: connector_id.to_string(),
                        operation,
                        source,
                    }
                })?;
                validate_outcome_identity(connector_id, operation, &outcome)?;
                Ok(outcome)
            }
        }
    }

    fn manual_outcome(
        &self,
        connector: &dyn AgentConnector,
        ctx: &ConnectorRuntimeContext,
        operation: ConnectorOperation,
    ) -> Result<ConnectorOperationOutcome, RegistryError> {
        let connector_id = connector.descriptor().id().to_string();
        let instructions = connector.manual_instructions(ctx).map_err(|source| {
            RegistryError::ManualInstructionsFailed {
                connector_id: connector_id.clone(),
                source,
            }
        })?;
        let integrations = connector
            .descriptor()
            .integrations()
            .iter()
            .map(|integration| {
                let result = match (integration.capability, integration.support) {
                    (_, SupportMode::Unsupported) => IntegrationResult::Unsupported,
                    (IntegrationCapability::Mcp, _) => IntegrationResult::NeedsAction,
                    // MCP 尚待手动完成时增强能力没有执行；用 skipped 避免误报成功或重复动作。
                    _ => IntegrationResult::Skipped,
                };
                IntegrationOperationResult {
                    capability: integration.capability,
                    result,
                    target_path: None,
                    backup_path: None,
                    message: (result == IntegrationResult::NeedsAction)
                        .then(|| MANUAL_ACTION_MESSAGE.to_string()),
                }
            })
            .collect();
        Ok(ConnectorOperationOutcome {
            connector_id,
            operation,
            result: ConnectorResult::NeedsAction,
            integrations,
            manual_instructions: Some(instructions),
            requires_restart: false,
            message: Some(MANUAL_ACTION_MESSAGE.to_string()),
        })
    }

    fn mutation_gate(&self, connector_id: &str) -> Result<Arc<Mutex<()>>, RegistryError> {
        self.mutation_gates
            .get(connector_id)
            .cloned()
            .ok_or_else(|| RegistryError::ConnectorNotFound {
                connector_id: connector_id.to_string(),
            })
    }
}

fn unknown_state(descriptor: &AgentConnectorDescriptor) -> AgentConnectorState {
    AgentConnectorState {
        detected: false,
        detection_path: None,
        integrations: descriptor
            .integrations()
            .iter()
            .map(|integration| IntegrationState {
                capability: integration.capability,
                status: IntegrationStateStatus::Unknown,
                target_path: None,
                message: Some(ISOLATED_STATE_MESSAGE.to_string()),
            })
            .collect(),
        requires_restart: false,
        message: Some(ISOLATED_STATE_MESSAGE.to_string()),
        mcp_command: None,
        agent_url: None,
    }
}

fn operation_support(
    descriptor: &AgentConnectorDescriptor,
    operation: ConnectorOperation,
) -> Result<SupportMode, RegistryError> {
    descriptor
        .operations()
        .iter()
        .find(|support| support.operation == operation)
        .map(|support| support.support)
        .ok_or_else(|| RegistryError::InvalidDescriptor {
            connector_id: descriptor.id().to_string(),
            source: ContractError::MissingOperation { operation },
        })
}

fn mcp_automation_gate(
    descriptor: &AgentConnectorDescriptor,
    operation: ConnectorOperation,
) -> Result<SupportMode, RegistryError> {
    let support = descriptor
        .integrations()
        .iter()
        .find(|integration| integration.capability == IntegrationCapability::Mcp)
        .map(|integration| integration.support)
        .ok_or_else(|| RegistryError::InvalidDescriptor {
            connector_id: descriptor.id().to_string(),
            source: ContractError::MissingIntegrationCapability {
                capability: IntegrationCapability::Mcp,
            },
        })?;

    // 自动 operation 仍以 MCP 为成立前提，不能跳过 manual/unsupported MCP 去变更增强能力。
    if support == SupportMode::Unsupported {
        return Err(RegistryError::UnsupportedCapability {
            connector_id: descriptor.id().to_string(),
            capability: IntegrationCapability::Mcp,
            operation,
        });
    }
    Ok(support)
}

fn verify_outcome(
    descriptor: &AgentConnectorDescriptor,
    status: ConnectorStatus,
) -> ConnectorOperationOutcome {
    let integrations = descriptor
        .integrations()
        .iter()
        .map(|support| {
            let state = status
                .integrations
                .iter()
                .find(|state| state.capability == support.capability);
            let (result, message, target_path) = match support.support {
                SupportMode::Unsupported => (IntegrationResult::Unsupported, None, None),
                SupportMode::Automatic | SupportMode::Manual => match state {
                    Some(state) => match state.status {
                        IntegrationStateStatus::Configured => (
                            IntegrationResult::AlreadyPresent,
                            state.message.clone(),
                            state.target_path.clone(),
                        ),
                        IntegrationStateStatus::NeedsAction => (
                            IntegrationResult::NeedsAction,
                            Some(
                                state
                                    .message
                                    .clone()
                                    .unwrap_or_else(|| "需要用户操作".to_string()),
                            ),
                            state.target_path.clone(),
                        ),
                        IntegrationStateStatus::Missing
                        | IntegrationStateStatus::Error
                        | IntegrationStateStatus::Unknown => (
                            IntegrationResult::Failed,
                            state.message.clone(),
                            state.target_path.clone(),
                        ),
                    },
                    None => (IntegrationResult::Failed, None, None),
                },
            };
            IntegrationOperationResult {
                capability: support.capability,
                result,
                target_path,
                backup_path: None,
                message,
            }
        })
        .collect::<Vec<_>>();
    let result = if integrations
        .iter()
        .any(|integration| integration.result == IntegrationResult::Failed)
    {
        ConnectorResult::Failed
    } else if integrations
        .iter()
        .any(|integration| integration.result == IntegrationResult::NeedsAction)
    {
        ConnectorResult::NeedsAction
    } else {
        ConnectorResult::Unchanged
    };
    ConnectorOperationOutcome {
        connector_id: descriptor.id().to_string(),
        operation: ConnectorOperation::Verify,
        result,
        integrations,
        manual_instructions: None,
        requires_restart: status.requires_restart,
        message: status.message,
    }
}

fn automatic_capabilities(descriptor: &AgentConnectorDescriptor) -> Vec<IntegrationCapability> {
    descriptor
        .integrations()
        .iter()
        .filter(|integration| integration.support == SupportMode::Automatic)
        .map(|integration| integration.capability)
        .collect()
}

fn retry_capabilities(
    descriptor: &AgentConnectorDescriptor,
    previous: &ConnectorOperationOutcome,
) -> Vec<IntegrationCapability> {
    let automatic = automatic_capabilities(descriptor)
        .into_iter()
        .collect::<HashSet<_>>();
    let previous_results = previous
        .integrations
        .iter()
        .map(|integration| (integration.capability, integration.result))
        .collect::<HashMap<_, _>>();
    let mcp_requires_retry = previous_results
        .get(&IntegrationCapability::Mcp)
        .is_some_and(|result| {
            matches!(
                result,
                IntegrationResult::Failed | IntegrationResult::NeedsAction
            )
        });

    // MCP 失败时，先前 skipped 的自动增强能力代表“依赖未执行”，需随 MCP 一起补跑。
    descriptor
        .integrations()
        .iter()
        .filter(|integration| automatic.contains(&integration.capability))
        .filter_map(|integration| {
            let result = previous_results.get(&integration.capability)?;
            let directly_retryable = matches!(
                result,
                IntegrationResult::Failed | IntegrationResult::NeedsAction
            );
            let skipped_due_to_mcp = mcp_requires_retry
                && integration.capability != IntegrationCapability::Mcp
                && *result == IntegrationResult::Skipped;
            (directly_retryable || skipped_due_to_mcp).then_some(integration.capability)
        })
        .collect()
}

fn validate_outcome_identity(
    connector_id: &str,
    operation: ConnectorOperation,
    outcome: &ConnectorOperationOutcome,
) -> Result<(), RegistryError> {
    if outcome.connector_id != connector_id {
        return Err(RegistryError::OutcomeConnectorMismatch {
            expected_connector_id: connector_id.to_string(),
            actual_connector_id: outcome.connector_id.clone(),
        });
    }
    if outcome.operation != operation {
        return Err(RegistryError::OutcomeOperationMismatch {
            expected_operation: operation,
            actual_operation: outcome.operation,
        });
    }
    Ok(())
}

fn duration_ms(started: Instant) -> u128 {
    started.elapsed().as_millis()
}

fn operation_name(operation: ConnectorOperation) -> &'static str {
    match operation {
        ConnectorOperation::Detect => "detect",
        ConnectorOperation::Install => "install",
        ConnectorOperation::Update => "update",
        ConnectorOperation::Status => "status",
        ConnectorOperation::Uninstall => "uninstall",
        ConnectorOperation::Verify => "verify",
    }
}

fn connector_result_name(result: ConnectorResult) -> &'static str {
    match result {
        ConnectorResult::Success => "success",
        ConnectorResult::Partial => "partial",
        ConnectorResult::Failed => "failed",
        ConnectorResult::Unchanged => "unchanged",
        ConnectorResult::NeedsAction => "needs_action",
    }
}

fn capability_name(capability: IntegrationCapability) -> &'static str {
    match capability {
        IntegrationCapability::Mcp => "mcp",
        IntegrationCapability::Skill => "skill",
        IntegrationCapability::SessionHook => "session_hook",
    }
}

fn integration_result_name(result: IntegrationResult) -> &'static str {
    match result {
        IntegrationResult::Installed => "installed",
        IntegrationResult::AlreadyPresent => "already_present",
        IntegrationResult::Skipped => "skipped",
        IntegrationResult::Unsupported => "unsupported",
        IntegrationResult::NeedsAction => "needs_action",
        IntegrationResult::Failed => "failed",
    }
}

fn capability_results(outcome: &ConnectorOperationOutcome) -> String {
    outcome
        .integrations
        .iter()
        .map(|integration| {
            format!(
                "{}={}",
                capability_name(integration.capability),
                integration_result_name(integration.result)
            )
        })
        .collect::<Vec<_>>()
        .join(",")
}

fn log_mutation_start(connector_id: &str, operation: ConnectorOperation) {
    tracing::info!(
        connector_id,
        operation = operation_name(operation),
        aggregate_result = "started",
        capability_results = "pending"
    );
}

fn log_mutation_finish(
    connector_id: &str,
    operation: ConnectorOperation,
    started: Instant,
    result: &Result<ConnectorOperationOutcome, RegistryError>,
) {
    match result {
        Ok(outcome) => tracing::info!(
            connector_id,
            operation = operation_name(operation),
            aggregate_result = connector_result_name(outcome.result),
            capability_results = capability_results(outcome),
            duration_ms = duration_ms(started)
        ),
        Err(error) => tracing::error!(
            connector_id,
            operation = operation_name(operation),
            aggregate_result = "failed",
            capability_results = "unavailable",
            error_kind = error.kind(),
            duration_ms = duration_ms(started)
        ),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mcp_install::contracts::{
        AgentConnectorDescriptor, AgentConnectorDescriptorInput, ConnectorManualInstructions,
        ConnectorOperation, ConnectorOperationOutcome, ConnectorPlatform, ConnectorResult,
        IntegrationCapability, IntegrationOperationResult, IntegrationResult, IntegrationState,
        IntegrationStateStatus, IntegrationSupport, OperationSupport, SupportMode,
    };
    use std::path::{Path, PathBuf};
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::sync::{mpsc, Arc, Barrier, Mutex};
    use std::thread;

    #[derive(Clone)]
    struct FakeConnector {
        descriptor: AgentConnectorDescriptor,
        detection: Result<ConnectorDetection, ConnectorError>,
        status: Result<ConnectorStatus, ConnectorError>,
        install_requests: Arc<Mutex<Vec<ConnectorInstallRequest>>>,
        status_calls: Arc<AtomicUsize>,
        uninstall_calls: Arc<AtomicUsize>,
        manual_instruction_calls: Arc<AtomicUsize>,
        mutation_probe: Option<Arc<MutationProbe>>,
        outcome_connector_id: Option<String>,
        outcome_operation: Option<ConnectorOperation>,
    }

    impl FakeConnector {
        fn automatic(id: &str) -> Self {
            Self::with_modes(id, SupportMode::Automatic, SupportMode::Automatic)
        }

        fn with_modes(id: &str, operation_mode: SupportMode, mcp_mode: SupportMode) -> Self {
            Self {
                descriptor: descriptor(id, operation_mode, mcp_mode),
                detection: Ok(ConnectorDetection {
                    detected: true,
                    detection_path: Some(PathBuf::from(format!("/detected/{id}"))),
                    message: None,
                }),
                status: Ok(ConnectorStatus {
                    integrations: states(IntegrationStateStatus::Configured),
                    requires_restart: false,
                    message: None,
                    mcp_command: None,
                    agent_url: None,
                }),
                install_requests: Arc::new(Mutex::new(Vec::new())),
                status_calls: Arc::new(AtomicUsize::new(0)),
                uninstall_calls: Arc::new(AtomicUsize::new(0)),
                manual_instruction_calls: Arc::new(AtomicUsize::new(0)),
                mutation_probe: None,
                outcome_connector_id: None,
                outcome_operation: None,
            }
        }

        fn with_detection(mut self, detection: Result<ConnectorDetection, ConnectorError>) -> Self {
            self.detection = detection;
            self
        }

        fn with_status(mut self, status: Result<ConnectorStatus, ConnectorError>) -> Self {
            self.status = status;
            self
        }

        fn with_probe(mut self, probe: Arc<MutationProbe>) -> Self {
            self.mutation_probe = Some(probe);
            self
        }

        fn with_outcome_identity(
            mut self,
            connector_id: &str,
            operation: ConnectorOperation,
        ) -> Self {
            self.outcome_connector_id = Some(connector_id.to_string());
            self.outcome_operation = Some(operation);
            self
        }

        fn requests(&self) -> Vec<ConnectorInstallRequest> {
            self.install_requests
                .lock()
                .expect("fake request lock")
                .clone()
        }

        fn uninstall_calls(&self) -> usize {
            self.uninstall_calls.load(Ordering::SeqCst)
        }

        fn status_calls(&self) -> usize {
            self.status_calls.load(Ordering::SeqCst)
        }

        fn manual_instruction_calls(&self) -> usize {
            self.manual_instruction_calls.load(Ordering::SeqCst)
        }

        fn outcome(&self, operation: ConnectorOperation) -> ConnectorOperationOutcome {
            ConnectorOperationOutcome {
                connector_id: self
                    .outcome_connector_id
                    .clone()
                    .unwrap_or_else(|| self.descriptor.id().to_string()),
                operation: self.outcome_operation.unwrap_or(operation),
                result: ConnectorResult::Unchanged,
                integrations: operation_results([
                    IntegrationResult::AlreadyPresent,
                    IntegrationResult::AlreadyPresent,
                    IntegrationResult::AlreadyPresent,
                ]),
                manual_instructions: None,
                requires_restart: false,
                message: None,
            }
        }
    }

    impl AgentConnector for FakeConnector {
        fn descriptor(&self) -> &AgentConnectorDescriptor {
            &self.descriptor
        }

        fn detect(
            &self,
            _ctx: &ConnectorRuntimeContext,
        ) -> Result<ConnectorDetection, ConnectorError> {
            self.detection.clone()
        }

        fn status(
            &self,
            _ctx: &ConnectorRuntimeContext,
        ) -> Result<ConnectorStatus, ConnectorError> {
            self.status_calls.fetch_add(1, Ordering::SeqCst);
            self.status.clone()
        }

        fn install(
            &self,
            _ctx: &ConnectorRuntimeContext,
            request: ConnectorInstallRequest,
        ) -> Result<ConnectorOperationOutcome, ConnectorError> {
            self.install_requests
                .lock()
                .expect("fake request lock")
                .push(request.clone());
            if let Some(probe) = &self.mutation_probe {
                probe.enter();
            }
            Ok(self.outcome(request.operation))
        }

        fn uninstall(
            &self,
            _ctx: &ConnectorRuntimeContext,
        ) -> Result<ConnectorOperationOutcome, ConnectorError> {
            self.uninstall_calls.fetch_add(1, Ordering::SeqCst);
            if let Some(probe) = &self.mutation_probe {
                probe.enter();
            }
            Ok(self.outcome(ConnectorOperation::Uninstall))
        }

        fn manual_instructions(
            &self,
            _ctx: &ConnectorRuntimeContext,
        ) -> Result<ConnectorManualInstructions, ConnectorError> {
            self.manual_instruction_calls.fetch_add(1, Ordering::SeqCst);
            Ok(ConnectorManualInstructions {
                summary: "请按步骤手动配置".to_string(),
                steps: vec!["打开 Agent 配置".to_string()],
                config_path: Some("~/.fixture/config.json".to_string()),
                manual_config: Some("{fixture = true}".to_string()),
                verification_prompt: Some("验证 SuperDev".to_string()),
            })
        }
    }

    struct MutationProbe {
        active: AtomicUsize,
        max_active: AtomicUsize,
        entered: mpsc::Sender<usize>,
        release: Option<Mutex<mpsc::Receiver<()>>>,
        parallel_barrier: Option<Arc<Barrier>>,
    }

    impl MutationProbe {
        fn serialized(entered: mpsc::Sender<usize>, release: mpsc::Receiver<()>) -> Self {
            Self {
                active: AtomicUsize::new(0),
                max_active: AtomicUsize::new(0),
                entered,
                release: Some(Mutex::new(release)),
                parallel_barrier: None,
            }
        }

        fn parallel(entered: mpsc::Sender<usize>, barrier: Arc<Barrier>) -> Self {
            Self {
                active: AtomicUsize::new(0),
                max_active: AtomicUsize::new(0),
                entered,
                release: None,
                parallel_barrier: Some(barrier),
            }
        }

        fn enter(&self) {
            let active = self.active.fetch_add(1, Ordering::SeqCst) + 1;
            self.max_active.fetch_max(active, Ordering::SeqCst);
            self.entered.send(active).expect("report mutation entry");

            if let Some(barrier) = &self.parallel_barrier {
                barrier.wait();
            }
            if let Some(release) = &self.release {
                release
                    .lock()
                    .expect("release receiver lock")
                    .recv()
                    .expect("release mutation");
            }

            self.active.fetch_sub(1, Ordering::SeqCst);
        }

        fn max_active(&self) -> usize {
            self.max_active.load(Ordering::SeqCst)
        }
    }

    fn descriptor(
        id: &str,
        operation_mode: SupportMode,
        mcp_mode: SupportMode,
    ) -> AgentConnectorDescriptor {
        AgentConnectorDescriptor::new(AgentConnectorDescriptorInput {
            id: id.to_string(),
            display_name: format!("Fixture {id}"),
            built_in: false,
            platforms: vec![ConnectorPlatform::Macos],
            integrations: vec![
                IntegrationSupport {
                    capability: IntegrationCapability::Mcp,
                    support: mcp_mode,
                },
                IntegrationSupport {
                    capability: IntegrationCapability::Skill,
                    support: SupportMode::Automatic,
                },
                IntegrationSupport {
                    capability: IntegrationCapability::SessionHook,
                    support: SupportMode::Automatic,
                },
            ],
            operations: [
                ConnectorOperation::Detect,
                ConnectorOperation::Install,
                ConnectorOperation::Update,
                ConnectorOperation::Status,
                ConnectorOperation::Uninstall,
                ConnectorOperation::Verify,
            ]
            .into_iter()
            .map(|operation| OperationSupport {
                operation,
                support: if matches!(
                    operation,
                    ConnectorOperation::Install
                        | ConnectorOperation::Update
                        | ConnectorOperation::Uninstall
                        | ConnectorOperation::Verify
                ) {
                    operation_mode
                } else {
                    SupportMode::Automatic
                },
            })
            .collect(),
            docs_url: None,
            verified_versions: None,
        })
        .expect("valid fake descriptor")
    }

    fn context() -> ConnectorRuntimeContext {
        ConnectorRuntimeContext::new(
            PathBuf::from("/home/fixture"),
            vec![PathBuf::from("/commands")],
            vec![PathBuf::from("/apps")],
            PathBuf::from("/bin/superdev-mcp"),
            Some(PathBuf::from("/skills/superdev")),
            None,
        )
    }

    #[test]
    fn connector_environment_exposes_only_known_path_overrides() {
        let env = ConnectorEnvironment::new(
            Some(PathBuf::from("/tmp/opencode.json")),
            Some(PathBuf::from("/tmp/openclaw.json")),
            Some(PathBuf::from("/tmp/kimi-code")),
        );
        let ctx = context().with_environment(env);
        assert_eq!(
            ctx.environment().opencode_config(),
            Some(Path::new("/tmp/opencode.json"))
        );
        assert_eq!(
            ctx.environment().openclaw_config_path(),
            Some(Path::new("/tmp/openclaw.json"))
        );
        assert_eq!(
            ctx.environment().kimi_code_home(),
            Some(Path::new("/tmp/kimi-code"))
        );
    }

    #[test]
    fn connector_runtime_context_defaults_to_no_overrides() {
        assert_eq!(context().environment(), &ConnectorEnvironment::default());
    }

    fn states(status: IntegrationStateStatus) -> Vec<IntegrationState> {
        [
            IntegrationCapability::Mcp,
            IntegrationCapability::Skill,
            IntegrationCapability::SessionHook,
        ]
        .into_iter()
        .map(|capability| IntegrationState {
            capability,
            status,
            target_path: None,
            message: None,
        })
        .collect()
    }

    fn operation_results(results: [IntegrationResult; 3]) -> Vec<IntegrationOperationResult> {
        [
            IntegrationCapability::Mcp,
            IntegrationCapability::Skill,
            IntegrationCapability::SessionHook,
        ]
        .into_iter()
        .zip(results)
        .map(|(capability, result)| IntegrationOperationResult {
            capability,
            result,
            target_path: None,
            backup_path: None,
            message: (result == IntegrationResult::NeedsAction).then(|| "需要用户操作".to_string()),
        })
        .collect()
    }

    fn prior_outcome(
        id: &str,
        operation: ConnectorOperation,
        results: [IntegrationResult; 3],
    ) -> ConnectorOperationOutcome {
        ConnectorOperationOutcome {
            connector_id: id.to_string(),
            operation,
            result: ConnectorResult::Partial,
            integrations: operation_results(results),
            manual_instructions: None,
            requires_restart: false,
            message: None,
        }
    }

    #[test]
    fn registry_rejects_duplicate_ids_and_looks_up_open_string_ids() {
        let first = Arc::new(FakeConnector::automatic("fixture-json-agent"));
        let duplicate = Arc::new(FakeConnector::automatic("fixture-json-agent"));
        let error = ConnectorRegistry::new(vec![first.clone(), duplicate])
            .expect_err("duplicate id must be rejected");
        assert!(matches!(
            error,
            RegistryError::DuplicateConnectorId { connector_id }
                if connector_id == "fixture-json-agent"
        ));

        let registry = ConnectorRegistry::new(vec![first]).expect("valid registry");
        assert_eq!(
            registry
                .descriptor("fixture-json-agent")
                .expect("open id lookup")
                .id(),
            "fixture-json-agent"
        );
        assert!(matches!(
            registry.descriptor("not-registered"),
            Err(RegistryError::ConnectorNotFound { connector_id })
                if connector_id == "not-registered"
        ));
    }

    #[test]
    fn list_preserves_registration_order_across_detection_states_and_errors() {
        let undetected = Arc::new(FakeConnector::automatic("undetected").with_detection(Ok(
            ConnectorDetection {
                detected: false,
                detection_path: None,
                message: Some("not found".to_string()),
            },
        )));
        let detected = Arc::new(FakeConnector::automatic("detected"));
        let broken = Arc::new(FakeConnector::automatic("broken").with_detection(Err(
            ConnectorError::new("detect_failed", "private path must not escape"),
        )));
        let registry =
            ConnectorRegistry::new(vec![undetected, detected, broken]).expect("valid registry");

        let summaries = registry.list(&context());
        let ids = summaries
            .iter()
            .map(|summary| summary.descriptor.id())
            .collect::<Vec<_>>();
        assert_eq!(ids, vec!["undetected", "detected", "broken"]);
        assert!(!summaries[0].state.detected);
        assert!(summaries[1].state.detected);
        assert!(!summaries[2].state.detected);
    }

    #[test]
    fn list_isolates_detect_and_status_errors_as_unknown_states() {
        let detect_failure = ConnectorError::new("detect_failed", "sensitive detect details");
        assert_eq!(detect_failure.code(), "detect_failed");
        assert_eq!(detect_failure.message(), "sensitive detect details");
        let detect_error =
            Arc::new(FakeConnector::automatic("detect-error").with_detection(Err(detect_failure)));
        let healthy = Arc::new(FakeConnector::automatic("healthy"));
        let status_error = Arc::new(FakeConnector::automatic("status-error").with_status(Err(
            ConnectorError::new("status_failed", "sensitive status details"),
        )));
        let registry = ConnectorRegistry::new(vec![detect_error, healthy, status_error])
            .expect("valid registry");

        let summaries = registry.list(&context());
        assert!(summaries[1].state.detected);
        for summary in [&summaries[0], &summaries[2]] {
            assert!(!summary.state.detected);
            assert_eq!(summary.state.integrations.len(), 3);
            assert!(summary
                .state
                .integrations
                .iter()
                .all(|state| state.status == IntegrationStateStatus::Unknown));
            let message = summary
                .state
                .message
                .as_deref()
                .expect("safe error message");
            assert!(!message.contains("sensitive"));
        }
    }

    #[test]
    fn same_connector_mutations_are_serialized() {
        let (entered_tx, entered_rx) = mpsc::channel();
        let (release_tx, release_rx) = mpsc::channel();
        let probe = Arc::new(MutationProbe::serialized(entered_tx, release_rx));
        let connector =
            Arc::new(FakeConnector::automatic("fixture-json-agent").with_probe(probe.clone()));
        let registry = Arc::new(ConnectorRegistry::new(vec![connector]).expect("valid registry"));
        let start = Arc::new(Barrier::new(3));
        let ctx = context();

        let install = {
            let registry = registry.clone();
            let start = start.clone();
            let ctx = ctx.clone();
            thread::spawn(move || {
                start.wait();
                registry.install("fixture-json-agent", &ctx, None)
            })
        };
        let update = {
            let registry = registry.clone();
            let start = start.clone();
            let ctx = ctx.clone();
            thread::spawn(move || {
                start.wait();
                registry.update("fixture-json-agent", &ctx, None)
            })
        };

        start.wait();
        assert_eq!(entered_rx.recv().expect("first mutation entered"), 1);
        release_tx.send(()).expect("release first mutation");
        assert_eq!(entered_rx.recv().expect("second mutation entered"), 1);
        release_tx.send(()).expect("release second mutation");
        install
            .join()
            .expect("install thread")
            .expect("install result");
        update
            .join()
            .expect("update thread")
            .expect("update result");
        assert_eq!(probe.max_active(), 1);
    }

    #[test]
    fn different_connector_mutations_can_overlap() {
        let (entered_tx, entered_rx) = mpsc::channel();
        let overlap = Arc::new(Barrier::new(2));
        let first_probe = Arc::new(MutationProbe::parallel(entered_tx.clone(), overlap.clone()));
        let second_probe = Arc::new(MutationProbe::parallel(entered_tx, overlap));
        let registry = Arc::new(
            ConnectorRegistry::new(vec![
                Arc::new(FakeConnector::automatic("first").with_probe(first_probe)),
                Arc::new(FakeConnector::automatic("second").with_probe(second_probe)),
            ])
            .expect("valid registry"),
        );
        let ctx = context();

        let first = {
            let registry = registry.clone();
            let ctx = ctx.clone();
            thread::spawn(move || registry.install("first", &ctx, None))
        };
        let second = {
            let registry = registry.clone();
            let ctx = ctx.clone();
            thread::spawn(move || registry.uninstall("second", &ctx))
        };

        assert_eq!(entered_rx.recv().expect("first connector entered"), 1);
        assert_eq!(entered_rx.recv().expect("second connector entered"), 1);
        first.join().expect("first thread").expect("first result");
        second
            .join()
            .expect("second thread")
            .expect("second result");
    }

    #[test]
    fn retry_selects_only_retryable_and_mcp_dependent_capabilities() {
        let connector = Arc::new(FakeConnector::automatic("fixture-json-agent"));
        let registry = ConnectorRegistry::new(vec![connector.clone()]).expect("valid registry");
        let ctx = context();

        let mcp_failed = prior_outcome(
            "fixture-json-agent",
            ConnectorOperation::Install,
            [
                IntegrationResult::Failed,
                IntegrationResult::Skipped,
                IntegrationResult::Installed,
            ],
        );
        registry
            .install("fixture-json-agent", &ctx, Some(&mcp_failed))
            .expect("retry failed MCP");

        let enhancements_failed = prior_outcome(
            "fixture-json-agent",
            ConnectorOperation::Install,
            [
                IntegrationResult::AlreadyPresent,
                IntegrationResult::NeedsAction,
                IntegrationResult::Failed,
            ],
        );
        registry
            .install("fixture-json-agent", &ctx, Some(&enhancements_failed))
            .expect("retry enhancements");

        let stable = prior_outcome(
            "fixture-json-agent",
            ConnectorOperation::Install,
            [
                IntegrationResult::AlreadyPresent,
                IntegrationResult::Installed,
                IntegrationResult::Unsupported,
            ],
        );
        registry
            .install("fixture-json-agent", &ctx, Some(&stable))
            .expect("stable retry is a no-op request");

        let requests = connector.requests();
        assert_eq!(
            requests[0].capabilities,
            vec![IntegrationCapability::Mcp, IntegrationCapability::Skill]
        );
        assert_eq!(
            requests[1].capabilities,
            vec![
                IntegrationCapability::Skill,
                IntegrationCapability::SessionHook
            ]
        );
        assert!(requests[2].capabilities.is_empty());
    }

    #[test]
    fn initial_install_uses_automatic_capabilities_and_preserves_open_id() {
        let connector = Arc::new(FakeConnector::automatic("fixture-json-agent"));
        let registry = ConnectorRegistry::new(vec![connector.clone()]).expect("valid registry");

        let outcome = registry
            .install("fixture-json-agent", &context(), None)
            .expect("install fixture connector");

        assert_eq!(outcome.connector_id, "fixture-json-agent");
        assert_eq!(connector.requests()[0].capabilities.len(), 3);
        assert_eq!(
            registry.list(&context())[0].descriptor.id(),
            "fixture-json-agent"
        );
    }

    #[test]
    fn manual_and_unsupported_operation_support_are_honored() {
        let manual = Arc::new(FakeConnector::with_modes(
            "manual-agent",
            SupportMode::Manual,
            SupportMode::Manual,
        ));
        let unsupported = Arc::new(FakeConnector::with_modes(
            "unsupported-agent",
            SupportMode::Unsupported,
            SupportMode::Unsupported,
        ));
        let registry =
            ConnectorRegistry::new(vec![manual.clone(), unsupported]).expect("valid registry");

        let outcome = registry
            .install("manual-agent", &context(), None)
            .expect("manual outcome");
        assert_eq!(outcome.result, ConnectorResult::NeedsAction);
        assert!(outcome.manual_instructions.is_some());
        assert_eq!(outcome.integrations.len(), 3);
        assert!(manual.requests().is_empty());
        assert_eq!(
            registry
                .manual_instructions("manual-agent", &context())
                .expect("read-only manual instructions")
                .summary,
            "请按步骤手动配置"
        );

        assert!(matches!(
            registry.install("unsupported-agent", &context(), None),
            Err(RegistryError::UnsupportedOperation {
                connector_id,
                operation: ConnectorOperation::Install,
            }) if connector_id == "unsupported-agent"
        ));
    }

    #[test]
    fn automatic_operations_with_manual_mcp_return_manual_outcomes_without_calling_driver() {
        let connector = Arc::new(FakeConnector::with_modes(
            "manual-mcp-agent",
            SupportMode::Automatic,
            SupportMode::Manual,
        ));
        let registry = ConnectorRegistry::new(vec![connector.clone()]).expect("valid registry");

        let outcomes = [
            registry
                .install("manual-mcp-agent", &context(), None)
                .expect("manual MCP install outcome"),
            registry
                .update("manual-mcp-agent", &context(), None)
                .expect("manual MCP update outcome"),
            registry
                .uninstall("manual-mcp-agent", &context())
                .expect("manual MCP uninstall outcome"),
        ];

        assert_eq!(
            outcomes
                .iter()
                .map(|outcome| outcome.operation)
                .collect::<Vec<_>>(),
            vec![
                ConnectorOperation::Install,
                ConnectorOperation::Update,
                ConnectorOperation::Uninstall,
            ]
        );
        for outcome in outcomes {
            assert_eq!(outcome.result, ConnectorResult::NeedsAction);
            assert!(outcome.manual_instructions.is_some());
            let mcp = outcome
                .integrations
                .iter()
                .find(|integration| integration.capability == IntegrationCapability::Mcp)
                .expect("MCP result");
            assert_eq!(mcp.result, IntegrationResult::NeedsAction);
            assert!(mcp
                .message
                .as_deref()
                .is_some_and(|message| !message.trim().is_empty()));
            assert!(outcome
                .integrations
                .iter()
                .filter(|integration| integration.capability != IntegrationCapability::Mcp)
                .all(|integration| integration.result == IntegrationResult::Skipped));
        }
        assert!(connector.requests().is_empty());
        assert_eq!(connector.uninstall_calls(), 0);
    }

    #[test]
    fn automatic_operations_with_unsupported_mcp_return_structured_errors_without_calling_driver() {
        let connector = Arc::new(FakeConnector::with_modes(
            "unsupported-mcp-agent",
            SupportMode::Automatic,
            SupportMode::Unsupported,
        ));
        let registry = ConnectorRegistry::new(vec![connector.clone()]).expect("valid registry");

        assert!(matches!(
            registry.install("unsupported-mcp-agent", &context(), None),
            Err(RegistryError::UnsupportedCapability {
                connector_id,
                capability: IntegrationCapability::Mcp,
                operation: ConnectorOperation::Install,
            }) if connector_id == "unsupported-mcp-agent"
        ));
        assert!(matches!(
            registry.update("unsupported-mcp-agent", &context(), None),
            Err(RegistryError::UnsupportedCapability {
                connector_id,
                capability: IntegrationCapability::Mcp,
                operation: ConnectorOperation::Update,
            }) if connector_id == "unsupported-mcp-agent"
        ));
        assert!(matches!(
            registry.uninstall("unsupported-mcp-agent", &context()),
            Err(RegistryError::UnsupportedCapability {
                connector_id,
                capability: IntegrationCapability::Mcp,
                operation: ConnectorOperation::Uninstall,
            }) if connector_id == "unsupported-mcp-agent"
        ));
        assert!(connector.requests().is_empty());
        assert_eq!(connector.uninstall_calls(), 0);
    }

    #[test]
    fn manual_operations_with_unsupported_mcp_return_errors_without_manual_instructions() {
        let connector = Arc::new(FakeConnector::with_modes(
            "manual-operation-unsupported-mcp",
            SupportMode::Manual,
            SupportMode::Unsupported,
        ));
        let registry = ConnectorRegistry::new(vec![connector.clone()]).expect("valid registry");

        assert!(matches!(
            registry.install("manual-operation-unsupported-mcp", &context(), None),
            Err(RegistryError::UnsupportedCapability {
                connector_id,
                capability: IntegrationCapability::Mcp,
                operation: ConnectorOperation::Install,
            }) if connector_id == "manual-operation-unsupported-mcp"
        ));
        assert!(matches!(
            registry.update("manual-operation-unsupported-mcp", &context(), None),
            Err(RegistryError::UnsupportedCapability {
                connector_id,
                capability: IntegrationCapability::Mcp,
                operation: ConnectorOperation::Update,
            }) if connector_id == "manual-operation-unsupported-mcp"
        ));
        assert!(matches!(
            registry.uninstall("manual-operation-unsupported-mcp", &context()),
            Err(RegistryError::UnsupportedCapability {
                connector_id,
                capability: IntegrationCapability::Mcp,
                operation: ConnectorOperation::Uninstall,
            }) if connector_id == "manual-operation-unsupported-mcp"
        ));
        assert!(connector.requests().is_empty());
        assert_eq!(connector.uninstall_calls(), 0);
        assert_eq!(connector.manual_instruction_calls(), 0);
    }

    #[test]
    fn prior_and_returned_outcome_identity_must_match_the_mutation() {
        let registry = ConnectorRegistry::new(vec![Arc::new(FakeConnector::automatic(
            "fixture-json-agent",
        ))])
        .expect("valid registry");
        let wrong_prior = prior_outcome(
            "another-agent",
            ConnectorOperation::Install,
            [
                IntegrationResult::Failed,
                IntegrationResult::Skipped,
                IntegrationResult::Skipped,
            ],
        );

        assert!(matches!(
            registry.install("fixture-json-agent", &context(), Some(&wrong_prior)),
            Err(RegistryError::OutcomeConnectorMismatch { .. })
        ));

        let wrong_operation_prior = prior_outcome(
            "fixture-json-agent",
            ConnectorOperation::Update,
            [
                IntegrationResult::Failed,
                IntegrationResult::Skipped,
                IntegrationResult::Skipped,
            ],
        );
        assert!(matches!(
            registry.install(
                "fixture-json-agent",
                &context(),
                Some(&wrong_operation_prior)
            ),
            Err(RegistryError::OutcomeOperationMismatch { .. })
        ));

        let wrong_return_id = ConnectorRegistry::new(vec![Arc::new(
            FakeConnector::automatic("fixture-json-agent")
                .with_outcome_identity("another-agent", ConnectorOperation::Install),
        )])
        .expect("valid registry");
        assert!(matches!(
            wrong_return_id.install("fixture-json-agent", &context(), None),
            Err(RegistryError::OutcomeConnectorMismatch { .. })
        ));

        let wrong_return_operation = ConnectorRegistry::new(vec![Arc::new(
            FakeConnector::automatic("fixture-json-agent")
                .with_outcome_identity("fixture-json-agent", ConnectorOperation::Update),
        )])
        .expect("valid registry");
        assert!(matches!(
            wrong_return_operation.install("fixture-json-agent", &context(), None),
            Err(RegistryError::OutcomeOperationMismatch { .. })
        ));
    }

    #[test]
    fn runtime_context_owns_inputs_and_exposes_read_only_getters() {
        let ctx = context();

        assert_eq!(ctx.home_dir(), Path::new("/home/fixture"));
        assert_eq!(ctx.command_dirs(), &[PathBuf::from("/commands")]);
        assert_eq!(ctx.app_dirs(), &[PathBuf::from("/apps")]);
        assert_eq!(ctx.mcp_binary(), Path::new("/bin/superdev-mcp"));
        assert_eq!(ctx.skill_source(), Some(Path::new("/skills/superdev")));
        assert_eq!(ctx.skill_source_error(), None);
    }

    #[test]
    fn verify_maps_status_without_mutation_and_preserves_restart_message() {
        let connector = Arc::new(FakeConnector::automatic("verify-agent").with_status(Ok(
            ConnectorStatus {
                integrations: vec![
                    IntegrationState {
                        capability: IntegrationCapability::Mcp,
                        status: IntegrationStateStatus::Configured,
                        target_path: None,
                        message: None,
                    },
                    IntegrationState {
                        capability: IntegrationCapability::Skill,
                        status: IntegrationStateStatus::Missing,
                        target_path: None,
                        message: None,
                    },
                    IntegrationState {
                        capability: IntegrationCapability::SessionHook,
                        status: IntegrationStateStatus::NeedsAction,
                        target_path: None,
                        message: Some("信任 hook".to_string()),
                    },
                ],
                requires_restart: true,
                message: Some("需要重启".to_string()),
                mcp_command: Some("/bin/superdev-mcp".to_string()),
                agent_url: Some("http://127.0.0.1:57017".to_string()),
            },
        )));
        let registry = ConnectorRegistry::new(vec![connector.clone()]).expect("valid registry");

        let outcome = registry
            .verify("verify-agent", &context())
            .expect("verify outcome");

        assert_eq!(outcome.operation, ConnectorOperation::Verify);
        assert_eq!(outcome.result, ConnectorResult::Failed);
        assert!(outcome.requires_restart);
        assert_eq!(outcome.message.as_deref(), Some("需要重启"));
        assert!(connector.requests().is_empty());
        assert_eq!(connector.uninstall_calls(), 0);
    }

    #[test]
    fn verify_status_error_is_structured_and_support_modes_are_routed() {
        let broken = Arc::new(
            FakeConnector::automatic("verify-broken")
                .with_status(Err(ConnectorError::new("status_failed", "hidden details"))),
        );
        let manual = Arc::new(FakeConnector::with_modes(
            "verify-manual",
            SupportMode::Manual,
            SupportMode::Automatic,
        ));
        let unsupported = Arc::new(FakeConnector::with_modes(
            "verify-unsupported",
            SupportMode::Unsupported,
            SupportMode::Automatic,
        ));
        let registry =
            ConnectorRegistry::new(vec![broken, manual, unsupported]).expect("valid registry");

        assert!(matches!(
            registry.verify("verify-broken", &context()),
            Err(RegistryError::ConnectorOperationFailed {
                operation: ConnectorOperation::Verify,
                ..
            })
        ));
        assert_eq!(
            registry
                .verify("verify-manual", &context())
                .expect("manual verify")
                .result,
            ConnectorResult::NeedsAction
        );
        assert!(matches!(
            registry.verify("verify-unsupported", &context()),
            Err(RegistryError::UnsupportedOperation {
                operation: ConnectorOperation::Verify,
                ..
            })
        ));
        assert!(matches!(
            registry.verify("unknown-verify", &context()),
            Err(RegistryError::ConnectorNotFound { .. })
        ));
    }

    #[test]
    fn verify_unsupported_mcp_is_rejected_before_status_or_manual_instructions() {
        let automatic = Arc::new(FakeConnector::with_modes(
            "verify-auto-unsupported-mcp",
            SupportMode::Automatic,
            SupportMode::Unsupported,
        ));
        let manual = Arc::new(FakeConnector::with_modes(
            "verify-manual-unsupported-mcp",
            SupportMode::Manual,
            SupportMode::Unsupported,
        ));
        let registry = ConnectorRegistry::new(vec![automatic.clone(), manual.clone()])
            .expect("valid registry");

        assert!(matches!(
            registry.verify("verify-auto-unsupported-mcp", &context()),
            Err(RegistryError::UnsupportedCapability {
                capability: IntegrationCapability::Mcp,
                operation: ConnectorOperation::Verify,
                ..
            })
        ));
        assert!(matches!(
            registry.verify("verify-manual-unsupported-mcp", &context()),
            Err(RegistryError::UnsupportedCapability {
                capability: IntegrationCapability::Mcp,
                operation: ConnectorOperation::Verify,
                ..
            })
        ));
        assert_eq!(automatic.status_calls(), 0);
        assert_eq!(automatic.manual_instruction_calls(), 0);
        assert_eq!(manual.status_calls(), 0);
        assert_eq!(manual.manual_instruction_calls(), 0);
    }
}
