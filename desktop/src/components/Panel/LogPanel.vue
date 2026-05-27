<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { useLogStore } from '@/stores/log'
import { useFilterStore } from '@/stores/filter'
import { useBookmarkStore } from '@/stores/bookmark'
import { useAgentStore } from '@/stores/agent'
import { useRemoteStore } from '@/stores/remote'
import { useRemoteLogStore } from '@/stores/remoteLog'
import { useDeploymentLogStore } from '@/stores/deploymentLog'
import { useWorkspaceStore } from '@/stores/workspace'
import PanelToolbar from './PanelToolbar.vue'
import LogRow from './LogRow.vue'
import BookmarkMarkerRow from './BookmarkMarkerRow.vue'
import LogHistorySeparatorRow from './LogHistorySeparatorRow.vue'
import { toDisplayEntry, type DisplayLogEntry } from '@/lib/logEngine'
import { tagColor } from '@/lib/tagColor'
import type { RemoteLogEntry } from '@/api/agent'
import type { PanelSource } from '@/stores/panel'
import {
  makeDisplayItems,
  computeDisplayStats,
  type LogDisplayItem,
  type DisplayStats,
} from '@/lib/logDisplay'

const props = defineProps<{
  panelId: string
  serviceId: string | null
  projectId: string | null
  logSourceId?: string | null
  logSourceIds?: string[] | null
  groupKey?: string | null
  source?: PanelSource | null
}>()

const logStore = useLogStore()
const filterStore = useFilterStore()
const bookmarkStore = useBookmarkStore()
const agentStore = useAgentStore()
const remote = useRemoteStore()
const remoteLogStore = useRemoteLogStore()
const deploymentLogStore = useDeploymentLogStore()
const workspace = useWorkspaceStore()

const toolbarRef = ref<InstanceType<typeof PanelToolbar> | null>(null)
const isFollowing = ref(true)
const newLogCount = ref(0)
const logListEl = ref<HTMLElement | null>(null)
const isLoadingHistory = ref(false)

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

const effectiveLogSourceIds = computed<string[]>(() => {
  if (props.logSourceIds && props.logSourceIds.length > 0) return props.logSourceIds
  if (props.logSourceId) return [props.logSourceId]
  return []
})

const isRemote = computed(() => effectiveLogSourceIds.value.length > 0 && !!props.groupKey)

onMounted(() => {
  if (props.source?.type === 'deployment') {
    deploymentLogStore.subscribe(props.source.deploymentId)
    void deploymentLogStore.loadMoreHistory(props.source.deploymentId)
  } else if (isRemote.value && props.groupKey) {
    for (const lsId of effectiveLogSourceIds.value) {
      void remoteLogStore.subscribe(lsId, props.groupKey)
    }
  } else if (props.serviceId) {
    void logStore.subscribe(props.serviceId)
  }
  if (props.projectId) void filterStore.loadProjectRules(props.projectId)
  refreshDisplayImmediately()
  scrollToBottom()
})

watch(
  () => props.projectId,
  (projectId, prev) => {
    if (projectId && projectId !== prev) void filterStore.loadProjectRules(projectId)
  },
)

onUnmounted(() => {
  if (props.source?.type === 'deployment') {
    deploymentLogStore.unsubscribe(props.source.deploymentId)
  } else if (isRemote.value && props.groupKey) {
    for (const lsId of effectiveLogSourceIds.value) {
      remoteLogStore.unsubscribe(lsId, props.groupKey)
    }
  } else if (props.serviceId) {
    logStore.unsubscribe(props.serviceId)
  }
  filterStore.removePanel(props.panelId)
  if (displayRefreshTimer) clearTimeout(displayRefreshTimer)
  cancelScrollRetries()
})

watch(() => props.serviceId, (newId, oldId) => {
  if (isRemote.value) return
  if (oldId) logStore.unsubscribe(oldId)
  if (newId) void logStore.subscribe(newId)
  isFollowing.value = true
  refreshDisplayImmediately()
})

watch(
  () => [effectiveLogSourceIds.value, props.groupKey] as const,
  ([newIds, newGroupKey], [oldIds, oldGroupKey]) => {
    if (oldGroupKey) {
      for (const lsId of (oldIds as string[])) {
        remoteLogStore.unsubscribe(lsId, oldGroupKey)
      }
    }
    if (newGroupKey) {
      for (const lsId of (newIds as string[])) {
        void remoteLogStore.subscribe(lsId, newGroupKey)
      }
    }
    isFollowing.value = true
    refreshDisplayImmediately()
  },
  { deep: true },
)

type RemoteDisplayLogEntry = DisplayLogEntry & { host_id?: string }

function toRemoteDisplayEntry(entry: RemoteLogEntry): RemoteDisplayLogEntry {
  return toDisplayEntry(entry) as RemoteDisplayLogEntry
}

