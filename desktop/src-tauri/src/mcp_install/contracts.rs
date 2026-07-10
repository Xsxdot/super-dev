// contracts.rs 定义 MCP 安装域对内、对外共享的规范化契约。
//
// 职责：
//   - 固定连接器描述、状态与操作结果的序列化形态
//   - 从集成支持能力确定性派生连接器支持级别
//   - 校验描述符完整性并聚合安装、更新操作结果
//
// 边界：
//   - 不注册连接器或选择具体适配器
//   - 不读写 Agent 配置、文件系统或 Tauri 状态
//   - 不执行安装、更新、卸载等有副作用操作

use serde::{de, Deserialize, Deserializer, Serialize};
use std::collections::HashSet;
use std::fmt;

const ALL_INTEGRATION_CAPABILITIES: [IntegrationCapability; 3] = [
    IntegrationCapability::Mcp,
    IntegrationCapability::Skill,
    IntegrationCapability::SessionHook,
];

const ALL_CONNECTOR_OPERATIONS: [ConnectorOperation; 6] = [
    ConnectorOperation::Detect,
    ConnectorOperation::Install,
    ConnectorOperation::Update,
    ConnectorOperation::Status,
    ConnectorOperation::Uninstall,
    ConnectorOperation::Verify,
];

/// ConnectorPlatform 标识连接器可运行的桌面平台。
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ConnectorPlatform {
    Macos,
    Windows,
    Linux,
}

/// IntegrationCapability 标识连接器能够管理的集成种类。
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum IntegrationCapability {
    Mcp,
    Skill,
    SessionHook,
}

/// ConnectorOperation 标识连接器支持的生命周期操作。
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ConnectorOperation {
    Detect,
    Install,
    Update,
    Status,
    Uninstall,
    Verify,
}

/// SupportMode 描述某项能力或操作的自动化程度。
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum SupportMode {
    Automatic,
    Manual,
    Unsupported,
}

/// SupportLevel 是由 MCP、Skill 与 Session Hook 支持方式派生的连接器等级。
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum SupportLevel {
    Full,
    Standard,
    McpCompatible,
    ManualLimited,
}

/// IntegrationStateStatus 描述单项集成当前的检测状态。
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum IntegrationStateStatus {
    Configured,
    Missing,
    NeedsAction,
    Error,
    Unknown,
}

/// ConnectorResult 描述一次连接器操作的整体结果。
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ConnectorResult {
    Success,
    Partial,
    Failed,
    Unchanged,
    NeedsAction,
}

/// IntegrationResult 描述一次操作中单项集成的结果。
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum IntegrationResult {
    Installed,
    AlreadyPresent,
    Skipped,
    Unsupported,
    NeedsAction,
    Failed,
}

/// IntegrationSupport 声明一个集成能力及其支持方式。
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub struct IntegrationSupport {
    pub capability: IntegrationCapability,
    pub support: SupportMode,
}

/// OperationSupport 声明一个连接器操作及其支持方式。
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub struct OperationSupport {
    pub operation: ConnectorOperation,
    pub support: SupportMode,
}

/// AgentConnectorDescriptorInput 收集构造连接器描述符所需的静态声明。
///
/// 边界：
///   - 不包含 support_level，该字段只能由 integrations 确定性派生
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AgentConnectorDescriptorInput {
    pub id: String,
    pub display_name: String,
    pub built_in: bool,
    pub platforms: Vec<ConnectorPlatform>,
    pub integrations: Vec<IntegrationSupport>,
    pub operations: Vec<OperationSupport>,
    pub docs_url: Option<String>,
    pub verified_versions: Option<Vec<String>>,
}

#[derive(Debug, Default)]
enum DeclaredSupportLevel {
    #[default]
    Missing,
    Present(Option<SupportLevel>),
}

fn deserialize_declared_support_level<'de, D>(
    deserializer: D,
) -> Result<DeclaredSupportLevel, D::Error>
where
    D: Deserializer<'de>,
{
    Option::<SupportLevel>::deserialize(deserializer).map(DeclaredSupportLevel::Present)
}

#[derive(Deserialize)]
#[serde(rename_all = "snake_case")]
struct AgentConnectorDescriptorWire {
    id: String,
    display_name: String,
    built_in: bool,
    platforms: Vec<ConnectorPlatform>,
    #[serde(default, deserialize_with = "deserialize_declared_support_level")]
    support_level: DeclaredSupportLevel,
    integrations: Vec<IntegrationSupport>,
    operations: Vec<OperationSupport>,
    docs_url: Option<String>,
    verified_versions: Option<Vec<String>>,
}

/// AgentConnectorDescriptor 描述连接器的静态能力与兼容范围。
#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub struct AgentConnectorDescriptor {
    id: String,
    display_name: String,
    built_in: bool,
    platforms: Vec<ConnectorPlatform>,
    support_level: Option<SupportLevel>,
    integrations: Vec<IntegrationSupport>,
    operations: Vec<OperationSupport>,
    docs_url: Option<String>,
    verified_versions: Option<Vec<String>>,
}

impl AgentConnectorDescriptor {
    /// new 构造并校验规范化连接器描述符，支持级别只从集成声明派生。
    ///
    /// 参数：
    ///   - input: 不含 support_level 的完整静态声明
    ///
    /// 返回：
    ///   - 成功时返回支持级别已确定性派生的描述符；否则返回首个结构化契约错误
    ///
    /// 注意：
    ///   - 调用方不能传入 support_level，避免静态声明与集成能力出现双重真值
    pub fn new(input: AgentConnectorDescriptorInput) -> Result<Self, ContractError> {
        let AgentConnectorDescriptorInput {
            id,
            display_name,
            built_in,
            platforms,
            integrations,
            operations,
            docs_url,
            verified_versions,
        } = input;
        let support_level = derive_support_level(&integrations)?;
        let descriptor = Self {
            id,
            display_name,
            built_in,
            platforms,
            support_level,
            integrations,
            operations,
            docs_url,
            verified_versions,
        };
        validate_descriptor(&descriptor)?;
        Ok(descriptor)
    }

    /// id 返回连接器的稳定标识。
    ///
    /// 返回：
    ///   - 构造时已校验为非空白的标识字符串引用
    pub fn id(&self) -> &str {
        &self.id
    }

    /// display_name 返回面向用户的连接器名称。
    ///
    /// 返回：
    ///   - 构造时已校验为非空白的展示名称引用
    pub fn display_name(&self) -> &str {
        &self.display_name
    }

    /// built_in 返回连接器是否由 SuperDev 内置提供。
    ///
    /// 返回：
    ///   - true 表示内置连接器，false 表示外部连接器
    pub fn built_in(&self) -> bool {
        self.built_in
    }

