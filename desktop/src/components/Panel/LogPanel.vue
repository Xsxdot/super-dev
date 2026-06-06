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
import { useRemoteStore } from '@/stores/remote'
import PanelToolbar from './PanelToolbar.vue'
import LogRow from './LogRow.vue'
import BookmarkMarkerRow from './BookmarkMarkerRow.vue'
import LogHistorySeparatorRow from './LogHistorySeparatorRow.vue'
import LogLifecycleSeparatorRow from './LogLifecycleSeparatorRow.vue'
import {
  buildDeploymentNodeStatus,
  logMatchesSelectedNodes,
  type DeploymentNodeIssueKind,
  type DeploymentNodeState,
} from '@/lib/deploymentNodeStatus'
import type { DisplayLogEntry } from '@/lib/logEngine'
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
const remoteStore = useRemoteStore()
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

const markerStartId = ref('')
const markerEndId = ref('')
const bookmarkCapturedIds = new Set<number>()

const cachedDisplay = ref<{ items: LogDisplayItem[]; stats: DisplayStats }>({
  items: [],
  stats: { total: 0, folded: 0, errors: 0, warns: 0 },
})

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

const currentNodeStatus = computed(() => {
  const dep = currentDeploymentInfo.value?.deployment
  if (!dep || dep.location !== 'remote') return null
  return buildDeploymentNodeStatus(dep, remoteStore.hosts, remoteStore.managedStatuses)
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

const ruleFilteredLogs = computed(() =>
  filterStore.applyFilters(props.panelId, props.projectId ?? null, rawLogs.value),
)

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
    applyItemsCountChange(oldCount, entryCount(cachedDisplay.value.items))
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
    applyItemsCountChange(oldCount, entryCount(cachedDisplay.value.items))
    if (isFollowing.value) pinToBottomIfFollowing()
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

// serviceNameFor 通过日志的 deployment_id 反查所属 service 名，反查不到时显示截断的 id。
function serviceNameFor(log: DisplayLogEntry): string {
  const info = agentStore.serviceForDeployment(log.deployment_id)
  return info?.service.name ?? log.deployment_id.slice(0, 12)
}

function closeActiveFoldsForScope() {
  if (props.source?.type === 'deployment') {
    deploymentLogStore.closeActiveFoldForDeployment(props.source.deploymentId)
  }
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
  closeActiveFoldsForScope()
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
  () => filterStore.getPanel(props.panelId).chips,
  () => refreshDisplayImmediately(),
  { deep: true },
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
    if (newCount > oldCount) pinToBottomIfFollowing()
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
  }
}

async function tryLoadMoreHistory() {
  if (props.source?.type === 'deployment') {
    if (!deploymentLogStore.hasMoreHistory(props.source.deploymentId)) return
    if (isLoadingHistory.value) return
    isLoadingHistory.value = true
    // 快速滚动时 range 可能短暂为空；按 0 补偿可以让重新测量后的窗口回到顶部附近。
    const prevStart = virtualizer.value.range?.startIndex ?? 0
    const prevCount = displayItems.value.length
    try {
      await deploymentLogStore.loadMoreHistory(props.source.deploymentId, INCREMENTAL_HISTORY_LIMIT)
      if (displayRefreshTimer) {
        clearTimeout(displayRefreshTimer)
        displayRefreshTimer = null
      }
      makeLogDisplay()
      await nextTick()
      const added = displayItems.value.length - prevCount
      if (added > 0) {
        programmaticScroll = true
        measureVirtualizer()
        virtualizer.value.scrollToIndex(prevStart + added, { align: 'start' })
        setTimeout(() => {
          measureVirtualizer()
          virtualizer.value.scrollToIndex(prevStart + added, { align: 'start' })
        }, 0)
        setTimeout(() => { programmaticScroll = false }, 80)
      }
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
              @selection-change="(t, r) => onLogSelection((displayItems[vRow.index] as any).log.id, t, r)"
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
    </div>

    <Transition name="fade">
      <button v-if="!isFollowing && newLogCount > 0" class="new-log-pill" @click="jumpToBottom">
        {{ t('panel.log.newLogs', { count: newLogCount }) }}
      </button>
    </Transition>

    <div class="status-bar" data-test="log-panel-status">
      <span>
        {{ t('panel.log.liveStats', { total: stats.total }) }}
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
