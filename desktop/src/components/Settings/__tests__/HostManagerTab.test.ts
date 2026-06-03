/**
 * HostManagerTab 测试设置页主机管理能力。
 *
 * 职责：
 *   - 验证空态、新建入口
 *   - 验证 Host 表单提交会走 remote store action
 *
 * 边界：
 *   - 不访问真实 agent HTTP 接口
 *   - 不调起真实 Tauri 文件对话框
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import HostManagerTab from '@/components/Settings/HostManagerTab.vue'
import { useRemoteStore } from '@/stores/remote'
import { installTestI18n } from '@/test-utils/i18n'

vi.mock('@tauri-apps/plugin-dialog', () => ({
  open: vi.fn(),
}))

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      listHosts: vi.fn().mockResolvedValue([]),
      listTunnels: vi.fn().mockResolvedValue([]),
      createHost: vi.fn(),
      updateHost: vi.fn(),
      deleteHost: vi.fn(),
      installHostAgent: vi.fn().mockResolvedValue({
        ok: true,
        host_id: 'h1',
        platform: 'linux/amd64',
        message: 'Agent installed and started',
      }),
      detectSshKeys: vi.fn().mockResolvedValue([]),
      testConnection: vi.fn().mockResolvedValue({ ok: true, message: '连接成功', latency_ms: 10 }),
    },
  }
})

describe('HostManagerTab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    localStorage.clear()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('空态展示提示文案', async () => {
    const wrapper = mount(HostManagerTab, { global: { plugins: [installTestI18n()] } })
    await new Promise(resolve => setTimeout(resolve))

    expect(wrapper.text()).toContain('还没有主机')
  })

  it('点击新建主机打开表单', async () => {
    const wrapper = mount(HostManagerTab, { global: { plugins: [installTestI18n()] } })

    await wrapper.find('[data-test="host-add"]').trigger('click')

    expect(wrapper.find('[data-test="host-form-name"]').exists()).toBe(true)
  })

  it('提交表单调用 store.createHost', async () => {
    const wrapper = mount(HostManagerTab, { global: { plugins: [installTestI18n()] } })
    const store = useRemoteStore()
    const spy = vi.spyOn(store, 'createHost').mockResolvedValue({
      id: 'h1',
      name: 'host-test',
      ssh_host: '1.1.1.1',
      ssh_port: 22,
      ssh_user: 'root',
      remote_agent_port: 57017,
      local_tunnel_port: 0,
      tags: [],
    })

    await wrapper.find('[data-test="host-add"]').trigger('click')
    await wrapper.find('[data-test="host-form-name"]').setValue('host-test')
    await wrapper.find('[data-test="host-form-host"]').setValue('1.1.1.1')
    await wrapper.find('[data-test="host-form-user"]').setValue('root')
    await wrapper.find('[data-test="host-form-submit"]').trigger('click')

    expect(spy).toHaveBeenCalled()
    expect(spy.mock.calls[0][0]).toMatchObject({
      name: 'host-test',
      ssh_host: '1.1.1.1',
      ssh_user: 'root',
    })
  })

  it('英文 locale 下渲染主机管理标题', async () => {
    const wrapper = mount(HostManagerTab, {
      global: { plugins: [installTestI18n('en-US')] },
    })

    await new Promise(resolve => setTimeout(resolve))

    expect(wrapper.text()).toContain('Hosts')
    expect(wrapper.text()).toContain('New Host')
  })

  it('点击安装 Agent 调用 store.installHostAgent', async () => {
    const wrapper = mount(HostManagerTab, { global: { plugins: [installTestI18n()] } })
    const store = useRemoteStore()
    await new Promise(resolve => setTimeout(resolve))
    store.hosts = [{
      id: 'h1',
      name: 'host-test',
      ssh_host: '1.1.1.1',
      ssh_port: 22,
      ssh_user: 'root',
      remote_agent_port: 57017,
      local_tunnel_port: 0,
      tags: [],
    }]
    const spy = vi.spyOn(store, 'installHostAgent').mockResolvedValue({
      ok: true,
      host_id: 'h1',
      platform: 'linux/amd64',
      message: 'Agent installed and started',
    })
    vi.spyOn(store, 'loadTunnels').mockResolvedValue(undefined)

    await wrapper.vm.$nextTick()
    await wrapper.find('[data-test="host-install-agent"]').trigger('click')

    expect(spy).toHaveBeenCalledWith('h1')
  })

  it('安装 Agent 失败时展示错误详情', async () => {
    const wrapper = mount(HostManagerTab, { global: { plugins: [installTestI18n()] } })
    const store = useRemoteStore()
    await new Promise(resolve => setTimeout(resolve))
    store.hosts = [{
      id: 'h1',
      name: 'host-test',
      ssh_host: '1.1.1.1',
      ssh_port: 22,
      ssh_user: 'root',
      remote_agent_port: 57017,
      local_tunnel_port: 0,
      tags: [],
    }]
    vi.spyOn(store, 'installHostAgent').mockRejectedValue(new Error('verify: connection refused'))

    await wrapper.vm.$nextTick()
    await wrapper.find('[data-test="host-install-agent"]').trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('verify: connection refused')
  })

  it('渲染 agent 健康徽章（与隧道状态正交）', async () => {
    const wrapper = mount(HostManagerTab, { global: { plugins: [installTestI18n()] } })
    const store = useRemoteStore()
    await new Promise(resolve => setTimeout(resolve))

    store.hosts = [{
      id: 'h1',
      name: 'srv',
      ssh_host: '1.1.1.1',
      ssh_port: 22,
      ssh_user: 'root',
      remote_agent_port: 57017,
      local_tunnel_port: 0,
      tags: [],
    }]
    // 隧道 open 且 agent unreachable —— 必须能同时看到两种状态
    store.applyTunnelUpdate({ host_id: 'h1', state: 'open', local_port: 57100 })
    store.applyTunnelUpdate({ host_id: 'h1', agent: 'unreachable' })

    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('open :57100')
    expect(wrapper.find('[data-test="agent-health"]').text()).toBe('unreachable')
  })

  it('渲染 agent 版本、最近检测和安装安全说明', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-06-03T10:00:00Z'))
    const wrapper = mount(HostManagerTab, { global: { plugins: [installTestI18n()] } })
    const store = useRemoteStore()
    await Promise.resolve()
    await Promise.resolve()

    store.hosts = [{
      id: 'h1',
      name: 'srv',
      ssh_host: '1.1.1.1',
      ssh_port: 22,
      ssh_user: 'root',
      remote_agent_port: 57017,
      local_tunnel_port: 0,
      tags: [],
    }]
    store.applyTunnelUpdate({
      host_id: 'h1',
      state: 'open',
      local_port: 57100,
      agent: 'healthy',
      agent_version: '0.1.0',
      agent_checked_at: '2026-06-03T09:59:48Z',
    })

    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-test="agent-meta"]').text()).toContain('v0.1.0 · 12s 前')
    const install = wrapper.find('[data-test="host-install-agent"]')
    expect(install.attributes('title')).toContain('通过 SSH 上传 agent 二进制')
    expect(wrapper.find('[data-test="host-install-help"]').text()).toContain('不影响业务进程')
  })
})
