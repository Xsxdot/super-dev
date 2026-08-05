/**
 * Dynamic Connector settings tests.
 *
 * Responsibility: verify shared summaries, operation gating, grouping, manual entry,
 * and the remote machine dimension (detect/install/uninstall on agent-only hosts).
 * Boundary: Tauri, filesystem, and agents-store network calls are mocked.
 */
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ask } from '@tauri-apps/plugin-dialog'
import McpManagerTab from '@/components/Settings/McpManagerTab.vue'
import { installTestI18n } from '@/test-utils/i18n'
import { useAgentsStore } from '@/stores/agents'
import type { AgentDTO } from '@/api/agent'
import * as api from '@/api/mcpInstall'
import type { AgentConnectorSummary, ConnectorOperationOutcome, McpDocs, RemoteAgentStatus } from '@/api/mcpInstall'

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
    detectRemoteCodingAgents: vi.fn(),
    installRemoteAgentConnector: vi.fn(),
    uninstallRemoteAgentConnector: vi.fn(),
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

// remoteHost builds a minimal AgentDTO fixture for the machine picker; only
// host_id/host_name/runtime.reachable matter to the picker and its online badge.
function remoteHost(hostId: string, hostName: string, reachable = true): AgentDTO {
  return {
    host_id: hostId,
    host_name: hostName,
    tags: [],
    transport: { chain: [] },
    config: { listen_address: '127.0.0.1', listen_port: 57017 },
    runtime: { installed: true, health: reachable ? 'healthy' : 'unreachable', reachable },
    security: { token_configured: true, provision_state: 'configured', tls: { mode: 'auto' } },
    updated_at: '2026-08-04T00:00:00Z',
  }
}

// remoteStatus builds a RemoteAgentStatus fixture with sane defaults so each test
// only overrides the field(s) it actually cares about.
function remoteStatus(overrides: Partial<RemoteAgentStatus> & { connector_id: string }): RemoteAgentStatus {
  return {
    display_name: overrides.connector_id,
    cli_present: true,
    mcp_installed: false,
    mcp_command: null,
    agent_url: null,
    skill_installed: false,
    hook_installed: false,
    remote_supported: true,
    ...overrides,
  }
}

async function mountTab() {
  const wrapper = mount(McpManagerTab, { global: { plugins: [installTestI18n('zh-CN')] } })
  await flushPromises()
  return wrapper
}

