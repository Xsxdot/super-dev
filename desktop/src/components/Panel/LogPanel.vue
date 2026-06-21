<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useVirtualizer } from '@tanstack/vue-virtual'
import { useFilterStore } from '@/stores/filter'
import { useBookmarkStore } from '@/stores/bookmark'
import { useAgentStore } from '@/stores/agent'
import { useDeploymentLogStore } from '@/stores/deploymentLog'
import { useDeploymentNodeSelectionStore } from '@/stores/deploymentNodeSelection'
import { useLogLifecycleStore } from '@/stores/logLifecycle'
import { useLogEvidenceStore } from '@/stores/logEvidence'
import { usePanelStore } from '@/stores/panel'
import { useRemoteStore } from '@/stores/remote'
import { useNodeStore } from '@/stores/node'
import PanelToolbar from './PanelToolbar.vue'
import LogRow from './LogRow.vue'
import BookmarkMarkerRow from './BookmarkMarkerRow.vue'
import LogContextMenu from './LogContextMenu.vue'
import PinNotePopover from './PinNotePopover.vue'
import LogHistorySeparatorRow from './LogHistorySeparatorRow.vue'
import LogLifecycleSeparatorRow from './LogLifecycleSeparatorRow.vue'
import {
  buildDeploymentNodeStatus,
  logMatchesSelectedNodes,
  type DeploymentNodeIssueKind,
  type DeploymentNodeState,
} from '@/lib/deploymentNodeStatus'
import { formatLogWithCursor, nearestLogIndexByCursorTime } from '@/lib/logEvidenceFormat'
import { logEvidenceDiagnostic } from '@/lib/logEvidenceDiagnostics'
import type { DisplayLogEntry } from '@/lib/logEngine'
import type { EvidencePin } from '@/stores/logEvidence'
import type { PanelSource } from '@/stores/panel'
import {
  makeDisplayItems,
  computeDisplayStats,
  type LogDisplayItem,
  type DisplayStats,
  type HistoryBoundary,
} from '@/lib/logDisplay'

const INITIAL_HISTORY_LIMIT = 200
const INCREMENTAL_HISTORY_LIMIT = 80
const HISTORY_PREFETCH_START_INDEX = 30
const FILTERED_HISTORY_BACKFILL_MAX_PAGES = 6
const LOG_VIRTUAL_OVERSCAN = 12

const props = defineProps<{
  panelId: string
  projectId?: string | null
  source?: PanelSource | null
}>()

const filterStore = useFilterStore()
const bookmarkStore = useBookmarkStore()
const agentStore = useAgentStore()
const deploymentLogStore = useDeploymentLogStore()
const deploymentNodeSelectionStore = useDeploymentNodeSelectionStore()
const logLifecycleStore = useLogLifecycleStore()
const evidenceStore = useLogEvidenceStore()
const panelStore = usePanelStore()
const remoteStore = useRemoteStore()
const nodeStore = useNodeStore()
const { t } = useI18n()

const toolbarRef = ref<InstanceType<typeof PanelToolbar> | null>(null)
const isFollowing = ref(true)
const newLogCount = ref(0)
const logListEl = ref<HTMLElement | null>(null)
const isLoadingHistory = ref(false)
const initialHistoryBoundary = ref<HistoryBoundary | null>(null)

const activeSelectionEntryId = ref<number | null>(null)
const activeSelectionText = ref<string | null>(null)
const activeSelectionRect = ref<DOMRect | null>(null)
const flashLogId = ref<string | null>(null)
const timeAnchorLogId = ref<string | null>(null)
const contextMenu = ref<{ x: number; y: number; log: DisplayLogEntry } | null>(null)
const editingPinId = ref<string | null>(null)
const pinNotePopoverStyle = ref<Record<string, string>>({ left: '14px', top: '44px' })

const markerStartId = ref('')
const markerEndId = ref('')
const bookmarkCapturedIds = new Set<string>()

const cachedDisplay = ref<{ items: LogDisplayItem[]; stats: DisplayStats }>({
  items: [],
  stats: { total: 0, folded: 0, errors: 0, warns: 0 },
})
const remoteManagedStatuses = computed(() =>
  nodeStore.managedStatuses.size > 0 ? nodeStore.managedStatuses : remoteStore.managedStatuses,
)

let displayRefreshTimer: ReturnType<typeof setTimeout> | null = null
let scrollRetryTimer: ReturnType<typeof setTimeout> | null = null
let programmaticScroll = false
let historyLoadToken = 0

function deploymentIdFromSource(source: PanelSource | null | undefined): string | null {
  return source?.type === 'deployment' ? source.deploymentId : null
}

const currentDeploymentInfo = computed(() => {
  const deploymentId = deploymentIdFromSource(props.source)
  return deploymentId ? agentStore.serviceForDeployment(deploymentId) : undefined
})

const evidenceSourceKey = computed(() => deploymentIdFromSource(props.source) ?? props.panelId)

