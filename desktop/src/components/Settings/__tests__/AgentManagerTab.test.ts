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
import { AgentAPIError, type AgentDTO, type Host, type NodeStatus } from '@/api/agent'

vi.mock('@/components/Settings/AgentBulkUpdateModal.vue', () => ({
  default: {
    props: ['visible', 'agents', 'hosts'],
    emits: ['cancel'],
    template: '<div v-if="visible" data-test="bulk-update-modal">bulk update</div>',
  },
}))

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

const hosts: Host[] = [
  {
    id: 'h2',
    name: 'us-02',
    tags: [],
  },
]

function hostForManager(id: string): Host {
  return {
    id,
    name: id,
    tags: [],
    ssh_host: '10.0.0.8',
    ssh_port: 22,
    ssh_user: 'root',
    ssh_private_key: 'KEY',
  }
}

async function mountPage(items: AgentDTO[], nodes: NodeStatus[] = [], hostItems: Host[] = hosts) {
  const agents = useAgentsStore()
  vi.spyOn(agents, 'loadAgents').mockResolvedValue(undefined)
  agents.agents = items
  const remote = useRemoteStore()
  vi.spyOn(remote, 'loadHosts').mockResolvedValue(undefined)
  remote.hosts = hostItems
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
  vi.unstubAllGlobals()
})

