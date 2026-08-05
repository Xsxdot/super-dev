/**
 * Agent Connector API 封装。
 *
 * 职责：
 *   - 调用 Tauri Connector 列表、安装、更新、验证、卸载和手动指引 command
 *   - 保持 Rust canonical Connector DTO 与 TypeScript 字段一致
 *   - 读取其他本机 stdio MCP Agent 的通用连接材料
 *   - 统一开放 Connector ID 与能力结果类型
 *   - 封装远端机器（只装了 agent）的编程智能体探测/安装/卸载三个 Tauri command
 *
 * 边界：
 *   - 不渲染引导界面
 *   - 不读写 agent settings
 *   - 远端操作经 Tauri command 转发到目标机 agent 的受限文件端点，本模块不感知
 *     本机 agent 代理链或 nodetransport 转发细节
 */
import { invoke } from '@tauri-apps/api/core'

export type ConnectorId = string
export type ConnectorPlatform = 'macos' | 'windows' | 'linux'
export type IntegrationCapability = 'mcp' | 'skill' | 'session_hook'
export type SupportMode = 'automatic' | 'manual' | 'unsupported'
export type SupportLevel = 'full' | 'standard' | 'mcp_compatible' | 'manual_limited'
export type IntegrationStateStatus = 'configured' | 'missing' | 'needs_action' | 'error' | 'unknown'
export type ConnectorOperation = 'detect' | 'install' | 'update' | 'status' | 'uninstall' | 'verify'
export type ConnectorResult = 'success' | 'partial' | 'failed' | 'unchanged' | 'needs_action'
export type IntegrationResult = 'installed' | 'already_present' | 'skipped' | 'unsupported' | 'needs_action' | 'failed'

export interface IntegrationSupport { capability: IntegrationCapability; support: SupportMode }
export interface OperationSupport { operation: ConnectorOperation; support: SupportMode }
export interface AgentConnectorDescriptor {
  id: ConnectorId; display_name: string; built_in: boolean; platforms: ConnectorPlatform[]
  support_level?: SupportLevel | null; integrations: IntegrationSupport[]; operations: OperationSupport[]
  docs_url?: string | null; verified_versions?: string[] | null
}
export interface IntegrationState {
  capability: IntegrationCapability; status: IntegrationStateStatus; target_path?: string | null; message?: string | null
}
export interface AgentConnectorState {
  detected: boolean
  detection_path?: string | null
  integrations: IntegrationState[]
  requires_restart: boolean
  message?: string | null
  /** 当前配置中的 SuperDev MCP 可执行命令；未配置时为空 */
  mcp_command?: string | null
  /** 当前配置中的 SUPERDEV_AGENT_URL；未配置时为空 */
  agent_url?: string | null
}
export interface AgentConnectorSummary { descriptor: AgentConnectorDescriptor; state: AgentConnectorState }
export interface IntegrationOperationResult {
  capability: IntegrationCapability; result: IntegrationResult; target_path?: string | null
  backup_path?: string | null; message?: string | null
}
export interface ConnectorManualInstructions {
  summary: string; steps: string[]; config_path?: string | null; manual_config?: string | null
  verification_prompt?: string | null
}
export interface ConnectorOperationOutcome {
  connector_id: ConnectorId; operation: ConnectorOperation; result: ConnectorResult
  integrations: IntegrationOperationResult[]; manual_instructions?: ConnectorManualInstructions | null
  requires_restart: boolean; message?: string | null
}

export interface GenericMcpConnectionMaterial {
  transport: 'stdio'
  command: string
  agent_url: string
  manual_config: string
}

export interface McpCapabilityTool {
  name: string
  purpose: string
  access: string
  reference: string
}

export interface McpCapabilitySection {
  id: string
  title: string
  description: string
  tools: McpCapabilityTool[]
}

export interface McpDocument {
  id: string
  title: string
  path: string
  content: string
}

export interface McpDocs {
  summary_sections: McpCapabilitySection[]
  documents: McpDocument[]
}

export async function listAgentConnectors(): Promise<AgentConnectorSummary[]> {
  return invoke<AgentConnectorSummary[]>('list_agent_connectors')
}

export async function installAgentConnector(connectorId: ConnectorId, previousOutcome?: ConnectorOperationOutcome | null): Promise<ConnectorOperationOutcome> {
  return invoke<ConnectorOperationOutcome>('install_agent_connector', { connectorId, previousOutcome: previousOutcome ?? null })
}

export async function updateAgentConnector(connectorId: ConnectorId, previousOutcome?: ConnectorOperationOutcome | null): Promise<ConnectorOperationOutcome> {
  return invoke<ConnectorOperationOutcome>('update_agent_connector', { connectorId, previousOutcome: previousOutcome ?? null })
}

export async function uninstallAgentConnector(connectorId: ConnectorId): Promise<ConnectorOperationOutcome> {
  return invoke<ConnectorOperationOutcome>('uninstall_agent_connector', { connectorId })
}

export async function verifyAgentConnector(connectorId: ConnectorId): Promise<ConnectorOperationOutcome> {
  return invoke<ConnectorOperationOutcome>('verify_agent_connector', { connectorId })
}

