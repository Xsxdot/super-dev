<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { useLogStore } from '@/stores/log'
import { useFilterStore } from '@/stores/filter'
import { useBookmarkStore } from '@/stores/bookmark'
import { useAgentStore } from '@/stores/agent'
import PanelToolbar from './PanelToolbar.vue'
import LogRow from './LogRow.vue'
import BookmarkMarkerRow from './BookmarkMarkerRow.vue'
import type { DisplayLogEntry } from '@/lib/logEngine'
import type { Bookmark } from '@/stores/bookmark'

const props = defineProps<{
  panelId: string
  serviceId: string | null
  projectId: string | null
}>()

const logStore = useLogStore()
const filterStore = useFilterStore()
const bookmarkStore = useBookmarkStore()
const agentStore = useAgentStore()

const toolbarRef = ref<InstanceType<typeof PanelToolbar> | null>(null)
const viewingRunId = ref<string | null>(null)
const isFollowing = ref(true)
const newLogCount = ref(0)
const logListEl = ref<HTMLElement | null>(null)

const activeSelectionEntryId = ref<number | null>(null)
const activeSelectionText = ref<string | null>(null)
const activeSelectionRect = ref<DOMRect | null>(null)

const markerStartId = ref('')
const markerEndId = ref('')
const bookmarkCapturedIds = new Set<number>()

type LogDisplayItem =
  | { kind: 'entry'; log: DisplayLogEntry }
  | { kind: 'markerStart'; id: string; date: Date }
  | { kind: 'markerEnd'; id: string; date: Date }

interface DisplayStats {
  total: number
  folded: number
  errors: number
  warns: number
}

const cachedDisplay = ref<{ items: LogDisplayItem[]; stats: DisplayStats }>({
  items: [],
  stats: { total: 0, folded: 0, errors: 0, warns: 0 },
})

let displayRefreshTimer: ReturnType<typeof setTimeout> | null = null
let scrollRetryTimer: ReturnType<typeof setTimeout> | null = null
let programmaticScroll = false

onMounted(() => {
  if (props.serviceId) logStore.subscribe(props.serviceId)
  if (props.projectId) filterStore.loadProjectRules(props.projectId)
  refreshDisplayImmediately()
  scrollToBottom()
})

onUnmounted(() => {
  if (props.serviceId) logStore.unsubscribe(props.serviceId)
  filterStore.removePanel(props.panelId)
  if (displayRefreshTimer) clearTimeout(displayRefreshTimer)
  cancelScrollRetries()
})

watch(() => props.serviceId, (newId, oldId) => {
  if (oldId) logStore.unsubscribe(oldId)
  if (newId) logStore.subscribe(newId)
  viewingRunId.value = null
  isFollowing.value = true
  refreshDisplayImmediately()
})

