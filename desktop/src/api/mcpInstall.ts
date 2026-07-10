/**
 * Agent Connector API 封装。
 *
 * 职责：
 *   - 调用 Tauri Connector 列表、安装、更新、验证、卸载和手动指引 command
 *   - 保持 Rust canonical Connector DTO 与 TypeScript 字段一致
 *   - 读取其他本机 stdio MCP Agent 的通用连接材料
 *   - 统一开放 Connector ID 与能力结果类型
 *
 * 边界：
 *   - 不渲染引导界面
 *   - 不读写 agent settings
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
  detected: boolean; detection_path?: string | null; integrations: IntegrationState[]
  requires_restart: boolean; message?: string | null
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
