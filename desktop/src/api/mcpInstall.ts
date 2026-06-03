/**
 * MCP 安装 API 封装。
 *
 * 职责：
 *   - 调用 Tauri install_mcp command
 *   - 统一安装结果类型和支持的智能体类型
 *
 * 边界：
 *   - 不渲染引导界面
 *   - 不读写 agent settings
 */
export type CodingAgent = 'claude-code' | 'codex' | 'cursor'

export interface InstallOutcome {
  installed: boolean
  already_present: boolean
  backup_path?: string | null
  config_path: string
  manual_config: string
}

export interface InstallHint {
  config_path: string
  manual_config: string
}

export async function installMcp(agent: CodingAgent): Promise<InstallOutcome> {
  const { invoke } = await import('@tauri-apps/api/core')
  return invoke<InstallOutcome>('install_mcp', { agent })
}

export async function getMcpInstallHint(agent: CodingAgent): Promise<InstallHint> {
  const { invoke } = await import('@tauri-apps/api/core')
  return invoke<InstallHint>('mcp_install_hint', { agent })
}
