/**
 * remote store 测试远程监听域的 Host、LogSource 与 Tunnel 状态。
 *
 * 职责：
 *   - 验证 Host / LogSource CRUD action 会同步本地 state
 *   - 验证 LogSource 按 Host tag 计算分组
 *   - 验证 managed deployment 状态按 host_id 缓存
 *
 * 边界：
 *   - 不建立真实 HTTP 连接，API 通过 mock 隔离
 *   - 不渲染任何组件
 */
import { describe, it, expect, beforeEach, vi, type Mock } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useRemoteStore } from '@/stores/remote'
import { api, type Host, type LogSource } from '@/api/agent'

vi.mock('@/stores/agent', () => ({
  useAgentStore: vi.fn(() => ({
    projects: [],
    projectById: vi.fn(() => undefined),
  })),
}))

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      listHosts: vi.fn(),
      createHost: vi.fn(),
      updateHost: vi.fn(),
      deleteHost: vi.fn(),
      getHostManagedDeploymentStatus: vi.fn(),
      listLogSources: vi.fn(),
      createLogSource: vi.fn(),
      updateLogSource: vi.fn(),
      deleteLogSource: vi.fn(),
    },
  }
})

const mockedApi = api as unknown as Record<string, Mock>

function makeHost(overrides: Partial<Host> = {}): Host {
  return {
    id: 'h1',
    name: 'host-01',
    public_ip: '203.0.113.10',
    private_ip: '10.0.0.1',
    tags: ['prod'],
    ...overrides,
  }
}

function makeLogSource(overrides: Partial<LogSource> = {}): LogSource {
  return {
    id: 'ls1',
    name: 'nova-api',
    type: 'journalctl',
    host_ids: ['h1'],
    tags: [],
    extra_args: [],
    ...overrides,
  }
}

