// workspaceStore 管理右侧项目、搜索和远程监听标签页，是侧边栏和内容区之间的导航状态。
//
// 职责：
//   - 管理项目日志、项目搜索和远程监听标签
//   - 在项目标签切换时保存/恢复 Panel 布局树
//   - 承载搜索页局部状态：结果、上下文、隐藏服务、固定服务
//
// 边界：
//   - 不渲染 UI，组件只读取这里的状态和动作
//   - 不直接订阅实时日志，项目标签仍由 Panel/LogPanel 负责
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { v4 as uuidv4 } from 'uuid'
import { api, type LogContextPageDirection, type LogEntry, type RemoteSearchFailure, type RemoteSearchParams, type RemoteSearchResponse, type RemoteSearchServiceColumn, type SearchLogsParams } from '@/api/agent'
import { useAgentStore } from './agent'
import {
  createEmptyPanelRoot,
  usePanelStore,
  type PanelNode,
  type PanelSource,
} from './panel'

export type WorkspaceTab =
  | ProjectWorkspaceTab
  | SearchWorkspaceTab
  | RemoteWorkspaceTab
  | RemoteSearchWorkspaceTab
  | RemoteAggregateTab

export interface ProjectWorkspaceTab {
  id: string
  type: 'project'
  projectId: string
  title: string
  layoutRoot: PanelNode
  focusedPanelId: string | null
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
  selectedLogId: number | null
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

export interface RemoteWorkspaceTab {
  id: string
  type: 'remote'
  logSourceId: string
  groupKey: string
  title: string
  layoutRoot: PanelNode
  focusedPanelId: string | null
}

export interface RemoteSearchWorkspaceTab {
  id: string
  type: 'remote-search'
  logSourceId?: string
  projectId?: string
  groupKey: string
  title: string
  query: string
  selectedServiceIds: string[]
  selectedHostIds: string[]
  status: 'empty' | 'loading' | 'results' | 'emptyResults' | 'error' | 'partialFailed' | 'failed'
  serviceColumns: RemoteSearchServiceColumn[]
  hiddenServiceIds: string[]
  pinnedServiceIds: string[]
  failures: RemoteSearchFailure[]
  nextCursor: string | null
  hasMore: boolean
  error: string | null
  requestSeq: number
}

interface RemoteSearchScope {
  serviceIds?: string[]
  hostIds?: string[]
}

export interface RemoteAggregateTab {
  id: string
  type: 'remote-aggregate'
  projectId: string
  serviceId: string
  serviceName: string
  logSourceIds: string[]
  groupKey: string
  title: string
  layoutRoot: PanelNode
  focusedPanelId: string | null
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

function makeRemoteTab(logSourceId: string, groupKey: string): RemoteWorkspaceTab {
  return {
    id: `remote:${logSourceId}:${groupKey}`,
    type: 'remote',
    logSourceId,
    groupKey,
    title: `Remote · ${groupKey}`,
    layoutRoot: createEmptyPanelRoot(),
    focusedPanelId: null,
  }
}

function makeRemoteSearchTab(logSourceId: string, groupKey: string): RemoteSearchWorkspaceTab {
  return makeRemoteSearchState({
    id: `remote-search:${logSourceId}:${groupKey}`,
    logSourceId,
    groupKey,
    title: `Remote Search · ${groupKey}`,
  })
}

function makeRemoteProjectSearchTab(projectId: string, groupKey: string, title: string): RemoteSearchWorkspaceTab {
  return makeRemoteSearchState({
    id: `remote-project-search:${projectId}:${groupKey}`,
    projectId,
    groupKey,
    title,
  })
}

function makeRemoteSearchState(
  base: Pick<RemoteSearchWorkspaceTab, 'id' | 'groupKey' | 'title'> & { logSourceId?: string; projectId?: string },
): RemoteSearchWorkspaceTab {
  return {
    ...base,
    type: 'remote-search',
    query: '',
    selectedServiceIds: [],
    selectedHostIds: [],
    status: 'empty',
    serviceColumns: [],
    hiddenServiceIds: [],
    pinnedServiceIds: [],
    failures: [],
    nextCursor: null,
    hasMore: false,
    error: null,
    requestSeq: 0,
  }
}

function remoteSearchStatus(result: RemoteSearchResponse, columns: RemoteSearchServiceColumn[]): RemoteSearchWorkspaceTab['status'] {
  if (result.status === 'failed') return 'failed'
  if (result.status === 'partial_failed') return 'partialFailed'
  return columns.some(column => column.entries.length > 0) ? 'results' : 'emptyResults'
}

function retainKnownServiceIds(serviceIds: string[], columns: RemoteSearchServiceColumn[]): string[] {
  const known = new Set(columns.map(column => column.service_id))
  return serviceIds.filter(serviceId => known.has(serviceId))
}

function resetRemoteSearchResults(tab: RemoteSearchWorkspaceTab) {
  tab.status = 'loading'
  tab.error = null
  tab.serviceColumns = []
  tab.failures = []
  tab.nextCursor = null
  tab.hasMore = false
}

function applyRemoteSearchResult(tab: RemoteSearchWorkspaceTab, result: RemoteSearchResponse) {
  const columns = result.service_columns ?? []
  tab.serviceColumns = columns
  tab.failures = result.failures ?? []
  tab.nextCursor = result.next_cursor || null
  tab.hasMore = result.has_more
  tab.hiddenServiceIds = retainKnownServiceIds(tab.hiddenServiceIds, columns)
  tab.pinnedServiceIds = retainKnownServiceIds(tab.pinnedServiceIds, columns)
  tab.status = remoteSearchStatus(result, columns)
}

function remoteSearchRequest(tab: RemoteSearchWorkspaceTab, cursor?: string): RemoteSearchParams {
  return {
    project_id: tab.projectId,
    group: tab.groupKey,
    query: tab.query,
    service_id: tab.selectedServiceIds.length ? tab.selectedServiceIds : undefined,
    host_id: tab.selectedHostIds.length ? tab.selectedHostIds : undefined,
    cursor,
    limit: 200,
  }
}

function remoteEntryStableKey(entry: RemoteSearchServiceColumn['entries'][number]): string {
  return entry.key ?? `${entry.service_id}:${entry.log_source_id ?? ''}:${entry.host_id}:${entry.id}`
}

function mergeRemoteColumnEntries(
  existing: RemoteSearchServiceColumn['entries'],
  incoming: RemoteSearchServiceColumn['entries'],
): RemoteSearchServiceColumn['entries'] {
  const byKey = new Map<string, RemoteSearchServiceColumn['entries'][number]>()
  for (const entry of existing) byKey.set(remoteEntryStableKey(entry), entry)
  for (const entry of incoming) {
    const key = remoteEntryStableKey(entry)
    if (!byKey.has(key)) byKey.set(key, entry)
  }
  return [...byKey.values()]
}

function mergeRemoteSearchResult(tab: RemoteSearchWorkspaceTab, result: RemoteSearchResponse) {
  const existingByService = new Map(tab.serviceColumns.map(column => [column.service_id, column]))
  const columns = result.service_columns?.map(column => {
    const existing = existingByService.get(column.service_id)
    if (!existing) return column
    const entries = mergeRemoteColumnEntries(existing.entries, column.entries)
    return {
      ...column,
      entries,
      result_count: entries.length,
    }
  }) ?? []
  tab.serviceColumns = columns
  tab.failures = result.failures ?? []
  tab.nextCursor = result.next_cursor || null
  tab.hasMore = result.has_more
  tab.hiddenServiceIds = retainKnownServiceIds(tab.hiddenServiceIds, columns)
  tab.pinnedServiceIds = retainKnownServiceIds(tab.pinnedServiceIds, columns)
  tab.status = remoteSearchStatus(result, columns)
}

function compareLogs(a: LogEntry, b: LogEntry): number {
  const timeDiff = new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime()
  return timeDiff || a.id - b.id
}

function mergeLogs(existing: LogEntry[], incoming: LogEntry[]): LogEntry[] {
  const byID = new Map<number, LogEntry>()
  for (const entry of existing) byID.set(entry.id, entry)
  for (const entry of incoming) byID.set(entry.id, entry)
  return [...byID.values()].sort(compareLogs)
}

function replaceRemoteAggregateSource(
  node: PanelNode,
  source: Extract<PanelSource, { type: 'remote-aggregate' }>,
): PanelNode {
  if (node.type === 'leaf') {
    if (node.source?.type !== 'remote-aggregate') return node
    if (node.source.serviceId !== source.serviceId || node.source.groupKey !== source.groupKey) return node
    return { ...node, serviceId: null, projectId: null, source }
  }
  return {
    ...node,
    first: replaceRemoteAggregateSource(node.first, source),
    second: replaceRemoteAggregateSource(node.second, source),
  }
}

export const useWorkspaceStore = defineStore('workspace', () => {
  const tabs = ref<WorkspaceTab[]>([])
  const activeTabId = ref<string | null>(null)
  const remoteHiddenHostIdsByTab = ref<Record<string, string[]>>({})

  const activeTab = computed(() => tabs.value.find(t => t.id === activeTabId.value) ?? null)

  function projectName(projectId: string): string {
    return useAgentStore().projectById(projectId)?.name ?? projectId
  }

  function isLogWorkspaceTab(tab: WorkspaceTab | null): tab is ProjectWorkspaceTab | RemoteWorkspaceTab | RemoteAggregateTab {
    return tab?.type === 'project' || tab?.type === 'remote' || tab?.type === 'remote-aggregate'
  }

  function saveActiveLogWorkspaceLayout() {
    const active = activeTab.value
    if (!isLogWorkspaceTab(active)) return
    const panel = usePanelStore()
    active.layoutRoot = panel.root
    active.focusedPanelId = panel.focusedPanelId
  }

  function saveActiveProjectLayout() {
    saveActiveLogWorkspaceLayout()
  }

  function activateTab(tabId: string) {
    saveActiveLogWorkspaceLayout()
    activeTabId.value = tabId
    const tab = activeTab.value
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

  function openService(projectId: string, serviceId: string) {
    const tab = ensureProjectTab(projectId)
    activateTab(tab.id)
    const panel = usePanelStore()
    const existing = panel.allLeaves.find(leaf => leaf.serviceId === serviceId)
    const targetPanelId = existing?.id ?? panel.targetPanelId()
    if (!targetPanelId) return
    panel.replaceScope(targetPanelId, serviceId, projectId)
    panel.setFocus(targetPanelId)
    saveActiveLogWorkspaceLayout()
  }

  function openSearch(projectId: string): SearchWorkspaceTab {
    saveActiveLogWorkspaceLayout()
    const tab = makeSearchTab(projectId, `Search · ${projectName(projectId)}`)
    tabs.value.push(tab)
    activeTabId.value = tab.id
    return tab
  }

  function openRemote(logSourceId: string, groupKey: string): RemoteWorkspaceTab {
    saveActiveLogWorkspaceLayout()
    const id = `remote:${logSourceId}:${groupKey}`
    const existing = tabs.value.find(
      (tab): tab is RemoteWorkspaceTab => tab.type === 'remote' && tab.id === id,
    )
    if (existing) {
      activateTab(existing.id)
      return existing
    }
    const tab = makeRemoteTab(logSourceId, groupKey)
    tabs.value.push(tab)
    activeTabId.value = tab.id
    const panel = usePanelStore()
    panel.setRoot(tab.layoutRoot, tab.focusedPanelId)
    const leafId = panel.targetPanelId()
    if (leafId) {
      panel.replaceSource(leafId, { type: 'remote-log-source', logSourceId, groupKey })
      panel.setFocus(leafId)
    }
    tab.layoutRoot = panel.root
    tab.focusedPanelId = panel.focusedPanelId
    return tab
  }

  function setRemoteHiddenHostIds(tabId: string, hidden: string[]) {
    remoteHiddenHostIdsByTab.value = {
      ...remoteHiddenHostIdsByTab.value,
      [tabId]: hidden,
    }
  }

  function hideRemoteHost(tabId: string, hostId: string) {
    const hidden = remoteHiddenHostIdsByTab.value[tabId] ?? []
    if (hidden.includes(hostId)) return
    setRemoteHiddenHostIds(tabId, [...hidden, hostId])
  }

  function showRemoteHost(tabId: string, hostId: string) {
    const hidden = remoteHiddenHostIdsByTab.value[tabId] ?? []
    if (!hidden.includes(hostId)) return
    setRemoteHiddenHostIds(tabId, hidden.filter(id => id !== hostId))
  }

  function toggleRemoteHost(tabId: string, hostId: string) {
    const hidden = remoteHiddenHostIdsByTab.value[tabId] ?? []
    if (hidden.includes(hostId)) {
      showRemoteHost(tabId, hostId)
    } else {
      hideRemoteHost(tabId, hostId)
    }
  }

  function visibleRemoteHostIds(tabId: string, hostIds: string[]): string[] {
    const hidden = new Set(remoteHiddenHostIdsByTab.value[tabId] ?? [])
    return hostIds.filter(hostId => !hidden.has(hostId))
  }

  function isRemoteHostVisible(tabId: string, hostId: string): boolean {
    return !(remoteHiddenHostIdsByTab.value[tabId] ?? []).includes(hostId)
  }

  function openRemoteSearch(logSourceId: string, groupKey: string): RemoteSearchWorkspaceTab {
    saveActiveLogWorkspaceLayout()
    const id = `remote-search:${logSourceId}:${groupKey}`
    const existing = tabs.value.find(
      (tab): tab is RemoteSearchWorkspaceTab => tab.type === 'remote-search' && tab.id === id,
    )
    if (existing) {
      activeTabId.value = existing.id
      return existing
    }
    const tab = makeRemoteSearchTab(logSourceId, groupKey)
    tabs.value.push(tab)
    activeTabId.value = tab.id
    return tab
  }

  function openRemoteProjectSearch(projectId: string, groupKey: string): RemoteSearchWorkspaceTab {
    saveActiveLogWorkspaceLayout()
    const id = `remote-project-search:${projectId}:${groupKey}`
    const existing = tabs.value.find(
      (tab): tab is RemoteSearchWorkspaceTab => tab.type === 'remote-search' && tab.id === id,
    )
    if (existing) {
      activeTabId.value = existing.id
      return existing
    }
    const tab = makeRemoteProjectSearchTab(projectId, groupKey, `Remote Search · ${projectName(projectId)}`)
    tabs.value.push(tab)
    activeTabId.value = tab.id
    return tabs.value[tabs.value.length - 1] as RemoteSearchWorkspaceTab
  }

  function openRemoteAggregate(
    projectId: string,
    serviceId: string,
    serviceName: string,
    logSourceIds: string[],
    groupKey: string,
  ): RemoteAggregateTab {
    saveActiveLogWorkspaceLayout()
    const id = `remote-aggregate:${serviceId}:${groupKey}`
    const existing = tabs.value.find(
      (tab): tab is RemoteAggregateTab => tab.type === 'remote-aggregate' && tab.id === id,
    )
    const source: PanelSource = { type: 'remote-aggregate', logSourceIds, groupKey, projectId, serviceId, serviceName }
    if (existing) {
      existing.projectId = projectId
      existing.serviceName = serviceName
      existing.logSourceIds = logSourceIds
      existing.title = `${serviceName} · ${groupKey}`
      activateTab(existing.id)
      existing.layoutRoot = replaceRemoteAggregateSource(
        existing.layoutRoot,
        source as Extract<PanelSource, { type: 'remote-aggregate' }>,
      )
      const panel = usePanelStore()
      panel.setRoot(existing.layoutRoot, existing.focusedPanelId)
      existing.layoutRoot = panel.root
      existing.focusedPanelId = panel.focusedPanelId
      return existing
    }
    const tab: RemoteAggregateTab = {
      id,
      type: 'remote-aggregate',
      projectId,
      serviceId,
      serviceName,
      logSourceIds,
      groupKey,
      title: `${serviceName} · ${groupKey}`,
      layoutRoot: createEmptyPanelRoot(),
      focusedPanelId: null,
    }
    tabs.value.push(tab)
    activeTabId.value = tab.id
    const panel = usePanelStore()
    panel.setRoot(tab.layoutRoot, tab.focusedPanelId)
    const leafId = panel.targetPanelId()
    if (leafId) {
      panel.replaceSource(leafId, source)
      panel.setFocus(leafId)
    }
    tab.layoutRoot = panel.root
    tab.focusedPanelId = panel.focusedPanelId
    return tab
  }

  function searchTab(tabId: string): SearchWorkspaceTab | null {
    const tab = tabs.value.find(t => t.id === tabId)
    return tab?.type === 'search' ? tab : null
  }

  function remoteSearchTab(tabId: string): RemoteSearchWorkspaceTab | null {
    const tab = tabs.value.find(t => t.id === tabId)
    return tab?.type === 'remote-search' ? tab : null
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
  ): { cursor_time: string; cursor_id: number } | null {
    const entries = [...(tab.contextByService[serviceId] ?? [])].sort(compareLogs)
    if (entries.length > 0) {
      const cursor = direction === 'before' ? entries[0] : entries[entries.length - 1]
      return { cursor_time: cursor.timestamp, cursor_id: cursor.id }
    }
    if (!tab.contextAnchorTime) return null
    // 当前服务在锚点附近没有日志时，以锚点时间继续向两端探测，避免空服务永远无法补数据。
    return { cursor_time: tab.contextAnchorTime, cursor_id: 0 }
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
    return tab.results.filter(entry => visible.has(entry.service_id)).sort(compareLogs)
  }

  function canLoadMoreSearchResults(tabId: string): boolean {
    const tab = searchTab(tabId)
    if (!tab || !tab.query || tab.loadingMoreResults) return false
    return visibleSearchResults(tab).length < visibleSearchTotal(tab)
  }

  function searchResultCursor(tab: SearchWorkspaceTab): { cursor_time: string; cursor_id: number } | null {
    const entries = visibleSearchResults(tab)
    const cursor = entries[entries.length - 1]
    return cursor ? { cursor_time: cursor.timestamp, cursor_id: cursor.id } : null
  }

  async function hideService(tabId: string, serviceId: string) {
    const tab = searchTab(tabId) ?? remoteSearchTab(tabId)
    if (!tab || tab.hiddenServiceIds.includes(serviceId)) return
    tab.hiddenServiceIds.push(serviceId)
    if (tab.type === 'search') await loadMoreSearchResults(tabId)
  }

  async function showService(tabId: string, serviceId: string) {
    const tab = searchTab(tabId) ?? remoteSearchTab(tabId)
    if (!tab) return
    tab.hiddenServiceIds = tab.hiddenServiceIds.filter(id => id !== serviceId)
    if (tab.type === 'search') await loadMoreSearchResults(tabId)
  }

  function pinService(tabId: string, serviceId: string) {
    const tab = searchTab(tabId) ?? remoteSearchTab(tabId)
    if (!tab || tab.pinnedServiceIds.includes(serviceId)) return
    tab.pinnedServiceIds.push(serviceId)
  }

  function unpinService(tabId: string, serviceId: string) {
    const tab = searchTab(tabId) ?? remoteSearchTab(tabId)
    if (!tab) return
    tab.pinnedServiceIds = tab.pinnedServiceIds.filter(id => id !== serviceId)
  }

  function selectSearchResult(tabId: string, logId: number): boolean {
    const tab = searchTab(tabId)
    if (!tab || tab.selectedLogId === logId) return false
    const hidden = new Set(tab.hiddenServiceIds)
    const exists = tab.results.some(
      entry => entry.id === logId && !hidden.has(entry.service_id),
    )
    if (!exists) return false
    tab.selectedLogId = logId
    return true
  }

  async function runSearch(tabId: string, query: string) {
    const tab = searchTab(tabId)
    const trimmed = query.trim()
    if (!tab || !trimmed) return
    tab.query = trimmed
    tab.title = `Search: ${trimmed}`
    tab.status = 'loading'
    tab.error = null
    try {
      const result = await api.searchLogs({ project: tab.projectId, q: trimmed })
      tab.results = result.items
      tab.serviceCounts = result.service_counts
      tab.selectedLogId = null
      tab.contextAnchorTime = null
      tab.contextByService = {}
      tab.hasMoreBeforeByService = {}
      tab.hasMoreAfterByService = {}
      tab.loadingMoreResults = false
      tab.status = result.items.length ? 'results' : 'emptyResults'
    } catch (err) {
      tab.error = err instanceof Error ? err.message : String(err)
      tab.status = 'error'
    }
  }

  async function runRemoteSearch(
    tabId: string,
    query: string,
    scope: RemoteSearchScope = {},
  ) {
    const tab = remoteSearchTab(tabId)
    const trimmed = query.trim()
    if (!tab || !tab.projectId || !trimmed) return
    const requestSeq = ++tab.requestSeq
    tab.query = trimmed
    tab.selectedServiceIds = scope.serviceIds ?? []
    tab.selectedHostIds = scope.hostIds ?? []
    tab.title = `Remote Search: ${trimmed}`
    resetRemoteSearchResults(tab)
    try {
      const result = await api.remoteSearch(remoteSearchRequest(tab))
      if (tab.requestSeq !== requestSeq) return
      applyRemoteSearchResult(tab, result)
    } catch (err) {
      if (tab.requestSeq !== requestSeq) return
      tab.error = err instanceof Error ? err.message : String(err)
      tab.status = 'error'
    }
  }

  async function loadMoreRemoteSearch(tabId: string): Promise<boolean> {
    const tab = remoteSearchTab(tabId)
    if (!tab || !tab.projectId || !tab.query || !tab.hasMore || !tab.nextCursor || tab.status === 'loading') return false
    const requestSeq = ++tab.requestSeq
    try {
      const result = await api.remoteSearch(remoteSearchRequest(tab, tab.nextCursor))
      if (tab.requestSeq !== requestSeq) return false
      mergeRemoteSearchResult(tab, result)
      return true
    } catch (err) {
      if (tab.requestSeq !== requestSeq) return false
      tab.error = err instanceof Error ? err.message : String(err)
      tab.status = 'error'
      return false
    }
  }

  async function loadContext(tabId: string, logId: number) {
    const tab = searchTab(tabId)
    if (!tab) return
    const visibleServices = visibleContextServiceIds(tab)
    const result = await api.fetchLogContext({
      project: tab.projectId,
      id: logId,
      service: visibleServices,
    })
    tab.selectedLogId = result.target_id
    tab.contextAnchorTime = result.anchor_time
    for (const serviceId of visibleServices) {
      if (tab.pinnedServiceIds.includes(serviceId)) continue
      tab.contextByService[serviceId] = result.items_by_service[serviceId] ?? []
      tab.hasMoreBeforeByService[serviceId] = true
      tab.hasMoreAfterByService[serviceId] = true
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
      service: serviceIds,
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
      .filter((item): item is { serviceId: string; cursor: { cursor_time: string; cursor_id: number } } =>
        item !== null,
      )
    if (requests.length === 0) return false

    tab[loadingKey] = true
    try {
      const pages = await Promise.all(
        requests.map(({ serviceId, cursor }) =>
          api.fetchLogContextPage({
            project: tab.projectId,
            service: serviceId,
            direction,
            cursor_time: cursor.cursor_time,
            cursor_id: cursor.cursor_id,
            limit: CONTEXT_PAGE_LIMIT,
          }),
        ),
      )
      let changed = false
      for (const page of pages) {
        hasMoreMap[page.service_id] = page.has_more
        if (page.items.length === 0) continue
        tab.contextByService[page.service_id] = mergeLogs(
          tab.contextByService[page.service_id] ?? [],
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
    tabs.value.splice(idx, 1)
    if (remoteHiddenHostIdsByTab.value[tabId]) {
      const { [tabId]: _removed, ...rest } = remoteHiddenHostIdsByTab.value
      remoteHiddenHostIdsByTab.value = rest
    }
    if (activeTabId.value !== tabId) return
    activeTabId.value = tabs.value[Math.max(0, idx - 1)]?.id ?? null
    const tab = activeTab.value
    if (isLogWorkspaceTab(tab)) {
      usePanelStore().setRoot(tab.layoutRoot, tab.focusedPanelId)
    }
  }

  return {
    tabs,
    activeTabId,
    activeTab,
    remoteHiddenHostIdsByTab,
    activateTab,
    openService,
    openSearch,
    openRemote,
    openRemoteSearch,
    openRemoteProjectSearch,
    openRemoteAggregate,
    hideRemoteHost,
    showRemoteHost,
    toggleRemoteHost,
    visibleRemoteHostIds,
    isRemoteHostVisible,
    searchTab,
    remoteSearchTab,
    hideService,
    showService,
    canLoadMoreSearchResults,
    pinService,
    unpinService,
    selectSearchResult,
    runSearch,
    runRemoteSearch,
    loadMoreRemoteSearch,
    loadContext,
    loadMoreSearchResults,
    loadMoreContext,
    closeTab,
    saveActiveProjectLayout,
    saveActiveLogWorkspaceLayout,
  }
})
