/**
 * onboardingPreview 为 Product Design 与浏览器回归提供确定性的首次启动夹具。
 *
 * 职责：
 *   - 在 Vite 开发环境且显式带 onboardingPreview=1 时提供固定检测与安装结果
 *   - 让同一 Vue 页面可在没有 Tauri runtime 的浏览器中完成视觉和交互验收
 *   - 覆盖七个生产内置 Connector 的 detected / undetected 两条 UI 路径
 *
 * 边界：
 *   - 生产构建永不启用，不持久化设置，也不读写真实 Agent 配置
 *   - 不替代 Tauri、sidecar 或真实 Agent 的运行验收
 *   - 不维护 TypeScript Connector 白名单；展示数据全部来自 descriptor 契约字段
 */
import type {
  AgentConnectorSummary,
  ConnectorOperation,
  ConnectorId,
  ConnectorOperationOutcome,
  GenericMcpConnectionMaterial,
  SupportLevel,
  SupportMode,
} from '@/api/mcpInstall'

const PREVIEW_MCP_COMMAND = '/Applications/SuperDev.app/Contents/MacOS/superdev-mcp'
const PREVIEW_AGENT_URL = 'http://127.0.0.1:57017'

/** isOnboardingPreviewMode 判断当前页面是否显式启用了开发预览夹具。 */
export function isOnboardingPreviewMode(): boolean {
  return Boolean(
    import.meta.env.DEV
    && typeof window !== 'undefined'
    // 夹具只服务纯浏览器视觉验收；Tauri dev 仍走真实 command，避免误写真实设置。
    && !('__TAURI_INTERNALS__' in window)
    && new URLSearchParams(window.location.search).get('onboardingPreview') === '1',
  )
}

type PreviewConnector = {
  id: string
  name: string
  supportLevel: SupportLevel
  hookSupport: SupportMode
  detected: boolean
  detectionPath?: string
}

/**
 * 七个生产内置 ID 的确定性夹具。
 * detected: Claude Code / Codex / OpenCode / Hermes
 * undetected: Cursor / OpenClaw / Kimi Code
 */
const PREVIEW_CONNECTORS: PreviewConnector[] = [
  { id: 'claude-code', name: 'Claude Code', supportLevel: 'full', hookSupport: 'automatic', detected: true, detectionPath: '/usr/local/bin/claude' },
  { id: 'codex', name: 'Codex', supportLevel: 'full', hookSupport: 'automatic', detected: true, detectionPath: '/usr/local/bin/codex' },
  { id: 'cursor', name: 'Cursor', supportLevel: 'full', hookSupport: 'automatic', detected: false },
  { id: 'opencode', name: 'OpenCode', supportLevel: 'standard', hookSupport: 'manual', detected: true, detectionPath: '/usr/local/bin/opencode' },
  { id: 'openclaw', name: 'OpenClaw', supportLevel: 'standard', hookSupport: 'manual', detected: false },
  { id: 'hermes', name: 'Hermes', supportLevel: 'full', hookSupport: 'automatic', detected: true, detectionPath: '/usr/local/bin/hermes' },
  { id: 'kimi-code', name: 'Kimi Code', supportLevel: 'standard', hookSupport: 'manual', detected: false },
]

/** previewConnectorSummaries 返回七个生产 Connector 的确定性摘要。 */
export function previewConnectorSummaries(): AgentConnectorSummary[] {
  const operations: ConnectorOperation[] = ['detect', 'install', 'update', 'status', 'uninstall', 'verify']
  return PREVIEW_CONNECTORS.map((item) => ({
    descriptor: {
      id: item.id,
      display_name: item.name,
      built_in: true,
      platforms: ['macos', 'windows', 'linux'],
      support_level: item.supportLevel,
      integrations: [
        { capability: 'mcp', support: 'automatic' },
        { capability: 'skill', support: 'automatic' },
        { capability: 'session_hook', support: item.hookSupport },
      ],
      operations: operations.map(operation => ({ operation, support: 'automatic' as const })),
    },
    state: {
      detected: item.detected,
      detection_path: item.detectionPath ?? null,
      integrations: [
        { capability: 'mcp', status: 'missing', target_path: null, message: null },
        { capability: 'skill', status: 'missing', target_path: null, message: null },
        { capability: 'session_hook', status: 'missing', target_path: null, message: null },
      ],
      requires_restart: false,
      message: null,
    },
  }))
}

/** previewConnectorOutcome 返回不触碰文件系统的确定性安装结果。 */
export function previewConnectorOutcome(agent: ConnectorId): ConnectorOperationOutcome {
  const root = `/preview/${agent}`
  // Kimi Code：MCP 成功但 Hook 需手动 → overall Partial，保留 working MCP 状态。
  if (agent === 'kimi-code') {
    return {
      connector_id: agent,
      operation: 'install',
      result: 'partial',
      requires_restart: true,
      integrations: [
        { capability: 'mcp', result: 'installed', target_path: `${root}/mcp.json`, backup_path: null, message: 'MCP 已配置' },
        { capability: 'skill', result: 'installed', target_path: `${root}/skills/superdev`, backup_path: null, message: null },
        {
          capability: 'session_hook',
          result: 'needs_action',
          target_path: null,
          backup_path: null,
          message: 'Session Hook 需按连接器指引手动配置后才会生效',
        },
      ],
      manual_instructions: {
        summary: '手动完成 Kimi Code Session Hook',
        steps: ['按文档配置 Hook', '重启 Kimi Code'],
        config_path: `${root}/mcp.json`,
        manual_config: '{"mcpServers":{"superdev":{"command":"/preview/superdev-mcp"}}}',
        verification_prompt: '确认 superdev MCP 可用',
      },
      message: 'Kimi Code 部分完成：MCP 可用，Hook 待手动',
    }
  }
  return {
    connector_id: agent,
    operation: 'install',
    result: 'success',
    requires_restart: false,
    integrations: [
      { capability: 'mcp', result: 'installed', target_path: `${root}/config`, backup_path: null, message: null },
      { capability: 'skill', result: 'installed', target_path: `${root}/skills/superdev`, backup_path: null, message: null },
      { capability: 'session_hook', result: 'installed', target_path: `${root}/hooks.json`, backup_path: null, message: null },
    ],
    manual_instructions: null,
    message: null,
  }
}

/** previewMcpConnectionMaterial 返回手动接入对话框使用的标准 JSON 示例。 */
export function previewMcpConnectionMaterial(): GenericMcpConnectionMaterial {
  return {
    transport: 'stdio',
    command: PREVIEW_MCP_COMMAND,
    agent_url: PREVIEW_AGENT_URL,
    manual_config: JSON.stringify({
      mcpServers: {
        superdev: {
          command: PREVIEW_MCP_COMMAND,
          env: { SUPERDEV_AGENT_URL: PREVIEW_AGENT_URL },
        },
      },
    }, null, 2),
  }
}