describe('McpManagerTab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    vi.mocked(api.listAgentConnectors).mockResolvedValue(summaries)
    vi.mocked(api.getMcpDocs).mockResolvedValue(docs)
    vi.mocked(api.installAgentConnector).mockResolvedValue({ ...operationOutcome, connector_id: 'fixture-json-agent', operation: 'install', result: 'success' })
    vi.mocked(api.updateAgentConnector).mockResolvedValue(operationOutcome)
    vi.mocked(api.verifyAgentConnector).mockResolvedValue({ ...operationOutcome, operation: 'verify', result: 'success', message: 'Configuration verified' })
    vi.mocked(api.uninstallAgentConnector).mockResolvedValue({ ...operationOutcome, operation: 'uninstall', result: 'success' })
    // agentsStore.loadAgents hits a real HTTP endpoint in production; stub it so the
    // machine picker's data source is fully test-controlled (mirrors AgentManagerTab.test.ts).
    vi.spyOn(useAgentsStore(), 'loadAgents').mockResolvedValue(undefined)
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

  describe('remote machine dimension', () => {
    it('offers the local machine first and lists connected remote hosts with their online state', async () => {
      useAgentsStore().agents = [remoteHost('host-1', 'Box One', true), remoteHost('host-2', 'Box Two', false)]
      const wrapper = await mountTab()

      const options = wrapper.findAll('[data-test="mcp-machine-picker"] option')
      expect(options[0].text()).toContain('本机')
      expect(options.some(o => o.text().includes('Box One') && o.text().includes('在线'))).toBe(true)
      expect(options.some(o => o.text().includes('Box Two') && o.text().includes('离线'))).toBe(true)
    })

    it('calls detect_remote_coding_agents with the selected host id after picking a remote machine', async () => {
      useAgentsStore().agents = [remoteHost('host-1', 'Box One')]
      vi.mocked(api.detectRemoteCodingAgents).mockResolvedValue([remoteStatus({ connector_id: 'codex', display_name: 'Codex' })])
      const wrapper = await mountTab()

      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-1')
      await flushPromises()

      expect(api.detectRemoteCodingAgents).toHaveBeenCalledWith('host-1')
      expect(wrapper.find('[data-test="mcp-remote-row-codex"]').exists()).toBe(true)
    })

    it('disables the row and buttons when the target machine has no CLI for that agent', async () => {
      useAgentsStore().agents = [remoteHost('host-1', 'Box One')]
      vi.mocked(api.detectRemoteCodingAgents).mockResolvedValue([
        remoteStatus({ connector_id: 'cursor', display_name: 'Cursor', cli_present: false }),
      ])
      const wrapper = await mountTab()
      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-1')
      await flushPromises()

      const row = wrapper.find('[data-test="mcp-remote-row-cursor"]')
      expect(row.classes()).toContain('mcp-remote-row-disabled')
      expect(wrapper.find('[data-test="mcp-remote-install-cursor"]').attributes('disabled')).toBeDefined()
      expect(wrapper.find('[data-test="mcp-remote-uninstall-cursor"]').attributes('disabled')).toBeDefined()
      // cli_present=false means detect_remote_agents never made a file request for this
      // connector (remote_install.rs short-circuits before reading config) — the three
      // status booleans are exactly as much a placeholder here as remote_supported=false.
      // Rendering "not configured"/command text would claim a real read that never happened.
      expect(wrapper.find('[data-test="mcp-remote-cli-missing-cursor"]').exists()).toBe(true)
      expect(wrapper.find('[data-test="mcp-remote-command-cursor"]').exists()).toBe(false)
      expect(wrapper.find('[data-test="mcp-remote-mcp-status-cursor"]').exists()).toBe(false)
    })

    it('renders a status-unavailable notice instead of "not configured" when the status read failed', async () => {
      useAgentsStore().agents = [remoteHost('host-1', 'Box One')]
      vi.mocked(api.detectRemoteCodingAgents).mockResolvedValue([
        remoteStatus({
          connector_id: 'claude-code',
          display_name: 'Claude Code',
          status_error: 'config parse failed: expected value at line 1 column 3',
        }),
      ])
      const wrapper = await mountTab()
      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-1')
      await flushPromises()

      // Same discipline as the cli_present=false and remote_supported=false branches:
      // the three status booleans are placeholders, so the status pills and detail grid
      // must not render — showing "not configured" would claim a read that came back broken.
      const notice = wrapper.find('[data-test="mcp-remote-status-error-claude-code"]')
      expect(notice.exists()).toBe(true)
      expect(notice.text()).toContain('config parse failed')
      expect(wrapper.find('[data-test="mcp-remote-mcp-status-claude-code"]').exists()).toBe(false)
      expect(wrapper.find('[data-test="mcp-remote-command-claude-code"]').exists()).toBe(false)
      // Install stays enabled: retrying is exactly what the user should be able to do,
      // and install re-reports the same error if it persists.
      expect(wrapper.find('[data-test="mcp-remote-install-claude-code"]').attributes('disabled')).toBeUndefined()
    })

    it('keeps rendering the real status grid when status_error is absent', async () => {
      useAgentsStore().agents = [remoteHost('host-1', 'Box One')]
      vi.mocked(api.detectRemoteCodingAgents).mockResolvedValue([
        remoteStatus({ connector_id: 'claude-code', display_name: 'Claude Code', mcp_installed: true }),
      ])
      const wrapper = await mountTab()
      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-1')
      await flushPromises()

      // Guards the other direction: the new branch must not swallow healthy rows.
      expect(wrapper.find('[data-test="mcp-remote-status-error-claude-code"]').exists()).toBe(false)
      expect(wrapper.find('[data-test="mcp-remote-mcp-status-claude-code"]').exists()).toBe(true)
    })

    it('disables install/uninstall and explains local-only setup when remote_supported is false', async () => {
      useAgentsStore().agents = [remoteHost('host-1', 'Box One')]
      vi.mocked(api.detectRemoteCodingAgents).mockResolvedValue([
        remoteStatus({ connector_id: 'grok', display_name: 'Grok', remote_supported: false }),
      ])
      const wrapper = await mountTab()
      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-1')
      await flushPromises()

      const row = wrapper.find('[data-test="mcp-remote-row-grok"]')
      expect(row.classes()).toContain('mcp-remote-row-disabled')
      expect(wrapper.find('[data-test="mcp-remote-install-grok"]').attributes('disabled')).toBeDefined()
      expect(wrapper.find('[data-test="mcp-remote-uninstall-grok"]').attributes('disabled')).toBeDefined()
      const notice = wrapper.find('[data-test="mcp-remote-unsupported-grok"]')
      expect(notice.exists()).toBe(true)
      expect(notice.text()).toContain('本地')
      // The three status booleans are placeholders when unsupported ("can't tell"), not "not installed" —
      // the row must not render an mcp/skill/hook detail grid that would misleadingly imply real status.
      expect(wrapper.find('[data-test="mcp-remote-command-grok"]').exists()).toBe(false)
    })

    it('renders "installed but pointing elsewhere" instead of a plain not-installed state', async () => {
      useAgentsStore().agents = [remoteHost('host-1', 'Box One')]
      vi.mocked(api.detectRemoteCodingAgents).mockResolvedValue([
        remoteStatus({
          connector_id: 'codex', display_name: 'Codex',
          mcp_installed: false, mcp_command: '/old/superdev-mcp', agent_url: 'http://127.0.0.1:57017',
        }),
        remoteStatus({ connector_id: 'cursor', display_name: 'Cursor', mcp_installed: false, mcp_command: null, agent_url: null }),
      ])
      const wrapper = await mountTab()
      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-1')
      await flushPromises()

      const misdirected = wrapper.find('[data-test="mcp-remote-misdirected-codex"]')
      expect(misdirected.exists()).toBe(true)
      expect(misdirected.text()).toContain('/old/superdev-mcp')
      expect(misdirected.text()).toContain('http://127.0.0.1:57017')
      expect(wrapper.find('[data-test="mcp-remote-install-codex"]').text()).toBe('修正指向')
      // The status pill itself must say "installed elsewhere", not just the alert/button —
      // a user scanning only the pill row should not read this as a plain red "not configured".
      expect(wrapper.find('[data-test="mcp-remote-mcp-status-codex"]').text()).toContain('已装 · 指向别处')

      // Plain missing (no prior entry at all) must stay visually/textually distinct.
      expect(wrapper.find('[data-test="mcp-remote-misdirected-cursor"]').exists()).toBe(false)
      expect(wrapper.find('[data-test="mcp-remote-install-cursor"]').text()).not.toBe('修正指向')
      expect(wrapper.find('[data-test="mcp-remote-mcp-status-cursor"]').text()).not.toContain('已装 · 指向别处')
    })

    it('shows an error message instead of a blank list when detect_remote_coding_agents rejects, and clears rows left over from a previous host', async () => {
      useAgentsStore().agents = [remoteHost('host-1', 'Box One'), remoteHost('host-2', 'Box Two')]
      // host-1 succeeds first (so remoteStatuses is non-empty going in) — that way the
      // "no codex row after the error" assertion below actually exercises the catch
      // branch's cleanup instead of trivially holding because nothing was ever fetched.
      vi.mocked(api.detectRemoteCodingAgents)
        .mockResolvedValueOnce([remoteStatus({ connector_id: 'codex', display_name: 'Codex' })])
        .mockRejectedValueOnce(new Error('agent unreachable'))
      const wrapper = await mountTab()
      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-1')
      await flushPromises()
      expect(wrapper.find('[data-test="mcp-remote-row-codex"]').exists()).toBe(true)

      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-2')
      await flushPromises()

      const error = wrapper.find('[data-test="mcp-remote-error"]')
      expect(error.exists()).toBe(true)
      expect(error.text()).toContain('agent unreachable')
      expect(wrapper.find('[data-test="mcp-remote-row-codex"]').exists()).toBe(false)
    })

    it('clears the previous host\'s rows and operation messages the instant the picker changes, before the new detect resolves', async () => {
      useAgentsStore().agents = [remoteHost('host-1', 'Box One'), remoteHost('host-2', 'Box Two')]
      vi.mocked(api.detectRemoteCodingAgents).mockResolvedValueOnce([remoteStatus({ connector_id: 'codex', display_name: 'Codex' })])
      vi.mocked(api.installRemoteAgentConnector).mockResolvedValue({ ...operationOutcome, connector_id: 'codex', operation: 'install', result: 'success' })
      const wrapper = await mountTab()
      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-1')
      await flushPromises()

      // Produce a real operation-result message on host-1 (keyed only by connector_id).
      vi.mocked(api.detectRemoteCodingAgents).mockResolvedValue([remoteStatus({ connector_id: 'codex', display_name: 'Codex', mcp_installed: true })])
      await wrapper.find('[data-test="mcp-remote-install-codex"]').trigger('click')
      await flushPromises()
      expect(wrapper.find('[data-test="mcp-remote-operation-message-codex"]').exists()).toBe(true)

      // Switch to host-2 with its detect deliberately left pending, to prove host-1's
      // codex row/message don't linger under the host-2 label during the tunnel round trip.
      let resolveHostTwoDetect: (value: RemoteAgentStatus[]) => void = () => {}
      vi.mocked(api.detectRemoteCodingAgents).mockReturnValueOnce(new Promise((resolve) => { resolveHostTwoDetect = resolve }))
      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-2')
      await flushPromises()

      expect(wrapper.find('[data-test="mcp-remote-row-codex"]').exists()).toBe(false)
      expect(wrapper.find('[data-test="mcp-remote-operation-message-codex"]').exists()).toBe(false)

      resolveHostTwoDetect([remoteStatus({ connector_id: 'codex', display_name: 'Codex' })])
      await flushPromises()
      expect(api.detectRemoteCodingAgents).toHaveBeenLastCalledWith('host-2')
      expect(wrapper.find('[data-test="mcp-remote-row-codex"]').exists()).toBe(true)
      // The row is host-2's fresh (unconfigured) codex, not a resurrection of host-1's message.
      expect(wrapper.find('[data-test="mcp-remote-operation-message-codex"]').exists()).toBe(false)
    })

    it('does not leave host-2\'s identical connector row disabled by a still-pending host-1 operation', async () => {
      useAgentsStore().agents = [remoteHost('host-1', 'Box One'), remoteHost('host-2', 'Box Two')]
      vi.mocked(api.detectRemoteCodingAgents).mockResolvedValue([remoteStatus({ connector_id: 'codex', display_name: 'Codex' })])
      // host-1's install deliberately never resolves within this test — proves the
      // "operation in flight" disabled state doesn't leak across the machine switch
      // while it's still pending (remoteOperationAgent has no host_id in its key).
      let resolveInstall: (value: ConnectorOperationOutcome) => void = () => {}
      vi.mocked(api.installRemoteAgentConnector).mockReturnValueOnce(new Promise((resolve) => { resolveInstall = resolve }))
      const wrapper = await mountTab()
      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-1')
      await flushPromises()

      await wrapper.find('[data-test="mcp-remote-install-codex"]').trigger('click')
      await flushPromises()
      // Sanity: host-1's own codex row is correctly disabled while its install is pending.
      expect(wrapper.find('[data-test="mcp-remote-install-codex"]').attributes('disabled')).toBeDefined()

      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-2')
      await flushPromises()

      // host-2's codex row must NOT be disabled by host-1's still-in-flight install.
      expect(wrapper.find('[data-test="mcp-remote-install-codex"]').attributes('disabled')).toBeUndefined()
      expect(wrapper.find('[data-test="mcp-remote-uninstall-codex"]').attributes('disabled')).toBeUndefined()

      // Resolving the stale install afterwards must not write anything onto host-2's row.
      resolveInstall({ ...operationOutcome, connector_id: 'codex', operation: 'install', result: 'success' })
      await flushPromises()
      expect(wrapper.find('[data-test="mcp-remote-operation-message-codex"]').exists()).toBe(false)
      expect(wrapper.find('[data-test="mcp-remote-install-codex"]').attributes('disabled')).toBeUndefined()
    })

    it('discards a stale detect response that resolves after the user has already switched to a different host', async () => {
      // Two independently-controlled pending promises simulate genuine out-of-order
      // network arrival: host-1's request is issued first but resolves last, after
      // host-2 is already selected. Asserting only the final state would not catch a
      // missing race guard — this property only shows itself mid-flight.
      useAgentsStore().agents = [remoteHost('host-1', 'Box One'), remoteHost('host-2', 'Box Two')]
      let resolveHostOne: (value: RemoteAgentStatus[]) => void = () => {}
      let resolveHostTwo: (value: RemoteAgentStatus[]) => void = () => {}
      vi.mocked(api.detectRemoteCodingAgents)
        .mockReturnValueOnce(new Promise((resolve) => { resolveHostOne = resolve }))
        .mockReturnValueOnce(new Promise((resolve) => { resolveHostTwo = resolve }))

      const wrapper = await mountTab()
      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-1')
      await flushPromises()
      expect(wrapper.find('[data-test="mcp-remote-row-codex"]').exists()).toBe(false)

      // Switch away before host-1's request comes back; this fires host-2's own
      // (also-pending) detect.
      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-2')
      await flushPromises()

      // host-1's request finally resolves, out of order, after host-2 is selected.
      resolveHostOne([remoteStatus({ connector_id: 'codex', display_name: 'Codex', mcp_installed: true })])
      await flushPromises()
      expect(wrapper.find('[data-test="mcp-remote-row-codex"]').exists()).toBe(false)

      // host-2's own (later-issued) request resolves — this is the data that should render.
      resolveHostTwo([remoteStatus({ connector_id: 'cursor', display_name: 'Cursor' })])
      await flushPromises()

      expect(wrapper.find('[data-test="mcp-remote-row-cursor"]').exists()).toBe(true)
      expect(wrapper.find('[data-test="mcp-remote-row-codex"]').exists()).toBe(false)
      expect(api.detectRemoteCodingAgents).toHaveBeenNthCalledWith(1, 'host-1')
      expect(api.detectRemoteCodingAgents).toHaveBeenNthCalledWith(2, 'host-2')
    })

    it('installs a remote connector with the correct host and connector id and refreshes remote status', async () => {
      useAgentsStore().agents = [remoteHost('host-1', 'Box One')]
      vi.mocked(api.detectRemoteCodingAgents)
        .mockResolvedValueOnce([remoteStatus({ connector_id: 'codex', display_name: 'Codex' })])
        .mockResolvedValue([remoteStatus({
          connector_id: 'codex', display_name: 'Codex', mcp_installed: true,
          mcp_command: '/agent/superdev-mcp', agent_url: 'http://127.0.0.1:58000',
        })])
      vi.mocked(api.installRemoteAgentConnector).mockResolvedValue({ ...operationOutcome, connector_id: 'codex', operation: 'install', result: 'success' })
      const wrapper = await mountTab()
      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-1')
      await flushPromises()

      await wrapper.find('[data-test="mcp-remote-install-codex"]').trigger('click')
      await flushPromises()

      expect(api.installRemoteAgentConnector).toHaveBeenCalledWith('host-1', 'codex', {
        configPathOverride: undefined,
      })
      expect(api.detectRemoteCodingAgents).toHaveBeenCalledTimes(2)
    })

    it('OpenClaw 远端行提供配置路径覆盖输入框，且值随安装请求下发', async () => {
      useAgentsStore().agents = [remoteHost('host-1', 'Box One')]
      vi.mocked(api.detectRemoteCodingAgents).mockResolvedValue([
        remoteStatus({ connector_id: 'openclaw', display_name: 'OpenClaw', remote_supported: true, cli_present: true }),
      ])
      vi.mocked(api.installRemoteAgentConnector).mockResolvedValue({
        ...operationOutcome, connector_id: 'openclaw', operation: 'install', result: 'success',
      })
      const wrapper = await mountTab()
      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-1')
      await flushPromises()

      const input = wrapper.find('[data-test="mcp-remote-config-override-openclaw"]')
      expect(input.exists()).toBe(true)

      await input.setValue('/home/u/.openclaw/openclaw.json')
      await wrapper.find('[data-test="mcp-remote-install-openclaw"]').trigger('click')
      await flushPromises()

      expect(api.installRemoteAgentConnector).toHaveBeenCalledWith(
        'host-1',
        'openclaw',
        { configPathOverride: '/home/u/.openclaw/openclaw.json' },
      )
    })

    it('其他连接器不显示配置路径覆盖输入框', async () => {
      useAgentsStore().agents = [remoteHost('host-1', 'Box One')]
      vi.mocked(api.detectRemoteCodingAgents).mockResolvedValue([
        remoteStatus({ connector_id: 'grok', display_name: 'Grok', remote_supported: true, cli_present: true }),
      ])
      const wrapper = await mountTab()
      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-1')
      await flushPromises()

      expect(wrapper.find('[data-test="mcp-remote-config-override-grok"]').exists()).toBe(false)
      expect(wrapper.find('[data-test="mcp-remote-config-override-openclaw"]').exists()).toBe(false)
    })

    it('confirms before uninstalling a remote connector and calls the API with host and connector id', async () => {
      useAgentsStore().agents = [remoteHost('host-1', 'Box One')]
      vi.mocked(api.detectRemoteCodingAgents).mockResolvedValue([remoteStatus({
        connector_id: 'codex', display_name: 'Codex', mcp_installed: true,
        mcp_command: '/agent/superdev-mcp', agent_url: 'http://127.0.0.1:58000',
      })])
      vi.mocked(api.uninstallRemoteAgentConnector).mockResolvedValue({ ...operationOutcome, connector_id: 'codex', operation: 'uninstall', result: 'success' })
      const wrapper = await mountTab()
      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-1')
      await flushPromises()

      await wrapper.find('[data-test="mcp-remote-uninstall-codex"]').trigger('click')
      await flushPromises()

      expect(ask).toHaveBeenCalled()
      expect(api.uninstallRemoteAgentConnector).toHaveBeenCalledWith('host-1', 'codex')
    })

    it('returns to the local path when the machine picker is reset to the local machine', async () => {
      useAgentsStore().agents = [remoteHost('host-1', 'Box One')]
      vi.mocked(api.detectRemoteCodingAgents).mockResolvedValue([remoteStatus({ connector_id: 'codex', display_name: 'Codex' })])
      const wrapper = await mountTab()
      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('host-1')
      await flushPromises()
      expect(wrapper.find('[data-test="mcp-remote-row-codex"]').exists()).toBe(true)

      await wrapper.find('[data-test="mcp-machine-picker"]').setValue('')
      await flushPromises()

      expect(wrapper.find('[data-test="mcp-install-codex"]').exists()).toBe(true)
      expect(wrapper.find('[data-test="mcp-remote-row-codex"]').exists()).toBe(false)
      expect(api.listAgentConnectors).toHaveBeenCalledTimes(2)
    })

    it('does not let an empty host_id in the agents store alias the local machine option', async () => {
      // '' is this component's own sentinel for "local machine" (see selectedHostId's
      // comment); a real AgentDTO should never carry an empty host_id, but if one ever
      // does, it must not silently become a second, indistinguishable "本机" entry.
      useAgentsStore().agents = [remoteHost('', 'Ghost'), remoteHost('host-1', 'Box One')]
      const wrapper = await mountTab()

      const options = wrapper.findAll('[data-test="mcp-machine-picker"] option')
      expect(options).toHaveLength(2)
      expect(options.filter(o => o.text().includes('本机'))).toHaveLength(1)
      expect(wrapper.text()).not.toContain('Ghost')
    })

    it('tells the user machine loading failed instead of implying there are no remote hosts', async () => {
      const agents = useAgentsStore()
      vi.mocked(agents.loadAgents).mockImplementation(async () => {
        agents.error = 'network down'
      })
      const wrapper = await mountTab()

      const loadError = wrapper.find('[data-test="mcp-machine-load-error"]')
      expect(loadError.exists()).toBe(true)
      expect(loadError.text()).toContain('network down')
      // The "no remote hosts" hint is a different claim (successfully loaded, list is
      // empty) and must not be shown at the same time as a load failure.
      expect(wrapper.text()).not.toContain('还没有接入的远端机器')
    })
  })
})
