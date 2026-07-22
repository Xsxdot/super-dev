/**
 * Dynamic Connector settings tests.
 *
 * Responsibility: verify shared summaries, operation gating, grouping, and manual entry.
 * Boundary: Tauri and filesystem operations are mocked.
 */
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ask } from '@tauri-apps/plugin-dialog'
import McpManagerTab from '@/components/Settings/McpManagerTab.vue'
import { installTestI18n } from '@/test-utils/i18n'
import * as api from '@/api/mcpInstall'
import type { AgentConnectorSummary, ConnectorOperationOutcome, McpDocs } from '@/api/mcpInstall'

vi.mock('@tauri-apps/plugin-dialog', () => ({ ask: vi.fn() }))
vi.mock('@/api/mcpInstall', async (importOriginal) => {
  const original = await importOriginal<typeof import('@/api/mcpInstall')>()
  return {
    ...original,
    listAgentConnectors: vi.fn(),
    installAgentConnector: vi.fn(),
    updateAgentConnector: vi.fn(),
    verifyAgentConnector: vi.fn(),
    uninstallAgentConnector: vi.fn(),
    getAgentConnectorManualInstructions: vi.fn(),
    getGenericMcpConnectionMaterial: vi.fn(),
    getMcpDocs: vi.fn(),
  }
})

function summary(id: string, detected: boolean, configured: boolean, builtIn = true): AgentConnectorSummary {
  const allAutomatic = id !== 'manual-limited'
  return {
    descriptor: {
      id,
      display_name: id === 'fixture-json-agent' ? 'Fixture JSON Agent' : id === 'codex' ? 'Codex' : id,
      built_in: builtIn,
      platforms: ['macos'],
      support_level: allAutomatic ? 'standard' : 'manual_limited',
      integrations: [
        { capability: 'mcp', support: allAutomatic ? 'automatic' : 'manual' },
        { capability: 'skill', support: 'unsupported' },
        { capability: 'session_hook', support: 'unsupported' },
      ],
      operations: ['install', 'update', 'verify', 'uninstall'].map(operation => ({
        operation: operation as 'install' | 'update' | 'verify' | 'uninstall',
        support: allAutomatic ? 'automatic' as const : 'manual' as const,
      })),
    },
    state: {
      detected,
      detection_path: detected ? `/bin/${id}` : null,
      integrations: [
        { capability: 'mcp', status: configured ? 'configured' : 'missing', target_path: `/config/${id}` },
        { capability: 'skill', status: 'missing' },
        { capability: 'session_hook', status: 'missing' },
      ],
      requires_restart: false,
      message: null,
      mcp_command: configured ? '/app/superdev-mcp' : null,
      agent_url: configured ? 'http://127.0.0.1:57017' : null,
    },
  }
}

const summaries = [
  summary('codex', true, true),
  summary('fixture-json-agent', true, false, false),
  summary('claude-code', false, false),
  summary('manual-limited', true, false, false),
]

const operationOutcome: ConnectorOperationOutcome = {
  connector_id: 'codex', operation: 'update', result: 'unchanged', requires_restart: false,
  integrations: [
    { capability: 'mcp', result: 'already_present' },
    { capability: 'skill', result: 'unsupported' },
    { capability: 'session_hook', result: 'unsupported' },
  ],
}

const docs: McpDocs = {
  summary_sections: [{
    id: 'logs', title: '日志与诊断', description: '读取日志',
    tools: [{ name: 'tail_logs', purpose: '查看近期日志', access: '读', reference: 'references/log-tools.md' }],
  }],
  documents: [
    { id: 'skill', title: 'SKILL.md', path: 'SKILL.md', content: '# SuperDev MCP 使用指南' },
    { id: 'references/log-tools.md', title: 'log-tools.md', path: 'references/log-tools.md', content: '# Log Tools\n`tail_logs`' },
  ],
}

async function mountTab() {
  const wrapper = mount(McpManagerTab, { global: { plugins: [installTestI18n('zh-CN')] } })
  await flushPromises()
  return wrapper
}