    /// platforms 返回连接器支持的平台只读列表。
    ///
    /// 返回：
    ///   - 非空且不重复的平台切片，调用方不能通过该切片修改描述符
    pub fn platforms(&self) -> &[ConnectorPlatform] {
        &self.platforms
    }

    /// support_level 返回由集成声明确定性派生的连接器支持级别。
    ///
    /// 返回：
    ///   - 自动或手动支持 MCP 时返回对应级别；MCP 不支持时返回 None
    ///
    /// 注意：
    ///   - 该值只能由 new 构造器写入，调用方不能独立覆盖
    pub fn support_level(&self) -> Option<SupportLevel> {
        self.support_level
    }

    /// integrations 返回三项集成能力的只读支持声明。
    ///
    /// 返回：
    ///   - MCP、Skill 与 Session Hook 各一次的支持声明切片
    pub fn integrations(&self) -> &[IntegrationSupport] {
        &self.integrations
    }

    /// operations 返回六项连接器操作的只读支持声明。
    ///
    /// 返回：
    ///   - detect、install、update、status、uninstall 与 verify 各一次的声明切片
    pub fn operations(&self) -> &[OperationSupport] {
        &self.operations
    }

    /// docs_url 返回可选的连接器文档地址。
    ///
    /// 返回：
    ///   - 已配置时返回字符串引用，未配置时返回 None
    pub fn docs_url(&self) -> Option<&str> {
        self.docs_url.as_deref()
    }

    /// verified_versions 返回可选的已验证 Agent 版本列表。
    ///
    /// 返回：
    ///   - 已声明时返回只读版本切片，未声明时返回 None
    pub fn verified_versions(&self) -> Option<&[String]> {
        self.verified_versions.as_deref()
    }
}

impl<'de> Deserialize<'de> for AgentConnectorDescriptor {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let AgentConnectorDescriptorWire {
            id,
            display_name,
            built_in,
            platforms,
            support_level,
            integrations,
            operations,
            docs_url,
            verified_versions,
        } = AgentConnectorDescriptorWire::deserialize(deserializer)?;
        let declared = match support_level {
            DeclaredSupportLevel::Missing => return Err(de::Error::missing_field("support_level")),
            DeclaredSupportLevel::Present(level) => level,
        };
        let descriptor = Self::new(AgentConnectorDescriptorInput {
            id,
            display_name,
            built_in,
            platforms,
            integrations,
            operations,
            docs_url,
            verified_versions,
        })
        .map_err(de::Error::custom)?;
        let derived = descriptor.support_level();
        if declared != derived {
            return Err(de::Error::custom(ContractError::SupportLevelMismatch {
                declared,
                derived,
            }));
        }
        Ok(descriptor)
    }
}

/// IntegrationState 描述单项集成的当前状态及其目标位置。
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub struct IntegrationState {
    pub capability: IntegrationCapability,
    pub status: IntegrationStateStatus,
    pub target_path: Option<String>,
    pub message: Option<String>,
}

/// AgentConnectorState 描述连接器及其各项集成的运行时状态。
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub struct AgentConnectorState {
    pub detected: bool,
    pub detection_path: Option<String>,
    pub integrations: Vec<IntegrationState>,
    pub requires_restart: bool,
    pub message: Option<String>,
}

/// AgentConnectorSummary 将静态描述符与运行时状态组成一个查询结果。
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub struct AgentConnectorSummary {
    pub descriptor: AgentConnectorDescriptor,
    pub state: AgentConnectorState,
}

/// IntegrationOperationResult 描述一次操作中单项集成的变更结果。
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub struct IntegrationOperationResult {
    pub capability: IntegrationCapability,
    pub result: IntegrationResult,
    pub target_path: Option<String>,
    pub backup_path: Option<String>,
    pub message: Option<String>,
}

/// ConnectorManualInstructions 提供 MCP 自动配置失败后的可复制手动接入指引。
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub struct ConnectorManualInstructions {
    pub summary: String,
    pub steps: Vec<String>,
    pub config_path: Option<String>,
    pub manual_config: Option<String>,
    pub verification_prompt: Option<String>,
}

/// ConnectorOperationOutcome 描述一次连接器操作及各项集成的规范化结果。
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub struct ConnectorOperationOutcome {
    pub connector_id: String,
    pub operation: ConnectorOperation,
    pub result: ConnectorResult,
    pub integrations: Vec<IntegrationOperationResult>,
    pub manual_instructions: Option<ConnectorManualInstructions>,
    pub requires_restart: bool,
    pub message: Option<String>,
}

/// ContractError 描述规范化契约在校验或结果聚合阶段发现的结构错误。
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum ContractError {
    EmptyConnectorId,
    EmptyDescriptorId,
    EmptyDisplayName,
    EmptyPlatforms,
    DuplicatePlatform {
        platform: ConnectorPlatform,
    },
    MissingIntegrationCapability {
        capability: IntegrationCapability,
    },
    DuplicateIntegrationCapability {
        capability: IntegrationCapability,
    },
    MissingOperation {
        operation: ConnectorOperation,
    },
    DuplicateOperation {
        operation: ConnectorOperation,
    },
    SupportLevelMismatch {
        declared: Option<SupportLevel>,
        derived: Option<SupportLevel>,
    },
    UnsupportedAggregationOperation {
        operation: ConnectorOperation,
    },
    MissingMcpIntegrationResult,
    InvalidMcpIntegrationResult {
        result: IntegrationResult,
    },
    MissingNeedsActionMessage {
        capability: IntegrationCapability,
    },
    MissingManualInstructions,
    MissingManualConfig,
    DuplicateIntegrationResult {
        capability: IntegrationCapability,
    },
}

impl fmt::Display for ContractError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::EmptyConnectorId => write!(formatter, "connector id is empty"),
            Self::EmptyDescriptorId => write!(formatter, "connector descriptor id is empty"),
            Self::EmptyDisplayName => {
                write!(formatter, "connector descriptor display_name is empty")
            }
            Self::EmptyPlatforms => write!(formatter, "connector descriptor platforms are empty"),
            Self::DuplicatePlatform { platform } => {
                write!(formatter, "duplicate connector platform: {platform:?}")
            }
            Self::MissingIntegrationCapability { capability } => {
                write!(formatter, "missing integration capability: {capability:?}")
            }
            Self::DuplicateIntegrationCapability { capability } => {
                write!(
                    formatter,
                    "duplicate integration capability: {capability:?}"
                )
            }
            Self::MissingOperation { operation } => {
                write!(formatter, "missing connector operation: {operation:?}")
            }
            Self::DuplicateOperation { operation } => {
                write!(formatter, "duplicate connector operation: {operation:?}")
            }
            Self::SupportLevelMismatch { declared, derived } => write!(
                formatter,
                "connector support level mismatch: declared={declared:?}, derived={derived:?}"
            ),
            Self::UnsupportedAggregationOperation { operation } => write!(
                formatter,
                "connector result aggregation does not support operation: {operation:?}"
            ),
            Self::MissingMcpIntegrationResult => {
                write!(formatter, "connector operation is missing the MCP result")
            }
            Self::InvalidMcpIntegrationResult { result } => {
                write!(formatter, "invalid MCP integration result: {result:?}")
            }
            Self::MissingNeedsActionMessage { capability } => write!(
                formatter,
                "needs_action integration result is missing an action message: {capability:?}"
            ),
            Self::MissingManualInstructions => {
                write!(
                    formatter,
                    "failed MCP result is missing manual instructions"
                )
            }
            Self::MissingManualConfig => {
                write!(
                    formatter,
                    "failed MCP result is missing a copyable manual config"
                )
            }
            Self::DuplicateIntegrationResult { capability } => {
                write!(formatter, "duplicate integration result: {capability:?}")
            }
        }
    }
}