const evidenceTrackLabel = computed(() => {
  const deploymentId = deploymentIdFromSource(props.source)
  const info = deploymentId ? agentStore.serviceForDeployment(deploymentId) : undefined
  return info ? `${info.service.name} · ${info.envName}` : (deploymentId ?? props.panelId)
})

const currentNodeStatus = computed(() => {
  const dep = currentDeploymentInfo.value?.deployment
  if (!dep || dep.location !== 'remote') return null
  return buildDeploymentNodeStatus(dep, remoteStore.hosts, remoteManagedStatuses.value)
})

const currentRemoteNodes = computed(() => currentNodeStatus.value?.nodes ?? [])

const currentRemoteHostIds = computed(() => {
  const dep = currentDeploymentInfo.value?.deployment
  return dep?.location === 'remote' ? [...new Set(dep.host_ids ?? [])] : []
})

async function refreshRemoteNodeContext(hostIds: string[]) {
  if (hostIds.length === 0) return
  try {
    if (remoteStore.hosts.length === 0) await remoteStore.loadHosts()
    if (nodeStore.managedStatuses.size > 0) return
    await remoteStore.refreshManagedStatuses(hostIds)
  } catch (err) {
    console.warn('[SuperDev] refresh log panel remote node status failed:', err)
  }
}

watch(
  currentRemoteHostIds,
  hostIds => void refreshRemoteNodeContext(hostIds),
  { immediate: true },
)

watch(
  () => `${deploymentIdFromSource(props.source) ?? ''}:${currentRemoteHostIds.value.join('|')}`,
  () => {
    const deploymentId = deploymentIdFromSource(props.source)
    if (!deploymentId || currentRemoteHostIds.value.length === 0) return
    deploymentNodeSelectionStore.ensureDeploymentNodes(deploymentId, currentRemoteHostIds.value)
  },
  { immediate: true },
)

async function subscribeDeployment(deploymentId: string) {
  deploymentLogStore.subscribe(deploymentId)
  initialHistoryBoundary.value = null
  const token = ++historyLoadToken
  await deploymentLogStore.loadMoreHistory(deploymentId, INITIAL_HISTORY_LIMIT)
  if (token !== historyLoadToken || deploymentId !== deploymentIdFromSource(props.source)) return
  const logs = deploymentLogStore.getLogs(deploymentId)
  const newest = logs[logs.length - 1]
  initialHistoryBoundary.value = newest ? { timestamp: newest.timestamp, id: newest.id } : null
  refreshDisplayImmediately()
  if (isFollowing.value) pinToBottomIfFollowing()
}

onMounted(() => {
  const deploymentId = deploymentIdFromSource(props.source)
  if (deploymentId) void subscribeDeployment(deploymentId)
  if (props.projectId) void filterStore.loadProjectRules(props.projectId)
  registerEvidenceTrack()
  refreshDisplayImmediately()
  scrollToBottom()
})

watch(
  () => deploymentIdFromSource(props.source),
  (deploymentId, prevDeploymentId) => {
    if (deploymentId === prevDeploymentId) return
    if (prevDeploymentId) deploymentLogStore.unsubscribe(prevDeploymentId)
    historyLoadToken++
    initialHistoryBoundary.value = null
    if (deploymentId) void subscribeDeployment(deploymentId)
    registerEvidenceTrack()
    isFollowing.value = true
    newLogCount.value = 0
    refreshDisplayImmediately()
  },
)

watch(
  () => props.projectId,
  (projectId, prev) => {
    if (projectId && projectId !== prev) void filterStore.loadProjectRules(projectId)
  },
)

onUnmounted(() => {
  const deploymentId = deploymentIdFromSource(props.source)
  if (deploymentId) deploymentLogStore.unsubscribe(deploymentId)
  evidenceStore.unregisterTrack(props.panelId)
  historyLoadToken++
  filterStore.removePanel(props.panelId)
  if (displayRefreshTimer) clearTimeout(displayRefreshTimer)
  cancelScrollRetries()
})

const rawLogs = computed<DisplayLogEntry[]>(() => {
  if (props.source?.type === 'deployment') {
    return deploymentLogStore.getLogs(props.source.deploymentId)
  }
  return []
})

const panelFilterSignature = computed(() => {
  const panel = filterStore.panelFilters[props.panelId]
  if (!panel) return ''
  const chips = panel.chips
    .map(chip => `${chip.id}:${chip.type}:${chip.keyword}`)
    .join('|')
  return `${panel.logic}:${chips}`
})

const ruleFilterResult = computed(() => {
  panelFilterSignature.value
  return filterStore.applyFiltersWithStats(props.panelId, props.projectId ?? null, rawLogs.value)
})

const ruleFilteredLogs = computed(() => ruleFilterResult.value.logs)

const ruleFilteredCount = computed(() => ruleFilterResult.value.filteredCount)

const selectedRemoteHostIds = computed(() => {
  const deploymentId = deploymentIdFromSource(props.source)
  return deploymentId ? deploymentNodeSelectionStore.selectedHostIds(deploymentId) : []
})

