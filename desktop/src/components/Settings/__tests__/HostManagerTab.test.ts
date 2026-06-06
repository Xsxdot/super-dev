/**
 * HostManagerTab 测试设置页主机管理能力。
 *
 * 职责：
 *   - 验证空态、新建入口
 *   - 验证 Host 表单提交会走 remote store action
 *
 * 边界：
 *   - 不访问真实 agent HTTP 或 WebSocket 接口
 *   - 不调起真实 Tauri 文件对话框
 */
import { describe, it, expect, beforeEach, afterEach, vi, type Mock } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import HostManagerTab from '@/components/Settings/HostManagerTab.vue'
import { useRemoteStore } from '@/stores/remote'
import { installTestI18n } from '@/test-utils/i18n'
import { api, type Host, type TunnelStatus } from '@/api/agent'

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
      checkHostAgent: vi.fn().mockResolvedValue({
        host_id: 'h1',
        state: 'open',
        local_port: 57100,
        agent: 'healthy',
        agent_version: '0.1.0',
        agent_checked_at: '2026-06-05T10:00:00Z',
      }),
      getHostManagedDeploymentStatus: vi.fn().mockResolvedValue({
        host_id: 'h1',
        host_name: 'host-test',
        desired_deployment_count: 0,
        desired_collector_count: 0,
        tunnel_connected: true,
        remote: { deployment_count: 0, collector_count: 0, collectors: [] },
      }),
      uninstallHostAgent: vi.fn().mockResolvedValue({
        result: { ok: true, host_id: 'h1', removed_data: false, message: 'Agent uninstalled' },
        tunnel: {
          host_id: 'h1',
          state: 'idle',
          agent: 'unreachable',
          agent_checked_at: '2026-06-05T10:00:00Z',
        },
      }),
      detectSshKeys: vi.fn().mockResolvedValue([]),
      testConnection: vi.fn().mockResolvedValue({ ok: true, message: '连接成功', latency_ms: 10 }),
    },
  }
})

const mockedApi = api as unknown as Record<string, Mock>

function host(overrides: Partial<Host> = {}): Host {
  return {
    id: 'h1',
    name: 'host-test',
    ssh_host: '1.1.1.1',
    ssh_port: 22,
    ssh_user: 'root',
    remote_agent_port: 57017,
    local_tunnel_port: 0,
    tags: [],
    ...overrides,
  }
}

async function flushMountedAsync() {
  await Promise.resolve()
  await Promise.resolve()
  await new Promise(resolve => setTimeout(resolve))
}

