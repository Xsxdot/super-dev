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
        tags: ['prod'],
        created_at: '',
        updated_at: '',
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
})