const filteredLogs = computed(() => {
  const nodes = currentRemoteNodes.value
  if (nodes.length === 0) return ruleFilteredLogs.value
  return ruleFilteredLogs.value.filter(log =>
    logMatchesSelectedNodes(log, nodes, selectedRemoteHostIds.value),
  )
})

const nodeFilterSignature = computed(() =>
  `${deploymentIdFromSource(props.source) ?? ''}:${selectedRemoteHostIds.value.join(',')}:${currentRemoteNodes.value.map(node => node.sourceIds.join('/')).join('|')}`,
)

const historyBoundary = computed(() => initialHistoryBoundary.value)

const lifecycleMarkers = computed(() => {
  const deploymentId = deploymentIdFromSource(props.source)
  return deploymentId ? logLifecycleStore.getMarkers(deploymentId) : []
})

function makeLogDisplay() {
  const logs = filteredLogs.value
  const bm = bookmarkStore.getBookmark(props.panelId)
  const displayBm =
    bm?.startTime != null
      ? {
          state: bm.state,
          startTime: bm.startTime,
          endTime: bm.endTime,
          lockedLogs: bm.lockedLogs,
        }
      : null
  const items = makeDisplayItems(logs, displayBm, {
    start: markerStartId.value,
    end: markerEndId.value,
  }, historyBoundary.value, lifecycleMarkers.value)
  cachedDisplay.value = { items, stats: computeDisplayStats(items) }
}

function scheduleDisplayRefresh() {
  if (displayRefreshTimer) clearTimeout(displayRefreshTimer)
  displayRefreshTimer = setTimeout(() => {
    displayRefreshTimer = null
    const oldCount = entryCount(cachedDisplay.value.items)
    makeLogDisplay()
    const newCount = entryCount(cachedDisplay.value.items)
    applyItemsCountChange(oldCount, newCount)
    settleVirtualizerAfterCountChange(oldCount, newCount)
  }, 32)
}

function refreshDisplayImmediately() {
  if (displayRefreshTimer) {
    clearTimeout(displayRefreshTimer)
    displayRefreshTimer = null
  }
  const oldCount = entryCount(cachedDisplay.value.items)
  nextTick(() => {
    makeLogDisplay()
    const newCount = entryCount(cachedDisplay.value.items)
    applyItemsCountChange(oldCount, newCount)
    settleVirtualizerAfterCountChange(oldCount, newCount)
  })
}

function entryCount(items: LogDisplayItem[]): number {
  return items.filter(i => i.kind === 'entry').length
}

const bookmark = computed(() => bookmarkStore.getBookmark(props.panelId))

function currentPanelSource(): PanelSource | null {
  return props.source ?? null
}

function bookmarkMatchesCurrentSource(): boolean {
  const bm = bookmark.value
  if (!bm?.source) return true
  return JSON.stringify(bm.source) === JSON.stringify(currentPanelSource())
}

function isHighlighted(log: DisplayLogEntry): boolean {
  const bm = bookmark.value
  if (!bm?.startTime) return false
  const ts = new Date(log.timestamp)
  if (bm.state === 'recording') return ts >= bm.startTime
  if (bm.state === 'done' && bm.endTime) return ts >= bm.startTime && ts <= bm.endTime
  return false
}

function registerEvidenceTrack() {
  evidenceStore.registerTrack({
    trackId: props.panelId,
    panelId: props.panelId,
    trackLabel: evidenceTrackLabel.value,
    sourceKey: evidenceSourceKey.value,
    getLogs: () => filteredLogs.value,
    jumpToLog,
    alignToTime,
  })
}

function evidencePinFor(log: DisplayLogEntry) {
  return evidenceStore.pinFor(props.panelId, evidenceSourceKey.value, log.id)
}

function toggleEvidencePin(log: DisplayLogEntry) {
  const existing = evidencePinFor(log)
  if (existing?.note.trim() && !window.confirm(t('panel.evidence.confirmRemoveNotedPin'))) return
  evidenceStore.togglePin({
    panelId: props.panelId,
    trackId: props.panelId,
    trackLabel: evidenceTrackLabel.value,
    sourceKey: evidenceSourceKey.value,
    log,
  })
  if (evidenceStore.timeSyncEnabled) {
    evidenceStore.alignOtherTracksToLog(props.panelId, log)
  }
}

const editingPin = computed(() =>
  editingPinId.value ? evidenceStore.pins.find(pin => pin.id === editingPinId.value) ?? null : null,
)

function openPinNotePopover(pin: EvidencePin, event?: MouseEvent) {
  editingPinId.value = pin.id
  if (event) {
    const width = 276
    const height = 150
    // 备注弹层跟随点击点，并夹在 viewport 内；否则在长日志/抽屉覆盖时会像“没反应”。
    const left = Math.max(8, Math.min(event.clientX + 8, window.innerWidth - width))
    const top = Math.max(8, Math.min(event.clientY + 8, window.innerHeight - height))
    pinNotePopoverStyle.value = { left: `${left}px`, top: `${top}px` }
  }
  logEvidenceDiagnostic('debug', 'pin.note.open', {
    panelId: props.panelId,
    trackId: pin.trackId,
    pinId: pin.id,
    pinLabel: pin.label,
  })
}

