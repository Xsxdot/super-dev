/**
 * HostManagerTab 测试设置页 Host 身份管理能力。
 *
 * 职责：
 *   - 验证空态、新建入口和 identity-only payload
 *   - 验证 Host 行展示 Agent 摘要但不承载 Agent 操作
 *
 * 边界：
 *   - 不访问真实 agent HTTP 或 WebSocket 接口
 *   - 不测试 Agent 配置 modal
 */
import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import HostManagerTab from '@/components/Settings/HostManagerTab.vue'
import { api, type Host } from '@/api/agent'
import { useAgentsStore } from '@/stores/agents'
import { useNodeStore } from '@/stores/node'
import { useRemoteStore } from '@/stores/remote'
import { installTestI18n } from '@/test-utils/i18n'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      listHosts: vi.fn().mockResolvedValue([]),
      createHost: vi.fn(),
      updateHost: vi.fn(),
      deleteHost: vi.fn(),
    },
  }
})

const mockedApi = api as unknown as Record<string, Mock>

function host(overrides: Partial<Host> = {}): Host {
  return {
    id: 'h1',
    name: 'host-test',
    public_ip: '203.0.113.10',
    private_ip: '10.0.0.10',
    tags: [],
    ...overrides,
  }
}

async function mountHostManager() {
  const agents = useAgentsStore()
  const nodes = useNodeStore()
  vi.spyOn(agents, 'loadAgents').mockResolvedValue(undefined)
  vi.spyOn(nodes, 'start').mockResolvedValue(undefined)
  vi.spyOn(nodes, 'stop').mockImplementation(() => undefined)
  const wrapper = mount(HostManagerTab, { global: { plugins: [installTestI18n('zh-CN')] } })
  await Promise.resolve()
  await Promise.resolve()
  return wrapper
}

describe('HostManagerTab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    mockedApi.listHosts.mockResolvedValue([])
  })

  it('空态展示提示文案', async () => {
    const wrapper = await mountHostManager()

    expect(wrapper.text()).toContain('还没有主机')
  })

  it('点击新建主机打开 identity-only 表单', async () => {
    const wrapper = await mountHostManager()

    await wrapper.find('[data-test="host-add"]').trigger('click')

    expect(wrapper.find('[data-test="host-form-name"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="host-form-host"]').exists()).toBe(false)
  })

  it('提交表单调用 store.createHost 且不带 SSH 字段', async () => {
    const wrapper = await mountHostManager()
    const store = useRemoteStore()
    const spy = vi.spyOn(store, 'createHost').mockResolvedValue(host())

    await wrapper.find('[data-test="host-add"]').trigger('click')
    await wrapper.find('[data-test="host-form-name"]').setValue('host-test')
    await wrapper.find('[data-test="host-form-public-ip"]').setValue('203.0.113.10')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')

    expect(spy).toHaveBeenCalledWith(expect.objectContaining({
      name: 'host-test',
      public_ip: '203.0.113.10',
    }))
    expect(spy.mock.calls[0][0]).not.toHaveProperty(['ssh', 'host'].join('_'))
  })

  it('renders agent summary from agents and node stores without install actions', async () => {
    const wrapper = await mountHostManager()
    const store = useRemoteStore()
    const agents = useAgentsStore()
    const nodes = useNodeStore()
    store.hosts = [host({ tags: ['prod'] })]
    agents.agents = [{
      host_id: 'h1',
      host_name: 'host-test',
      tags: ['prod'],
      transport: { chain: [{ type: 'direct', direct: { address: '100.64.0.8:57017' } }] },
      runtime: { installed: false, health: 'unknown', reachable: false },
      security: { token_configured: false, provision_state: 'not-configured' },
    }]
    nodes.applySnapshot([{
      host_id: 'h1',
      name: 'host-test',
      reachable: true,
      agent: { installed: true, health: 'healthy', reachable: true, version: '0.1.0' },
      deployments: [],
      updated_at: '2026-06-06T10:00:00Z',
    }])

    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-test="host-agent-summary"]').text()).toContain('direct · healthy · v0.1.0')
    expect(wrapper.find('[data-test="host-install-agent"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="host-refresh-agent-h1"]').exists()).toBe(false)
  })
})
