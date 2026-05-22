/**
 * RemoteHostChips 测试远程日志面板的 Host 筛选条。
 *
 * 职责：
 *   - 验证按 remoteLog view 渲染 group 内 Host
 *   - 验证点击 chip 上抛选中集合
 *
 * 边界：
 *   - 不建立远程日志连接
 */
import { describe, it, expect, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import RemoteHostChips from '@/components/Panel/RemoteHostChips.vue'
import { useRemoteStore } from '@/stores/remote'
import { useRemoteLogStore } from '@/stores/remoteLog'

function seedSession() {
  const remote = useRemoteStore()
  const remoteLog = useRemoteLogStore()
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
    {
      id: 'h2',
      name: 'host-02',
      ssh_host: '',
      ssh_port: 22,
      ssh_user: '',
      remote_agent_port: 57017,
      tags: ['prod'],
      created_at: '',
      updated_at: '',
    },
  ]
  remoteLog.sessions.set('ls1::all', {
    refCount: 1,
    view: {
      log_source: {
        id: 'ls1',
        name: 'x',
        type: 'journalctl',
        host_ids: ['h1', 'h2'],
        created_at: '',
        updated_at: '',
      },
      groups: [{ group_key: 'all', host_ids: ['h1', 'h2'] }],
      hosts: [],
    },
    logs: [],
    sockets: new Map(),
    errors: new Map(),
    loadingHistory: false,
  } as unknown as never)
  remoteLog.sessions = new Map(remoteLog.sessions)
}

describe('RemoteHostChips', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('渲染该 group 内每个 host 的 chip', () => {
    seedSession()

    const wrapper = mount(RemoteHostChips, {
      props: { logSourceId: 'ls1', groupKey: 'all', selectedHostIds: new Set<string>() },
    })

    const chips = wrapper.findAll('[data-test="remote-host-chip"]')
    expect(chips).toHaveLength(2)
    expect(chips[0].text()).toContain('host-01')
  })

  it('点击 chip emit 更新选中集合', async () => {
    seedSession()
    const wrapper = mount(RemoteHostChips, {
      props: { logSourceId: 'ls1', groupKey: 'all', selectedHostIds: new Set<string>() },
    })

    await wrapper.find('[data-test="remote-host-chip"]').trigger('click')

    const emitted = wrapper.emitted('update:selectedHostIds')!
    const next = emitted[0][0] as Set<string>
    expect(next.has('h1')).toBe(true)
  })
})