function closePinNotePopover() {
  editingPinId.value = null
}

function savePinNote(note: string) {
  const pinId = editingPinId.value
  if (!pinId) return
  evidenceStore.updateNote(pinId, note)
  editingPinId.value = null
}

// serviceNameFor 通过日志的 deployment_id 反查所属 service 名，反查不到时显示截断的 id。
function serviceNameFor(log: DisplayLogEntry): string {
  const info = agentStore.serviceForDeployment(log.deployment_id)
  return info?.service.name ?? log.deployment_id.slice(0, 12)
}

watch(
  [filteredLogs, () => bookmark.value?.state, () => deploymentLogStore.logSourceRevision],
  ([logs, state]) => {
    if (state !== 'recording' || !bookmark.value?.startTime || !bookmarkMatchesCurrentSource()) return
    const startTime = bookmark.value.startTime
    for (const log of logs) {
      if (new Date(log.timestamp) < startTime) continue
      bookmarkCapturedIds.add(log.id)
      bookmarkStore.appendToBookmark(props.panelId, log)
    }
  },
)

function onEndBookmark() {
  bookmarkStore.endBookmark(
    props.panelId,
    bookmarkMatchesCurrentSource() ? filteredLogs.value : [],
    bookmarkCapturedIds,
  )
}

watch(
  () => deploymentLogStore.logSourceRevision,
  () => scheduleDisplayRefresh(),
  { deep: true },
)

watch(
  lifecycleMarkers,
  () => scheduleDisplayRefresh(),
  { deep: true },
)

watch(
  () => bookmark.value?.state,
  (state, prev) => {
    if (state === 'recording') {
      bookmarkCapturedIds.clear()
      markerStartId.value = crypto.randomUUID()
      markerEndId.value = crypto.randomUUID()
    }
    if (prev === 'recording' && state === 'done') {
      bookmarkCapturedIds.clear()
    }
    if (!state || state === 'idle') {
      markerStartId.value = ''
      markerEndId.value = ''
    }
    refreshDisplayImmediately()
  },
)

watch(
  panelFilterSignature,
  () => refreshDisplayImmediately(),
)

watch(
  nodeFilterSignature,
  () => refreshDisplayImmediately(),
)

watch(
  () => (props.projectId ? filterStore.projectRules[props.projectId] : undefined),
  () => scheduleDisplayRefresh(),
  { deep: true },
)

function cancelScrollRetries() {
  if (scrollRetryTimer) {
    clearTimeout(scrollRetryTimer)
    scrollRetryTimer = null
  }
}

function measureVirtualizer() {
  virtualizer.value.measure()
}

function logPanelDiagnostic(level: 'debug' | 'info' | 'warn', event: string, context: Record<string, unknown>) {
  if (typeof window === 'undefined') return
  window.dispatchEvent(new CustomEvent('superdev:log-panel', {
    detail: {
      scope: 'log-panel',
      level,
      event,
      at: new Date().toISOString(),
      panelId: props.panelId,
      ...context,
    },
  }))
}

function settleVirtualizerAfterCountChange(oldCount: number, newCount: number) {
  if (oldCount === newCount) return
  nextTick(() => {
    const rangeStart = virtualizer.value.range?.startIndex ?? 0
    measureVirtualizer()
    logPanelDiagnostic('debug', 'virtualizer.count_change', {
      oldCount,
      newCount,
      rangeStart,
      following: isFollowing.value,
    })
    if (isFollowing.value) {
      // 过滤规则既可能增加也可能减少可见行；follow 模式必须贴到新底部，避免停在旧 range 的空白区。
      pinToBottomIfFollowing()
      return
    }
    if (newCount > 0 && rangeStart >= displayItems.value.length) {
      virtualizer.value.scrollToIndex(displayItems.value.length - 1, { align: 'end' })
    }
  })
}

async function scrollToBottom() {
  programmaticScroll = true
  await nextTick()
  const count = displayItems.value.length
  if (count > 0) {
    virtualizer.value.scrollToIndex(count - 1, { align: 'end' })
  }
  setTimeout(() => {
    programmaticScroll = false
  }, 80)
}

function scheduleScrollRetries() {
  cancelScrollRetries()
  const delays = [50, 120, 250]
  let i = 0
  const run = () => {
    if (!isFollowing.value || i >= delays.length) {
      scrollRetryTimer = null
      return
    }
    scrollRetryTimer = setTimeout(async () => {
      if (!isFollowing.value) {
        scrollRetryTimer = null
        return
      }
      await scrollToBottom()
      i++
      run()
    }, delays[i])
  }
  run()
}