describe('McpManagerTab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.listAgentConnectors).mockResolvedValue(summaries)
    vi.mocked(api.getMcpDocs).mockResolvedValue(docs)
    vi.mocked(api.installAgentConnector).mockResolvedValue({ ...operationOutcome, connector_id: 'fixture-json-agent', operation: 'install', result: 'success' })
    vi.mocked(api.updateAgentConnector).mockResolvedValue(operationOutcome)
    vi.mocked(api.verifyAgentConnector).mockResolvedValue({ ...operationOutcome, operation: 'verify', result: 'success', message: 'Configuration verified' })
    vi.mocked(api.uninstallAgentConnector).mockResolvedValue({ ...operationOutcome, operation: 'uninstall', result: 'success' })
    vi.mocked(api.getAgentConnectorManualInstructions).mockResolvedValue({
      summary: 'Configure Codex manually', steps: ['Open settings'], config_path: '/config/codex',
      manual_config: '[mcp_servers.superdev]\ncommand = "/app/superdev-mcp"',
    })
    vi.mocked(ask).mockResolvedValue(true)
  })

  it('renders detected open IDs first and collapses undetected built-ins', async () => {
    const wrapper = await mountTab()

    expect(wrapper.text()).toContain('Codex')
    expect(wrapper.text()).toContain('Fixture JSON Agent')
    expect(wrapper.find('[data-test="mcp-support-level-fixture-json-agent"]').text()).toBe('standard')
    expect(wrapper.text()).not.toContain('claude-code')
    expect(wrapper.find('[data-test="mcp-generic-manual-card"]').exists()).toBe(true)

    await wrapper.find('[data-test="mcp-toggle-other-builtins"]').trigger('click')
    expect(wrapper.text()).toContain('claude-code')
  })

  it('renders eight production connectors from registry data without a whitelist', async () => {
    const eight = [
      summary('claude-code', true, false),
      summary('codex', true, true),
      summary('cursor', false, false),
      summary('opencode', true, false),
      summary('openclaw', false, false),
      summary('hermes', true, false),
      summary('kimi-code', false, false),
      summary('grok', true, false),
    ].map((item, index) => {
      const names = ['Claude Code', 'Codex', 'Cursor', 'OpenCode', 'OpenClaw', 'Hermes', 'Kimi Code', 'Grok']
      const levels = ['full', 'full', 'full', 'standard', 'standard', 'full', 'standard', 'full'] as const
      item.descriptor.display_name = names[index]
      item.descriptor.support_level = levels[index]
      if (levels[index] === 'standard') {
        item.descriptor.integrations = [
          { capability: 'mcp', support: 'automatic' },
          { capability: 'skill', support: 'automatic' },
          { capability: 'session_hook', support: 'manual' },
        ]
      }
      return item
    })
    expect(eight).toHaveLength(8)
    vi.mocked(api.listAgentConnectors).mockResolvedValue(eight)
    const wrapper = await mountTab()

    // Detected connectors visible; labels come from descriptor.display_name.
    expect(wrapper.text()).toContain('Claude Code')
    expect(wrapper.text()).toContain('OpenCode')
    expect(wrapper.text()).toContain('Hermes')
    expect(wrapper.text()).toContain('Grok')
    expect(wrapper.find('[data-test="mcp-support-level-opencode"]').text()).toBe('standard')
    expect(wrapper.find('[data-test="mcp-support-level-hermes"]').text()).toBe('full')
    expect(wrapper.find('[data-test="mcp-support-level-grok"]').text()).toBe('full')
    expect(wrapper.find('[data-test="mcp-install-opencode"]').exists()).toBe(true)

    await wrapper.find('[data-test="mcp-toggle-other-builtins"]').trigger('click')
    expect(wrapper.text()).toContain('OpenClaw')
    expect(wrapper.text()).toContain('Kimi Code')
    expect(wrapper.find('[data-test="mcp-support-level-kimi-code"]').text()).toBe('standard')
  })

  it('renders runtime mcp command and agent url without treating success messages as warnings', async () => {
    const wrapper = await mountTab()

    const codexCard = wrapper.find('[data-test="mcp-install-codex"]').element.closest('article')
    expect(codexCard?.textContent).toContain('/app/superdev-mcp')
    expect(codexCard?.textContent).toContain('http://127.0.0.1:57017')
    expect(wrapper.findAll('[data-test="mcp-state-message"]')).toHaveLength(0)
    // 已配置不再永久展示重启告警；重启提示只来自写操作 outcome。
    expect(wrapper.findAll('[data-test="mcp-restart-hint"]')).toHaveLength(0)
  })

  it('preserves working mcp state for partial kimi-code results with needs_action hook', async () => {
    const kimiMissing = summary('kimi-code', true, false)
    kimiMissing.descriptor.display_name = 'Kimi Code'
    kimiMissing.descriptor.support_level = 'standard'
    const kimiConfigured = summary('kimi-code', true, true)
    kimiConfigured.descriptor.display_name = 'Kimi Code'
    kimiConfigured.descriptor.support_level = 'standard'
    kimiConfigured.state.integrations = [
      { capability: 'mcp', status: 'configured', target_path: '/config/kimi-code', message: 'MCP ready' },
      { capability: 'skill', status: 'configured', target_path: '/skills/superdev' },
      { capability: 'session_hook', status: 'needs_action', message: 'Hook needs manual setup' },
    ]
    const partialOutcome: ConnectorOperationOutcome = {
      connector_id: 'kimi-code',
      operation: 'install',
      result: 'partial',
      requires_restart: true,
      integrations: [
        { capability: 'mcp', result: 'installed', message: 'MCP ready' },
        { capability: 'skill', result: 'installed' },
        { capability: 'session_hook', result: 'needs_action', message: 'Hook needs manual setup' },
      ],
      message: 'Partial: MCP works',
      manual_instructions: {
        summary: 'Finish hook manually',
        steps: ['Configure hook'],
        manual_config: '{"mcpServers":{"superdev":{}}}',
      },
    }
    vi.mocked(api.listAgentConnectors)
      .mockResolvedValueOnce([kimiMissing])
      .mockResolvedValue([kimiConfigured])
    vi.mocked(api.installAgentConnector).mockResolvedValue(partialOutcome)
    const wrapper = await mountTab()
    await wrapper.find('[data-test="mcp-install-kimi-code"]').trigger('click')
    await flushPromises()

    // Install still runs for partial (MCP succeeded); refresh shows configured MCP + needs_action hook.
    expect(api.installAgentConnector).toHaveBeenCalledWith('kimi-code', null)
    expect(wrapper.find('[data-test="mcp-operation-message-kimi-code"]').text()).toContain('部分完成')
    expect(wrapper.find('[data-test="mcp-operation-message-kimi-code"]').classes()).toContain('settings-alert-warning')
    expect(wrapper.find('[data-test="mcp-restart-hint"]').exists()).toBe(true)
    expect(wrapper.text()).toMatch(/configured|已配置|MCP ready/i)
    expect(wrapper.text()).toMatch(/needs_action|Hook needs manual|手动/i)

    // MCP 已配置后按钮走 update，必须把 prior partial outcome 传给 Registry 做增量重试。
    vi.mocked(api.updateAgentConnector).mockResolvedValue(partialOutcome)
    await wrapper.find('[data-test="mcp-install-kimi-code"]').trigger('click')
    await flushPromises()
    expect(api.updateAgentConnector).toHaveBeenLastCalledWith('kimi-code', partialOutcome)
  })

  it('uses update for configured MCP and install for missing MCP', async () => {
    const wrapper = await mountTab()

    await wrapper.find('[data-test="mcp-install-codex"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="mcp-install-fixture-json-agent"]').trigger('click')
    await flushPromises()

    expect(api.updateAgentConnector).toHaveBeenCalledWith('codex', null)
    expect(api.installAgentConnector).toHaveBeenCalledWith('fixture-json-agent', null)
    expect(api.listAgentConnectors).toHaveBeenCalledTimes(3)
  })

  it('surfaces failed install outcomes instead of claiming updated', async () => {
    vi.mocked(api.installAgentConnector).mockResolvedValue({
      connector_id: 'fixture-json-agent',
      operation: 'install',
      result: 'failed',
      requires_restart: false,
      integrations: [
        { capability: 'mcp', result: 'failed', message: 'config corrupt' },
        { capability: 'skill', result: 'skipped' },
        { capability: 'session_hook', result: 'skipped' },
      ],
      manual_instructions: {
        summary: 'Paste the manual config',
        steps: ['Open Agent settings'],
        manual_config: '{"mcpServers":{}}',
      },
    })
    const wrapper = await mountTab()
    await wrapper.find('[data-test="mcp-install-fixture-json-agent"]').trigger('click')
    await flushPromises()

    const message = wrapper.find('[data-test="mcp-operation-message-fixture-json-agent"]')
    expect(message.text()).toContain('安装失败')
    expect(message.text()).toContain('Paste the manual config')
    expect(message.classes()).toContain('settings-alert-danger')
    expect(message.text()).not.toContain('已更新')
  })

  it('gates every automatic operation from the descriptor', async () => {
    const wrapper = await mountTab()

    expect(wrapper.find('[data-test="mcp-install-manual-limited"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-test="mcp-verify-manual-limited"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-test="mcp-uninstall-manual-limited"]').attributes('disabled')).toBeDefined()

    await wrapper.find('[data-test="mcp-verify-codex"]').trigger('click')
    await flushPromises()
    expect(api.verifyAgentConnector).toHaveBeenCalledWith('codex')
  })

  it('confirms precise uninstall and refreshes the shared summary', async () => {
    const wrapper = await mountTab()

    await wrapper.find('[data-test="mcp-uninstall-codex"]').trigger('click')
    await flushPromises()

    expect(ask).toHaveBeenCalled()
    expect(api.uninstallAgentConnector).toHaveBeenCalledWith('codex')
    expect(api.listAgentConnectors).toHaveBeenCalledTimes(2)
  })

  it('shows connector-provided manual instructions', async () => {
    const wrapper = await mountTab()

    await wrapper.find('[data-test="mcp-manual-codex"]').trigger('click')
    await flushPromises()

    expect(api.getAgentConnectorManualInstructions).toHaveBeenCalledWith('codex')
    expect(wrapper.text()).toContain('[mcp_servers.superdev]')
  })

  it('reuses the local/cloud manual dialog for any other MCP Agent', async () => {
    const wrapper = await mountTab()

    await wrapper.find('[data-test="mcp-open-generic-manual"]').trigger('click')
    await wrapper.find('[data-test="manual-env-cloud"]').trigger('click')

    expect(wrapper.find('[data-test="manual-cloud-limit"]').text()).toContain('Remote MCP Gateway')
    expect(wrapper.find('[data-test="manual-config"]').exists()).toBe(false)
  })

  it('retains shared capability docs below the dynamic list', async () => {
    const wrapper = await mountTab()
    expect(wrapper.text()).toContain('日志与诊断')
    expect(wrapper.text()).toContain('tail_logs')

    await wrapper.find('[data-test="mcp-doc-references/log-tools.md"]').trigger('click')
    expect(wrapper.find('[data-test="mcp-doc-content"]').text()).toContain('# Log Tools')
  })
})