const rawLogs = computed<DisplayLogEntry[]>(() => {
  if (props.source?.type === 'deployment') {
    return deploymentLogStore.getLogs(props.source.deploymentId)
  }
  if (isRemote.value && props.groupKey) {
    const allLogs = effectiveLogSourceIds.value.flatMap(lsId =>
      remoteLogStore.logsOf(lsId, props.groupKey!).map(entry => ({ logSourceId: lsId, entry }))
    )
    const seen = new Set<string>()
    const deduped = allLogs.filter(({ logSourceId, entry }) => {
      const key = `${logSourceId}:${entry.host_id}:${entry.id}`
      if (seen.has(key)) return false
      seen.add(key)
      return true
    }).map(({ entry }) => entry)
    deduped.sort((a, b) => {
      const t = new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime()
      return t !== 0 ? t : a.id - b.id
    })
    const allHostIds = [...new Set(deduped.map(entry => entry.host_id))]
    const visibleHostIds = new Set(workspace.visibleRemoteHostIds(props.panelId, allHostIds))
    return deduped
      .filter(entry => visibleHostIds.has(entry.host_id))
      .map(toRemoteDisplayEntry)
  }
  if (props.serviceId) return logStore.getLogs(props.serviceId)
  if (props.projectId) {
    const proj = agentStore.projectById(props.projectId)
    if (!proj) return []
    return proj.services
      .flatMap((s: { id: string }) => logStore.getLogs(s.id))
      .sort((a: DisplayLogEntry, b: DisplayLogEntry) => a.id - b.id)
  }
  return []
})

const filteredLogs = computed(() =>
  filterStore.applyFilters(props.panelId, props.projectId, rawLogs.value),
)

const historyBoundary = computed(() =>
  props.serviceId && !isRemote.value ? logStore.getHistoryBoundary(props.serviceId) : null,
)

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
  }, historyBoundary.value)
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
  if (props.source !== undefined) return props.source
  if (props.serviceId && props.projectId) {
    return { type: 'local-service', serviceId: props.serviceId, projectId: props.projectId }
  }
  if (props.projectId) return { type: 'local-project', projectId: props.projectId }
  if (props.groupKey && props.logSourceIds?.length) {
    return { type: 'remote-aggregate', logSourceIds: props.logSourceIds, groupKey: props.groupKey }
  }
  if (props.groupKey && props.logSourceId) {
    return { type: 'remote-log-source', logSourceId: props.logSourceId, groupKey: props.groupKey }
  }
  return null
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

function serviceNameFor(log: DisplayLogEntry): string {
  const svc = agentStore.serviceById(log.service_id)
  return svc?.name ?? log.service_id.slice(0, 12)
}

function hostMetaFor(log: DisplayLogEntry): { name: string; color: string } | null {
  if (!isRemote.value) return null
  const hostId = (log as RemoteDisplayLogEntry).host_id
  if (!hostId) return null
  const host = remote.hostById(hostId)
  if (!host) return { name: hostId, color: 'var(--text-tertiary)' }
  const tag = host.tags[0]
  return {
    name: host.name,
    color: tag ? tagColor(tag) : 'var(--text-tertiary)',
  }
}

function scopeServiceIds(): string[] {
  if (isRemote.value) return []
  if (props.serviceId) return [props.serviceId]
  if (!props.projectId) return []
  const project = agentStore.projectById(props.projectId)
  return project?.services.map(s => s.id) ?? []
}

function closeActiveFoldsForScope() {
  for (const serviceId of scopeServiceIds()) {
    logStore.closeActiveFoldForService(serviceId)
  }
}

watch(
  [filteredLogs, () => bookmark.value?.state, () => logStore.logSourceRevision],
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
  [() => logStore.logSourceRevision, () => remoteLogStore.logSourceRevision, () => deploymentLogStore.logSourceRevision],
  () => scheduleDisplayRefresh(),
  { deep: true },
)

