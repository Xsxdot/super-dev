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
    env_selected_service_ids: {},
  }
}

function log(
  id: number,
  deploymentId: string,
  message: string,
  timestamp = '2026-05-20T22:41:32.000Z',
): LogEntry {
  return {
    id,
    deployment_id: deploymentId,
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

  it('ensureProjectTab 创建并复用项目标签（deployment 多面板容器）', () => {
    const api = service('svc-api', 'api')
    useAgentStore().projects = [project([api])]
    const workspace = useWorkspaceStore()

    const tab1 = workspace.ensureProjectTab('proj-1')
    const tab2 = workspace.ensureProjectTab('proj-1')

    expect(workspace.tabs.filter(t => t.type === 'project')).toHaveLength(1)
    expect(tab1.id).toBe(tab2.id)
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

    // 在 proj-1 的容器 tab 内拖入 dep-api，在 proj-2 的容器 tab 内拖入 dep-admin。
    const tabA = workspace.ensureProjectTab('proj-1')
    workspace.activateTab(tabA.id)
    panel.replaceScope(panel.targetPanelId()!, 'dep-api', null)
    workspace.saveActiveLogWorkspaceLayout()
    const firstTabId = workspace.activeTabId

    const tabB = workspace.ensureProjectTab('proj-2')
    workspace.activateTab(tabB.id)
    panel.replaceScope(panel.targetPanelId()!, 'dep-admin', null)
    workspace.saveActiveLogWorkspaceLayout()
    expect(panel.allLeaves[0].serviceId).toBe('dep-admin')

    workspace.activateTab(firstTabId!)

    expect(panel.allLeaves[0].serviceId).toBe('dep-api')
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
      deployment_counts: { 'svc-api': 1 },
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
        deployment_counts: { 'svc-api': 2 },
        has_more: true,
      })
      .mockResolvedValueOnce({
        query: 'trace-8f21',
        total: 2,
        items: [log(2, 'svc-api', 'second', '2026-05-20T22:41:33.000Z')],
        deployment_counts: { 'svc-api': 2 },
        has_more: false,
      })
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')

    await workspace.runSearch(tab.id, 'trace-8f21')
    await workspace.loadMoreSearchResults(tab.id)

    expect(agentApi.searchLogs).toHaveBeenLastCalledWith({
      project: 'proj-1',
      q: 'trace-8f21',
      deployment: ['svc-api'],
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
        deployment_counts: { 'svc-logger': 1000, 'svc-server': 2 },
        has_more: true,
      })
      .mockResolvedValueOnce({
        query: 'trace-8f21',
        total: 2,
        items: [log(1001, 'svc-server', 'server first', '2026-05-20T22:41:30.000Z')],
        deployment_counts: { 'svc-server': 2 },
        has_more: true,
      })
    const workspace = useWorkspaceStore()
    const tab = workspace.openSearch('proj-1')

    await workspace.runSearch(tab.id, 'trace-8f21')
    await workspace.hideService(tab.id, 'svc-logger')

    expect(agentApi.searchLogs).toHaveBeenLastCalledWith({
      project: 'proj-1',
      q: 'trace-8f21',
      deployment: ['svc-server'],
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
      items_by_deployment: {
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
      deployment: ['svc-api', 'svc-worker'],
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
      deployment_id: params.deployment,
      direction: params.direction,
      items:
        params.deployment === 'svc-api'
          ? [log(1, 'svc-api', 'older api', '2026-05-20T22:41:30.000Z')]
          : [log(2, 'svc-worker', 'older worker', '2026-05-20T22:41:31.000Z')],
      has_more: params.deployment === 'svc-api',
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
      deployment: 'svc-api',
      direction: 'before',
      cursor_time: '2026-05-20T22:41:32.000Z',
      cursor_id: 9,
      limit: 200,
    })
    expect(agentApi.fetchLogContextPage).toHaveBeenCalledWith({
      project: 'proj-1',
      deployment: 'svc-worker',
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
      deployment_id: params.deployment,
      direction: params.direction,
      items: [log(2, params.deployment, 'older worker', '2026-05-20T22:41:31.000Z')],
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
      deployment: 'svc-worker',
      direction: 'before',
      cursor_time: '2026-05-20T22:41:32.000Z',
      cursor_id: 10,
      limit: 200,
    })
    expect(tab.contextByService['svc-api'].map(entry => entry.message)).toEqual(['pinned api'])
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

    it('初始化以该 deployment 为来源的单叶子分栏树，并同步到 panel store', () => {
      const store = useWorkspaceStore()
      const panel = usePanelStore()
      store.openDeployment('dep1', 'Deploy #1')

      const tab = store.activeTab
      expect(tab?.type).toBe('deployment')
      // deployment tab 携带 layoutRoot，初始为单个 deployment 叶子（可拖入其他 deployment 分栏）
      if (tab?.type === 'deployment') {
        expect(tab.layoutRoot.type).toBe('leaf')
        if (tab.layoutRoot.type === 'leaf') {
          expect(tab.layoutRoot.source).toEqual({ type: 'deployment', deploymentId: 'dep1' })
        }
      }
      // panel store 已切到该 tab 的布局，叶子来源即 dep1
      expect(panel.allLeaves).toHaveLength(1)
      expect(panel.allLeaves[0]?.source).toEqual({ type: 'deployment', deploymentId: 'dep1' })
    })

    it('切走再切回 deployment tab 时恢复其各自分栏布局', () => {
      const store = useWorkspaceStore()
      const panel = usePanelStore()
      store.openDeployment('dep1', 'Deploy #1')
      const firstTabId = store.activeTabId!

      // 在 dep1 的面板里分栏拖入 dep2
      const leafId = panel.allLeaves[0]!.id
      panel.splitLeafWithSource(leafId, 'h', { type: 'deployment', deploymentId: 'dep2' }, 'second')
      expect(panel.allLeaves).toHaveLength(2)

      // 打开另一个 deployment tab（dep3），panel 切到单叶子
      store.openDeployment('dep3', 'Deploy #3')
      expect(panel.allLeaves).toHaveLength(1)

      // 切回 dep1，恢复其 2 叶子分栏
      store.activateTab(firstTabId)
      expect(panel.allLeaves).toHaveLength(2)
    })
  })

})