class MockWebSocket {
  static instances: MockWebSocket[] = []
  url: string
  onmessage: ((event: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  readyState = 1

  constructor(url: string) {
    this.url = url
    MockWebSocket.instances.push(this)
  }

  close() {
    this.readyState = 3
    this.onclose?.()
  }
}

describe('HostManagerTab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    MockWebSocket.instances = []
    vi.stubGlobal('WebSocket', MockWebSocket)
    localStorage.clear()
    mockedApi.listHosts.mockResolvedValue([])
    mockedApi.listTunnels.mockResolvedValue([])
    mockedApi.getHostManagedDeploymentStatus.mockResolvedValue({
      host_id: 'h1',
      host_name: 'host-test',
      desired_deployment_count: 0,
      desired_collector_count: 0,
      tunnel_connected: true,
      remote: { deployment_count: 0, collector_count: 0, collectors: [] },
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
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

  it('uses shared settings table and toolbar classes', async () => {
    const wrapper = mount(HostManagerTab, { global: { plugins: [installTestI18n('zh-CN')] } })
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
    await wrapper.vm.$nextTick()

    expect(wrapper.find('.settings-pane-header').exists()).toBe(true)
    expect(wrapper.find('.settings-toolbar').exists()).toBe(true)
    expect(wrapper.find('.settings-table').exists()).toBe(true)
    expect(wrapper.find('[data-test="host-add"]').classes()).toContain('settings-btn-primary')
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

  it('进入页面后自动检测远端 Agent 状态', async () => {
    mockedApi.listHosts.mockResolvedValue([host()])
    mockedApi.checkHostAgent.mockResolvedValue({
      host_id: 'h1',
      state: 'open',
      local_port: 57100,
      agent: 'healthy',
      agent_version: '0.1.0',
      agent_checked_at: '2026-06-05T10:00:00Z',
    } satisfies TunnelStatus)

    mount(HostManagerTab, { global: { plugins: [installTestI18n()] } })
    await flushMountedAsync()

    expect(mockedApi.checkHostAgent).toHaveBeenCalledWith('h1')
  })

  it('点击 Agent 刷新 icon 重新检测全部远端主机', async () => {
    const wrapper = mount(HostManagerTab, { global: { plugins: [installTestI18n()] } })
    const store = useRemoteStore()
    await flushMountedAsync()
    store.hosts = [host()]
    const spy = vi.spyOn(store, 'checkHostAgent').mockResolvedValue({
      host_id: 'h1',
      state: 'open',
      agent: 'healthy',
    })

    await wrapper.vm.$nextTick()
    await wrapper.find('[data-test="agent-refresh-all"]').trigger('click')

    expect(spy).toHaveBeenCalledWith('h1')
  })

  it('每行 Agent 刷新 icon 只检测当前主机', async () => {
    const wrapper = mount(HostManagerTab, { global: { plugins: [installTestI18n()] } })
    const store = useRemoteStore()
    await flushMountedAsync()
    store.hosts = [host({ id: 'h1', name: 'alpha' }), host({ id: 'h2', name: 'beta' })]
    const spy = vi.spyOn(store, 'checkHostAgent').mockResolvedValue({
      host_id: 'h2',
      state: 'open',
      agent: 'healthy',
    })

    await wrapper.vm.$nextTick()
    await wrapper.find('[data-test="host-refresh-agent-h2"]').trigger('click')

    expect(spy).toHaveBeenCalledTimes(1)
    expect(spy).toHaveBeenCalledWith('h2')
  })

  it('根据 Agent 检测结果显示安装或重装', async () => {
    const wrapper = mount(HostManagerTab, { global: { plugins: [installTestI18n('zh-CN')] } })
    const store = useRemoteStore()
    await flushMountedAsync()
    store.hosts = [host()]

    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-test="host-install-agent"]').text()).toBe('安装')

    store.applyTunnelUpdate({
      host_id: 'h1',
      state: 'open',
      agent: 'healthy',
      agent_version: '0.1.0',
      agent_checked_at: '2026-06-05T10:00:00Z',
    })
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-test="host-install-agent"]').text()).toBe('重装')
  })

  it('卸载 Agent 默认保留数据，可勾选同时删除数据', async () => {
    const wrapper = mount(HostManagerTab, { global: { plugins: [installTestI18n()] } })
    const store = useRemoteStore()
    await flushMountedAsync()
    store.hosts = [host()]
    const spy = vi.spyOn(store, 'uninstallHostAgent').mockResolvedValue({
      ok: true,
      host_id: 'h1',
      removed_data: true,
      message: 'Agent uninstalled',
    })

    await wrapper.vm.$nextTick()
    await wrapper.find('[data-test="host-uninstall-agent"]').trigger('click')
    expect(wrapper.find('[data-test="agent-uninstall-modal"]').exists()).toBe(true)
    const removeData = wrapper.find('[data-test="agent-uninstall-remove-data"]')
    expect((removeData.element as HTMLInputElement).checked).toBe(false)

    await removeData.setValue(true)
    await wrapper.find('[data-test="agent-uninstall-confirm"]').trigger('click')

    expect(spy).toHaveBeenCalledWith('h1', true)
  })

  it('Agent 操作说明固定在下一行，编辑和删除保持间距', async () => {
    const wrapper = mount(HostManagerTab, { global: { plugins: [installTestI18n()] } })
    const store = useRemoteStore()
    await flushMountedAsync()
    store.hosts = [host()]

    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-test="host-agent-actions"]').classes()).toContain('agent-action-row')
    expect(wrapper.find('[data-test="host-install-help"]').classes()).toContain('install-help-row')
    expect(wrapper.find('[data-test="host-row-actions"]').classes()).toContain('row-actions')
    expect(wrapper.find('[data-test="host-edit"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="host-delete"]').exists()).toBe(true)
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

  it('渲染远端编排和 collector 异常状态', async () => {
    mockedApi.listHosts.mockResolvedValue([host({ name: 'mac-02' })])
    mockedApi.getHostManagedDeploymentStatus.mockResolvedValue({
      host_id: 'h1',
      host_name: 'mac-02',
      desired_deployment_count: 1,
      desired_collector_count: 1,
      tunnel_connected: true,
      remote: {
        deployment_count: 1,
        collector_count: 1,
        collectors: [{
          deployment_id: 'local-browser-worker-prod',
          service_name: 'local-browser-worker',
          env_name: 'prod',
          name: '~/Library/Logs/local-browser-worker/worker.log',
          type: 'file_tail',
          desired: true,
          running: false,
          error: 'invalid path',
        }],
      },
    })

    const wrapper = mount(HostManagerTab, { global: { plugins: [installTestI18n('zh-CN')] } })
    await flushMountedAsync()

    expect(wrapper.find('[data-test="host-managed-status"]').text()).toContain('编排 1/1 · Collector 0/1')
    expect(wrapper.find('[data-test="host-managed-issue"]').text()).toContain('local-browser-worker：invalid path')
  })
})