function pinToBottomIfFollowing() {
  if (!isFollowing.value) return
  newLogCount.value = 0
  scrollToBottom()
  scheduleScrollRetries()
}

function applyItemsCountChange(oldCount: number, newCount: number) {
  if (isFollowing.value) {
    newLogCount.value = 0
  } else {
    newLogCount.value += Math.max(0, newCount - oldCount)
  }
}

function onScroll() {
  if (programmaticScroll) return
  const el = logListEl.value
  if (!el) return
  const dist = el.scrollHeight - el.scrollTop - el.clientHeight
  const wasFollowing = isFollowing.value
  if (dist >= 50) {
    isFollowing.value = false
    cancelScrollRetries()
  } else {
    isFollowing.value = true
    newLogCount.value = 0
    if (!wasFollowing) pinToBottomIfFollowing()
  }
  const range = virtualizer.value.range
  if (range && range.startIndex < HISTORY_PREFETCH_START_INDEX) {
    void tryLoadMoreHistory()
  }
}

function onWheel(e: WheelEvent) {
  if (e.deltaY < 0) {
    isFollowing.value = false
    cancelScrollRetries()
    const rangeStart = virtualizer.value.range?.startIndex ?? 0
    if (rangeStart < HISTORY_PREFETCH_START_INDEX || displayItems.value.length < HISTORY_PREFETCH_START_INDEX) {
      void tryLoadMoreHistory()
    }
  }
}

async function tryLoadMoreHistory() {
  if (props.source?.type === 'deployment') {
    const deploymentId = props.source.deploymentId
    if (!deploymentLogStore.hasMoreHistory(deploymentId)) return
    if (isLoadingHistory.value) return
    isLoadingHistory.value = true
    // 快速滚动时 range 可能短暂为空；按 0 补偿可以让重新测量后的窗口回到顶部附近。
    const prevStart = virtualizer.value.range?.startIndex ?? 0
    const prevCount = displayItems.value.length
    let loadedPages = 0
    try {
      while (deploymentLogStore.hasMoreHistory(deploymentId) && loadedPages < FILTERED_HISTORY_BACKFILL_MAX_PAGES) {
        loadedPages++
        const result = await deploymentLogStore.loadMoreHistory(deploymentId, INCREMENTAL_HISTORY_LIMIT)
        if (displayRefreshTimer) {
          clearTimeout(displayRefreshTimer)
          displayRefreshTimer = null
        }
        const beforeVisibleCount = displayItems.value.length
        makeLogDisplay()
        await nextTick()
        const afterVisibleCount = displayItems.value.length
        logPanelDiagnostic('debug', 'history.backfill.page', {
          deploymentId,
          loadedPages,
          rawAdded: result.added,
          visibleAdded: afterVisibleCount - beforeVisibleCount,
          hasMoreHistory: deploymentLogStore.hasMoreHistory(deploymentId),
        })
        // 过滤条件可能把整页历史都隐藏掉；继续向上找，避免用户在无滚动条时卡住。
        if (result.added <= 0 || afterVisibleCount > beforeVisibleCount) break
      }
      const visibleAdded = displayItems.value.length - prevCount
      if (visibleAdded > 0) {
        programmaticScroll = true
        measureVirtualizer()
        virtualizer.value.scrollToIndex(prevStart + visibleAdded, { align: 'start' })
        setTimeout(() => {
          measureVirtualizer()
          virtualizer.value.scrollToIndex(prevStart + visibleAdded, { align: 'start' })
        }, 0)
        setTimeout(() => { programmaticScroll = false }, 80)
      }
      logPanelDiagnostic('debug', 'history.backfill.done', {
        deploymentId,
        loadedPages,
        visibleAdded,
        hasMoreHistory: deploymentLogStore.hasMoreHistory(deploymentId),
      })
    } finally {
      isLoadingHistory.value = false
    }
  }
}

function jumpToBottom() {
  isFollowing.value = true
  newLogCount.value = 0
  pinToBottomIfFollowing()
}

function onLogSelection(logId: number, text: string | null, rect: DOMRect | null) {
  if (text && rect) {
    activeSelectionEntryId.value = logId
    activeSelectionText.value = text
    activeSelectionRect.value = rect
  } else if (activeSelectionEntryId.value === logId) {
    clearLogSelection()
  }
}

function clearLogSelection() {
  activeSelectionEntryId.value = null
  activeSelectionText.value = null
  activeSelectionRect.value = null
}

function fillChipFromSelection() {
  const text = activeSelectionText.value
  if (!text) return
  toolbarRef.value?.fillChipInput(text)
  clearLogSelection()
  window.getSelection()?.removeAllRanges()
}

function openLogContextMenu(event: MouseEvent, log: DisplayLogEntry) {
  contextMenu.value = {
    x: event.clientX,
    y: event.clientY,
    log,
  }
}

function closeLogContextMenu() {
  contextMenu.value = null
}