describe('useRemoteStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  describe('hosts', () => {
    it('loadHosts 拉取并写入 state', async () => {
      mockedApi.listHosts.mockResolvedValue([makeHost()])
      const store = useRemoteStore()

      await store.loadHosts()

      expect(store.hosts).toHaveLength(1)
      expect(store.hosts[0].name).toBe('host-01')
    })

    it('createHost 成功后追加到 hosts', async () => {
      const created = makeHost({ id: 'h2', name: 'host-02' })
      mockedApi.createHost.mockResolvedValue(created)
      const store = useRemoteStore()

      await store.createHost({
        name: 'host-02',
        public_ip: '203.0.113.11',
        tags: ['prod'],
      })

      expect(store.hosts.some(h => h.id === 'h2')).toBe(true)
    })

    it('updateHost 替换对应 id', async () => {
      mockedApi.listHosts.mockResolvedValue([makeHost()])
      const store = useRemoteStore()
      await store.loadHosts()
      const updated = makeHost({ tags: ['prod', 'temp'] })
      mockedApi.updateHost.mockResolvedValue(updated)

      await store.updateHost('h1', { tags: ['prod', 'temp'] })

      expect(store.hosts[0].tags).toEqual(['prod', 'temp'])
    })

    it('deleteHost 从 hosts 移除', async () => {
      mockedApi.listHosts.mockResolvedValue([makeHost()])
      const store = useRemoteStore()
      await store.loadHosts()
      mockedApi.deleteHost.mockResolvedValue(undefined)

      await store.deleteHost('h1')

      expect(store.hosts).toHaveLength(0)
    })

    it('hostById getter 按 id 查找', async () => {
      mockedApi.listHosts.mockResolvedValue([makeHost()])
      const store = useRemoteStore()

      await store.loadHosts()

      expect(store.hostById('h1')?.name).toBe('host-01')
      expect(store.hostById('missing')).toBeUndefined()
    })

    it('getHostManagedDeploymentStatus 拉取后写入共享 managed status 缓存', async () => {
      mockedApi.getHostManagedDeploymentStatus.mockResolvedValue({
        host_id: 'h1',
        host_name: 'host-01',
        desired_deployment_count: 1,
        desired_collector_count: 1,
        active_collector_count: 1,
        tunnel_connected: true,
        remote: { deployment_count: 1, collector_count: 1, active_collector_count: 1, collectors: [] },
      })
      const store = useRemoteStore()

      await store.getHostManagedDeploymentStatus('h1')

      expect(store.managedStatusOf('h1')?.desired_deployment_count).toBe(1)
      expect(store.managedStatuses.get('h1')?.host_name).toBe('host-01')
    })
  })

  describe('log sources', () => {
    it('loadLogSources 拉取并写入', async () => {
      mockedApi.listLogSources.mockResolvedValue([makeLogSource()])
      const store = useRemoteStore()

      await store.loadLogSources()

      expect(store.logSources).toHaveLength(1)
    })

    it('groupsOf 按 LogSource.tags 生成分组', async () => {
      mockedApi.listHosts.mockResolvedValue([
        makeHost({ id: 'h1', tags: [] }),
        makeHost({ id: 'h2', tags: [] }),
      ])
      mockedApi.listLogSources.mockResolvedValue([
        makeLogSource({ id: 'ls1', host_ids: ['h1', 'h2'], tags: ['prod', 'test'] }),
      ])
      const store = useRemoteStore()

      await store.loadHosts()
      await store.loadLogSources()

      const groups = store.groupsOf('ls1')
      const map = Object.fromEntries(groups.map(g => [g.key, g.hostIds.sort()]))
      // all + prod + test, each containing all hosts (groups are views, not filters)
      expect(map.all).toEqual(['h1', 'h2'])
      expect(map.prod).toEqual(['h1', 'h2'])
      expect(map.test).toEqual(['h1', 'h2'])
      // Host has no tags; no host-derived groups
      expect(map.temp).toBeUndefined()
    })

    it('groupsOf 不存在的 LogSource 返回空数组', () => {
      const store = useRemoteStore()

      expect(store.groupsOf('missing')).toEqual([])
    })
  })

  describe('remoteServiceGroupsOf', () => {
    it('returns empty when no bound logSources', () => {
      const store = useRemoteStore()
      store.hosts = []
      store.logSources = []
      expect(store.remoteServiceGroupsOf('proj-1')).toEqual([])
    })

    it('aggregates logSources by serviceId with correct tag grouping', () => {
      const store = useRemoteStore()
      store.hosts = [
        makeHost({ id: 'h1' }),
        makeHost({ id: 'h2' }),
        makeHost({ id: 'h3' }),
      ]
      store.logSources = [
        makeLogSource({ id: 'ls-a', host_ids: ['h1'], tags: ['prod'], project_id: 'proj-1', service_id: 'svc-server' }),
        makeLogSource({ id: 'ls-b', host_ids: ['h2', 'h3'], tags: ['test'], project_id: 'proj-1', service_id: 'svc-server' }),
      ]

      const result = store.remoteServiceGroupsOf('proj-1')
      expect(result).toHaveLength(1)

      const svcGroup = result[0]
      expect(svcGroup.serviceId).toBe('svc-server')
      expect(svcGroup.logSourceIds).toEqual(['ls-a', 'ls-b'])

      const groupMap = Object.fromEntries(svcGroup.groups.map(g => [g.key, g.hostIds]))
      expect(groupMap['all']).toHaveLength(3)
      expect(groupMap['all']).toEqual(expect.arrayContaining(['h1', 'h2', 'h3']))
      // prod only from ls-a → only h1
      expect(groupMap['prod']).toEqual(['h1'])
      // test only from ls-b → h2, h3
      expect(groupMap['test']).toHaveLength(2)
      expect(groupMap['test']).toEqual(expect.arrayContaining(['h2', 'h3']))
    })

    it('ignores logSources bound to other projects', () => {
      const store = useRemoteStore()
      store.hosts = [makeHost({ id: 'h1' })]
      store.logSources = [
        makeLogSource({ id: 'ls-a', host_ids: ['h1'], project_id: 'proj-other', service_id: 'svc-server' }),
      ]
      expect(store.remoteServiceGroupsOf('proj-1')).toEqual([])
    })
  })
})