impl std::error::Error for ContractError {}

fn unique_support_for(
    integrations: &[IntegrationSupport],
    capability: IntegrationCapability,
) -> Result<SupportMode, ContractError> {
    let mut matches = integrations
        .iter()
        .filter(|integration| integration.capability == capability);
    let support = matches
        .next()
        .map(|integration| integration.support)
        .ok_or(ContractError::MissingIntegrationCapability { capability })?;

    // 重复声明会让结果受输入顺序影响，因此立即返回可定位错误而不挑选任一声明。
    if matches.next().is_some() {
        return Err(ContractError::DuplicateIntegrationCapability { capability });
    }
    Ok(support)
}

/// derive_support_level 从三项集成支持方式确定性派生连接器支持级别。
///
/// 参数：
///   - integrations: MCP、Skill 与 Session Hook 的支持声明
///
/// 返回：
///   - MCP 不支持时返回 Ok(None)，其余完整声明返回 Ok(Some(level))
///   - 任一能力缺失或重复时返回可定位的 ContractError
///
/// 注意：
///   - 输入顺序不参与派生，连接器描述符的完整性由 validate_descriptor 单独校验
pub fn derive_support_level(
    integrations: &[IntegrationSupport],
) -> Result<Option<SupportLevel>, ContractError> {
    let mcp = unique_support_for(integrations, IntegrationCapability::Mcp)?;
    let skill = unique_support_for(integrations, IntegrationCapability::Skill)?;
    let session_hook = unique_support_for(integrations, IntegrationCapability::SessionHook)?;

    Ok(match mcp {
        SupportMode::Unsupported => None,
        SupportMode::Manual => Some(SupportLevel::ManualLimited),
        SupportMode::Automatic => match skill {
            SupportMode::Automatic => match session_hook {
                SupportMode::Automatic => Some(SupportLevel::Full),
                SupportMode::Manual | SupportMode::Unsupported => Some(SupportLevel::Standard),
            },
            SupportMode::Manual | SupportMode::Unsupported => Some(SupportLevel::McpCompatible),
        },
    })
}

/// validate_descriptor 校验连接器描述符的规范化完整性。
///
/// 参数：
///   - descriptor: 待校验的静态连接器描述符
///
/// 返回：
///   - 描述符完整且支持级别正确时返回 Ok；否则返回首个确定性结构错误
///
/// 注意：
///   - 三项集成能力和六项操作必须各出现一次，平台列表必须非空且不能重复
pub fn validate_descriptor(descriptor: &AgentConnectorDescriptor) -> Result<(), ContractError> {
    if descriptor.id.trim().is_empty() {
        return Err(ContractError::EmptyDescriptorId);
    }
    if descriptor.display_name.trim().is_empty() {
        return Err(ContractError::EmptyDisplayName);
    }
    if descriptor.platforms.is_empty() {
        return Err(ContractError::EmptyPlatforms);
    }

    let mut platforms = HashSet::new();
    for platform in descriptor.platforms.iter().copied() {
        if !platforms.insert(platform) {
            return Err(ContractError::DuplicatePlatform { platform });
        }
    }

    for capability in ALL_INTEGRATION_CAPABILITIES {
        match descriptor
            .integrations
            .iter()
            .filter(|integration| integration.capability == capability)
            .count()
        {
            0 => return Err(ContractError::MissingIntegrationCapability { capability }),
            1 => {}
            _ => return Err(ContractError::DuplicateIntegrationCapability { capability }),
        }
    }

    for operation in ALL_CONNECTOR_OPERATIONS {
        match descriptor
            .operations
            .iter()
            .filter(|support| support.operation == operation)
            .count()
        {
            0 => return Err(ContractError::MissingOperation { operation }),
            1 => {}
            _ => return Err(ContractError::DuplicateOperation { operation }),
        }
    }

    let derived = derive_support_level(&descriptor.integrations)?;
    if descriptor.support_level != derived {
        return Err(ContractError::SupportLevelMismatch {
            declared: descriptor.support_level,
            derived,
        });
    }

    Ok(())
}