async function copyTextToClipboard(text: string, event: string, log: DisplayLogEntry) {
  try {
    await navigator.clipboard.writeText(text)
    logEvidenceDiagnostic('info', event, {
      panelId: props.panelId,
      trackId: props.panelId,
      deploymentId: log.deployment_id,
      cursorTime: log.timestamp,
      cursorId: log.id,
    })
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err)
    logEvidenceDiagnostic('error', `${event}.failure`, {
      panelId: props.panelId,
      trackId: props.panelId,
      deploymentId: log.deployment_id,
      cursorTime: log.timestamp,
      cursorId: log.id,
      error: message,
    })
    window.alert(t('panel.evidence.copyFailed', { message }))
  }
}

async function copyContextLog() {
  if (!contextMenu.value) return
  const log = contextMenu.value.log
  await copyTextToClipboard(log.message, 'log.copy.success', log)
  closeLogContextMenu()
}

async function copyContextLogWithCursor() {
  if (!contextMenu.value) return
  const log = contextMenu.value.log
  await copyTextToClipboard(formatLogWithCursor(log), 'log.copy_with_cursor.success', log)
  closeLogContextMenu()
}

function addContextPin() {
  if (!contextMenu.value) return
  toggleEvidencePin(contextMenu.value.log)
  closeLogContextMenu()
}

function removeContextPin() {
  if (!contextMenu.value) return
  const pin = evidencePinFor(contextMenu.value.log)
  if (pin?.note.trim() && !window.confirm(t('panel.evidence.confirmRemoveNotedPin'))) return
  if (pin) evidenceStore.removePin(pin.id)
  closeLogContextMenu()
}

function alignOtherPanesFromContext() {
  if (!contextMenu.value) return
  evidenceStore.alignOtherTracksToLog(props.panelId, contextMenu.value.log)
  closeLogContextMenu()
}

function displayIndexOfLog(logId: string): number {
  return displayItems.value.findIndex(item => item.kind === 'entry' && item.log.id === logId)
}

async function jumpToLog(logId: string): Promise<boolean> {
  const index = displayIndexOfLog(logId)
  if (index < 0) return false
  // 程序化滚动不能被 onScroll 当作用户离开 live-follow，否则跳转会破坏追踪状态。
  programmaticScroll = true
  await nextTick()
  virtualizer.value.scrollToIndex(index, { align: 'center' })
  flashLogId.value = logId
  window.setTimeout(() => {
    if (flashLogId.value === logId) flashLogId.value = null
    programmaticScroll = false
  }, 900)
  return true
}

async function alignToTime(cursorTime: string, cursorId: string): Promise<boolean> {
  const logIndex = nearestLogIndexByCursorTime(filteredLogs.value, cursorTime, cursorId)
  if (logIndex < 0) {
    logEvidenceDiagnostic('warn', 'time_sync.align.miss', { panelId: props.panelId, trackId: props.panelId, cursorTime, cursorId })
    return false
  }
  const target = filteredLogs.value[logIndex]
  const displayIndex = displayIndexOfLog(target.id)
  if (displayIndex < 0) {
    // 日志可能已在 store 中但未进入当前过滤后的虚拟列表，保留软失败便于抽屉继续展示快照。
    logEvidenceDiagnostic('warn', 'time_sync.align.not_rendered', { panelId: props.panelId, trackId: props.panelId, cursorTime, cursorId })
    return false
  }
  programmaticScroll = true
  await nextTick()
  virtualizer.value.scrollToIndex(displayIndex, { align: 'center' })
  timeAnchorLogId.value = target.id
  window.setTimeout(() => {
    programmaticScroll = false
  }, 80)
  logEvidenceDiagnostic('info', 'time_sync.align.success', {
    panelId: props.panelId,
    trackId: props.panelId,
    deploymentId: target.deployment_id,
    cursorTime: target.timestamp,
    cursorId: target.id,
  })
  return true
}

const selectionButtonStyle = computed(() => {
  const rect = activeSelectionRect.value
  const list = logListEl.value
  if (!rect || !list) return { display: 'none' }
  const listRect = list.getBoundingClientRect()
  return {
    left: `${rect.right - listRect.left + list.scrollLeft + 4}px`,
    top: `${rect.top - listRect.top + list.scrollTop - 4}px`,
  }
})

const stats = computed(() => cachedDisplay.value.stats)
const displayItems = computed(() => cachedDisplay.value.items)

const virtualizer = useVirtualizer(
  computed(() => ({
    count: displayItems.value.length,
    getScrollElement: () => logListEl.value,
    estimateSize: () => 22,
    getItemKey: (index: number) => displayItems.value[index]?.id ?? index,
    overscan: LOG_VIRTUAL_OVERSCAN,
  }))
)

function nodeHealthColor(health: string): string {
  if (health === 'healthy') return '#3fb950'
  if (health === 'warning') return '#d29922'
  if (health === 'failed') return '#f85149'
  return '#6e7681'
}

