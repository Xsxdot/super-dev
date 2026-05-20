// workspaceStore 管理右侧项目/搜索标签页，是侧边栏和内容区之间的导航状态。
//
// 职责：
//   - 管理项目日志标签和项目搜索标签
//   - 在项目标签切换时保存/恢复 Panel 布局树
//   - 承载搜索页局部状态：结果、上下文、隐藏服务、固定服务
//
// 边界：
//   - 不渲染 UI，组件只读取这里的状态和动作
//   - 不直接订阅实时日志，项目标签仍由 Panel/LogPanel 负责
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { v4 as uuidv4 } from 'uuid'
import { api, type LogEntry } from '@/api/agent'
import { useAgentStore } from './agent'
import {
  createEmptyPanelRoot,
  usePanelStore,
  type PanelNode,
} from './panel'

export type WorkspaceTab = ProjectWorkspaceTab | SearchWorkspaceTab

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
  error: string | null
}

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
    error: null,
  }
}

export const useWorkspaceStore = defineStore('workspace', () => {
  const tabs = ref<WorkspaceTab[]>([])
  const activeTabId = ref<string | null>(null)

  const activeTab = computed(() => tabs.value.find(t => t.id === activeTabId.value) ?? null)

  function projectName(projectId: string): string {
    return useAgentStore().projectById(projectId)?.name ?? projectId
  }

  function saveActiveProjectLayout() {
    const active = activeTab.value
    if (!active || active.type !== 'project') return
    const panel = usePanelStore()
    active.layoutRoot = panel.root
    active.focusedPanelId = panel.focusedPanelId
  }

  function activateTab(tabId: string) {
    saveActiveProjectLayout()
    activeTabId.value = tabId
    const tab = activeTab.value
    if (tab?.type === 'project') {
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
    saveActiveProjectLayout()
  }

  function openSearch(projectId: string): SearchWorkspaceTab {
    saveActiveProjectLayout()
    const tab = makeSearchTab(projectId, `Search · ${projectName(projectId)}`)
    tabs.value.push(tab)
    activeTabId.value = tab.id
    return tab
  }

  function searchTab(tabId: string): SearchWorkspaceTab | null {
    const tab = tabs.value.find(t => t.id === tabId)
    return tab?.type === 'search' ? tab : null
  }

  function hideService(tabId: string, serviceId: string) {
    const tab = searchTab(tabId)
    if (!tab || tab.hiddenServiceIds.includes(serviceId)) return
    tab.hiddenServiceIds.push(serviceId)
  }

  function showService(tabId: string, serviceId: string) {
    const tab = searchTab(tabId)
    if (!tab) return
    tab.hiddenServiceIds = tab.hiddenServiceIds.filter(id => id !== serviceId)
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
      tab.status = result.items.length ? 'results' : 'emptyResults'
    } catch (err) {
      tab.error = err instanceof Error ? err.message : String(err)
      tab.status = 'error'
    }
  }

  async function loadContext(tabId: string, logId: number) {
    const tab = searchTab(tabId)
    if (!tab) return
    const visibleServices = Object.keys(tab.serviceCounts).filter(
      serviceId => !tab.hiddenServiceIds.includes(serviceId),
    )
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
    }
  }

  function closeTab(tabId: string) {
    const idx = tabs.value.findIndex(t => t.id === tabId)
    if (idx < 0) return
    tabs.value.splice(idx, 1)
    if (activeTabId.value !== tabId) return
    activeTabId.value = tabs.value[Math.max(0, idx - 1)]?.id ?? null
    const tab = activeTab.value
    if (tab?.type === 'project') {
      usePanelStore().setRoot(tab.layoutRoot, tab.focusedPanelId)
    }
  }

  return {
    tabs,
    activeTabId,
    activeTab,
    activateTab,
    openService,
    openSearch,
    searchTab,
    hideService,
    showService,
    pinService,
    unpinService,
    runSearch,
    loadContext,
    closeTab,
    saveActiveProjectLayout,
  }
})
