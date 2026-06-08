/**
 * AgentManagerTab tests the lifecycle-oriented Agent settings page.
 *
 * 职责：
 *   - 验证 Agent 行展示主机、阶段、当前连接和展开链路
 *   - 验证每行只有阶段主按钮和更多菜单，不再平铺六个等权动作
 *   - 验证主按钮和菜单打开统一 AgentConfigPanel 的正确页签
 *
 * 边界：
 *   - 不访问真实 agent HTTP API
 *   - 不打开真实 NodeRegistry WebSocket
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AgentManagerTab from '@/components/Settings/AgentManagerTab.vue'
import { useAgentsStore } from '@/stores/agents'
import { useNodeStore } from '@/stores/node'
import { useRemoteStore } from '@/stores/remote'
import { installTestI18n } from '@/test-utils/i18n'
import type { AgentDTO, NodeStatus } from '@/api/agent'

const mountedWrappers: Array<{ unmount: () => void }> = []

function agent(overrides: Partial<AgentDTO> = {}): AgentDTO {
  return {
    host_id: 'h1',
    host_name: 'ali-01',
    tags: ['prod'],
    transport: {
      chain: [
        { type: 'direct', direct: { address: '100.64.0.8:57017' } },
        { type: 'tunnel', tunnel: { remote_agent_port: 57017 } },
      ],
    },
    config: { listen_address: '127.0.0.1', listen_port: 57017 },
    runtime: { installed: false, health: 'unknown', reachable: false },
    security: { token_configured: false, provision_state: 'not-configured', tls: { mode: 'auto' } },
    updated_at: '2026-06-07T10:00:00Z',
    ...overrides,
  }
}

function node(route?: NodeStatus['route']): NodeStatus {
  return {
    host_id: 'h1',
    name: 'ali-01',
    reachable: true,
    agent: { installed: true, health: 'healthy', reachable: true, version: '0.1.0' },
    deployments: [],
    route,
    updated_at: '2026-06-07T10:00:00Z',
  }
}

async function mountPage(items: AgentDTO[], nodes: NodeStatus[] = []) {
  const agents = useAgentsStore()
  vi.spyOn(agents, 'loadAgents').mockResolvedValue(undefined)
  agents.agents = items
  const remote = useRemoteStore()
  vi.spyOn(remote, 'loadHosts').mockResolvedValue(undefined)
  const nodeStore = useNodeStore()
  vi.spyOn(nodeStore, 'start').mockResolvedValue(undefined)
  nodeStore.applySnapshot(nodes)
  const wrapper = mount(AgentManagerTab, {
    attachTo: document.body,
    global: { plugins: [installTestI18n()] },
  })
  mountedWrappers.push(wrapper)
  await wrapper.vm.$nextTick()
  return { wrapper, agents }
}

function bodyMenu(hostId: string): HTMLElement {
  const menu = document.body.querySelector(`[data-test="agent-menu-${hostId}"]`)
  if (!(menu instanceof HTMLElement)) throw new Error(`agent menu ${hostId} not found`)
  return menu
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

afterEach(() => {
  for (const wrapper of mountedWrappers.splice(0)) {
    wrapper.unmount()
  }
  document.body.innerHTML = ''
})

describe('AgentManagerTab', () => {
  it('renders degraded route summary from nodeStore and expandable chain details', async () => {
    const { wrapper } = await mountPage([agent({
      runtime: { installed: true, health: 'healthy', reachable: true },
    })], [node({
      selected_index: 1,
      selected_type: 'tunnel',
      degraded: true,
      last_results: [
        { index: 0, transport_type: 'direct', status: 'unreachable', reachable: false, error: 'connection refused', checked_at: '2026-06-07T10:00:00Z' },
        { index: 1, transport_type: 'tunnel', status: 'reachable', reachable: true, latency_ms: 9, checked_at: '2026-06-07T10:00:01Z' },
      ],
    })])

    expect(wrapper.text()).toContain('ali-01')
    expect(wrapper.text()).toContain('降级中')
    expect(wrapper.text()).toContain(':57017')
    expect(wrapper.text()).toContain('健康')
    await wrapper.find('[data-test="agent-route-toggle-h1"]').trigger('click')
    expect(wrapper.find('[data-test="agent-route-row-h1-0"]').text()).toContain('connection refused')
    expect(wrapper.find('[data-test="agent-route-row-h1-1"]').text()).toContain('当前走通')
  })

  it('shows one primary action plus a more menu instead of duplicated row links', async () => {
    const { wrapper } = await mountPage([agent()])

    expect(wrapper.find('[data-test="agent-primary-h1"]').text()).toContain('安装')
    expect(wrapper.find('[data-test="agent-more-h1"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="agent-edit-h1"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="agent-security-h1"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="agent-install-h1"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="agent-generate-command-h1"]').exists()).toBe(false)
  })

  it('opens the unified panel on the install tab from the primary pending-install action', async () => {
    const { wrapper } = await mountPage([agent()])

    await wrapper.find('[data-test="agent-primary-h1"]').trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-test="agent-panel"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="agent-panel-tab-install"]').classes()).toContain('active')
  })

  it('opens transport and security tabs from the more menu', async () => {
    const { wrapper } = await mountPage([agent()])

    await wrapper.find('[data-test="agent-more-h1"]').trigger('click')
    bodyMenu('h1').querySelector<HTMLElement>('[data-test="agent-menu-transport-h1"]')?.click()
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-test="agent-panel-tab-transport"]').classes()).toContain('active')
    await wrapper.find('[data-test="agent-panel"] .settings-btn-ghost').trigger('click')

    await wrapper.find('[data-test="agent-more-h1"]').trigger('click')
    bodyMenu('h1').querySelector<HTMLElement>('[data-test="agent-menu-security-h1"]')?.click()
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-test="agent-panel-tab-security"]').classes()).toContain('active')
  })

  it('closes the more menu when clicking outside the menu', async () => {
    const { wrapper } = await mountPage([agent()])

    await wrapper.find('[data-test="agent-more-h1"]').trigger('click')
    expect(document.body.querySelector('[data-test="agent-menu-h1"]')).toBeTruthy()
    document.body.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await wrapper.vm.$nextTick()

    expect(document.body.querySelector('[data-test="agent-menu-h1"]')).toBeFalsy()
  })

  it('renders the more menu outside the table scroll surface to avoid clipping', async () => {
    const { wrapper } = await mountPage([agent()])

    await wrapper.find('[data-test="agent-more-h1"]').trigger('click')
    const menu = bodyMenu('h1')

    expect(menu.closest('.settings-surface-scroll')).toBeNull()
    expect(menu.classList.contains('agent-action-menu')).toBe(true)
  })

  it('runs checkAgent directly for healthy primary action', async () => {
    const { wrapper, agents } = await mountPage([agent({
      runtime: { installed: true, health: 'healthy', reachable: true },
    })], [node({ selected_index: 0, selected_type: 'direct', degraded: false })])
    vi.spyOn(agents, 'checkAgent').mockResolvedValue(agent())

    await wrapper.find('[data-test="agent-primary-h1"]').trigger('click')

    expect(agents.checkAgent).toHaveBeenCalledWith('h1')
    expect(wrapper.find('[data-test="agent-panel"]').exists()).toBe(false)
  })
})