function issueLabel(kind: DeploymentNodeIssueKind, detail?: string): string {
  if (kind === 'host-error') return detail || t('panel.log.nodeHostError')
  if (kind === 'collector-error') return detail || t('panel.log.nodeCollectorError')
  return t(`panel.log.nodeIssues.${kind}`)
}

function nodeIssueLabel(node: DeploymentNodeState): string {
  if (!node.issue) return t('panel.log.nodeHealthy')
  return issueLabel(node.issue.kind, node.issue.detail)
}

function nodeSummaryLabel(): string {
  const status = currentNodeStatus.value
  if (!status) return ''
  if (status.collectorExpected > 0) {
    return t('panel.log.nodeSummary', {
      selected: selectedRemoteHostIds.value.length,
      total: status.total,
      collectors: status.collectorReady,
      desiredCollectors: status.collectorExpected,
    })
  }
  return t('panel.log.nodeSummaryNoCollector', {
    selected: selectedRemoteHostIds.value.length,
    total: status.total,
  })
}

function isNodeSelected(hostId: string): boolean {
  const deploymentId = deploymentIdFromSource(props.source)
  return !!deploymentId && deploymentNodeSelectionStore.isNodeSelected(deploymentId, hostId)
}

function toggleNode(hostId: string) {
  const deploymentId = deploymentIdFromSource(props.source)
  if (!deploymentId) return
  deploymentNodeSelectionStore.toggleNode(deploymentId, hostId)
}
</script>

<template>
  <div class="log-panel">
    <PanelToolbar
      ref="toolbarRef"
      :panel-id="panelId"
      :source="source"
      :project-id="projectId"
      @end-bookmark="onEndBookmark"
    />
    <div
      v-if="currentRemoteNodes.length > 0"
      class="node-filter-strip"
      data-test="log-node-filter-strip"
    >
      <span class="node-summary">{{ nodeSummaryLabel() }}</span>
      <button
        v-for="node in currentRemoteNodes"
        :key="node.hostId"
        type="button"
        class="node-filter-chip"
        :class="{ selected: isNodeSelected(node.hostId) }"
        :title="nodeIssueLabel(node)"
        data-test="log-node-filter-chip"
        @mousedown.prevent
        @click="toggleNode(node.hostId)"
      >
        <span class="node-dot" :style="{ background: nodeHealthColor(node.health) }" />
        <span class="node-name">{{ node.hostName }}</span>
      </button>
    </div>
    <div ref="logListEl" class="log-list" @scroll="onScroll" @wheel="onWheel">
      <div v-if="source?.type === 'deployment' && isLoadingHistory" class="history-loading">{{ t('panel.log.historyLoading') }}</div>
      <div v-else-if="source?.type === 'deployment' && !deploymentLogStore.hasMoreHistory(source.deploymentId)" class="history-end">{{ t('panel.log.historyEnd') }}</div>

      <div :style="{ height: virtualizer.getTotalSize() + 'px', position: 'relative' }">
        <div
          v-for="vRow in virtualizer.getVirtualItems()"
          :key="String(vRow.key)"
          :data-index="vRow.index"
          :ref="(el) => { if (el) virtualizer.measureElement(el as Element) }"
          :style="{ position: 'absolute', top: vRow.start + 'px', width: '100%' }"
        >
          <template v-if="displayItems[vRow.index]">
            <BookmarkMarkerRow
              v-if="displayItems[vRow.index].kind === 'markerStart'"
              :is-start="true"
              :date="(displayItems[vRow.index] as any).date"
            />
            <BookmarkMarkerRow
              v-else-if="displayItems[vRow.index].kind === 'markerEnd'"
              :is-start="false"
              :date="(displayItems[vRow.index] as any).date"
            />
            <LogHistorySeparatorRow
              v-else-if="displayItems[vRow.index].kind === 'historySeparator'"
            />
            <LogLifecycleSeparatorRow
              v-else-if="displayItems[vRow.index].kind === 'lifecycleSeparator'"
              :marker="(displayItems[vRow.index] as any).marker"
            />
            <LogRow
              v-else-if="displayItems[vRow.index].kind === 'entry'"
              :log="(displayItems[vRow.index] as any).log"
              :service-name="serviceNameFor((displayItems[vRow.index] as any).log)"
              :highlighted="isHighlighted((displayItems[vRow.index] as any).log)"
              :evidence-pin="evidencePinFor((displayItems[vRow.index] as any).log)"
              :evidence-flash="flashLogId === (displayItems[vRow.index] as any).log.id"
              :time-anchor="timeAnchorLogId === (displayItems[vRow.index] as any).log.id"
              @selection-change="(t, r) => onLogSelection((displayItems[vRow.index] as any).log.id, t, r)"
              @toggle-pin="toggleEvidencePin"
              @edit-pin="openPinNotePopover"
              @row-context-menu="openLogContextMenu"
            />
          </template>
        </div>
      </div>

      <button
        v-if="activeSelectionText && activeSelectionRect"
        class="selection-add-btn"
        :style="selectionButtonStyle"
        :title="t('panel.log.addSelectionToFilter')"
        @mousedown.prevent
        @click="fillChipFromSelection"
      >
        +
      </button>

      <LogContextMenu
        v-if="contextMenu"
        :x="contextMenu.x"
        :y="contextMenu.y"
        :has-pin="!!evidencePinFor(contextMenu.log)"
        :can-align="panelStore.allLeaves.length > 1"
        @copy-log="copyContextLog"
        @copy-log-with-cursor="copyContextLogWithCursor"
        @add-pin="addContextPin"
        @remove-pin="removeContextPin"
        @align-time="alignOtherPanesFromContext"
        @close="closeLogContextMenu"
      />
      <PinNotePopover
        v-if="editingPin"
        class="pin-note-popover-instance"
        :style="pinNotePopoverStyle"
        :pin="editingPin"
        @save="savePinNote"
        @close="closePinNotePopover"
      />
    </div>

    <Transition name="fade">
      <button v-if="!isFollowing && newLogCount > 0" class="new-log-pill" @click="jumpToBottom">
        {{ t('panel.log.newLogs', { count: newLogCount }) }}
      </button>
    </Transition>

    <div class="status-bar" data-test="log-panel-status">
      <span>
        {{ t('panel.log.liveStats', { total: stats.total }) }}
        <template v-if="ruleFilteredCount > 0"> · {{ t('panel.log.filtered', { count: ruleFilteredCount }) }}</template>
        <template v-if="stats.folded > 0"> · {{ t('panel.log.folded', { count: stats.folded }) }}</template>
      </span>
      <div class="status-badges">
        <span v-if="stats.errors > 0" class="badge error">{{ t('panel.log.errors', { count: stats.errors }) }}</span>
        <span v-if="stats.warns > 0" class="badge warn">{{ t('panel.log.warnings', { count: stats.warns }) }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.log-panel {
  container-type: inline-size;
  display: flex;
  flex-direction: column;
  flex: 1;
  overflow: hidden;
  position: relative;
}

.node-filter-strip {
  display: flex;
  align-items: center;
  gap: 6px;
  min-height: 34px;
  padding: 5px 10px;
  border-top: 1px solid rgba(139, 148, 158, 0.12);
  border-bottom: 1px solid rgba(139, 148, 158, 0.12);
  background: rgba(13, 24, 34, 0.62);
  overflow-x: auto;
  flex-shrink: 0;
}

.node-summary {
  color: var(--text-tertiary);
  font-size: 11px;
  white-space: nowrap;
}

.node-filter-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 24px;
  padding: 0 8px;
  border: 1px solid rgba(139, 148, 158, 0.2);
  border-radius: 5px;
  background: rgba(255, 255, 255, 0.025);
  color: var(--text-tertiary);
  cursor: pointer;
  white-space: nowrap;
}

