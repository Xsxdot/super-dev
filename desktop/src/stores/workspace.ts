// workspaceStore 管理右侧项目、搜索和部署标签页，是侧边栏和内容区之间的导航状态。
//
// 职责：
//   - 管理项目日志、项目搜索和部署标签
//   - 在项目标签切换时保存/恢复 Panel 布局树
//   - 承载搜索页局部状态：结果、上下文、隐藏服务、固定服务
//
// 边界：
//   - 不渲染 UI，组件只读取这里的状态和动作
//   - 不直接订阅实时日志，项目标签仍由 Panel/LogPanel 负责
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { v4 as uuidv4 } from 'uuid'
import { api, type LogContextPageDirection, type LogEntry, type SearchLogsParams } from '@/api/agent'
import type { ConfigDraft } from '@/lib/configDraft'
import { useAgentStore } from './agent'
import {
  createDeploymentPanelRoot,
  createEmptyPanelRoot,
  usePanelStore,
  type PanelNode,
} from './panel'
import { useRunConsoleStore } from './runConsole'

export type WorkspaceTab =
  | ProjectWorkspaceTab
  | SearchWorkspaceTab
  | DeploymentTab
  | ProjectOverviewWorkspaceTab
  | RunConsoleWorkspaceTab
  | NodesWorkspaceTab

export interface ProjectWorkspaceTab {
  id: string
  type: 'project'
  projectId: string
  title: string
  layoutRoot: PanelNode
  focusedPanelId: string | null
}

export interface ProjectOverviewWorkspaceTab {
  id: string
  type: 'overview'
  projectId: string
  title: string
  overviewState: ProjectOverviewState
}

export type ProjectOverviewSubtab = 'runtime' | 'pipelines' | 'ingress' | 'config'

export interface ProjectConfigSurfaceState {
  draft: ConfigDraft
  activeEnv: string
  activeServiceId: string
  renamingEnv: string
  errors: string[]
  saveError: string | null
}

export interface ProjectOverviewState {
  activeTab: ProjectOverviewSubtab
  config?: ProjectConfigSurfaceState
}

export interface NodesWorkspaceTab {
  id: 'nodes'
  type: 'nodes'
  title: string
}

export interface SearchWorkspaceTab {
  id: string
  type: 'search'
  projectId: string
  title: string
  query: string
  status: 'empty' | 'loading' | 'results' | 'emptyResults' | 'error'
  results: LogEntry[]
  serviceCounts: Record<string, number>
  hiddenServiceIds: string[]
  selectedLogId: string | null
  selectedLogDeploymentId: string | null
  contextAnchorTime: string | null
  contextByService: Record<string, LogEntry[]>
  pinnedServiceIds: string[]
  hasMoreBeforeByService: Record<string, boolean>
  hasMoreAfterByService: Record<string, boolean>
  loadingMoreResults: boolean
  loadingMoreBefore: boolean
  loadingMoreAfter: boolean
  error: string | null
}

export interface DeploymentTab {
  id: string
  type: 'deployment'
  deploymentId: string
  title: string
  // layoutRoot/focusedPanelId 让 deployment tab 也走 PanelLayout 分栏树：
  // 初始为单个 deployment 叶子，从侧边栏拖入其他 deployment 即可分栏并排看日志。
  layoutRoot: PanelNode
  focusedPanelId: string | null
}

export interface RunConsoleWorkspaceTab {
  id: string
  type: 'run'
  projectId: string
  pipelineId: string
  runId: string
  mode: 'live' | 'replay'
  title: string
}

const SEARCH_PAGE_LIMIT = 1000
const CONTEXT_PAGE_LIMIT = 200

function makeProjectTab(projectId: string, title: string): ProjectWorkspaceTab {
  return {
    id: uuidv4(),
    type: 'project',
    projectId,
    title,
    layoutRoot: createEmptyPanelRoot(),
    focusedPanelId: null,
  }
}

function makeProjectOverviewTab(projectId: string, title: string): ProjectOverviewWorkspaceTab {
  return {
    id: `overview:${projectId}`,
    type: 'overview',
    projectId,
    title,
    overviewState: { activeTab: 'runtime' },
  }
}

function makeNodesTab(): NodesWorkspaceTab {
  return {
    id: 'nodes',
    type: 'nodes',
    title: 'nodes',
  }
}