watch(
  () => workspace.remoteHiddenHostIdsByTab[props.panelId]?.join('|'),
  () => refreshDisplayImmediately(),
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

async function scrollToBottom() {
  programmaticScroll = true
  await nextTick()
  const el = logListEl.value
  if (!el) {
    programmaticScroll = false
    return
  }
  el.scrollTop = el.scrollHeight
  const lastEntry = el.querySelector('[data-log-id]:last-of-type')
  if (lastEntry && 'scrollIntoView' in lastEntry) {
    lastEntry.scrollIntoView({ block: 'end' })
  }
  requestAnimationFrame(() => {
    programmaticScroll = false
  })
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
  // 滚动到顶部附近时加载更早历史
  if (el.scrollTop < 80) {
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
    const el = logListEl.value
    const prevScrollHeight = el?.scrollHeight ?? 0
    await deploymentLogStore.loadMoreHistory(props.source.deploymentId)
    await nextTick()
    if (el) {
      const added = el.scrollHeight - prevScrollHeight
      if (added > 0) {
        programmaticScroll = true
        el.scrollTop += added
        requestAnimationFrame(() => { programmaticScroll = false })
      }
    }
    isLoadingHistory.value = false
    return
  }
  if (isRemote.value && props.groupKey) {
    if (isLoadingHistory.value) return
    isLoadingHistory.value = true
    const el = logListEl.value
    const prevScrollHeight = el?.scrollHeight ?? 0
    await Promise.all(
      effectiveLogSourceIds.value.map(lsId =>
        remoteLogStore.loadHistory(lsId, props.groupKey!)
      )
    )
    await nextTick()
    if (el) {
      const added = el.scrollHeight - prevScrollHeight
      if (added > 0) {
        programmaticScroll = true
        el.scrollTop += added
        requestAnimationFrame(() => { programmaticScroll = false })
      }
    }
    isLoadingHistory.value = false
    return
  }
  if (!props.serviceId) return
  if (!logStore.hasMoreHistory(props.serviceId)) return
  if (isLoadingHistory.value) return
  isLoadingHistory.value = true
  const el = logListEl.value
  // 记住加载前的 scrollHeight，加载完后补偿滚动位置
  const prevScrollHeight = el?.scrollHeight ?? 0
  await logStore.loadMoreHistory(props.serviceId)
  await nextTick()
  if (el) {
    const added = el.scrollHeight - prevScrollHeight
    if (added > 0) {
      programmaticScroll = true
      el.scrollTop += added
      requestAnimationFrame(() => { programmaticScroll = false })
    }
  }
  isLoadingHistory.value = false
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
</script>

<template>
  <div class="log-panel">
    <PanelToolbar
      ref="toolbarRef"
      :panel-id="panelId"
      :service-id="serviceId"
      :project-id="projectId"
      :log-source-id="logSourceId"
      :group-key="groupKey"
      @end-bookmark="onEndBookmark"
    />
    <div ref="logListEl" class="log-list" @scroll="onScroll" @wheel="onWheel">
      <div v-if="(serviceId || isRemote || source?.type === 'deployment') && isLoadingHistory" class="history-loading">加载历史记录中…</div>
      <div v-else-if="(serviceId && !logStore.hasMoreHistory(serviceId)) || (source?.type === 'deployment' && !deploymentLogStore.hasMoreHistory(source.deploymentId))" class="history-end">— 已到最早记录 —</div>
      <template
        v-for="item in displayItems"
        :key="item.id"
      >
        <BookmarkMarkerRow
          v-if="item.kind === 'markerStart'"
          :is-start="true"
          :date="item.date"
        />
        <BookmarkMarkerRow
          v-else-if="item.kind === 'markerEnd'"
          :is-start="false"
          :date="item.date"
        />
        <LogHistorySeparatorRow
          v-else-if="item.kind === 'historySeparator'"
        />
        <LogRow
          v-else-if="item.kind === 'entry'"
          :log="item.log"
          :service-name="serviceNameFor(item.log)"
          :host-name="hostMetaFor(item.log)?.name"
          :host-color="hostMetaFor(item.log)?.color"
          :highlighted="isHighlighted(item.log)"
          @selection-change="(t, r) => onLogSelection(item.log.id, t, r)"
        />
      </template>

      <button
        v-if="activeSelectionText && activeSelectionRect"
        class="selection-add-btn"
        :style="selectionButtonStyle"
        title="填入过滤关键词"
        @mousedown.prevent
        @click="fillChipFromSelection"
      >
        +
      </button>
    </div>

    <Transition name="fade">
      <button v-if="!isFollowing && newLogCount > 0" class="new-log-pill" @click="jumpToBottom">
        ↓ {{ newLogCount }} 条新日志
      </button>
    </Transition>

    <div class="status-bar">
      <span>
        实时 · 显示 {{ stats.total }} 条
        <template v-if="stats.folded > 0"> · 折叠 {{ stats.folded }} 条</template>
      </span>
      <div class="status-badges">
        <span v-if="stats.errors > 0" class="badge error">● {{ stats.errors }} 错误</span>
        <span v-if="stats.warns > 0" class="badge warn">● {{ stats.warns }} 警告</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.log-panel {
  display: flex;
  flex-direction: column;
  flex: 1;
  overflow: hidden;
  position: relative;
}
.log-list {
  flex: 1;
  overflow-y: auto;
  background: var(--bg-primary);
  padding: 4px 0;
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
  padding: 2px 10px;
  background: var(--bg-elevated);
  border-top: 1px solid var(--border-secondary);
  font-size: 10px;
  color: var(--text-tertiary);
  flex-shrink: 0;
}
.status-badges { display: flex; gap: 8px; }
.badge { font-size: 9px; padding: 1px 6px; border-radius: 3px; }
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
