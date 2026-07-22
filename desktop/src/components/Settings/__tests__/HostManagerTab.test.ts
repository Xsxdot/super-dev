/**
 * HostManagerTab 测试设置页 Host 身份管理能力。
 *
 * 职责：
 *   - 验证空态、新建入口和 Host SSH 连接信息 payload
 *   - 验证 Host 行不展示或操作 Agent 配置
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
    ssh_host: '10.0.0.10',
    ssh_port: 22,
    ssh_user: 'root',
    ssh_credential_configured: true,
    ssh_private_key_configured: true,
    ssh_host_key_fingerprint_configured: true,
    tags: [],
    ...overrides,
  }
}

async function mountHostManager() {
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

  it('点击新建主机打开 Host SSH 表单', async () => {
    const wrapper = await mountHostManager()

    await wrapper.find('[data-test="host-add"]').trigger('click')

    expect(wrapper.find('[data-test="host-form-name"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="host-form-ssh-host"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="host-form-ssh-private-key"]').exists()).toBe(true)
  })

  it('提交表单调用 store.createHost 且保存 SSH 私钥内容', async () => {
    const wrapper = await mountHostManager()
    const store = useRemoteStore()
    const spy = vi.spyOn(store, 'createHost').mockResolvedValue(host())

    await wrapper.find('[data-test="host-add"]').trigger('click')
    await wrapper.find('[data-test="host-form-name"]').setValue('host-test')
    await wrapper.find('[data-test="host-form-public-ip"]').setValue('203.0.113.10')
    await wrapper.find('[data-test="host-form-ssh-host"]').setValue('10.0.0.10')
    await wrapper.find('[data-test="host-form-ssh-user"]').setValue('root')
    await wrapper.find('[data-test="host-form-ssh-private-key"]').setValue('PRIVATE KEY CONTENT')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')

    expect(spy).toHaveBeenCalledWith(expect.objectContaining({
      name: 'host-test',
      public_ip: '203.0.113.10',
      ssh_host: '10.0.0.10',
      ssh_user: 'root',
      ssh_private_key: 'PRIVATE KEY CONTENT',
    }))
    expect(spy.mock.calls[0][0]).not.toHaveProperty('ssh_key_path')
  })

  it('does not render Agent summary or Agent actions inside Host management', async () => {
    const wrapper = await mountHostManager()
    const store = useRemoteStore()
    store.hosts = [host({ tags: ['prod'] })]

    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-test="host-agent-summary"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="host-install-agent"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="host-refresh-agent-h1"]').exists()).toBe(false)
  })
})