function makeSearchTab(projectId: string, title: string): SearchWorkspaceTab {
  return {
    id: uuidv4(),
    type: 'search',
    projectId,
    title,
    query: '',
    status: 'empty',
    results: [],
    serviceCounts: {},
    hiddenServiceIds: [],
    selectedLogId: null,
    selectedLogDeploymentId: null,
    contextAnchorTime: null,
    contextByService: {},
    pinnedServiceIds: [],
    hasMoreBeforeByService: {},
    hasMoreAfterByService: {},
    loadingMoreResults: false,
    loadingMoreBefore: false,
    loadingMoreAfter: false,
    error: null,
  }
}

function compareLogs(a: LogEntry, b: LogEntry): number {
  const timeDiff = new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime()
  if (timeDiff !== 0) return timeDiff
  if (a.id < b.id) return -1
  if (a.id > b.id) return 1
  return a.deployment_id.localeCompare(b.deployment_id)
}

function logKey(entry: LogEntry): string {
  return `${entry.deployment_id}:${entry.id}`
}

function mergeLogs(existing: LogEntry[], incoming: LogEntry[]): LogEntry[] {
  const byKey = new Map<string, LogEntry>()
  for (const entry of existing) byKey.set(logKey(entry), entry)
  for (const entry of incoming) byKey.set(logKey(entry), entry)
  return [...byKey.values()].sort(compareLogs)
}

