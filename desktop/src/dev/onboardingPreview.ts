/**
 * onboardingPreview 为 Product Design 与浏览器回归提供确定性的首次启动夹具。
 *
 * 职责：
 *   - 在 Vite 开发环境且显式带 onboardingPreview=1 时提供固定检测与安装结果
 *   - 让同一 Vue 页面可在没有 Tauri runtime 的浏览器中完成视觉和交互验收
 *
 * 边界：
 *   - 生产构建永不启用，不持久化设置，也不读写真实 Agent 配置
 *   - 不替代 Tauri、sidecar 或真实 Agent 的运行验收
 */
import type {
  AgentConnectorSummary,
  ConnectorOperation,
  ConnectorId,
  ConnectorOperationOutcome,
  GenericMcpConnectionMaterial,
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

/** previewConnectorSummaries 返回含未知开放 ID 的确定性 Connector 摘要。 */
export function previewConnectorSummaries(): AgentConnectorSummary[] {
  const operations: ConnectorOperation[] = ['detect', 'install', 'update', 'status', 'uninstall', 'verify']
  const descriptor = (id: string, name: string, builtIn = true): AgentConnectorSummary['descriptor'] => ({
    id, display_name: name, built_in: builtIn, platforms: ['macos'], support_level: 'full',
    integrations: [
      { capability: 'mcp', support: 'automatic' },
      { capability: 'skill', support: 'automatic' },
      { capability: 'session_hook', support: 'automatic' },
    ],
    operations: operations.map(operation => ({ operation, support: 'automatic' })),
  })
  return [
    { descriptor: descriptor('claude-code', 'Claude Code'), state: { detected: true, detection_path: '/usr/local/bin/claude', integrations: [], requires_restart: false } },
    { descriptor: descriptor('codex', 'Codex'), state: { detected: true, detection_path: '/usr/local/bin/codex', integrations: [], requires_restart: false } },
    { descriptor: descriptor('fixture-json-agent', 'Fixture JSON Agent', false), state: { detected: true, detection_path: '/usr/local/bin/fixture-json-agent', integrations: [], requires_restart: false } },
  ]
}

/** previewConnectorOutcome 返回不触碰文件系统的确定性安装结果。 */
export function previewConnectorOutcome(agent: ConnectorId): ConnectorOperationOutcome {
  const root = `/preview/${agent}`
  return {
    connector_id: agent, operation: 'install', result: 'success', requires_restart: false,
    integrations: [
      { capability: 'mcp', result: 'installed', target_path: `${root}/config`, backup_path: null },
      { capability: 'skill', result: 'installed', target_path: `${root}/skills/superdev`, backup_path: null },
      { capability: 'session_hook', result: 'installed', target_path: `${root}/hooks.json`, backup_path: null },
    ],
    manual_instructions: null, message: null,
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
