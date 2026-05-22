/**
 * LogPanel.remote 测试日志面板的远程模式生命周期。
 *
 * 职责：
 *   - 验证远程模式挂载订阅、卸载取消订阅
 *   - 验证远程日志行带 Host 前缀
 *
 * 边界：
 *   - 不建立真实 WebSocket
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import LogPanel from '@/components/Panel/LogPanel.vue'
import { useRemoteStore } from '@/stores/remote'
import { useRemoteLogStore } from '@/stores/remoteLog'
import { useWorkspaceStore } from '@/stores/workspace'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return { ...actual, api: { ...actual.api }, WS_BASE: 'ws://127.0.0.1:57018' }
})

describe('LogPanel 远程模式', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('挂载时调 remoteLog.subscribe；卸载时 unsubscribe', () => {
    const remoteLog = useRemoteLogStore()
    const sub = vi.spyOn(remoteLog, 'subscribe').mockResolvedValue(undefined)
    const unsub = vi.spyOn(remoteLog, 'unsubscribe').mockReturnValue()

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'p1',
        serviceId: null,
        projectId: null,
        logSourceId: 'ls1',
        groupKey: 'all',
      },
    })

    expect(sub).toHaveBeenCalledWith('ls1', 'all')
    wrapper.unmount()
    expect(unsub).toHaveBeenCalledWith('ls1', 'all')
  })

  it('远程日志行展示 host 前缀', async () => {
    const remote = useRemoteStore()
    remote.hosts = [
      {
        id: 'h1',
        name: 'host-01',
        ssh_host: '',
        ssh_port: 22,
        ssh_user: '',
        remote_agent_port: 57017,
        local_tunnel_port: 0,
        tags: ['prod'],
      },
    ]
    const remoteLog = useRemoteLogStore()
    vi.spyOn(remoteLog, 'subscribe').mockResolvedValue(undefined)
    vi.spyOn(remoteLog, 'logsOf').mockReturnValue([
      {
        id: 1,
        service_id: 's',
        run_id: 'r',
        timestamp: '2026-05-21T12:00:00Z',
        level: 'INFO',
        message: 'hello',
        stream: 'stdout',
        host_id: 'h1',
      },
    ])

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'p1',
        serviceId: null,
        projectId: null,
        logSourceId: 'ls1',
        groupKey: 'all',
      },
    })
    await new Promise(resolve => setTimeout(resolve))

    expect(wrapper.text()).toContain('[host-01]')
    expect(wrapper.text()).toContain('hello')
  })



  it('远程模式按 workspace tab 的节点可见性过滤已加载日志', async () => {
    const remote = useRemoteStore()
    remote.hosts = [
      {
        id: 'h1',
        name: 'host-01',
        ssh_host: '',
        ssh_port: 22,
        ssh_user: '',
        remote_agent_port: 57017,
        local_tunnel_port: 0,
        tags: ['prod'],
      },
      {
        id: 'h2',
        name: 'host-02',
        ssh_host: '',
        ssh_port: 22,
        ssh_user: '',
        remote_agent_port: 57017,
        local_tunnel_port: 0,
        tags: ['prod'],
      },
    ]
    const workspace = useWorkspaceStore() as ReturnType<typeof useWorkspaceStore> & {
      hideRemoteHost: (tabId: string, hostId: string) => void
    }
    const tab = workspace.openRemote('ls1', 'all')
    workspace.hideRemoteHost(tab.id, 'h1')

    const remoteLog = useRemoteLogStore()
    vi.spyOn(remoteLog, 'subscribe').mockResolvedValue(undefined)
    vi.spyOn(remoteLog, 'logsOf').mockReturnValue([
      {
        id: 1,
        service_id: 's',
        run_id: 'r',
        timestamp: '2026-05-21T12:00:00Z',
        level: 'INFO',
        message: 'hidden host line',
        stream: 'stdout',
        host_id: 'h1',
      },
      {
        id: 2,
        service_id: 's',
        run_id: 'r',
        timestamp: '2026-05-21T12:00:01Z',
        level: 'INFO',
        message: 'visible host line',
        stream: 'stdout',
        host_id: 'h2',
      },
    ])

    const wrapper = mount(LogPanel, {
      props: {
        panelId: tab.id,
        serviceId: null,
        projectId: null,
        logSourceId: 'ls1',
        groupKey: 'all',
      },
    })
    await new Promise(resolve => setTimeout(resolve))

    expect(wrapper.text()).not.toContain('hidden host line')
    expect(wrapper.text()).toContain('visible host line')
  })

  it('remote-aggregate keeps same host/id entries from different log sources and hides both by host', async () => {
    const remote = useRemoteStore()
    remote.hosts = [
      {
        id: 'h1',
        name: 'host-01',
        ssh_host: '',
        ssh_port: 22,
        ssh_user: '',
        remote_agent_port: 57017,
        local_tunnel_port: 0,
        tags: ['prod'],
      },
    ]
    const workspace = useWorkspaceStore() as ReturnType<typeof useWorkspaceStore> & {
      hideRemoteHost: (tabId: string, hostId: string) => void
    }
    const tab = workspace.openRemoteAggregate('project-a', 'service-api', 'api', ['ls-a', 'ls-b'], 'all')

    const remoteLog = useRemoteLogStore()
    vi.spyOn(remoteLog, 'subscribe').mockResolvedValue(undefined)
    vi.spyOn(remoteLog, 'logsOf').mockImplementation((logSourceId: string) => [
      {
        id: 1,
        service_id: 's',
        run_id: 'r',
        timestamp: '2026-05-21T12:00:00Z',
        level: 'INFO',
        message: `${logSourceId} aggregate line`,
        stream: 'stdout',
        host_id: 'h1',
      },
    ])

    const wrapper = mount(LogPanel, {
      props: {
        panelId: tab.id,
        serviceId: null,
        projectId: null,
        logSourceIds: ['ls-a', 'ls-b'],
        groupKey: 'all',
      },
    })
    await new Promise(resolve => setTimeout(resolve))

    expect(wrapper.text()).toContain('ls-a aggregate line')
    expect(wrapper.text()).toContain('ls-b aggregate line')

    workspace.hideRemoteHost(tab.id, 'h1')
    await new Promise(resolve => setTimeout(resolve))

    expect(wrapper.text()).not.toContain('ls-a aggregate line')
    expect(wrapper.text()).not.toContain('ls-b aggregate line')
  })

  it('single remote tab does not render a top host chip filter separate from the bottom bar state', () => {
    const remoteLog = useRemoteLogStore()
    vi.spyOn(remoteLog, 'subscribe').mockResolvedValue(undefined)

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'remote:ls1:all',
        serviceId: null,
        projectId: null,
        logSourceId: 'ls1',
        groupKey: 'all',
      },
    })

    expect(wrapper.findComponent({ name: 'RemoteHostChips' }).exists()).toBe(false)
  })

  it('远程模式工具栏搜索按钮打开 remote-search tab', async () => {
    const remoteLog = useRemoteLogStore()
    vi.spyOn(remoteLog, 'subscribe').mockResolvedValue(undefined)
    const workspace = useWorkspaceStore()
    const spy = vi.spyOn(workspace, 'openRemoteSearch')
    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'p1',
        serviceId: null,
        projectId: null,
        logSourceId: 'ls1',
        groupKey: 'all',
      },
    })

    await wrapper.find('[data-test="remote-search-button"]').trigger('click')

    expect(spy).toHaveBeenCalledWith('ls1', 'all')
  })
})
