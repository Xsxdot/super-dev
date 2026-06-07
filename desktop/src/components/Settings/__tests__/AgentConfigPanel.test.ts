/**
 * AgentConfigPanel unified lifecycle panel tests.
 *
 * 职责：
 *   - 验证四步 Agent 配置面板替代旧的三个独立弹窗
 *   - 验证监听/TLS 是安装参数的唯一编辑来源
 *   - 验证连接链编辑与探测流程仍通过 agents store
 *
 * 边界：
 *   - 不访问真实 agent HTTP API
 *   - 不测试 AgentManagerTab 行布局
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AgentConfigPanel from '../AgentConfigPanel.vue'
import { useAgentsStore } from '@/stores/agents'
import { installTestI18n } from '@/test-utils/i18n'
import type { AgentDTO, NodeStatus } from '@/api/agent'

function agent(overrides: Partial<AgentDTO> = {}): AgentDTO {
  return {
    host_id: 'h1',
    host_name: 'ali-01',
    tags: [],
    transport: {
      chain: [
        { type: 'direct', direct: { address: '100.64.0.8:57017' } },
        { type: 'tunnel', tunnel: { remote_agent_port: 57017 } },
      ],
    },
    config: { listen_address: '127.0.0.1', listen_port: 57017 },
    runtime: { installed: true, health: 'healthy', reachable: true, version: '0.1.0' },
    security: { token_configured: false, provision_state: 'pending-bootstrap', tls: { mode: 'auto', server_name: 'ali-01' } },
    ...overrides,
  }
}

function node(): NodeStatus {
  return {
    host_id: 'h1',
    name: 'ali-01',
    reachable: true,
    agent: { installed: true, health: 'healthy', reachable: true, version: '0.1.0' },
    deployments: [],
    route: {
      selected_index: 1,
      selected_type: 'tunnel',
      degraded: true,
      last_results: [
        { index: 0, transport_type: 'direct', status: 'unreachable', reachable: false, error: 'connection refused', checked_at: '2026-06-07T10:00:00Z' },
        { index: 1, transport_type: 'tunnel', status: 'reachable', reachable: true, latency_ms: 8, checked_at: '2026-06-07T10:00:01Z' },
      ],
    },
    updated_at: '2026-06-07T10:00:00Z',
  }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.restoreAllMocks()
})

describe('AgentConfigPanel', () => {
  it('opens on the requested default tab', () => {
    const wrapper = mount(AgentConfigPanel, {
      props: { visible: true, agent: agent(), initialTab: 'install' },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="agent-panel-tab-install"]').classes()).toContain('active')
    expect(wrapper.find('[data-test="agent-install-generate"]').exists()).toBe(true)
  })

  it('saves listening and manual TLS config from the security tab', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'updateAgentConfig').mockResolvedValue(agent())
    const wrapper = mount(AgentConfigPanel, {
      props: { visible: true, agent: agent(), initialTab: 'security' },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="agent-tls-mode-manual"]').setValue(true)
    await wrapper.find('[data-test="agent-manual-advanced-toggle"]').trigger('click')
    await wrapper.find('[data-test="agent-server-name"]').setValue('agent.internal')
    await wrapper.find('[data-test="agent-ca-cert"]').setValue('PEM')
    await wrapper.find('[data-test="agent-security-save"]').trigger('click')

    expect(store.updateAgentConfig).toHaveBeenCalledWith('h1', {
      config: { listen_address: '127.0.0.1', listen_port: 57017 },
      security: {
        token_configured: false,
        provision_state: 'pending-bootstrap',
        tls: { mode: 'manual', server_name: 'agent.internal', ca_cert: 'PEM' },
      },
    })
  })

  it('hides manual certificate fields until manual TLS advanced is opened', async () => {
    const wrapper = mount(AgentConfigPanel, {
      props: { visible: true, agent: agent(), initialTab: 'security' },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="agent-server-name"]').exists()).toBe(false)
    await wrapper.find('[data-test="agent-tls-mode-manual"]').setValue(true)
    expect(wrapper.find('[data-test="agent-server-name"]').exists()).toBe(false)
    await wrapper.find('[data-test="agent-manual-advanced-toggle"]').trigger('click')
    expect(wrapper.find('[data-test="agent-server-name"]').exists()).toBe(true)
  })

  it('generates install command from security-tab listen values without exposing duplicate inputs', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'generateInstallCommand').mockResolvedValue({ command: 'curl install', expires_at: '2026-06-07T10:30:00Z', token_id: 'tok_1' })
    const wrapper = mount(AgentConfigPanel, {
      props: { visible: true, agent: agent(), initialTab: 'install' },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="agent-install-bind-address-input"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="agent-install-bind-preview"]').text()).toContain('127.0.0.1:57017')
    await wrapper.find('[data-test="agent-install-controller-url"]').setValue('http://controller:57017')
    await wrapper.find('[data-test="agent-install-generate"]').trigger('click')

    expect(store.generateInstallCommand).toHaveBeenCalledWith('h1', {
      method: 'generated_command',
      controller_url: 'http://controller:57017',
      bind_address: '127.0.0.1',
      remote_agent_port: 57017,
      transport_type: 'direct',
      token_ttl_minutes: 30,
    })
    expect(wrapper.text()).toContain('curl install')
  })

  it('saves ordered transport chain and tests one entry', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'updateAgentTransport').mockResolvedValue(agent())
    vi.spyOn(store, 'testTransport').mockResolvedValue({
      index: 0,
      transport_type: 'direct',
      status: 'reachable',
      reachable: true,
      latency_ms: 7,
      checked_at: '2026-06-07T10:00:00Z',
    })
    const wrapper = mount(AgentConfigPanel, {
      props: { visible: true, agent: agent(), initialTab: 'transport' },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="transport-move-down-0"]').trigger('click')
    await wrapper.find('[data-test="transport-test-0"]').trigger('click')
    expect(store.testTransport).toHaveBeenCalledWith('h1', 0)
    expect(wrapper.text()).toContain('reachable')

    await wrapper.find('[data-test="agent-transport-save"]').trigger('click')
    expect(store.updateAgentTransport).toHaveBeenCalledWith('h1', {
      transport: {
        chain: [
          { type: 'tunnel', tunnel: { remote_agent_port: 57017 } },
          { type: 'direct', direct: { address: '100.64.0.8:57017' } },
        ],
      },
    })
  })

  it('locks probe tab until agent is installed', async () => {
    const wrapper = mount(AgentConfigPanel, {
      props: {
        visible: true,
        agent: agent({ runtime: { installed: false, health: 'unknown', reachable: false } }),
        initialTab: 'probe',
      },
      global: { plugins: [installTestI18n('en-US')] },
    })

    expect(wrapper.find('[data-test="agent-probe-locked"]').exists()).toBe(true)
    await wrapper.find('[data-test="agent-probe-go-install"]').trigger('click')
    expect(wrapper.find('[data-test="agent-panel-tab-install"]').classes()).toContain('active')
  })

  it('runs full probe and refreshes health when installed', async () => {
    const store = useAgentsStore()
    vi.spyOn(store, 'testTransport')
      .mockResolvedValueOnce({ index: 0, transport_type: 'direct', status: 'unreachable', reachable: false, error: 'connection refused', checked_at: '2026-06-07T10:00:00Z' })
      .mockResolvedValueOnce({ index: 1, transport_type: 'tunnel', status: 'reachable', reachable: true, latency_ms: 8, checked_at: '2026-06-07T10:00:01Z' })
    vi.spyOn(store, 'checkAgent').mockResolvedValue(agent())
    const wrapper = mount(AgentConfigPanel, {
      props: { visible: true, agent: agent(), node: node(), initialTab: 'probe' },
      global: { plugins: [installTestI18n('en-US')] },
    })

    await wrapper.find('[data-test="agent-probe-run"]').trigger('click')

    expect(store.testTransport).toHaveBeenCalledWith('h1', 0)
    expect(store.testTransport).toHaveBeenCalledWith('h1', 1)
    expect(store.checkAgent).toHaveBeenCalledWith('h1')
    expect(wrapper.find('[data-test="agent-probe-result-1"]').text()).toContain('reachable')
  })
})