export async function getAgentConnectorManualInstructions(connectorId: ConnectorId): Promise<ConnectorManualInstructions> {
  return invoke<ConnectorManualInstructions>('agent_connector_manual_instructions', { connectorId })
}

/**
 * getGenericMcpConnectionMaterial 读取未知本机 Agent 可参考的标准 stdio MCP 材料。
 *
 * 返回：
 *   - 打包 sidecar 的绝对命令、Agent URL 与标准 mcpServers JSON 示例
 *
 * 注意：
 *   - 该 API 不写配置，也不推断未知 Agent 的配置路径或 schema 方言
 */
export async function getGenericMcpConnectionMaterial(): Promise<GenericMcpConnectionMaterial> {
  return invoke<GenericMcpConnectionMaterial>('generic_mcp_connection_material')
}

/**
 * getMcpDocs 读取 MCP 功能说明和 bundled superdev skill 文档。
 *
 * 返回：
 *   - 结构化工具能力摘要和 skill/reference 文档
 *
 * 注意：
 *   - 文档来自打包资源，API 层不做渲染或解析
 */
export async function getMcpDocs(): Promise<McpDocs> {
  return invoke<McpDocs>('mcp_docs')
}

/**
 * RemoteAgentStatus 是「只装了 agent 的远端机器」上单个连接器的接入状态。
 *
 * 与本机 `AgentConnectorState` 的关键差异：
 *   - `cli_present` 来自目标机 detect 端点的 PATH 查找，不是桌面机本地扫描
 *   - `mcp_installed` 只有在 command/args/SUPERDEV_AGENT_URL 三者与**这台机器自己
 *     的 agent** 完全一致时才为 true；`mcp_command`/`agent_url` 非空但
 *     `mcp_installed` 为 false 表示「装了但指向别处」，不是「没装」
 *   - `remote_supported` 为 false 时，`mcp_installed`/`skill_installed`/
 *     `hook_installed` 都只是占位 false——真实语义是「查不到」，不是「没装」。
 *     该字段为 false 的连接器（openclaw / grok）依赖目标机本地 CLI 进程写配置，
 *     远端只提供受限文件端点，不提供远程执行原语
 *   - `status_error` 非空时同样是「查不到」：目标机问到了，但配置读回来是坏的
 *     （解析失败 / 403 / 传输中断），三个状态位仍只是占位 false
 */
export interface RemoteAgentStatus {
  connector_id: ConnectorId
  display_name: string
  cli_present: boolean
  mcp_installed: boolean
  mcp_command?: string | null
  agent_url?: string | null
  skill_installed: boolean
  hook_installed: boolean
  remote_supported: boolean
  /**
   * status_error 是「状态没读出来」的原因，读成功时为 null。
   *
   * 必须与 `remote_supported=false` / `cli_present=false` 同等对待——三者都表示
   * 三个布尔状态位是占位值而非事实，渲染成「未配置」就是把「没查到」说成
   * 「查过、真没有」。
   */
  status_error?: string | null
}

/**
 * detectRemoteCodingAgents 探测目标机上已安装的编程智能体及其 SuperDev 接入状态。
 *
 * 参数：
 *   - hostId: 目标机器在本机 agent 里的注册 ID
 *
 * 返回：
 *   - 每个内置连接器在目标机上的 CLI 存在性与三项接入状态
 *
 * 注意：
 *   - 只读操作；失败时（目标机不可达 / detect 响应无法解析等）会 reject，
 *     调用方需要向用户解释「查不到」而非把异常吞掉渲染成空列表
 */
export async function detectRemoteCodingAgents(hostId: string): Promise<RemoteAgentStatus[]> {
  return invoke<RemoteAgentStatus[]>('detect_remote_coding_agents', { hostId })
}

/**
 * installRemoteAgentConnector 在目标机上安装单个连接器的 MCP + skill + hook。
 *
 * 参数：
 *   - hostId: 目标机器 ID
 *   - connectorId: 待安装的连接器 ID
 *
 * 返回：
 *   - 与本机同构的连接器操作结果（MCP / skill / session hook 三项）
 *
 * 注意：
 *   - `remote_supported=false` 的连接器会显式 reject 并点名 host 与 connector，
 *     不会静默写出半套配置；调用前应先用 `remote_supported` 禁用入口按钮
 */
export async function installRemoteAgentConnector(hostId: string, connectorId: ConnectorId): Promise<ConnectorOperationOutcome> {
  return invoke<ConnectorOperationOutcome>('install_remote_agent_connector', { hostId, connectorId })
}

/**
 * uninstallRemoteAgentConnector 从目标机移除单个连接器的 SuperDev 接入。
 *
 * 参数与返回语义同 `installRemoteAgentConnector`；只删除 SuperDev 自己写入的部分。
 */
export async function uninstallRemoteAgentConnector(hostId: string, connectorId: ConnectorId): Promise<ConnectorOperationOutcome> {
  return invoke<ConnectorOperationOutcome>('uninstall_remote_agent_connector', { hostId, connectorId })
}
