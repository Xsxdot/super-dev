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
})
