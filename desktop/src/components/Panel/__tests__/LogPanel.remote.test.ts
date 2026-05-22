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
import { useBookmarkStore } from '@/stores/bookmark'
import { useWorkspaceStore } from '@/stores/workspace'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return { ...actual, api: { ...actual.api }, WS_BASE: 'ws://127.0.0.1:57018' }
})

describe('LogPanel 远程模式', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

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

  it('同步录制中切换远程来源后不会把新来源日志追加到旧 source 书签', async () => {
    const remoteLog = useRemoteLogStore()
    vi.spyOn(remoteLog, 'subscribe').mockResolvedValue(undefined)
    vi.spyOn(remoteLog, 'unsubscribe').mockReturnValue()
    vi.spyOn(remoteLog, 'logsOf').mockImplementation((logSourceId: string) => {
      if (logSourceId === 'ls2') {
        return [{
          id: 2,
          service_id: 'remote-service',
          run_id: 'run-2',
          timestamp: new Date(Date.now() + 1000).toISOString(),
          level: 'INFO',
          message: 'new source should not be captured',
          stream: 'stdout',
          host_id: 'h2',
        }]
      }
      return []
    })
    const bookmarkStore = useBookmarkStore()

    const wrapper = mount(LogPanel, {
      props: {
        panelId: 'p1',
        serviceId: null,
        projectId: null,
        logSourceId: 'ls1',
        groupKey: 'all',
        source: { type: 'remote-log-source', logSourceId: 'ls1', groupKey: 'all' },
      },
    })
    bookmarkStore.startBookmark('p1', null, {
      type: 'remote-log-source',
      logSourceId: 'ls1',
      groupKey: 'all',
    })

    await wrapper.setProps({
      logSourceId: 'ls2',
      source: { type: 'remote-log-source', logSourceId: 'ls2', groupKey: 'all' },
    })
    remoteLog.logSourceRevision++
    await new Promise(resolve => setTimeout(resolve))

    expect(bookmarkStore.getBookmark('p1')?.lockedLogs).toEqual([])
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