const rawLogs = computed<DisplayLogEntry[]>(() => {
  if (viewingRunId.value && props.serviceId) {
    return logStore.getHistoryLogs(props.serviceId)
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

function makeDisplayItems(logs: DisplayLogEntry[], bm: Bookmark | null): LogDisplayItem[] {
  const items: LogDisplayItem[] = []
  if (bm?.startTime) {
    const startTime = bm.startTime
    if (bm.state === 'done') {
      const endTime = bm.endTime ?? new Date()
      let locked = bm.lockedLogs
      if (locked.length === 0) {
        locked = logs.filter(l => {
          const t = new Date(l.timestamp)
          return t >= startTime && t <= endTime
        })
      }
      const lockedIds = new Set(locked.map(l => l.id))
      const before = logs.filter(
        l => new Date(l.timestamp) < startTime && !lockedIds.has(l.id),
      )
      const after = logs.filter(
        l => new Date(l.timestamp) > endTime && !lockedIds.has(l.id),
      )
      for (const log of before) items.push({ kind: 'entry', log })
      if (markerStartId.value) {
        items.push({ kind: 'markerStart', id: markerStartId.value, date: startTime })
      }
      for (const log of locked) items.push({ kind: 'entry', log })
      if (markerEndId.value) {
        items.push({ kind: 'markerEnd', id: markerEndId.value, date: endTime })
      }
      for (const log of after) items.push({ kind: 'entry', log })
    } else {
      const before = logs.filter(l => new Date(l.timestamp) < startTime)
      const after = logs.filter(l => new Date(l.timestamp) >= startTime)
      for (const log of before) items.push({ kind: 'entry', log })
      if ((after.length > 0 || bm.state === 'recording') && markerStartId.value) {
        items.push({ kind: 'markerStart', id: markerStartId.value, date: startTime })
      }
      for (const log of after) items.push({ kind: 'entry', log })
    }
  } else {
    for (const log of logs) items.push({ kind: 'entry', log })
  }
  return items
}

function computeStats(items: LogDisplayItem[]): DisplayStats {
  let folded = 0
  let errors = 0
  let warns = 0
  let total = 0
  for (const item of items) {
    if (item.kind !== 'entry') continue
    total++
    const e = item.log
    const rc = e.repeat_count ?? 1
    if (rc > 1) folded += rc - 1
    if (e.level === 'ERROR') errors++
    else if (e.level === 'WARN') warns++
  }
  return { total, folded, errors, warns }
}

function makeLogDisplay() {
  const logs = filteredLogs.value
  const bm = bookmarkStore.getBookmark(props.panelId)
  const items = makeDisplayItems(logs, bm)
  cachedDisplay.value = { items, stats: computeStats(items) }
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

watch(
  [filteredLogs, () => bookmark.value?.state],
  ([logs, state]) => {
    if (state !== 'recording' || !bookmark.value?.startTime) return
    const startTime = bookmark.value.startTime
    for (const log of logs) {
      if (new Date(log.timestamp) < startTime) continue
      if (bookmarkCapturedIds.has(log.id)) continue
      bookmarkCapturedIds.add(log.id)
      bookmarkStore.appendToBookmark(props.panelId, log)
    }
  },
)

function onEndBookmark() {
  bookmarkStore.endBookmark(props.panelId, filteredLogs.value)
}

function backfillLockedLogsIfNeeded() {
  bookmarkStore.finalizeLockedLogs(props.panelId, filteredLogs.value)
}

watch(() => logStore.logSourceRevision, () => scheduleDisplayRefresh())

watch(
  () => bookmark.value?.state,
  (state, prev) => {
    if (state === 'recording') {
      bookmarkCapturedIds.clear()
      markerStartId.value = crypto.randomUUID()
      markerEndId.value = crypto.randomUUID()
    }
    if (prev === 'recording' && state === 'done') {
      backfillLockedLogsIfNeeded()
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

watch(viewingRunId, () => {
  isFollowing.value = true
  refreshDisplayImmediately()
})

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
  if (lastEntry) lastEntry.scrollIntoView({ block: 'end' })
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
    return
  }
  isFollowing.value = true
  newLogCount.value = 0
  if (!wasFollowing) pinToBottomIfFollowing()
}

function onWheel(e: WheelEvent) {
  if (e.deltaY < 0) {
    isFollowing.value = false
    cancelScrollRetries()
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

const historyRunIds = computed(() =>
  props.serviceId ? logStore.getRunIds(props.serviceId) : [],
)

async function selectRun(runId: string | null) {
  viewingRunId.value = runId
  if (runId && props.serviceId) {
    await logStore.loadHistoryLogs(props.serviceId, runId)
  }
  isFollowing.value = false
  refreshDisplayImmediately()
  await nextTick()
  scrollToBottom()
}

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
      :history-run-ids="historyRunIds"
      :viewing-run-id="viewingRunId"
      @select-run="selectRun"
      @end-bookmark="onEndBookmark"
    />

    <div v-if="viewingRunId" class="history-banner">
      <span>🕐 查看历史记录 · {{ stats.total }} 条</span>
      <button @click="selectRun(null)">返回实时</button>
    </div>

    <div ref="logListEl" class="log-list" @scroll="onScroll" @wheel="onWheel">
      <template
        v-for="item in displayItems"
        :key="item.kind === 'entry' ? `e-${item.log.id}` : item.id"
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
        <LogRow
          v-else
          :log="item.log"
          :service-name="serviceNameFor(item.log)"
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
        {{ viewingRunId ? '历史' : '实时' }} · 显示 {{ stats.total }} 条
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
.history-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 4px 12px;
  background: rgba(210, 153, 34, 0.15);
  border-bottom: 1px solid var(--border-secondary);
  font-size: 12px;
  flex-shrink: 0;
}
.history-banner button {
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 4px;
  color: var(--text-secondary);
  font-size: 11px;
  padding: 2px 8px;
  cursor: pointer;
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
</style>