.node-filter-chip.selected {
  border-color: rgba(88, 166, 255, 0.34);
  background: rgba(31, 111, 235, 0.12);
  color: var(--text-primary);
}

.node-filter-chip:hover {
  color: var(--text-primary);
}

.node-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.node-name {
  max-width: 104px;
  overflow: hidden;
  font-size: 11px;
  text-overflow: ellipsis;
}

.log-list {
  flex: 1;
  overflow-y: auto;
  background: rgba(7, 15, 22, 0.72);
  padding: 8px 10px;
  position: relative;
}
.selection-add-btn {
  position: absolute;
  z-index: 10;
  width: 22px;
  height: 22px;
  border-radius: 4px;
  border: 1px solid rgba(31, 111, 235, 0.5);
  background: rgba(31, 111, 235, 0.9);
  color: #fff;
  font-size: 14px;
  font-weight: 700;
  line-height: 1;
  cursor: pointer;
  box-shadow: 0 2px 6px rgba(0, 0, 0, 0.35);
}
.new-log-pill {
  position: absolute;
  bottom: 36px;
  left: 50%;
  transform: translateX(-50%);
  z-index: 20;
  background: #1f6feb;
  color: #fff;
  border: none;
  border-radius: 12px;
  padding: 6px 14px;
  font-size: 11px;
  font-weight: 500;
  cursor: pointer;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.4);
}
.status-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-height: 28px;
  padding: 4px 10px;
  background: rgba(255, 255, 255, 0.025);
  border-top: 1px solid rgba(139, 148, 158, 0.16);
  font-size: 10px;
  color: var(--text-tertiary);
  flex-shrink: 0;
}
.status-badges { display: flex; gap: 8px; }
.badge { font-size: 10px; padding: 2px 7px; border-radius: 5px; }
.badge.error { color: #f85149; background: rgba(248, 81, 73, 0.1); }
.badge.warn { color: #d29922; background: rgba(210, 153, 34, 0.1); }
.fade-enter-active, .fade-leave-active { transition: opacity 0.2s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }
.history-loading,
.history-end {
  text-align: center;
  padding: 6px 0;
  font-size: 10px;
  color: var(--text-tertiary);
}
</style>
