/**
 * workspaceStore 测试右侧项目/搜索标签页状态。
 *
 * 职责：
 *   - 验证服务点击复用项目标签
 *   - 验证搜索按钮每次创建新的搜索标签
 *   - 验证隐藏服务和固定服务是搜索标签局部状态
 *
 * 边界：
 *   - 不渲染组件
 *   - 不建立真实 agent 连接
 */
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api as agentApi } from '@/api/agent'
import { useAgentStore } from '../agent'
import { usePanelStore } from '../panel'
import { useWorkspaceStore } from '../workspace'
import type { LogEntry, Project, Service } from '@/api/agent'

function service(id: string, name: string, projectId = 'proj-1'): Service {
  return {
    id,
    project_id: projectId,
    name,
    status: 'running',
    command: 'pnpm dev',
    work_dir: '/tmp/project',
    required: false,
    order: 1,
  }
}

function project(services: Service[], id = 'proj-1', name = 'Project'): Project {
  return {
    id,
    name,
    root_path: '/tmp/project',
    services,
    selected_service_ids: [],
  }
}

function log(
  id: number,
  serviceId: string,
  message: string,
  timestamp = '2026-05-20T22:41:32.000Z',
): LogEntry {
  return {
    id,
    service_id: serviceId,
    run_id: 'run-1',
    timestamp,
    level: 'INFO',
    message,
    stream: 'stdout',
  }
}