export const useWorkspaceStore = defineStore('workspace', () => {
  const tabs = ref<WorkspaceTab[]>([])
  const activeTabId = ref<string | null>(null)
  const runtimeWorkspaceMaximized = ref(false)

  const activeTab = computed(() => tabs.value.find(t => t.id === activeTabId.value) ?? null)
  const isRuntimeWorkspaceMaximized = computed(() =>
    runtimeWorkspaceMaximized.value && isLogWorkspaceTab(activeTab.value),
  )

  function projectName(projectId: string): string {
    return useAgentStore().projectById(projectId)?.name ?? projectId
  }

  // 携带 PanelLayout 分栏树的 tab：项目聚合 tab 与 deployment tab 都按 layoutRoot 走分栏。
  function isLogWorkspaceTab(tab: WorkspaceTab | null): tab is ProjectWorkspaceTab | DeploymentTab {
    return tab?.type === 'project' || tab?.type === 'deployment'
  }

  function saveActiveLogWorkspaceLayout() {
    const active = activeTab.value
    if (!isLogWorkspaceTab(active)) return
    const panel = usePanelStore()
    active.layoutRoot = panel.root
    active.focusedPanelId = panel.focusedPanelId
  }

  function activateTab(tabId: string) {
    saveActiveLogWorkspaceLayout()
    activeTabId.value = tabId
    const tab = activeTab.value
    if (!isLogWorkspaceTab(tab)) runtimeWorkspaceMaximized.value = false
    if (isLogWorkspaceTab(tab)) {
      usePanelStore().setRoot(tab.layoutRoot, tab.focusedPanelId)
    }
  }

  function ensureProjectTab(projectId: string): ProjectWorkspaceTab {
    const existing = tabs.value.find(
      (tab): tab is ProjectWorkspaceTab => tab.type === 'project' && tab.projectId === projectId,
    )
    if (existing) return existing
    const tab = makeProjectTab(projectId, projectName(projectId))
    tabs.value.push(tab)
    return tab
  }

  function openProjectOverview(projectId: string): ProjectOverviewWorkspaceTab {
    saveActiveLogWorkspaceLayout()
    const id = `overview:${projectId}`
    const existing = tabs.value.find(
      (tab): tab is ProjectOverviewWorkspaceTab => tab.type === 'overview' && tab.id === id,
    )
    if (existing) {
      runtimeWorkspaceMaximized.value = false
      activeTabId.value = existing.id
      return existing
    }
    const tab = makeProjectOverviewTab(projectId, projectName(projectId))
    tabs.value.unshift(tab)
    runtimeWorkspaceMaximized.value = false
    activeTabId.value = tab.id
    return tab
  }

  function updateProjectOverviewState(tabId: string, state: ProjectOverviewState) {
    const tab = tabs.value.find(
      (candidate): candidate is ProjectOverviewWorkspaceTab => candidate.type === 'overview' && candidate.id === tabId,
    )
    if (!tab) return
    tab.overviewState = state
  }

  function openNodesTab(): NodesWorkspaceTab {
    saveActiveLogWorkspaceLayout()
    const existing = tabs.value.find((tab): tab is NodesWorkspaceTab => tab.type === 'nodes')
    if (existing) {
      runtimeWorkspaceMaximized.value = false
      activeTabId.value = existing.id
      return existing
    }
    const tab = makeNodesTab()
    tabs.value.unshift(tab)
    runtimeWorkspaceMaximized.value = false
    activeTabId.value = tab.id
    return tab
  }

  function openSearch(projectId: string): SearchWorkspaceTab {
    saveActiveLogWorkspaceLayout()
    const tab = makeSearchTab(projectId, projectName(projectId))
    tabs.value.push(tab)
    runtimeWorkspaceMaximized.value = false
    activeTabId.value = tab.id
    return tab
  }

  function openDeployment(deploymentId: string, title: string): DeploymentTab {
    saveActiveLogWorkspaceLayout()
    const id = `deployment:${deploymentId}`
    const existing = tabs.value.find(
      (tab): tab is DeploymentTab => tab.type === 'deployment' && tab.id === id,
    )
    if (existing) {
      activeTabId.value = existing.id
      usePanelStore().setRoot(existing.layoutRoot, existing.focusedPanelId)
      return existing
    }
    const tab: DeploymentTab = {
      id,
      type: 'deployment',
      deploymentId,
      title,
      layoutRoot: createDeploymentPanelRoot(deploymentId),
      focusedPanelId: null,
    }
    tabs.value.push(tab)
    activeTabId.value = tab.id
    usePanelStore().setRoot(tab.layoutRoot, tab.focusedPanelId)
    return tab
  }

  function openRunConsole(params: {
    projectId: string
    pipelineId: string
    runId: string
    mode: 'live' | 'replay'
    title: string
  }): RunConsoleWorkspaceTab {
    saveActiveLogWorkspaceLayout()
    const id = `run:${params.runId}`
    const existing = tabs.value.find(
      (tab): tab is RunConsoleWorkspaceTab => tab.type === 'run' && tab.id === id,
    )
    if (existing) {
      existing.mode = params.mode
      existing.title = params.title
      runtimeWorkspaceMaximized.value = false
      activeTabId.value = existing.id
      return existing
    }
    const tab: RunConsoleWorkspaceTab = {
      id,
      type: 'run',
      projectId: params.projectId,
      pipelineId: params.pipelineId,
      runId: params.runId,
      mode: params.mode,
      title: params.title,
    }
    tabs.value.push(tab)
    runtimeWorkspaceMaximized.value = false
    activeTabId.value = tab.id
    return tab
  }

  function searchTab(tabId: string): SearchWorkspaceTab | null {
    const tab = tabs.value.find(t => t.id === tabId)
    return tab?.type === 'search' ? tab : null
  }

  function visibleContextServiceIds(tab: SearchWorkspaceTab): string[] {
    return Object.keys(tab.serviceCounts).filter(
      serviceId => !tab.hiddenServiceIds.includes(serviceId),
    )
  }

  function contextCursor(
    tab: SearchWorkspaceTab,
    serviceId: string,
    direction: LogContextPageDirection,
  ): { cursor_time: string; cursor_id: string } | null {
    const entries = [...(tab.contextByService[serviceId] ?? [])].sort(compareLogs)
    if (entries.length > 0) {
      const cursor = direction === 'before' ? entries[0] : entries[entries.length - 1]
      return { cursor_time: cursor.timestamp, cursor_id: cursor.id }
    }
    if (!tab.contextAnchorTime) return null
    // 当前服务在锚点附近没有日志时，以锚点时间继续向两端探测，避免空服务永远无法补数据。
    return { cursor_time: tab.contextAnchorTime, cursor_id: '0' }
  }

  function visibleSearchServiceIds(tab: SearchWorkspaceTab): string[] {
    return Object.keys(tab.serviceCounts).filter(
      serviceId => !tab.hiddenServiceIds.includes(serviceId),
    )
  }

  function visibleSearchTotal(tab: SearchWorkspaceTab): number {
    return visibleSearchServiceIds(tab).reduce(
      (sum, serviceId) => sum + (tab.serviceCounts[serviceId] ?? 0),
      0,
    )
  }

  function visibleSearchResults(tab: SearchWorkspaceTab): LogEntry[] {
    const visible = new Set(visibleSearchServiceIds(tab))
    return tab.results.filter(entry => visible.has(entry.deployment_id)).sort(compareLogs)
  }

  function clearSearchContext(tab: SearchWorkspaceTab) {
    tab.selectedLogId = null
    tab.selectedLogDeploymentId = null
    tab.contextAnchorTime = null
    tab.contextByService = {}
    tab.hasMoreBeforeByService = {}
    tab.hasMoreAfterByService = {}
  }

  function canLoadMoreSearchResults(tabId: string): boolean {
    const tab = searchTab(tabId)
    if (!tab || !tab.query || tab.loadingMoreResults) return false
    return visibleSearchResults(tab).length < visibleSearchTotal(tab)
  }

  function searchResultCursor(tab: SearchWorkspaceTab): { cursor_time: string; cursor_id: string } | null {
    const entries = visibleSearchResults(tab)
    const cursor = entries[entries.length - 1]
    return cursor ? { cursor_time: cursor.timestamp, cursor_id: cursor.id } : null
  }

  async function hideService(tabId: string, serviceId: string) {
    const tab = searchTab(tabId)
    if (!tab || tab.hiddenServiceIds.includes(serviceId)) return
    tab.hiddenServiceIds.push(serviceId)
    await loadMoreSearchResults(tabId)
  }

  async function showService(tabId: string, serviceId: string) {
    const tab = searchTab(tabId)
    if (!tab) return
    tab.hiddenServiceIds = tab.hiddenServiceIds.filter(id => id !== serviceId)
    await loadMoreSearchResults(tabId)
  }

  function pinService(tabId: string, serviceId: string) {
    const tab = searchTab(tabId)
    if (!tab || tab.pinnedServiceIds.includes(serviceId)) return
    tab.pinnedServiceIds.push(serviceId)
  }

  function unpinService(tabId: string, serviceId: string) {
    const tab = searchTab(tabId)
    if (!tab) return
    tab.pinnedServiceIds = tab.pinnedServiceIds.filter(id => id !== serviceId)
  }

  function selectSearchResult(tabId: string, logId: string, deploymentId?: string): boolean {
    const tab = searchTab(tabId)
    if (!tab) return false
    const hidden = new Set(tab.hiddenServiceIds)
    const selected = deploymentId
      ? tab.results.find(entry =>
        entry.id === logId
        && entry.deployment_id === deploymentId
        && !hidden.has(entry.deployment_id),
      )
      : tab.results.find(entry => entry.id === logId && !hidden.has(entry.deployment_id))
    if (!selected) return false
    if (tab.selectedLogId === selected.id && tab.selectedLogDeploymentId === selected.deployment_id) {
      return false
    }
    tab.selectedLogId = logId
    tab.selectedLogDeploymentId = selected.deployment_id
    return true
  }

  async function runSearch(tabId: string, query: string) {
    const tab = searchTab(tabId)
    const trimmed = query.trim()
    if (!tab || !trimmed) return
    tab.query = trimmed
    tab.title = trimmed
    tab.status = 'loading'
    tab.error = null
    try {
      const result = await api.searchLogs({ project: tab.projectId, q: trimmed })
      tab.results = result.items
      tab.serviceCounts = result.deployment_counts
      clearSearchContext(tab)
      tab.loadingMoreResults = false
      tab.status = result.items.length ? 'results' : 'emptyResults'
    } catch (err) {
      tab.error = err instanceof Error ? err.message : String(err)
      tab.status = 'error'
    }
  }

  async function loadContext(tabId: string, target: LogEntry | string) {
    const tab = searchTab(tabId)
    if (!tab) return
    const visibleServices = visibleContextServiceIds(tab)
    const targetID = typeof target === 'string' ? target : target.id
    const targetDeploymentID = typeof target === 'string'
      ? tab.results.find(entry => entry.id === target)?.deployment_id
      : target.deployment_id
    tab.error = null
    try {
      const result = await api.fetchLogContext({
        project: tab.projectId,
        id: targetID,
        target_deployment: targetDeploymentID,
        deployment: visibleServices,
      })
      tab.selectedLogId = result.target_id
      tab.selectedLogDeploymentId = targetDeploymentID ?? null
      tab.contextAnchorTime = result.anchor_time
      for (const serviceId of visibleServices) {
        if (tab.pinnedServiceIds.includes(serviceId)) continue
        tab.contextByService[serviceId] = result.items_by_deployment[serviceId] ?? []
        tab.hasMoreBeforeByService[serviceId] = true
        tab.hasMoreAfterByService[serviceId] = true
      }
    } catch (err) {
      // A failed anchor lookup must not leave the previous trace visible as if it matched the new hit.
      clearSearchContext(tab)
      tab.error = err instanceof Error ? err.message : String(err)
    }
  }

  async function loadMoreSearchResults(tabId: string): Promise<boolean> {
    const tab = searchTab(tabId)
    if (!tab || !tab.query || tab.loadingMoreResults) return false
    if (!canLoadMoreSearchResults(tabId)) return false
    const serviceIds = visibleSearchServiceIds(tab)
    if (serviceIds.length === 0) return false

    const cursor = searchResultCursor(tab)
    const params: SearchLogsParams = {
      project: tab.projectId,
      q: tab.query,
      deployment: serviceIds,
      limit: SEARCH_PAGE_LIMIT,
    }
    if (cursor) {
      params.cursor_time = cursor.cursor_time
      params.cursor_id = cursor.cursor_id
    }

    tab.loadingMoreResults = true
    try {
      const result = await api.searchLogs(params)
      tab.results = mergeLogs(tab.results, result.items)
      return result.items.length > 0
    } catch (err) {
      tab.error = err instanceof Error ? err.message : String(err)
      return false
    } finally {
      tab.loadingMoreResults = false
    }
  }

  async function loadMoreContext(tabId: string, direction: LogContextPageDirection): Promise<boolean> {
    const tab = searchTab(tabId)
    if (!tab || !tab.contextAnchorTime) return false
    const loadingKey = direction === 'before' ? 'loadingMoreBefore' : 'loadingMoreAfter'
    const hasMoreMap =
      direction === 'before' ? tab.hasMoreBeforeByService : tab.hasMoreAfterByService
    if (tab[loadingKey]) return false

    const requests = visibleContextServiceIds(tab)
      .filter(serviceId => !tab.pinnedServiceIds.includes(serviceId))
      .filter(serviceId => hasMoreMap[serviceId] !== false)
      .map(serviceId => {
        const cursor = contextCursor(tab, serviceId, direction)
        if (!cursor) return null
        return { serviceId, cursor }
      })
      .filter((item): item is { serviceId: string; cursor: { cursor_time: string; cursor_id: string } } =>
        item !== null,
      )
    if (requests.length === 0) return false

    tab[loadingKey] = true
    try {
      const pages = await Promise.all(
        requests.map(({ serviceId, cursor }) =>
          api.fetchLogContextPage({
            project: tab.projectId,
            deployment: serviceId,
            direction,
            cursor_time: cursor.cursor_time,
            cursor_id: cursor.cursor_id,
            limit: CONTEXT_PAGE_LIMIT,
          }),
        ),
      )
      let changed = false
      for (const page of pages) {
        hasMoreMap[page.deployment_id] = page.has_more
        if (page.items.length === 0) continue
        tab.contextByService[page.deployment_id] = mergeLogs(
          tab.contextByService[page.deployment_id] ?? [],
          page.items,
        )
        changed = true
      }
      return changed
    } catch (err) {
      tab.error = err instanceof Error ? err.message : String(err)
      return false
    } finally {
      tab[loadingKey] = false
    }
  }

  function closeTab(tabId: string) {
    const idx = tabs.value.findIndex(t => t.id === tabId)
    if (idx < 0) return
    const closing = tabs.value[idx]
    if (closing?.type === 'run') {
      useRunConsoleStore().disposeRun(closing.runId)
    }
    tabs.value.splice(idx, 1)
    if (activeTabId.value !== tabId) return
    activeTabId.value = tabs.value[Math.max(0, idx - 1)]?.id ?? null
    const tab = activeTab.value
    if (!isLogWorkspaceTab(tab)) runtimeWorkspaceMaximized.value = false
    if (isLogWorkspaceTab(tab)) {
      usePanelStore().setRoot(tab.layoutRoot, tab.focusedPanelId)
    }
  }

  function setRuntimeWorkspaceMaximized(maximized: boolean) {
    runtimeWorkspaceMaximized.value = maximized && isLogWorkspaceTab(activeTab.value)
  }

  function toggleRuntimeWorkspaceMaximized() {
    setRuntimeWorkspaceMaximized(!isRuntimeWorkspaceMaximized.value)
  }

  return {
    tabs,
    activeTabId,
    runtimeWorkspaceMaximized,
    isRuntimeWorkspaceMaximized,
    activeTab,
    activateTab,
    // ensureProjectTab 作为 deployment 多面板容器 tab 的入口保留，供后续在项目 tab 中拖入多个 deployment 分栏使用。
    ensureProjectTab,
    openProjectOverview,
    updateProjectOverviewState,
    openNodesTab,
    openSearch,
    openDeployment,
    openRunConsole,
    searchTab,
    hideService,
    showService,
    canLoadMoreSearchResults,
    pinService,
    unpinService,
    selectSearchResult,
    runSearch,
    loadContext,
    loadMoreSearchResults,
    loadMoreContext,
    closeTab,
    saveActiveLogWorkspaceLayout,
    setRuntimeWorkspaceMaximized,
    toggleRuntimeWorkspaceMaximized,
  }
})
