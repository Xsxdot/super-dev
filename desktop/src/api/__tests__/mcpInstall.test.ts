/**
 * MCP install API client 测试。
 *
 * 职责：
 *   - 验证桌面端 MCP Tauri command 调用名称和参数
 *   - 验证新增状态、卸载、文档 API 的类型入口
 *
 * 边界：
 *   - 不调用真实 Tauri command
 *   - 不读写真实 Agent 配置
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { invoke } from '@tauri-apps/api/core'
import {
  detectCodingAgents,
  getMcpDocs,
  getMcpStatus,
  installMcp,
  uninstallMcp,
} from '@/api/mcpInstall'

vi.mock('@tauri-apps/api/core', () => ({
  invoke: vi.fn(),
}))

afterEach(() => {
  vi.clearAllMocks()
})

describe('mcpInstall API', () => {
  it('detects supported coding agents', async () => {
    vi.mocked(invoke).mockResolvedValue([
      { agent: 'codex', installed: true, detection_path: '/usr/local/bin/codex' },
    ])

    const result = await detectCodingAgents()

    expect(result[0].agent).toBe('codex')
    expect(invoke).toHaveBeenCalledWith('detect_coding_agents')
  })

  it('reads MCP status', async () => {
    vi.mocked(invoke).mockResolvedValue([{ agent: 'codex', mcp_configured: true }])

    const result = await getMcpStatus()

    expect(result[0].agent).toBe('codex')
    expect(invoke).toHaveBeenCalledWith('mcp_status')
  })

  it('installs and uninstalls MCP for an agent', async () => {
    vi.mocked(invoke).mockResolvedValueOnce({ agent: 'codex' }).mockResolvedValueOnce({ agent: 'codex' })

    await installMcp('codex')
    await uninstallMcp('codex')

    expect(invoke).toHaveBeenNthCalledWith(1, 'install_mcp', { agent: 'codex' })
    expect(invoke).toHaveBeenNthCalledWith(2, 'uninstall_mcp', { agent: 'codex' })
  })

  it('reads MCP capability and skill docs', async () => {
    vi.mocked(invoke).mockResolvedValue({
      summary_sections: [{ id: 'logs', title: '日志', tools: [] }],
      documents: [{ id: 'skill', title: 'SKILL.md', path: '/tmp/SKILL.md', content: '# SuperDev' }],
    })

    const docs = await getMcpDocs()

    expect(docs.documents[0].title).toBe('SKILL.md')
    expect(invoke).toHaveBeenCalledWith('mcp_docs')
  })
})
