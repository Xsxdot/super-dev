/**
 * AgentManagerTab 测试一等 Agent 设置页。
 *
 * 职责：
 *   - 验证 Agent 列表展示连接方式和运行态
 *   - 验证生成安装命令入口存在
 *   - 验证新增 Agent 和统一安全配置入口存在
 *
 * 边界：
 *   - 不访问真实 agent HTTP API
 *   - 不打开真实 NodeRegistry WebSocket
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AgentManagerTab from '@/components/Settings/AgentManagerTab.vue'
import { useAgentsStore } from '@/stores/agents'
import { useNodeStore } from '@/stores/node'
import { useRemoteStore } from '@/stores/remote'
import { installTestI18n } from '@/test-utils/i18n'

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

describe('AgentManagerTab', () => {
  it('renders agent runtime from nodeStore and offers generated command action', async () => {
    const agents = useAgentsStore()
    vi.spyOn(agents, 'loadAgents').mockResolvedValue(undefined)
    agents.agents = [{
      host_id: 'h1',
      host_name: 'ali-01',
      tags: ['prod'],
      transport: { chain: [{ type: 'direct', direct: { address: '100.64.0.8:57017' } }] },
      config: { listen_port: 57017 },
      runtime: { installed: false, health: 'unknown', reachable: false },
      security: { token_configured: false, provision_state: 'not-configured', tls: { mode: 'auto' } },
    }]
    const remote = useRemoteStore()
    vi.spyOn(remote, 'loadHosts').mockResolvedValue(undefined)
    const nodes = useNodeStore()
    vi.spyOn(nodes, 'start').mockResolvedValue(undefined)
    nodes.applySnapshot([{
      host_id: 'h1',
      name: 'ali-01',
      reachable: true,
      agent: { installed: true, health: 'healthy', reachable: true, version: '0.1.0' },
      deployments: [],
      updated_at: '2026-06-06T10:00:00Z',
    }])

    const wrapper = mount(AgentManagerTab, { global: { plugins: [installTestI18n()] } })
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('ali-01')
    expect(wrapper.text()).toContain('direct')
    expect(wrapper.text()).toContain('healthy')
    expect(wrapper.find('[data-test="agent-generate-command-h1"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="agent-security-h1"]').exists()).toBe(true)
  })

  it('opens install command modal from install action', async () => {
    const agents = useAgentsStore()
    vi.spyOn(agents, 'loadAgents').mockResolvedValue(undefined)
    agents.agents = [{
      host_id: 'h1',
      host_name: 'ali-01',
      tags: ['prod'],
      transport: { chain: [{ type: 'direct', direct: { address: '100.64.0.8:57017' } }] },
      config: { listen_port: 57017 },
      runtime: { installed: false, health: 'unknown', reachable: false },
      security: { token_configured: false, provision_state: 'not-configured', tls: { mode: 'auto' } },
    }]
    const remote = useRemoteStore()
    vi.spyOn(remote, 'loadHosts').mockResolvedValue(undefined)
    const nodes = useNodeStore()
    vi.spyOn(nodes, 'start').mockResolvedValue(undefined)

    const wrapper = mount(AgentManagerTab, { global: { plugins: [installTestI18n()] } })
    await wrapper.vm.$nextTick()
    await wrapper.find('[data-test="agent-install-h1"]').trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-test="agent-install-generate"]').exists()).toBe(true)
  })

  it('opens create agent modal with hosts that do not yet have an agent', async () => {
    const agents = useAgentsStore()
    vi.spyOn(agents, 'loadAgents').mockResolvedValue(undefined)
    agents.agents = []
    const remote = useRemoteStore()
    vi.spyOn(remote, 'loadHosts').mockResolvedValue(undefined)
    remote.hosts = [{ id: 'h2', name: 'host-02', tags: [], ssh_host: '10.0.0.2', ssh_port: 22, ssh_user: 'root' } as any]
    const nodes = useNodeStore()
    vi.spyOn(nodes, 'start').mockResolvedValue(undefined)

    const wrapper = mount(AgentManagerTab, { global: { plugins: [installTestI18n()] } })
    await wrapper.vm.$nextTick()
    await wrapper.find('[data-test="agent-create"]').trigger('click')

    expect(wrapper.find('[data-test="agent-create-host"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('host-02')
  })
})