describe('workspaceStore', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
    setActivePinia(createPinia())
  })

  it('openService 创建项目标签并打开服务', () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    const workspace = useWorkspaceStore()
    const panel = usePanelStore()

    workspace.openService('proj-1', 'svc-api')

    expect(workspace.tabs).toHaveLength(1)
    expect(workspace.activeTab?.type).toBe('project')
    expect(panel.allLeaves[0].serviceId).toBe('svc-api')
  })

  it('openService 复用已有项目标签', () => {
    const api = service('svc-api', 'api')
    const worker = service('svc-worker', 'worker')
    useAgentStore().projects = [project([api, worker])]
    const workspace = useWorkspaceStore()

    workspace.openService('proj-1', 'svc-api')
    workspace.openService('proj-1', 'svc-worker')

    expect(workspace.tabs.filter(t => t.type === 'project')).toHaveLength(1)
  })

  it('openSearch 每次创建新的搜索标签', () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    const workspace = useWorkspaceStore()

    workspace.openSearch('proj-1')
    workspace.openSearch('proj-1')

    expect(workspace.tabs.filter(t => t.type === 'search')).toHaveLength(2)
    expect(workspace.activeTab?.type).toBe('search')
  })

  it('openRemote 为同一远程分组复用标签', () => {
    const workspace = useWorkspaceStore()

    workspace.openRemote('ls1', 'all')
    workspace.openRemote('ls1', 'all')

    expect(workspace.tabs.filter(t => t.type === 'remote')).toHaveLength(1)
    expect(workspace.activeTab).toMatchObject({
      type: 'remote',
      logSourceId: 'ls1',
      groupKey: 'all',
    })
  })

  it('openRemoteSearch 为远程分组创建搜索标签', () => {
    const workspace = useWorkspaceStore()

    workspace.openRemoteSearch('ls1', 'prod')

    expect(workspace.activeTab).toMatchObject({
      type: 'remote-search',
      logSourceId: 'ls1',
      groupKey: 'prod',
    })
  })

  it('项目标签切换时恢复各自的 panel root', () => {
    const api = service('svc-api', 'api', 'proj-1')
    const admin = service('svc-admin', 'admin', 'proj-2')
    useAgentStore().projects = [
      project([api], 'proj-1', 'Project A'),
      project([admin], 'proj-2', 'Project B'),
    ]
    const workspace = useWorkspaceStore()
    const panel = usePanelStore()

    workspace.openService('proj-1', 'svc-api')
    const firstTabId = workspace.activeTabId
    workspace.openService('proj-2', 'svc-admin')
    expect(panel.allLeaves[0].serviceId).toBe('svc-admin')

    workspace.activateTab(firstTabId!)

    expect(panel.allLeaves[0].serviceId).toBe('svc-api')
  })

  it('openRemote 将远程监听作为日志工作区 panel source 打开', () => {
    const workspace = useWorkspaceStore()
    const panel = usePanelStore()

    const tab = workspace.openRemote('remote-prod', 'all') as any

    expect(tab.layoutRoot).toBeTruthy()
    expect(tab.focusedPanelId).toBeTruthy()
    expect((panel.allLeaves[0] as any).source).toEqual({
      type: 'remote-log-source',
      logSourceId: 'remote-prod',
      groupKey: 'all',
    })
  })

  it('remote 与 project 标签切换时分别保存和恢复 layoutRoot 与焦点', () => {
    const api = service('svc-api', 'api', 'project-A')
    useAgentStore().projects = [project([api], 'project-A', 'Project A')]
    const workspace = useWorkspaceStore()
    const panel = usePanelStore()

    workspace.openService('project-A', 'svc-api')
    const projectTabId = workspace.activeTabId!
    const projectPanelId = panel.allLeaves[0].id

    const remoteTab = workspace.openRemote('remote-prod', 'all') as any
    const remotePanelId = panel.allLeaves[0].id
    ;((panel as any).splitLeafWithSource ?? (() => undefined))(
      remotePanelId,
      'h',
      {
        type: 'remote-log-source',
        logSourceId: 'remote-prod',
        groupKey: 'api',
      },
      'second',
    )
    const focusedRemoteId = panel.allLeaves[1]?.id
    panel.setFocus(focusedRemoteId)
    ;((workspace as any).saveActiveLogWorkspaceLayout ?? workspace.saveActiveProjectLayout)()

    workspace.activateTab(projectTabId)
    expect(panel.allLeaves).toHaveLength(1)
    expect(panel.allLeaves[0].id).toBe(projectPanelId)

    workspace.activateTab(remoteTab.id)

    expect(panel.allLeaves).toHaveLength(2)
    expect(panel.focusedPanelId).toBe(focusedRemoteId)
    expect(panel.allLeaves.map(leaf => (leaf as any).source?.groupKey)).toEqual(['all', 'api'])
  })

  it('openRemoteAggregate 复用标签时同步更新 layoutRoot 内的远程聚合来源', () => {
    const workspace = useWorkspaceStore()
    const panel = usePanelStore()

    const tab = workspace.openRemoteAggregate(
      'project-A',
      'svc-api',
      'api',
      ['remote-1'],
      'all',
    ) as any
    const firstLeafId = panel.allLeaves[0].id

    workspace.openRemoteAggregate(
      'project-A',
      'svc-api',
      'api',
      ['remote-2', 'remote-3'],
      'all',
    )

    expect(workspace.activeTab?.id).toBe(tab.id)
    expect((workspace.activeTab as any).logSourceIds).toEqual(['remote-2', 'remote-3'])
    expect(panel.allLeaves[0].id).toBe(firstLeafId)
    expect((panel.allLeaves[0] as any).source).toMatchObject({
      type: 'remote-aggregate',
      logSourceIds: ['remote-2', 'remote-3'],
      groupKey: 'all',
      projectId: 'project-A',
      serviceId: 'svc-api',
      serviceName: 'api',
    })
  })

  it('搜索标签的隐藏和固定服务互不影响', () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    const workspace = useWorkspaceStore()

    const first = workspace.openSearch('proj-1')
    workspace.hideService(first.id, 'svc-api')
    workspace.pinService(first.id, 'svc-api')
    const second = workspace.openSearch('proj-1')

    expect(workspace.searchTab(first.id)?.hiddenServiceIds).toEqual(['svc-api'])
    expect(workspace.searchTab(first.id)?.pinnedServiceIds).toEqual(['svc-api'])
    expect(workspace.searchTab(second.id)?.hiddenServiceIds).toEqual([])
    expect(workspace.searchTab(second.id)?.pinnedServiceIds).toEqual([])
  })



  it('远程节点隐藏状态按远程 tab 隔离', () => {
    const workspace = useWorkspaceStore() as ReturnType<typeof useWorkspaceStore> & {
      hideRemoteHost: (tabId: string, hostId: string) => void
      visibleRemoteHostIds: (tabId: string, hostIds: string[]) => string[]
    }

    const all = workspace.openRemote('ls1', 'all')
    const prod = workspace.openRemote('ls1', 'prod')
    workspace.hideRemoteHost(all.id, 'h1')

    expect(workspace.visibleRemoteHostIds(all.id, ['h1', 'h2'])).toEqual(['h2'])
    expect(workspace.visibleRemoteHostIds(prod.id, ['h1', 'h2'])).toEqual(['h1', 'h2'])
  })

  it('关闭远程 tab 时清理该 tab 的节点隐藏状态', () => {
    const workspace = useWorkspaceStore() as ReturnType<typeof useWorkspaceStore> & {
      hideRemoteHost: (tabId: string, hostId: string) => void
      visibleRemoteHostIds: (tabId: string, hostIds: string[]) => string[]
    }

    const tab = workspace.openRemote('ls1', 'all')
    workspace.hideRemoteHost(tab.id, 'h1')
    workspace.closeTab(tab.id)
    const reopened = workspace.openRemote('ls1', 'all')

    expect(workspace.visibleRemoteHostIds(reopened.id, ['h1', 'h2'])).toEqual(['h1', 'h2'])
  })

  it('runSearch 将搜索结果写入当前搜索标签', async () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    vi.spyOn(agentApi, 'searchLogs').mockResolvedValue({
      query: 'trace-8f21',
      total: 1,
      items: [log(1, 'svc-api', 'trace-8f21 target')],
      service_counts: { 'svc-api': 1 },
      has_more: false,
    })
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')

    await workspace.runSearch(tab.id, ' trace-8f21 ')

    expect(workspace.searchTab(tab.id)?.status).toBe('results')
    expect(workspace.searchTab(tab.id)?.query).toBe('trace-8f21')
    expect(workspace.searchTab(tab.id)?.serviceCounts).toEqual({ 'svc-api': 1 })
  })

  it('loadMoreSearchResults 使用最后一条可见命中继续加载', async () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    vi.spyOn(agentApi, 'searchLogs')
      .mockResolvedValueOnce({
        query: 'trace-8f21',
        total: 2,
        items: [log(1, 'svc-api', 'first', '2026-05-20T22:41:32.000Z')],
        service_counts: { 'svc-api': 2 },
        has_more: true,
      })
      .mockResolvedValueOnce({
        query: 'trace-8f21',
        total: 2,
        items: [log(2, 'svc-api', 'second', '2026-05-20T22:41:33.000Z')],
        service_counts: { 'svc-api': 2 },
        has_more: false,
      })
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')

    await workspace.runSearch(tab.id, 'trace-8f21')
    await workspace.loadMoreSearchResults(tab.id)

    expect(agentApi.searchLogs).toHaveBeenLastCalledWith({
      project: 'proj-1',
      q: 'trace-8f21',
      service: ['svc-api'],
      cursor_time: '2026-05-20T22:41:32.000Z',
      cursor_id: 1,
      limit: 1000,
    })
    expect(tab.results.map(entry => entry.message)).toEqual(['first', 'second'])
  })

  it('隐藏占满首屏的服务后自动补齐可见服务命中', async () => {
    const logger = service('svc-logger', 'logger')
    const server = service('svc-server', 'server')
    useAgentStore().projects = [project([logger, server])]
    vi.spyOn(agentApi, 'searchLogs')
      .mockResolvedValueOnce({
        query: 'trace-8f21',
        total: 1002,
        items: [log(1, 'svc-logger', 'logger first', '2026-05-20T22:41:32.000Z')],
        service_counts: { 'svc-logger': 1000, 'svc-server': 2 },
        has_more: true,
      })
      .mockResolvedValueOnce({
        query: 'trace-8f21',
        total: 2,
        items: [log(1001, 'svc-server', 'server first', '2026-05-20T22:41:30.000Z')],
        service_counts: { 'svc-server': 2 },
        has_more: true,
      })
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')

    await workspace.runSearch(tab.id, 'trace-8f21')
    await workspace.hideService(tab.id, 'svc-logger')

    expect(agentApi.searchLogs).toHaveBeenLastCalledWith({
      project: 'proj-1',
      q: 'trace-8f21',
      service: ['svc-server'],
      limit: 1000,
    })
    expect(tab.results.map(entry => entry.message)).toEqual(['server first', 'logger first'])
  })

  it('loadContext 只更新未固定的可见服务上下文', async () => {
    const api = service('svc-api', 'api')
    const worker = service('svc-worker', 'worker')
    const billing = service('svc-billing', 'billing')
    useAgentStore().projects = [project([api, worker, billing])]
    vi.spyOn(agentApi, 'fetchLogContext').mockResolvedValue({
      target_id: 9,
      anchor_time: '2026-05-20T22:41:32.000Z',
      items_by_service: {
        'svc-api': [log(9, 'svc-api', 'new api')],
        'svc-worker': [log(10, 'svc-worker', 'new worker')],
      },
    })
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-api': 1, 'svc-worker': 1, 'svc-billing': 1 }
    tab.contextByService['svc-api'] = [log(1, 'svc-api', 'old api')]
    workspace.pinService(tab.id, 'svc-api')
    workspace.hideService(tab.id, 'svc-billing')

    await workspace.loadContext(tab.id, 9)

    expect(agentApi.fetchLogContext).toHaveBeenCalledWith({
      project: 'proj-1',
      id: 9,
      service: ['svc-api', 'svc-worker'],
    })
    expect(tab.contextByService['svc-api'].map(entry => entry.message)).toEqual(['old api'])
    expect(tab.contextByService['svc-worker'].map(entry => entry.message)).toEqual(['new worker'])
  })

  it('loadMoreContext 按可见服务独立游标向上补充上下文', async () => {
    const api = service('svc-api', 'api')
    const worker = service('svc-worker', 'worker')
    const billing = service('svc-billing', 'billing')
    useAgentStore().projects = [project([api, worker, billing])]
    vi.spyOn(agentApi, 'fetchLogContextPage').mockImplementation(async params => ({
      service_id: params.service,
      direction: params.direction,
      items:
        params.service === 'svc-api'
          ? [log(1, 'svc-api', 'older api', '2026-05-20T22:41:30.000Z')]
          : [log(2, 'svc-worker', 'older worker', '2026-05-20T22:41:31.000Z')],
      has_more: params.service === 'svc-api',
    }))
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-api': 1, 'svc-worker': 1, 'svc-billing': 1 }
    tab.contextAnchorTime = '2026-05-20T22:41:32.000Z'
    tab.contextByService = {
      'svc-api': [log(9, 'svc-api', 'current api', '2026-05-20T22:41:32.000Z')],
      'svc-worker': [],
    }
    workspace.hideService(tab.id, 'svc-billing')

    await workspace.loadMoreContext(tab.id, 'before')

    expect(agentApi.fetchLogContextPage).toHaveBeenCalledWith({
      project: 'proj-1',
      service: 'svc-api',
      direction: 'before',
      cursor_time: '2026-05-20T22:41:32.000Z',
      cursor_id: 9,
      limit: 200,
    })
    expect(agentApi.fetchLogContextPage).toHaveBeenCalledWith({
      project: 'proj-1',
      service: 'svc-worker',
      direction: 'before',
      cursor_time: '2026-05-20T22:41:32.000Z',
      cursor_id: 0,
      limit: 200,
    })
    expect(agentApi.fetchLogContextPage).toHaveBeenCalledTimes(2)
    expect(tab.contextByService['svc-api'].map(entry => entry.message)).toEqual([
      'older api',
      'current api',
    ])
    expect(tab.contextByService['svc-worker'].map(entry => entry.message)).toEqual([
      'older worker',
    ])
    expect(tab.hasMoreBeforeByService['svc-api']).toBe(true)
    expect(tab.hasMoreBeforeByService['svc-worker']).toBe(false)
  })

  it('loadMoreContext 不请求已固定服务，避免固定列跟随分页刷新', async () => {
    const api = service('svc-api', 'api')
    const worker = service('svc-worker', 'worker')
    useAgentStore().projects = [project([api, worker])]
    vi.spyOn(agentApi, 'fetchLogContextPage').mockImplementation(async params => ({
      service_id: params.service,
      direction: params.direction,
      items: [log(2, params.service, 'older worker', '2026-05-20T22:41:31.000Z')],
      has_more: false,
    }))
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-api': 1, 'svc-worker': 1 }
    tab.contextAnchorTime = '2026-05-20T22:41:32.000Z'
    tab.contextByService = {
      'svc-api': [log(9, 'svc-api', 'pinned api', '2026-05-20T22:41:32.000Z')],
      'svc-worker': [log(10, 'svc-worker', 'current worker', '2026-05-20T22:41:32.000Z')],
    }
    workspace.pinService(tab.id, 'svc-api')

    await workspace.loadMoreContext(tab.id, 'before')

    expect(agentApi.fetchLogContextPage).toHaveBeenCalledTimes(1)
    expect(agentApi.fetchLogContextPage).toHaveBeenCalledWith({
      project: 'proj-1',
      service: 'svc-worker',
      direction: 'before',
      cursor_time: '2026-05-20T22:41:32.000Z',
      cursor_id: 10,
      limit: 200,
    })
    expect(tab.contextByService['svc-api'].map(entry => entry.message)).toEqual(['pinned api'])
  })

  it('openRemoteProjectSearch creates a project-level remote search tab with presentation state', () => {
    const api = service('svc-api', 'api')
    const worker = service('svc-worker', 'worker')
    useAgentStore().projects = [project([api, worker])]
    const workspace = useWorkspaceStore() as any

    expect(typeof workspace.openRemoteProjectSearch).toBe('function')
    const tab = workspace.openRemoteProjectSearch('proj-1', 'all')

    expect(tab).toMatchObject({
      type: 'remote-search',
      projectId: 'proj-1',
      groupKey: 'all',
      status: 'empty',
      serviceColumns: [],
      hiddenServiceIds: [],
      pinnedServiceIds: [],
      failures: [],
      nextCursor: null,
      hasMore: false,
    })
    expect(tab.logSourceId).toBeUndefined()
    expect(workspace.activeTab).toBe(tab)
  })

  it('runRemoteSearch stores service columns and hide/pin only changes presentation state', async () => {
    const api = service('svc-api', 'api')
    const worker = service('svc-worker', 'worker')
    useAgentStore().projects = [project([api, worker])]
    vi.spyOn(agentApi, 'remoteSearch').mockResolvedValue({
      query: 'error',
      status: 'partial_failed',
      service_columns: [
        {
          service_id: 'svc-api',
          service_name: 'api',
          status: 'success',
          result_count: 1,
          node_count: 1,
          nodes: [{ host_id: 'host-a', host_name: 'node-a', status: 'success', count: 1 }],
          entries: [{
            key: 'svc-api/ls-api/host-a:101',
            id: 101,
            service_id: 'svc-api',
            log_source_id: 'ls-api',
            host_id: 'host-a',
            host_name: 'node-a',
            run_id: 'run-1',
            timestamp: '2026-05-20T22:41:32.000Z',
            level: 'ERROR',
            message: 'api error',
            stream: 'stderr',
          }],
        },
        {
          service_id: 'svc-worker',
          service_name: 'worker',
          status: 'partial_failed',
          result_count: 0,
          node_count: 1,
          nodes: [{ host_id: 'host-b', host_name: 'node-b', status: 'failed', count: 0, error: 'boom' }],
          entries: [],
        },
      ],
      failures: [{ service_id: 'svc-worker', host_id: 'host-b', kind: 'failed', message: 'boom' }],
      next_cursor: 'cursor-1',
      has_more: true,
    } as any)
    const workspace = useWorkspaceStore() as any
    const tab = workspace.openRemoteProjectSearch('proj-1', 'all')

    await workspace.runRemoteSearch(tab.id, ' error ')
    await workspace.hideService(tab.id, 'svc-worker')
    workspace.pinService(tab.id, 'svc-api')

    expect(agentApi.remoteSearch).toHaveBeenCalledWith({
      project_id: 'proj-1',
      group: 'all',
      query: 'error',
      limit: 200,
    })
    expect(tab.status).toBe('partialFailed')
    expect(tab.serviceColumns.map((column: any) => column.service_id)).toEqual(['svc-api', 'svc-worker'])
    expect(tab.failures).toHaveLength(1)
    expect(tab.nextCursor).toBe('cursor-1')
    expect(tab.hiddenServiceIds).toEqual(['svc-worker'])
    expect(tab.pinnedServiceIds).toEqual(['svc-api'])
    expect(tab.serviceColumns).toHaveLength(2)
  })

  it('runRemoteSearch ignores stale responses from rapid consecutive searches', async () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    let resolveFirst: (value: any) => void = () => {}
    vi.spyOn(agentApi, 'remoteSearch')
      .mockImplementationOnce(() => new Promise(resolve => { resolveFirst = resolve }))
      .mockResolvedValueOnce({
        query: 'panic',
        status: 'success',
        service_columns: [{
          service_id: 'svc-api',
          service_name: 'api',
          status: 'success',
          result_count: 1,
          node_count: 1,
          nodes: [],
          entries: [{ key: 'svc-api/ls-api/host-a:2', id: 2, service_id: 'svc-api', log_source_id: 'ls-api', host_id: 'host-a', host_name: 'node-a', run_id: 'r', timestamp: '2026-05-20T22:41:33.000Z', level: 'ERROR', message: 'panic target', stream: 'stderr' }],
        }],
        failures: [],
        next_cursor: '',
        has_more: false,
      } as any)
    const workspace = useWorkspaceStore() as any
    const tab = workspace.openRemoteProjectSearch('proj-1', 'all')

    const first = workspace.runRemoteSearch(tab.id, 'error')
    await workspace.runRemoteSearch(tab.id, 'panic')
    resolveFirst({
      query: 'error',
      status: 'success',
      service_columns: [{ service_id: 'svc-api', service_name: 'api', status: 'success', result_count: 1, node_count: 1, nodes: [], entries: [{ key: 'old', id: 1, service_id: 'svc-api', log_source_id: 'ls-api', host_id: 'host-a', host_name: 'node-a', run_id: 'r', timestamp: '2026-05-20T22:41:32.000Z', level: 'INFO', message: 'stale error', stream: 'stdout' }] }],
      failures: [],
      next_cursor: '',
      has_more: false,
    })
    await first

    expect(tab.query).toBe('panic')
    expect(tab.serviceColumns[0].entries[0].message).toBe('panic target')
  })

  it('local search hide and pin behavior remains scoped to local search tabs', async () => {
    const api = service('svc-api', 'api')
    const worker = service('svc-worker', 'worker')
    useAgentStore().projects = [project([api, worker])]
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')
    tab.serviceCounts = { 'svc-api': 1, 'svc-worker': 1 }

    await workspace.hideService(tab.id, 'svc-worker')
    workspace.pinService(tab.id, 'svc-api')

    expect(workspace.searchTab(tab.id)?.hiddenServiceIds).toEqual(['svc-worker'])
    expect(workspace.searchTab(tab.id)?.pinnedServiceIds).toEqual(['svc-api'])
  })



  it('loadMoreRemoteSearch de-duplicates project remote entries by stable key', async () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    vi.spyOn(agentApi, 'remoteSearch')
      .mockResolvedValueOnce({
        query: 'error',
        status: 'success',
        service_columns: [{
          service_id: 'svc-api',
          service_name: 'api',
          status: 'success',
          result_count: 1,
          node_count: 1,
          nodes: [],
          entries: [
            { key: 'svc-api/ls-api/host-a:1', id: 1, service_id: 'svc-api', log_source_id: 'ls-api', host_id: 'host-a', host_name: 'node-a', run_id: 'r', timestamp: '2026-05-20T22:41:32.000Z', level: 'ERROR', message: 'first error', stream: 'stderr' },
          ],
        }],
        failures: [],
        next_cursor: 'cursor-1',
        has_more: true,
      } as any)
      .mockResolvedValueOnce({
        query: 'error',
        status: 'success',
        service_columns: [{
          service_id: 'svc-api',
          service_name: 'api',
          status: 'success',
          result_count: 2,
          node_count: 1,
          nodes: [],
          entries: [
            { key: 'svc-api/ls-api/host-a:1', id: 1, service_id: 'svc-api', log_source_id: 'ls-api', host_id: 'host-a', host_name: 'node-a', run_id: 'r', timestamp: '2026-05-20T22:41:32.000Z', level: 'ERROR', message: 'first error duplicate', stream: 'stderr' },
            { key: 'svc-api/ls-api/host-a:2', id: 2, service_id: 'svc-api', log_source_id: 'ls-api', host_id: 'host-a', host_name: 'node-a', run_id: 'r', timestamp: '2026-05-20T22:41:33.000Z', level: 'ERROR', message: 'second error', stream: 'stderr' },
          ],
        }],
        failures: [],
        next_cursor: '',
        has_more: false,
      } as any)
    const workspace = useWorkspaceStore() as any
    const tab = workspace.openRemoteProjectSearch('proj-1', 'all')

    await workspace.runRemoteSearch(tab.id, 'error')
    await workspace.loadMoreRemoteSearch(tab.id)

    expect(tab.serviceColumns[0].entries.map((entry: any) => entry.key)).toEqual([
      'svc-api/ls-api/host-a:1',
      'svc-api/ls-api/host-a:2',
    ])
    expect(tab.serviceColumns[0].result_count).toBe(2)
  })


  describe('openDeployment', () => {
    it('creates a deployment tab', () => {
      const store = useWorkspaceStore()
      store.openDeployment('dep1', 'Deploy #1')
      expect(store.tabs.some(t => t.type === 'deployment' && t.deploymentId === 'dep1')).toBe(true)
    })

    it('does not create duplicate tabs for same deploymentId', () => {
      const store = useWorkspaceStore()
      store.openDeployment('dep1', 'Deploy #1')
      store.openDeployment('dep1', 'Deploy #1')
      expect(store.tabs.filter(t => t.type === 'deployment' && t.deploymentId === 'dep1')).toHaveLength(1)
    })

    it('sets the deployment tab as active', () => {
      const store = useWorkspaceStore()
      store.openDeployment('dep1', 'Deploy #1')
      expect(store.activeTab).toMatchObject({
        type: 'deployment',
        deploymentId: 'dep1',
        title: 'Deploy #1',
      })
    })

    it('re-activates an existing deployment tab without duplicating', () => {
      const store = useWorkspaceStore()
      store.openDeployment('dep1', 'Deploy #1')
      const firstTabId = store.activeTabId

      // Open a different tab to change active
      store.openSearch('proj-1')
      expect(store.activeTab?.type).toBe('search')

      // Re-open same deployment tab
      store.openDeployment('dep1', 'Deploy #1')
      expect(store.activeTabId).toBe(firstTabId)
      expect(store.tabs.filter(t => t.type === 'deployment')).toHaveLength(1)
    })
  })

  it('runRemoteSearch sends selected services, selected hosts, and cursor for project remote search', async () => {
    const api = service('svc-api', 'api')
    const worker = service('svc-worker', 'worker')
    useAgentStore().projects = [project([api, worker])]
    vi.spyOn(agentApi, 'remoteSearch').mockResolvedValue({
      query: 'error',
      status: 'success',
      service_columns: [],
      failures: [],
      next_cursor: '',
      has_more: false,
    } as any)
    const workspace = useWorkspaceStore() as any
    const tab = workspace.openRemoteProjectSearch('proj-1', 'all')

    await workspace.runRemoteSearch(tab.id, ' error ', {
      serviceIds: ['svc-api'],
      hostIds: ['host-a'],
    })
    tab.nextCursor = 'cursor-next'
    tab.hasMore = true
    await workspace.loadMoreRemoteSearch(tab.id)

    expect(agentApi.remoteSearch).toHaveBeenNthCalledWith(1, {
      project_id: 'proj-1',
      group: 'all',
      query: 'error',
      service_id: ['svc-api'],
      host_id: ['host-a'],
      limit: 200,
    })
    expect(agentApi.remoteSearch).toHaveBeenNthCalledWith(2, {
      project_id: 'proj-1',
      group: 'all',
      query: 'error',
      service_id: ['svc-api'],
      host_id: ['host-a'],
      cursor: 'cursor-next',
      limit: 200,
    })
  })

})