describe('AgentManagerTab', () => {
  it('opens the batch update modal from the toolbar', async () => {
    const { wrapper } = await mountPage([agent({
      runtime: { installed: true, health: 'healthy', reachable: true, version: '0.1.0' },
    })], [], [hostForManager('h1')])

    await wrapper.find('[data-test="agent-bulk-update"]').trigger('click')

    expect(wrapper.find('[data-test="bulk-update-modal"]').exists()).toBe(true)
  })

  it('opens the unified AgentConfigPanel for new Agent creation and creates from the connection chain', async () => {
    const { wrapper, agents } = await mountPage([])
    vi.spyOn(agents, 'createAgent').mockResolvedValue(agent({
      host_id: 'h2',
      host_name: 'us-02',
      tags: [],
      transport: { chain: [{ type: 'tunnel', tunnel: { remote_agent_port: 57017 } }] },
    }))
    vi.spyOn(agents, 'generateInstallCommand').mockResolvedValue({ command: 'curl install h2', expires_at: '2026-06-07T10:30:00Z', token_id: 'tok_1' })

    await wrapper.find('[data-test="agent-create"]').trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-test="agent-panel"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="agent-panel-tab-security"]').classes()).toContain('active')
    expect(wrapper.find('[data-test="agent-create-host"]').exists()).toBe(true)
    expect(wrapper.find('.agent-create-modal').exists()).toBe(false)

    await wrapper.find('[data-test="agent-security-save"]').trigger('click')
    await wrapper.vm.$nextTick()

    expect(agents.createAgent).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="agent-panel-tab-transport"]').classes()).toContain('active')

    await wrapper.find('[data-test="agent-transport-save"]').trigger('click')
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()

    expect(agents.createAgent).toHaveBeenCalled()
    expect(wrapper.find('[data-test="agent-panel-tab-install"]').classes()).toContain('active')
    expect(wrapper.find('[data-test="agent-create-before-install"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="agent-install-generate"]').exists()).toBe(true)

    await wrapper.find('[data-test="agent-install-generate"]').trigger('click')

    expect(agents.generateInstallCommand).toHaveBeenCalledWith('h2', expect.objectContaining({
      method: 'generated_command',
      transport_type: 'tunnel',
    }))
  })

  it('renders a newly created agent while the backend health snapshot is still empty', async () => {
    const { wrapper } = await mountPage([agent({
      host_id: 'h2',
      host_name: 'jp',
      runtime: { installed: false, health: '' as AgentDTO['runtime']['health'], reachable: false },
    })])

    expect(wrapper.find('[data-test="agent-row"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="agent-primary-h2"]').text()).toContain('安装')
    expect(wrapper.text()).toContain('未知')
  })

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
    expect(wrapper.find('[data-test="agent-route-row-h1-0"]').text()).toContain('探活失败')
    expect(wrapper.find('[data-test="agent-route-row-h1-1"]').text()).toContain('探活成功')
    expect(wrapper.find('[data-test="agent-route-row-h1-1"]').text()).toContain('当前')
    expect(wrapper.find('[data-test="agent-route-row-h1-1"]').text()).not.toContain('当前走通')
  })

  it('shows the current route as reachable when the agent is healthy', async () => {
    const { wrapper } = await mountPage([agent({
      runtime: { installed: true, health: 'healthy', reachable: true },
    })])

    await wrapper.find('[data-test="agent-route-toggle-h1"]').trigger('click')

    const firstRow = wrapper.find('[data-test="agent-route-row-h1-0"]')
    expect(firstRow.text()).toContain('探活成功')
    expect(firstRow.text()).toContain('当前')
    expect(firstRow.text()).not.toContain('未测')
  })

  it('renders localized transport names in route details', async () => {
    const { wrapper } = await mountPage([agent({
      runtime: { installed: true, health: 'healthy', reachable: true },
    })], [node({
      selected_index: 1,
      selected_type: 'tunnel',
      degraded: true,
    })])

    await wrapper.find('[data-test="agent-route-toggle-h1"]').trigger('click')

    expect(wrapper.find('[data-test="agent-route-row-h1-0"]').text()).toContain('直连 · 100.64.0.8:57017')
    expect(wrapper.find('[data-test="agent-route-row-h1-1"]').text()).toContain('隧道 · :57017')
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

  it('defaults to preserving Agent data while explaining the uninstall impact', async () => {
    const { wrapper, agents } = await mountPage([agent({
      runtime: { installed: true, health: 'healthy', reachable: true },
    })], [], [hostForManager('h1')])
    vi.spyOn(agents, 'uninstallAgent').mockResolvedValue({
      ok: true,
      host_id: 'h1',
      removed_data: false,
      message: 'Agent uninstalled',
    })
    await wrapper.find('[data-test="agent-more-h1"]').trigger('click')
    const uninstallButton = bodyMenu('h1').querySelector<HTMLElement>('[data-test="agent-menu-uninstall-h1"]')
    expect(uninstallButton?.textContent).toContain('卸载 Agent')
    uninstallButton?.click()
    await wrapper.vm.$nextTick()

    const modal = wrapper.find('[data-test="agent-uninstall-modal"]')
    expect(modal.exists()).toBe(true)
    expect(modal.text()).toContain('Agent 自身及其直接启动的子进程可能停止')
    expect(modal.text()).toContain('其他 systemd 服务和 Docker 容器会继续运行')
    expect(modal.text()).toContain('默认保留远端 Agent 数据和日志')
    expect((modal.find('[data-test="agent-uninstall-purge"]').element as HTMLInputElement).checked).toBe(false)

    await modal.find('[data-test="agent-uninstall-confirm"]').trigger('click')
    await vi.waitFor(() => expect(agents.uninstallAgent).toHaveBeenCalledWith('h1', false))
  })

  it('requires typing the Host name before purging Agent data and logs', async () => {
    const { wrapper, agents } = await mountPage([agent()], [], [hostForManager('h1')])
    vi.spyOn(agents, 'uninstallAgent').mockResolvedValue({
      ok: true,
      host_id: 'h1',
      removed_data: true,
      message: 'Agent uninstalled',
    })

    await wrapper.find('[data-test="agent-more-h1"]').trigger('click')
    bodyMenu('h1').querySelector<HTMLElement>('[data-test="agent-menu-uninstall-h1"]')?.click()
    await wrapper.vm.$nextTick()

    const modal = wrapper.find('[data-test="agent-uninstall-modal"]')
    await modal.find('[data-test="agent-uninstall-purge"]').setValue(true)
    expect(modal.text()).toContain('永久删除')
    expect(modal.find('[data-test="agent-uninstall-confirm"]').attributes('disabled')).toBeDefined()

    await modal.find('[data-test="agent-uninstall-confirm-name"]').setValue('h1')
    expect(modal.find('[data-test="agent-uninstall-confirm"]').attributes('disabled')).toBeDefined()
    await modal.find('[data-test="agent-uninstall-confirm-name"]').setValue('ali-01')
    expect(modal.find('[data-test="agent-uninstall-confirm"]').attributes('disabled')).toBeUndefined()

    await modal.find('[data-test="agent-uninstall-confirm"]').trigger('click')
    await vi.waitFor(() => expect(agents.uninstallAgent).toHaveBeenCalledWith('h1', true))
  })

  it.each([
    { stage: 'remote_uninstall', status: 502, expected: '远端 Agent 卸载失败' },
    { stage: 'config_remove', status: 500, expected: '远端 Agent 已卸载，但 Controller 配置移除失败' },
  ])('shows the $stage recovery stage to the user', async ({ stage, status, expected }) => {
    const { wrapper, agents } = await mountPage([agent()], [], [hostForManager('h1')])
    vi.spyOn(agents, 'uninstallAgent').mockRejectedValue(new AgentAPIError('fixture failure', status, {
      error: 'fixture failure',
      stage,
    }))
    await wrapper.find('[data-test="agent-more-h1"]').trigger('click')
    bodyMenu('h1').querySelector<HTMLElement>('[data-test="agent-menu-uninstall-h1"]')?.click()
    await wrapper.vm.$nextTick()
    await wrapper.find('[data-test="agent-uninstall-confirm"]').trigger('click')

    await vi.waitFor(() => expect(wrapper.text()).toContain(expected))
  })

  it('reveals version-matched manual scripts only after automatic remote uninstall fails', async () => {
    const { wrapper, agents } = await mountPage([agent()], [], [hostForManager('h1')])
    vi.spyOn(agents, 'uninstallAgent').mockRejectedValue(new AgentAPIError('ssh unavailable', 502, {
      error: 'ssh unavailable',
      stage: 'remote_uninstall',
    }))

    expect(wrapper.find('[data-test="agent-manual-uninstall"]').exists()).toBe(false)
    await wrapper.find('[data-test="agent-more-h1"]').trigger('click')
    bodyMenu('h1').querySelector<HTMLElement>('[data-test="agent-menu-uninstall-h1"]')?.click()
    await wrapper.vm.$nextTick()
    await wrapper.find('[data-test="agent-uninstall-confirm"]').trigger('click')

    await vi.waitFor(() => expect(wrapper.find('[data-test="agent-manual-uninstall"]').exists()).toBe(true))
    const guidance = wrapper.find('[data-test="agent-manual-uninstall"]')
    expect(guidance.text()).toContain('Agent 配置已保留')
    expect(guidance.text()).toContain('sh uninstall-agent.sh')
    expect(guidance.text()).toContain('powershell -ExecutionPolicy Bypass -File .\\uninstall-agent.ps1')
    expect(guidance.find('[data-test="agent-uninstall-script-shell"]').attributes('href')).toBe(
      'http://127.0.0.1:57018/api/agents/uninstall-scripts/uninstall-agent.sh',
    )
    expect(guidance.find('[data-test="agent-uninstall-script-powershell"]').attributes('href')).toBe(
      'http://127.0.0.1:57018/api/agents/uninstall-scripts/uninstall-agent.ps1',
    )
    expect(agents.agents).toHaveLength(1)
  })

  it('offers Controller-only detach only after manual guidance and requires a second risk confirmation', async () => {
    const { wrapper, agents } = await mountPage([agent()], [], [hostForManager('h1')])
    vi.spyOn(agents, 'uninstallAgent').mockRejectedValue(new AgentAPIError('ssh unavailable', 502, {
      error: 'ssh unavailable',
      stage: 'remote_uninstall',
    }))
    const detach = vi.spyOn(agents, 'detachAgent').mockResolvedValue({ status: 'detached', host_id: 'h1' })

    expect(wrapper.find('[data-test="agent-detach-unavailable"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="agent-detach-modal"]').exists()).toBe(false)

    await wrapper.find('[data-test="agent-more-h1"]').trigger('click')
    bodyMenu('h1').querySelector<HTMLElement>('[data-test="agent-menu-uninstall-h1"]')?.click()
    await wrapper.vm.$nextTick()
    await wrapper.find('[data-test="agent-uninstall-confirm"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-test="agent-manual-uninstall"]').exists()).toBe(true))

    const fallback = wrapper.find('[data-test="agent-detach-unavailable"]')
    expect(fallback.text()).toContain('无法完成手动卸载')
    await fallback.trigger('click')

    const modal = wrapper.find('[data-test="agent-detach-modal"]')
    expect(modal.text()).toContain('远端 Agent 及其直接启动的子进程可能仍在运行')
    expect(modal.text()).toContain('不会卸载远端 Agent')
    expect(detach).not.toHaveBeenCalled()

    await modal.find('[data-test="agent-detach-confirm"]').trigger('click')
    await vi.waitFor(() => expect(detach).toHaveBeenCalledWith('h1', 'manual_uninstall_failed'))
    expect(wrapper.find('[data-test="agent-manual-uninstall"]').exists()).toBe(false)
  })

  it('keeps one Host manual fallback visible when another Host uninstalls successfully', async () => {
    const h1 = agent({ host_id: 'h1', host_name: 'ali-01' })
    const h2 = agent({ host_id: 'h2', host_name: 'us-02' })
    const { wrapper, agents } = await mountPage([h1, h2], [], [hostForManager('h1'), hostForManager('h2')])
    vi.spyOn(agents, 'uninstallAgent').mockImplementation(async (hostId) => {
      if (hostId === 'h1') {
        throw new AgentAPIError('ssh unavailable', 502, {
          error: 'ssh unavailable',
          stage: 'remote_uninstall',
        })
      }
      agents.agents = agents.agents.filter(item => item.host_id !== hostId)
      return { ok: true, host_id: hostId, removed_data: false, message: 'Agent uninstalled' }
    })

    await wrapper.find('[data-test="agent-more-h1"]').trigger('click')
    bodyMenu('h1').querySelector<HTMLElement>('[data-test="agent-menu-uninstall-h1"]')?.click()
    await wrapper.vm.$nextTick()
    await wrapper.find('[data-test="agent-uninstall-confirm"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-test="agent-manual-uninstall"]').text()).toContain('ali-01'))

    await wrapper.find('[data-test="agent-more-h2"]').trigger('click')
    bodyMenu('h2').querySelector<HTMLElement>('[data-test="agent-menu-uninstall-h2"]')?.click()
    await wrapper.vm.$nextTick()
    await wrapper.find('[data-test="agent-uninstall-confirm"]').trigger('click')
    await vi.waitFor(() => expect(agents.uninstallAgent).toHaveBeenCalledWith('h2', false))

    expect(wrapper.find('[data-test="agent-manual-uninstall"]').text()).toContain('ali-01')
    expect(wrapper.find('[data-test="agent-detach-unavailable"]').exists()).toBe(true)
  })

  it('does not suggest manual uninstall when only Controller config removal failed', async () => {
    const { wrapper, agents } = await mountPage([agent()], [], [hostForManager('h1')])
    vi.spyOn(agents, 'uninstallAgent').mockRejectedValue(new AgentAPIError('store unavailable', 500, {
      error: 'store unavailable',
      stage: 'config_remove',
    }))

    await wrapper.find('[data-test="agent-more-h1"]').trigger('click')
    bodyMenu('h1').querySelector<HTMLElement>('[data-test="agent-menu-uninstall-h1"]')?.click()
    await wrapper.vm.$nextTick()
    await wrapper.find('[data-test="agent-uninstall-confirm"]').trigger('click')

    await vi.waitFor(() => expect(wrapper.text()).toContain('Controller 配置移除失败'))
    expect(wrapper.find('[data-test="agent-manual-uninstall"]').exists()).toBe(false)
  })
})
