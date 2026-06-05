/**
 * MCP 安装 API 封装。
 *
 * 职责：
 *   - 调用 Tauri install_mcp command
 *   - 调用 Tauri detect_coding_agents command
 *   - 调用 Tauri MCP 状态、卸载和文档 command
 *   - 统一安装结果类型和支持的智能体类型
 *
 * 边界：
 *   - 不渲染引导界面
 *   - 不读写 agent settings
 */
export type CodingAgent = 'claude-code' | 'codex' | 'cursor'

export interface CodingAgentAvailability {
  agent: CodingAgent
  installed: boolean
  detection_path?: string | null
}

export interface SkillInstallOutcome {
  installed: boolean
  already_present: boolean
  target_path: string
  backup_path?: string | null
  error?: string | null
}

export interface InstallOutcome {
  installed: boolean
  already_present: boolean
  agent: CodingAgent
  backup_path?: string | null
  config_path: string
  manual_config: string
  skill: SkillInstallOutcome
}

export interface InstallHint {
  agent: CodingAgent
  config_path: string
  manual_config: string
  skill_target_path: string
}

export interface McpStatus {
  agent: CodingAgent
  agent_installed: boolean
  detection_path?: string | null
  config_path: string
  config_exists: boolean
  mcp_configured: boolean
  mcp_command?: string | null
  agent_url?: string | null
  config_error?: string | null
  skill_path: string
  skill_installed: boolean
  skill_matches_bundled?: boolean | null
  skill_error?: string | null
}

export interface UninstallOutcome {
  agent: CodingAgent
  config_path: string
  removed_config: boolean
  config_backup_path?: string | null
  skill_path: string
  removed_skill: boolean
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

export async function detectCodingAgents(): Promise<CodingAgentAvailability[]> {
  const { invoke } = await import('@tauri-apps/api/core')
  return invoke<CodingAgentAvailability[]>('detect_coding_agents')
}

export async function installMcp(agent: CodingAgent): Promise<InstallOutcome> {
  const { invoke } = await import('@tauri-apps/api/core')
  return invoke<InstallOutcome>('install_mcp', { agent })
}

export async function getMcpInstallHint(agent: CodingAgent): Promise<InstallHint> {
  const { invoke } = await import('@tauri-apps/api/core')
  return invoke<InstallHint>('mcp_install_hint', { agent })
}

/**
 * getMcpStatus 读取所有支持 Agent 的 SuperDev MCP/skill 状态。
 *
 * 返回：
 *   - Agent 检测、配置文件、MCP server 和 skill 状态列表
 *
 * 注意：
 *   - 只调用只读 Tauri command，不修改本地配置
 */
export async function getMcpStatus(): Promise<McpStatus[]> {
  const { invoke } = await import('@tauri-apps/api/core')
  return invoke<McpStatus[]>('mcp_status')
}

/**
 * uninstallMcp 从指定 Agent 移除 SuperDev MCP 配置和 skill。
 *
 * 参数：
 *   - agent: Agent 标识，支持 claude-code、codex、cursor
 *
 * 返回：
 *   - 配置项和 skill 目录的移除结果
 *
 * 注意：
 *   - Tauri command 只删除 superdev MCP server，不删除其他 MCP 配置
 */
export async function uninstallMcp(agent: CodingAgent): Promise<UninstallOutcome> {
  const { invoke } = await import('@tauri-apps/api/core')
  return invoke<UninstallOutcome>('uninstall_mcp', { agent })
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
  const { invoke } = await import('@tauri-apps/api/core')
  return invoke<McpDocs>('mcp_docs')
}