/// aggregate_connector_result 将安装或更新的单项集成结果聚合为连接器结果。
///
/// 参数：
///   - connector_id: 执行操作的非空白连接器标识
///   - operation: 当前操作，仅支持 install 与 update
///   - integrations: MCP、Skill 与 Session Hook 各一次的操作结果
///   - manual_instructions: 自动 MCP 配置失败时的手动接入指引
///   - requires_restart: 是否需要重启 Agent，由适配器判断并原样传播
///   - message: 面向调用方的整体说明，由适配器生成并原样传播
///
/// 返回：
///   - 成功时返回保留输入明细的规范化结果；结构不合法时返回 ContractError
///
/// 注意：
///   - skipped 与 unsupported 仅作为非 MCP 增强项的中性结果
///   - needs_action 必须携带非空白动作说明，否则拒绝聚合
///   - MCP failed 必须携带包含非空白 manual_config 的手动指引
pub fn aggregate_connector_result(
    connector_id: String,
    operation: ConnectorOperation,
    integrations: Vec<IntegrationOperationResult>,
    manual_instructions: Option<ConnectorManualInstructions>,
    requires_restart: bool,
    message: Option<String>,
) -> Result<ConnectorOperationOutcome, ContractError> {
    if connector_id.trim().is_empty() {
        return Err(ContractError::EmptyConnectorId);
    }
    if !matches!(
        operation,
        ConnectorOperation::Install | ConnectorOperation::Update
    ) {
        return Err(ContractError::UnsupportedAggregationOperation { operation });
    }

    let mut seen_capabilities = HashSet::new();
    for integration in &integrations {
        if !seen_capabilities.insert(integration.capability) {
            return Err(ContractError::DuplicateIntegrationResult {
                capability: integration.capability,
            });
        }
    }

    for capability in ALL_INTEGRATION_CAPABILITIES {
        if !seen_capabilities.contains(&capability) {
            return if capability == IntegrationCapability::Mcp {
                Err(ContractError::MissingMcpIntegrationResult)
            } else {
                Err(ContractError::MissingIntegrationCapability { capability })
            };
        }
    }

    let mcp_result = integrations
        .iter()
        .find(|integration| integration.capability == IntegrationCapability::Mcp)
        .map(|integration| integration.result)
        .ok_or(ContractError::MissingMcpIntegrationResult)?;
    if matches!(
        mcp_result,
        IntegrationResult::Skipped | IntegrationResult::Unsupported
    ) {
        return Err(ContractError::InvalidMcpIntegrationResult { result: mcp_result });
    }
    if mcp_result == IntegrationResult::Failed {
        let instructions = manual_instructions
            .as_ref()
            .ok_or(ContractError::MissingManualInstructions)?;
        if instructions
            .manual_config
            .as_deref()
            .map(str::trim)
            .filter(|config| !config.is_empty())
            .is_none()
        {
            return Err(ContractError::MissingManualConfig);
        }
    }

    // needs_action 是交互契约而非普通状态；缺少明确动作时前端无法帮助用户继续。
    for capability in ALL_INTEGRATION_CAPABILITIES {
        if let Some(integration) = integrations
            .iter()
            .find(|integration| integration.capability == capability)
        {
            if integration.result == IntegrationResult::NeedsAction
                && integration
                    .message
                    .as_deref()
                    .map(str::trim)
                    .filter(|message| !message.is_empty())
                    .is_none()
            {
                return Err(ContractError::MissingNeedsActionMessage { capability });
            }
        }
    }

    let has_problematic_enhancement = integrations
        .iter()
        .filter(|integration| integration.capability != IntegrationCapability::Mcp)
        .any(|integration| {
            matches!(
                integration.result,
                IntegrationResult::Failed | IntegrationResult::NeedsAction
            )
        });

    // MCP 是连接器成立的基础能力：它自身失败或等待用户动作时，不能被增强项成功掩盖。
    let result = if mcp_result == IntegrationResult::Failed {
        ConnectorResult::Failed
    } else if mcp_result == IntegrationResult::NeedsAction {
        ConnectorResult::NeedsAction
    } else if has_problematic_enhancement {
        ConnectorResult::Partial
    } else if integrations
        .iter()
        .any(|integration| integration.result == IntegrationResult::Installed)
    {
        // installed 可能来自 MCP，也可能来自已存在 MCP 之上的增强项；两者都表示本次有实际变更。
        ConnectorResult::Success
    } else {
        // already_present、skipped、unsupported 都不代表本次发生了变更。
        ConnectorResult::Unchanged
    };

    Ok(ConnectorOperationOutcome {
        connector_id,
        operation,
        result,
        integrations,
        manual_instructions,
        requires_restart,
        message,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn integration_support(
        capability: IntegrationCapability,
        support: SupportMode,
    ) -> IntegrationSupport {
        IntegrationSupport {
            capability,
            support,
        }
    }

    fn operation_support(operation: ConnectorOperation, support: SupportMode) -> OperationSupport {
        OperationSupport { operation, support }
    }

    fn complete_descriptor() -> AgentConnectorDescriptor {
        AgentConnectorDescriptor::new(AgentConnectorDescriptorInput {
            id: "codex".to_string(),
            display_name: "Codex".to_string(),
            built_in: true,
            platforms: vec![
                ConnectorPlatform::Macos,
                ConnectorPlatform::Windows,
                ConnectorPlatform::Linux,
            ],
            integrations: vec![
                integration_support(IntegrationCapability::Mcp, SupportMode::Automatic),
                integration_support(IntegrationCapability::Skill, SupportMode::Automatic),
                integration_support(IntegrationCapability::SessionHook, SupportMode::Automatic),
            ],
            operations: vec![
                operation_support(ConnectorOperation::Detect, SupportMode::Automatic),
                operation_support(ConnectorOperation::Install, SupportMode::Automatic),
                operation_support(ConnectorOperation::Update, SupportMode::Automatic),
                operation_support(ConnectorOperation::Status, SupportMode::Automatic),
                operation_support(ConnectorOperation::Uninstall, SupportMode::Automatic),
                operation_support(ConnectorOperation::Verify, SupportMode::Automatic),
            ],
            docs_url: Some("https://example.com/codex".to_string()),
            verified_versions: Some(vec!["1.0.0".to_string()]),
        })
        .expect("complete descriptor")
    }

    fn integration_result(
        capability: IntegrationCapability,
        result: IntegrationResult,
    ) -> IntegrationOperationResult {
        IntegrationOperationResult {
            capability,
            result,
            target_path: None,
            backup_path: None,
            message: (result == IntegrationResult::NeedsAction)
                .then(|| "Complete the required Agent action".to_string()),
        }
    }

    fn manual_instructions() -> ConnectorManualInstructions {
        ConnectorManualInstructions {
            summary: "Install SuperDev MCP manually".to_string(),
            steps: vec![
                "Open the Agent MCP settings".to_string(),
                "Paste the generated configuration".to_string(),
            ],
            config_path: Some("~/.codex/config.toml".to_string()),
            manual_config: Some("{}".to_string()),
            verification_prompt: Some("Ask the Agent to list SuperDev tools".to_string()),
        }
    }

    fn aggregate(
        operation: ConnectorOperation,
        mcp_result: IntegrationResult,
        enhancement_results: &[IntegrationResult],
        requires_restart: bool,
    ) -> Result<ConnectorOperationOutcome, ContractError> {
        let mut integrations = vec![integration_result(IntegrationCapability::Mcp, mcp_result)];
        let enhancement_capabilities = [
            IntegrationCapability::Skill,
            IntegrationCapability::SessionHook,
        ];
        integrations.extend(enhancement_capabilities.into_iter().enumerate().map(
            |(index, capability)| {
                let result = enhancement_results
                    .get(index)
                    .copied()
                    .unwrap_or(IntegrationResult::Skipped);
                integration_result(capability, result)
            },
        ));

        aggregate_connector_result(
            "codex".to_string(),
            operation,
            integrations,
            Some(manual_instructions()),
            requires_restart,
            Some("operation message".to_string()),
        )
    }

    #[test]
    fn complete_descriptor_is_valid() {
        let descriptor = complete_descriptor();
        assert_eq!(descriptor.id(), "codex");
        assert_eq!(descriptor.display_name(), "Codex");
        assert!(descriptor.built_in());
        assert_eq!(
            descriptor.platforms(),
            &[
                ConnectorPlatform::Macos,
                ConnectorPlatform::Windows,
                ConnectorPlatform::Linux,
            ]
        );
        assert_eq!(descriptor.support_level(), Some(SupportLevel::Full));
        assert_eq!(descriptor.integrations().len(), 3);
        assert_eq!(descriptor.operations().len(), 6);
        assert_eq!(descriptor.docs_url(), Some("https://example.com/codex"));
        assert_eq!(
            descriptor.verified_versions(),
            Some(&["1.0.0".to_string()][..])
        );
        assert_eq!(validate_descriptor(&descriptor), Ok(()));
    }

    #[test]
    fn descriptor_deserialization_rejects_tampered_or_invalid_derived_contracts() {
        let descriptor = complete_descriptor();

        let mut mismatched = serde_json::to_value(&descriptor).expect("serialize descriptor");
        mismatched["support_level"] = serde_json::json!("standard");
        assert!(serde_json::from_value::<AgentConnectorDescriptor>(mismatched).is_err());

        let mut missing_skill = serde_json::to_value(&descriptor).expect("serialize descriptor");
        missing_skill["integrations"]
            .as_array_mut()
            .expect("integration array")
            .retain(|integration| integration["capability"] != "skill");
        assert!(serde_json::from_value::<AgentConnectorDescriptor>(missing_skill).is_err());

        let mut duplicate_hook = serde_json::to_value(&descriptor).expect("serialize descriptor");
        let integrations = duplicate_hook["integrations"]
            .as_array_mut()
            .expect("integration array");
        integrations.push(serde_json::json!({
            "capability": "session_hook",
            "support": "unsupported"
        }));
        assert!(serde_json::from_value::<AgentConnectorDescriptor>(duplicate_hook).is_err());
    }

    #[test]
    fn descriptor_rejects_empty_id_and_display_name() {
        let mut descriptor = complete_descriptor();
        descriptor.id = "  ".to_string();
        assert_eq!(
            validate_descriptor(&descriptor),
            Err(ContractError::EmptyDescriptorId)
        );

        descriptor.id = "codex".to_string();
        descriptor.display_name = "\n\t".to_string();
        assert_eq!(
            validate_descriptor(&descriptor),
            Err(ContractError::EmptyDisplayName)
        );
    }

    #[test]
    fn descriptor_requires_unique_non_empty_platforms() {
        let mut descriptor = complete_descriptor();
        descriptor.platforms.clear();
        assert_eq!(
            validate_descriptor(&descriptor),
            Err(ContractError::EmptyPlatforms)
        );

        descriptor.platforms = vec![ConnectorPlatform::Linux, ConnectorPlatform::Linux];
        assert_eq!(
            validate_descriptor(&descriptor),
            Err(ContractError::DuplicatePlatform {
                platform: ConnectorPlatform::Linux,
            })
        );
    }

    #[test]
    fn descriptor_requires_each_capability_exactly_once_including_mcp() {
        let mut descriptor = complete_descriptor();
        descriptor
            .integrations
            .retain(|integration| integration.capability != IntegrationCapability::Mcp);
        assert_eq!(
            validate_descriptor(&descriptor),
            Err(ContractError::MissingIntegrationCapability {
                capability: IntegrationCapability::Mcp,
            })
        );

        descriptor = complete_descriptor();
        descriptor.integrations.push(integration_support(
            IntegrationCapability::Skill,
            SupportMode::Automatic,
        ));
        assert_eq!(
            validate_descriptor(&descriptor),
            Err(ContractError::DuplicateIntegrationCapability {
                capability: IntegrationCapability::Skill,
            })
        );
    }

    #[test]
    fn descriptor_requires_each_operation_exactly_once() {
        let mut descriptor = complete_descriptor();
        descriptor
            .operations
            .retain(|operation| operation.operation != ConnectorOperation::Verify);
        assert_eq!(
            validate_descriptor(&descriptor),
            Err(ContractError::MissingOperation {
                operation: ConnectorOperation::Verify,
            })
        );

        descriptor = complete_descriptor();
        descriptor.operations.push(operation_support(
            ConnectorOperation::Install,
            SupportMode::Automatic,
        ));
        assert_eq!(
            validate_descriptor(&descriptor),
            Err(ContractError::DuplicateOperation {
                operation: ConnectorOperation::Install,
            })
        );
    }

    #[test]
    fn descriptor_support_level_must_match_derivation() {
        let mut descriptor = complete_descriptor();
        descriptor.support_level = Some(SupportLevel::Standard);

        assert_eq!(
            validate_descriptor(&descriptor),
            Err(ContractError::SupportLevelMismatch {
                declared: Some(SupportLevel::Standard),
                derived: Some(SupportLevel::Full),
            })
        );
    }

    #[test]
    fn support_level_is_full_only_when_all_integrations_are_automatic() {
        let integrations = vec![
            integration_support(IntegrationCapability::SessionHook, SupportMode::Automatic),
            integration_support(IntegrationCapability::Mcp, SupportMode::Automatic),
            integration_support(IntegrationCapability::Skill, SupportMode::Automatic),
        ];

        assert_eq!(
            derive_support_level(&integrations),
            Ok(Some(SupportLevel::Full))
        );
    }

    #[test]
    fn support_level_is_standard_when_mcp_and_skill_are_automatic_but_hook_is_not() {
        for hook_support in [SupportMode::Manual, SupportMode::Unsupported] {
            let integrations = vec![
                integration_support(IntegrationCapability::Mcp, SupportMode::Automatic),
                integration_support(IntegrationCapability::Skill, SupportMode::Automatic),
                integration_support(IntegrationCapability::SessionHook, hook_support),
            ];

            assert_eq!(
                derive_support_level(&integrations),
                Ok(Some(SupportLevel::Standard)),
                "hook support {hook_support:?}"
            );
        }
    }

    #[test]
    fn support_level_is_mcp_compatible_when_skill_is_not_automatic() {
        for skill_support in [SupportMode::Manual, SupportMode::Unsupported] {
            let integrations = vec![
                integration_support(IntegrationCapability::Mcp, SupportMode::Automatic),
                integration_support(IntegrationCapability::Skill, skill_support),
                integration_support(IntegrationCapability::SessionHook, SupportMode::Automatic),
            ];

            assert_eq!(
                derive_support_level(&integrations),
                Ok(Some(SupportLevel::McpCompatible)),
                "skill support {skill_support:?}"
            );
        }
    }

    #[test]
    fn support_level_is_manual_limited_for_manual_mcp_and_none_for_unsupported_mcp() {
        let mut integrations = vec![
            integration_support(IntegrationCapability::Mcp, SupportMode::Manual),
            integration_support(IntegrationCapability::Skill, SupportMode::Automatic),
            integration_support(IntegrationCapability::SessionHook, SupportMode::Automatic),
        ];
        assert_eq!(
            derive_support_level(&integrations),
            Ok(Some(SupportLevel::ManualLimited))
        );

        integrations[0].support = SupportMode::Unsupported;
        assert_eq!(derive_support_level(&integrations), Ok(None));
    }

    #[test]
    fn support_level_derivation_rejects_missing_or_duplicate_capabilities() {
        let missing_skill = vec![
            integration_support(IntegrationCapability::Mcp, SupportMode::Automatic),
            integration_support(IntegrationCapability::SessionHook, SupportMode::Automatic),
        ];
        assert_eq!(
            derive_support_level(&missing_skill),
            Err(ContractError::MissingIntegrationCapability {
                capability: IntegrationCapability::Skill,
            })
        );

        let duplicate_hook = vec![
            integration_support(IntegrationCapability::Mcp, SupportMode::Automatic),
            integration_support(IntegrationCapability::Skill, SupportMode::Automatic),
            integration_support(IntegrationCapability::SessionHook, SupportMode::Manual),
            integration_support(IntegrationCapability::SessionHook, SupportMode::Unsupported),
        ];
        assert_eq!(
            derive_support_level(&duplicate_hook),
            Err(ContractError::DuplicateIntegrationCapability {
                capability: IntegrationCapability::SessionHook,
            })
        );
    }

    #[test]
    fn install_and_update_aggregation_follow_the_canonical_truth_table() {
        let cases = [
            (
                "mcp failed",
                IntegrationResult::Failed,
                vec![IntegrationResult::Installed],
                ConnectorResult::Failed,
            ),
            (
                "mcp needs action",
                IntegrationResult::NeedsAction,
                vec![IntegrationResult::Installed],
                ConnectorResult::NeedsAction,
            ),
            (
                "mcp installed with already-present enhancement",
                IntegrationResult::Installed,
                vec![IntegrationResult::AlreadyPresent],
                ConnectorResult::Success,
            ),
            (
                "mcp installed with skipped and unsupported enhancements",
                IntegrationResult::Installed,
                vec![IntegrationResult::Skipped, IntegrationResult::Unsupported],
                ConnectorResult::Success,
            ),
            (
                "mcp installed with failed enhancement",
                IntegrationResult::Installed,
                vec![IntegrationResult::Failed],
                ConnectorResult::Partial,
            ),
            (
                "mcp installed with needs-action enhancement",
                IntegrationResult::Installed,
                vec![IntegrationResult::NeedsAction],
                ConnectorResult::Partial,
            ),
            (
                "mcp already present with neutral enhancements",
                IntegrationResult::AlreadyPresent,
                vec![IntegrationResult::Skipped, IntegrationResult::Unsupported],
                ConnectorResult::Unchanged,
            ),
            (
                "mcp already present with installed enhancement",
                IntegrationResult::AlreadyPresent,
                vec![IntegrationResult::Unsupported, IntegrationResult::Installed],
                ConnectorResult::Success,
            ),
            (
                "mcp already present with failed enhancement",
                IntegrationResult::AlreadyPresent,
                vec![IntegrationResult::Failed],
                ConnectorResult::Partial,
            ),
            (
                "mcp already present with needs-action enhancement",
                IntegrationResult::AlreadyPresent,
                vec![IntegrationResult::NeedsAction],
                ConnectorResult::Partial,
            ),
        ];

        for operation in [ConnectorOperation::Install, ConnectorOperation::Update] {
            for (name, mcp_result, enhancements, expected) in &cases {
                let outcome = aggregate(operation, *mcp_result, enhancements, false)
                    .unwrap_or_else(|error| panic!("{operation:?} {name}: {error:?}"));
                assert_eq!(outcome.result, *expected, "{operation:?} {name}");
            }
        }
    }

    #[test]
    fn aggregation_propagates_restart_and_message_without_reordering_integrations() {
        let outcome = aggregate(
            ConnectorOperation::Install,
            IntegrationResult::Installed,
            &[IntegrationResult::Skipped, IntegrationResult::Unsupported],
            true,
        )
        .expect("valid aggregate");

        assert!(outcome.requires_restart);
        assert_eq!(outcome.message.as_deref(), Some("operation message"));
        assert_eq!(outcome.manual_instructions, Some(manual_instructions()));
        assert_eq!(
            outcome.integrations[0].capability,
            IntegrationCapability::Mcp
        );
        assert_eq!(
            outcome.integrations[1].capability,
            IntegrationCapability::Skill
        );
        assert_eq!(
            outcome.integrations[2].capability,
            IntegrationCapability::SessionHook
        );
    }

    #[test]
    fn aggregation_rejects_skipped_or_unsupported_mcp_even_when_an_enhancement_installs() {
        for invalid_result in [IntegrationResult::Skipped, IntegrationResult::Unsupported] {
            assert_eq!(
                aggregate_connector_result(
                    "codex".to_string(),
                    ConnectorOperation::Install,
                    vec![
                        integration_result(IntegrationCapability::Mcp, invalid_result),
                        integration_result(
                            IntegrationCapability::Skill,
                            IntegrationResult::Installed,
                        ),
                        integration_result(
                            IntegrationCapability::SessionHook,
                            IntegrationResult::Skipped,
                        ),
                    ],
                    None,
                    false,
                    None,
                ),
                Err(ContractError::InvalidMcpIntegrationResult {
                    result: invalid_result,
                })
            );
        }
    }

    #[test]
    fn aggregation_requires_copyable_manual_config_only_when_mcp_fails() {
        let failed_integrations = || {
            vec![
                integration_result(IntegrationCapability::Mcp, IntegrationResult::Failed),
                integration_result(IntegrationCapability::Skill, IntegrationResult::Skipped),
                integration_result(
                    IntegrationCapability::SessionHook,
                    IntegrationResult::Unsupported,
                ),
            ]
        };

        assert_eq!(
            aggregate_connector_result(
                "codex".to_string(),
                ConnectorOperation::Install,
                failed_integrations(),
                None,
                false,
                None,
            ),
            Err(ContractError::MissingManualInstructions)
        );

        for manual_config in [None, Some("  \n".to_string())] {
            let mut instructions = manual_instructions();
            instructions.manual_config = manual_config;
            assert_eq!(
                aggregate_connector_result(
                    "codex".to_string(),
                    ConnectorOperation::Install,
                    failed_integrations(),
                    Some(instructions),
                    false,
                    None,
                ),
                Err(ContractError::MissingManualConfig)
            );
        }

        let failed = aggregate_connector_result(
            "codex".to_string(),
            ConnectorOperation::Install,
            failed_integrations(),
            Some(manual_instructions()),
            false,
            None,
        )
        .expect("failed MCP with copyable manual config");
        assert_eq!(failed.result, ConnectorResult::Failed);

        let unchanged_without_manual_instructions = aggregate_connector_result(
            "codex".to_string(),
            ConnectorOperation::Install,
            vec![
                integration_result(
                    IntegrationCapability::Mcp,
                    IntegrationResult::AlreadyPresent,
                ),
                integration_result(IntegrationCapability::Skill, IntegrationResult::Skipped),
                integration_result(
                    IntegrationCapability::SessionHook,
                    IntegrationResult::Unsupported,
                ),
            ],
            None,
            false,
            None,
        )
        .expect("non-failed MCP does not require manual instructions");
        assert_eq!(
            unchanged_without_manual_instructions.result,
            ConnectorResult::Unchanged
        );
    }

    #[test]
    fn aggregation_requires_a_nonblank_message_for_every_needs_action_result() {
        for (capability, message) in [
            (IntegrationCapability::Mcp, None),
            (IntegrationCapability::Skill, Some("  \n".to_string())),
        ] {
            let mut integrations = vec![
                integration_result(
                    IntegrationCapability::Mcp,
                    IntegrationResult::AlreadyPresent,
                ),
                integration_result(IntegrationCapability::Skill, IntegrationResult::Skipped),
                integration_result(
                    IntegrationCapability::SessionHook,
                    IntegrationResult::Skipped,
                ),
            ];
            let needs_action = IntegrationOperationResult {
                capability,
                result: IntegrationResult::NeedsAction,
                target_path: None,
                backup_path: None,
                message,
            };
            if capability == IntegrationCapability::Mcp {
                integrations[0] = needs_action;
            } else {
                integrations[1] = needs_action;
            }

            assert_eq!(
                aggregate_connector_result(
                    "codex".to_string(),
                    ConnectorOperation::Install,
                    integrations,
                    None,
                    false,
                    None,
                ),
                Err(ContractError::MissingNeedsActionMessage { capability })
            );
        }
    }

    #[test]
    fn aggregation_rejects_missing_or_duplicate_mcp_and_non_install_operations() {
        assert_eq!(
            aggregate_connector_result(
                "codex".to_string(),
                ConnectorOperation::Install,
                vec![
                    integration_result(IntegrationCapability::Skill, IntegrationResult::Installed,),
                    integration_result(
                        IntegrationCapability::SessionHook,
                        IntegrationResult::Skipped,
                    )
                ],
                None,
                false,
                None,
            ),
            Err(ContractError::MissingMcpIntegrationResult)
        );

        assert_eq!(
            aggregate_connector_result(
                "codex".to_string(),
                ConnectorOperation::Install,
                vec![
                    integration_result(IntegrationCapability::Mcp, IntegrationResult::Installed,),
                    integration_result(
                        IntegrationCapability::Mcp,
                        IntegrationResult::AlreadyPresent,
                    ),
                    integration_result(IntegrationCapability::Skill, IntegrationResult::Skipped,),
                    integration_result(
                        IntegrationCapability::SessionHook,
                        IntegrationResult::Skipped,
                    ),
                ],
                None,
                false,
                None,
            ),
            Err(ContractError::DuplicateIntegrationResult {
                capability: IntegrationCapability::Mcp,
            })
        );

        assert_eq!(
            aggregate_connector_result(
                "codex".to_string(),
                ConnectorOperation::Status,
                vec![integration_result(
                    IntegrationCapability::Mcp,
                    IntegrationResult::AlreadyPresent,
                )],
                None,
                false,
                None,
            ),
            Err(ContractError::UnsupportedAggregationOperation {
                operation: ConnectorOperation::Status,
            })
        );
    }

    #[test]
    fn aggregation_requires_all_three_capabilities_exactly_once() {
        for missing_capability in [
            IntegrationCapability::Skill,
            IntegrationCapability::SessionHook,
        ] {
            let mut integrations = vec![
                integration_result(
                    IntegrationCapability::Mcp,
                    IntegrationResult::AlreadyPresent,
                ),
                integration_result(IntegrationCapability::Skill, IntegrationResult::Skipped),
                integration_result(
                    IntegrationCapability::SessionHook,
                    IntegrationResult::Unsupported,
                ),
            ];
            integrations.retain(|integration| integration.capability != missing_capability);

            assert_eq!(
                aggregate_connector_result(
                    "codex".to_string(),
                    ConnectorOperation::Install,
                    integrations,
                    None,
                    false,
                    None,
                ),
                Err(ContractError::MissingIntegrationCapability {
                    capability: missing_capability,
                })
            );
        }

        let mut duplicate_hook = vec![
            integration_result(
                IntegrationCapability::Mcp,
                IntegrationResult::AlreadyPresent,
            ),
            integration_result(IntegrationCapability::Skill, IntegrationResult::Skipped),
            integration_result(
                IntegrationCapability::SessionHook,
                IntegrationResult::Unsupported,
            ),
        ];
        duplicate_hook.push(integration_result(
            IntegrationCapability::SessionHook,
            IntegrationResult::Skipped,
        ));
        assert_eq!(
            aggregate_connector_result(
                "codex".to_string(),
                ConnectorOperation::Update,
                duplicate_hook,
                None,
                false,
                None,
            ),
            Err(ContractError::DuplicateIntegrationResult {
                capability: IntegrationCapability::SessionHook,
            })
        );
    }

    #[test]
    fn aggregation_rejects_an_empty_connector_id() {
        assert_eq!(
            aggregate_connector_result(
                " \n\t".to_string(),
                ConnectorOperation::Install,
                vec![
                    integration_result(
                        IntegrationCapability::Mcp,
                        IntegrationResult::AlreadyPresent,
                    ),
                    integration_result(IntegrationCapability::Skill, IntegrationResult::Skipped,),
                    integration_result(
                        IntegrationCapability::SessionHook,
                        IntegrationResult::Unsupported,
                    ),
                ],
                None,
                false,
                None,
            ),
            Err(ContractError::EmptyConnectorId)
        );
    }

    #[test]
    fn enum_json_values_are_fixed_snake_case() {
        let cases = [
            (
                serde_json::to_value(ConnectorPlatform::Macos).unwrap(),
                "macos",
            ),
            (
                serde_json::to_value(ConnectorPlatform::Windows).unwrap(),
                "windows",
            ),
            (
                serde_json::to_value(ConnectorPlatform::Linux).unwrap(),
                "linux",
            ),
            (
                serde_json::to_value(IntegrationCapability::Mcp).unwrap(),
                "mcp",
            ),
            (
                serde_json::to_value(IntegrationCapability::Skill).unwrap(),
                "skill",
            ),
            (
                serde_json::to_value(IntegrationCapability::SessionHook).unwrap(),
                "session_hook",
            ),
            (
                serde_json::to_value(ConnectorOperation::Detect).unwrap(),
                "detect",
            ),
            (
                serde_json::to_value(ConnectorOperation::Install).unwrap(),
                "install",
            ),
            (
                serde_json::to_value(ConnectorOperation::Update).unwrap(),
                "update",
            ),
            (
                serde_json::to_value(ConnectorOperation::Status).unwrap(),
                "status",
            ),
            (
                serde_json::to_value(ConnectorOperation::Uninstall).unwrap(),
                "uninstall",
            ),
            (
                serde_json::to_value(ConnectorOperation::Verify).unwrap(),
                "verify",
            ),
            (
                serde_json::to_value(SupportMode::Automatic).unwrap(),
                "automatic",
            ),
            (serde_json::to_value(SupportMode::Manual).unwrap(), "manual"),
            (
                serde_json::to_value(SupportMode::Unsupported).unwrap(),
                "unsupported",
            ),
            (serde_json::to_value(SupportLevel::Full).unwrap(), "full"),
            (
                serde_json::to_value(SupportLevel::Standard).unwrap(),
                "standard",
            ),
            (
                serde_json::to_value(SupportLevel::McpCompatible).unwrap(),
                "mcp_compatible",
            ),
            (
                serde_json::to_value(SupportLevel::ManualLimited).unwrap(),
                "manual_limited",
            ),
            (
                serde_json::to_value(IntegrationStateStatus::Configured).unwrap(),
                "configured",
            ),
            (
                serde_json::to_value(IntegrationStateStatus::Missing).unwrap(),
                "missing",
            ),
            (
                serde_json::to_value(IntegrationStateStatus::NeedsAction).unwrap(),
                "needs_action",
            ),
            (
                serde_json::to_value(IntegrationStateStatus::Error).unwrap(),
                "error",
            ),
            (
                serde_json::to_value(IntegrationStateStatus::Unknown).unwrap(),
                "unknown",
            ),
            (
                serde_json::to_value(ConnectorResult::Success).unwrap(),
                "success",
            ),
            (
                serde_json::to_value(ConnectorResult::Partial).unwrap(),
                "partial",
            ),
            (
                serde_json::to_value(ConnectorResult::Failed).unwrap(),
                "failed",
            ),
            (
                serde_json::to_value(ConnectorResult::Unchanged).unwrap(),
                "unchanged",
            ),
            (
                serde_json::to_value(ConnectorResult::NeedsAction).unwrap(),
                "needs_action",
            ),
            (
                serde_json::to_value(IntegrationResult::Installed).unwrap(),
                "installed",
            ),
            (
                serde_json::to_value(IntegrationResult::AlreadyPresent).unwrap(),
                "already_present",
            ),
            (
                serde_json::to_value(IntegrationResult::Skipped).unwrap(),
                "skipped",
            ),
            (
                serde_json::to_value(IntegrationResult::Unsupported).unwrap(),
                "unsupported",
            ),
            (
                serde_json::to_value(IntegrationResult::NeedsAction).unwrap(),
                "needs_action",
            ),
            (
                serde_json::to_value(IntegrationResult::Failed).unwrap(),
                "failed",
            ),
        ];

        for (actual, expected) in cases {
            assert_eq!(actual, serde_json::Value::String(expected.to_string()));
        }
    }

    #[test]
    fn summary_json_round_trip_preserves_fixed_field_names() {
        let summary = AgentConnectorSummary {
            descriptor: complete_descriptor(),
            state: AgentConnectorState {
                detected: true,
                detection_path: Some("/Applications/Codex.app".to_string()),
                integrations: vec![IntegrationState {
                    capability: IntegrationCapability::Mcp,
                    status: IntegrationStateStatus::Configured,
                    target_path: Some("~/.codex/config.toml".to_string()),
                    message: None,
                }],
                requires_restart: false,
                message: Some("ready".to_string()),
            },
        };

        let json = serde_json::to_value(&summary).expect("serialize summary");
        assert_eq!(
            json,
            serde_json::json!({
                "descriptor": {
                    "id": "codex",
                    "display_name": "Codex",
                    "built_in": true,
                    "platforms": ["macos", "windows", "linux"],
                    "support_level": "full",
                    "integrations": [
                        { "capability": "mcp", "support": "automatic" },
                        { "capability": "skill", "support": "automatic" },
                        { "capability": "session_hook", "support": "automatic" }
                    ],
                    "operations": [
                        { "operation": "detect", "support": "automatic" },
                        { "operation": "install", "support": "automatic" },
                        { "operation": "update", "support": "automatic" },
                        { "operation": "status", "support": "automatic" },
                        { "operation": "uninstall", "support": "automatic" },
                        { "operation": "verify", "support": "automatic" }
                    ],
                    "docs_url": "https://example.com/codex",
                    "verified_versions": ["1.0.0"]
                },
                "state": {
                    "detected": true,
                    "detection_path": "/Applications/Codex.app",
                    "integrations": [{
                        "capability": "mcp",
                        "status": "configured",
                        "target_path": "~/.codex/config.toml",
                        "message": null
                    }],
                    "requires_restart": false,
                    "message": "ready"
                }
            })
        );

        let decoded: AgentConnectorSummary =
            serde_json::from_value(json).expect("deserialize summary");
        assert_eq!(decoded, summary);
    }

    #[test]
    fn operation_outcome_json_round_trip_preserves_fixed_field_names() {
        let outcome = ConnectorOperationOutcome {
            connector_id: "codex".to_string(),
            operation: ConnectorOperation::Install,
            result: ConnectorResult::Partial,
            integrations: vec![IntegrationOperationResult {
                capability: IntegrationCapability::Skill,
                result: IntegrationResult::Failed,
                target_path: Some("~/.codex/skills/superdev".to_string()),
                backup_path: Some("~/.codex/skills/superdev.bak".to_string()),
                message: Some("permission denied".to_string()),
            }],
            manual_instructions: Some(manual_instructions()),
            requires_restart: true,
            message: Some("MCP installed; skill failed".to_string()),
        };

        let json = serde_json::to_value(&outcome).expect("serialize outcome");
        assert_eq!(
            json,
            serde_json::json!({
                "connector_id": "codex",
                "operation": "install",
                "result": "partial",
                "integrations": [{
                    "capability": "skill",
                    "result": "failed",
                    "target_path": "~/.codex/skills/superdev",
                    "backup_path": "~/.codex/skills/superdev.bak",
                    "message": "permission denied"
                }],
                "manual_instructions": {
                    "summary": "Install SuperDev MCP manually",
                    "steps": [
                        "Open the Agent MCP settings",
                        "Paste the generated configuration"
                    ],
                    "config_path": "~/.codex/config.toml",
                    "manual_config": "{}",
                    "verification_prompt": "Ask the Agent to list SuperDev tools"
                },
                "requires_restart": true,
                "message": "MCP installed; skill failed"
            })
        );

        let decoded: ConnectorOperationOutcome =
            serde_json::from_value(json).expect("deserialize outcome");
        assert_eq!(decoded, outcome);
    }
}
