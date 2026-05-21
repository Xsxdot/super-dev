<!--
搜索上下文服务分栏

职责：
  - 按服务列展示同一时间栅格的上下文日志
  - 支持服务列固定/取消固定
  - 高亮搜索目标日志

边界：
  - 不负责构造时间栅格，使用 searchBuckets 工具
  - 不执行上下文 API 请求
-->
<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import { buildSearchBuckets, type SearchBucketRow } from '@/lib/searchBuckets'
import type { LogContextPageDirection, LogEntry } from '@/api/agent'

const props = defineProps<{ tabId: string }>()

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const tab = computed(() => workspace.searchTab(props.tabId))
const columnsEl = ref<HTMLElement | null>(null)
const selectedFromColumnsScrollId = ref<number | null>(null)
const suppressScrollSelectionId = ref<number | null>(null)
const pinnedScrollTopByService = ref<Record<string, number>>({})
let suppressScrollSelectionTimer: ReturnType<typeof window.setTimeout> | null = null
const EDGE_LOAD_THRESHOLD = 80

const visibleServiceIds = computed(() => {
  if (!tab.value) return []
  return Object.keys(tab.value.serviceCounts).filter(
    serviceId => !tab.value!.hiddenServiceIds.includes(serviceId),
  )
})

const pinnedServiceIds = computed(() => {
  if (!tab.value) return []
  const pinned = new Set(tab.value.pinnedServiceIds)
  return visibleServiceIds.value.filter(serviceId => pinned.has(serviceId))
})

const scrollingServiceIds = computed(() => {
  if (!tab.value) return []
  const pinned = new Set(tab.value.pinnedServiceIds)
  return visibleServiceIds.value.filter(serviceId => !pinned.has(serviceId))
})

const buckets = computed(() => {
  if (!tab.value) return []
  return buildSearchBuckets({
    serviceIds: scrollingServiceIds.value,
    itemsByService: tab.value.contextByService,
  })
})

function columnTemplateFor(serviceIds: string[]): string {
  const columnCount = serviceIds.length
  // 每个可见命中服务占一列；服务少时平分可用宽度，服务多时保留最小宽度并横向滚动。
  return columnCount > 0 ? `repeat(${columnCount}, minmax(300px, 1fr))` : ''
}

const columnTemplate = computed(() => columnTemplateFor(scrollingServiceIds.value))

const pinnedColumnTemplate = computed(() => columnTemplateFor(pinnedServiceIds.value))

const pinnedPanelStyle = computed(() => ({
  '--pinned-width': `${pinnedServiceIds.value.length * 300}px`,
  gridTemplateColumns: pinnedColumnTemplate.value,
}))

function pinnedBuckets(serviceId: string): SearchBucketRow[] {
  if (!tab.value) return []
  return buildSearchBuckets({
    serviceIds: [serviceId],
    itemsByService: tab.value.contextByService,
  })
}

function pinnedOffsetStyle(serviceId: string) {
  const offset = pinnedScrollTopByService.value[serviceId] ?? 0
  return { transform: `translateY(-${offset}px)` }
}

const canLoadBefore = computed(() => {
  if (!tab.value) return false
  return scrollingServiceIds.value.some(serviceId => tab.value!.hasMoreBeforeByService[serviceId] !== false)
})

const canLoadAfter = computed(() => {
  if (!tab.value) return false
  return scrollingServiceIds.value.some(serviceId => tab.value!.hasMoreAfterByService[serviceId] !== false)
})

function serviceName(serviceId: string): string {
  return agentStore.serviceById(serviceId)?.name ?? serviceId
}

function timeLabel(entry: LogEntry): string {
  return new Date(entry.timestamp).toISOString().slice(11, 23)
}

function isBlank(bucket: SearchBucketRow, serviceId: string): boolean {
  return bucket.cells[serviceId]?.blank ?? true
}

function cellEntries(bucket: SearchBucketRow, serviceId: string): LogEntry[] {
  return bucket.cells[serviceId]?.entries ?? []
}

function suppressScrollSelection(logId: number) {
  suppressScrollSelectionId.value = logId
  if (suppressScrollSelectionTimer) {
    window.clearTimeout(suppressScrollSelectionTimer)
  }
  // 外部选中会触发 scrollIntoView，浏览器随后的 scroll 事件不应该反向改写选中项。
  suppressScrollSelectionTimer = window.setTimeout(() => {
    if (suppressScrollSelectionId.value === logId) {
      suppressScrollSelectionId.value = null
    }
    suppressScrollSelectionTimer = null
  }, 160)
}

function togglePin(serviceId: string) {
  if (!tab.value) return
  if (tab.value.pinnedServiceIds.includes(serviceId)) {
    workspace.unpinService(tab.value.id, serviceId)
    const { [serviceId]: _removed, ...next } = pinnedScrollTopByService.value
    pinnedScrollTopByService.value = next
  } else {
    pinnedScrollTopByService.value = {
      ...pinnedScrollTopByService.value,
      [serviceId]: columnsEl.value?.scrollTop ?? 0,
    }
    workspace.pinService(tab.value.id, serviceId)
  }
}

async function loadMore(direction: LogContextPageDirection) {
  if (!tab.value) return
  const el = columnsEl.value
  const previousHeight = el?.scrollHeight ?? 0
  const previousTop = el?.scrollTop ?? 0
  const changed = await workspace.loadMoreContext(tab.value.id, direction)
  await nextTick()
  if (direction === 'before' && changed && el) {
    el.scrollTop = previousTop + el.scrollHeight - previousHeight
  }
}

function syncSelectedResultFromScroll(el: HTMLElement) {
  const currentTab = tab.value
  if (!currentTab) return
  const hidden = new Set(currentTab.hiddenServiceIds)
  const resultIds = new Set(
    currentTab.results
      .filter(entry => !hidden.has(entry.service_id))
      .map(entry => entry.id),
  )
  if (resultIds.size === 0) return

  const viewport = el.getBoundingClientRect()
  const viewportCenter = viewport.top + (viewport.bottom - viewport.top) / 2
  let candidate: { id: number; distance: number } | null = null

  const entryEls = Array.from(el.querySelectorAll<HTMLElement>('.context-entry'))
  for (const entryEl of entryEls) {
    const entryId = Number(entryEl.dataset.entryId)
    if (!resultIds.has(entryId)) continue
    const rect = entryEl.getBoundingClientRect()
    if (rect.bottom < viewport.top || rect.top > viewport.bottom) continue
    const entryCenter = rect.top + (rect.bottom - rect.top) / 2
    const distance = Math.abs(entryCenter - viewportCenter)
    if (!candidate || distance < candidate.distance) {
      candidate = { id: entryId, distance }
    }
  }

  if (!candidate) return
  if (workspace.selectSearchResult(currentTab.id, candidate.id)) {
    selectedFromColumnsScrollId.value = candidate.id
  }
}

function handleScroll(event: Event) {
  const el = event.currentTarget as HTMLElement
  if (suppressScrollSelectionId.value === null) {
    syncSelectedResultFromScroll(el)
  }
  if (el.scrollTop <= EDGE_LOAD_THRESHOLD) {
    void loadMore('before')
    return
  }
  const distanceToBottom = el.scrollHeight - el.scrollTop - el.clientHeight
  if (distanceToBottom <= EDGE_LOAD_THRESHOLD) {
    void loadMore('after')
  }
}

watch(
  () => tab.value?.selectedLogId,
  async selectedLogId => {
    if (!selectedLogId) return
    if (selectedFromColumnsScrollId.value === selectedLogId) {
      selectedFromColumnsScrollId.value = null
      return
    }
    suppressScrollSelection(selectedLogId)
    await nextTick()
    columnsEl.value
      ?.querySelector(`[data-entry-id="${selectedLogId}"]`)
      ?.scrollIntoView({ block: 'center', inline: 'nearest' })
  },
)

onBeforeUnmount(() => {
  if (suppressScrollSelectionTimer) {
    window.clearTimeout(suppressScrollSelectionTimer)
  }
})
</script>

<template>
  <div v-if="tab?.contextAnchorTime" class="columns-shell">
    <div
      v-if="pinnedServiceIds.length"
      class="pinned-columns"
      :style="pinnedPanelStyle"
    >
      <div
        v-for="serviceId in pinnedServiceIds"
        :key="serviceId"
        class="pinned-column"
      >
        <div class="column-header pinned">
          <span class="service-name">{{ serviceName(serviceId) }}</span>
          <button class="pin-btn" @click="togglePin(serviceId)">已固定</button>
        </div>
        <div class="pinned-body">
          <div class="pinned-grid" :style="pinnedOffsetStyle(serviceId)">
            <div
              v-for="bucket in pinnedBuckets(serviceId)"
              :key="bucket.bucketStart"
              class="bucket-row pinned-row"
              :style="{ gridTemplateColumns: 'minmax(300px, 1fr)' }"
            >
              <div
                class="bucket-cell"
                :class="{ blank: isBlank(bucket, serviceId) }"
              >
                <div class="bucket-time">{{ bucket.bucketLabel }}</div>
                <div v-if="isBlank(bucket, serviceId)" class="blank-cell" />
                <div v-else class="entry-stack">
                  <div
                    v-for="entry in cellEntries(bucket, serviceId)"
                    :key="entry.id"
                    class="context-entry"
                    :class="{ target: entry.id === tab.selectedLogId }"
                    :data-entry-id="entry.id"
                  >
                    <span class="entry-time">{{ timeLabel(entry) }}</span>
                    <span class="entry-level">{{ entry.level }}</span>
                    <span class="entry-message">{{ entry.message }}</span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div
      v-if="scrollingServiceIds.length"
      ref="columnsEl"
      class="columns"
      @scroll="handleScroll"
    >
      <div class="columns-grid">
        <div class="columns-header" :style="{ gridTemplateColumns: columnTemplate }">
          <div
            v-for="serviceId in scrollingServiceIds"
            :key="serviceId"
            class="column-header"
          >
            <span class="service-name">{{ serviceName(serviceId) }}</span>
            <button class="pin-btn" @click="togglePin(serviceId)">固定</button>
          </div>
        </div>

        <button
          v-if="canLoadBefore"
          class="load-edge before"
          :disabled="tab.loadingMoreBefore"
          @click="loadMore('before')"
        >
          {{ tab.loadingMoreBefore ? '加载中...' : '加载更早' }}
        </button>

        <div
          v-for="bucket in buckets"
          :key="bucket.bucketStart"
          class="bucket-row"
          :style="{ gridTemplateColumns: columnTemplate }"
        >
          <div
            v-for="serviceId in scrollingServiceIds"
            :key="serviceId"
            class="bucket-cell"
            :class="{ blank: isBlank(bucket, serviceId) }"
          >
            <div class="bucket-time">{{ bucket.bucketLabel }}</div>
            <div v-if="isBlank(bucket, serviceId)" class="blank-cell" />
            <div v-else class="entry-stack">
              <div
                v-for="entry in cellEntries(bucket, serviceId)"
                :key="entry.id"
                class="context-entry"
                :class="{ target: entry.id === tab.selectedLogId }"
                :data-entry-id="entry.id"
              >
                <span class="entry-time">{{ timeLabel(entry) }}</span>
                <span class="entry-level">{{ entry.level }}</span>
                <span class="entry-message">{{ entry.message }}</span>
              </div>
            </div>
          </div>
        </div>

        <button
          v-if="canLoadAfter"
          class="load-edge after"
          :disabled="tab.loadingMoreAfter"
          @click="loadMore('after')"
        >
          {{ tab.loadingMoreAfter ? '加载中...' : '加载更新' }}
        </button>
      </div>
    </div>

    <div v-else class="columns pinned-only">
      <div class="columns-grid">
        <div class="columns-header" :style="{ gridTemplateColumns: 'minmax(300px, 1fr)' }">
          <div class="column-header placeholder">
            <span class="service-name">已固定全部服务</span>
          </div>
        </div>
        <div class="pinned-only-empty">取消固定后继续联动滚动</div>
      </div>
    </div>
  </div>
  <div v-else class="columns-empty">
    点击左侧命中日志查看跨服务上下文
  </div>
</template>

<style scoped>
.columns-shell {
  height: 100%;
  min-width: 0;
  display: flex;
  overflow: hidden;
}
.pinned-columns {
  flex: 0 0 min(var(--pinned-width), 55%);
  display: grid;
  min-width: min(var(--pinned-width), 55%);
  max-width: 55%;
  overflow-x: auto;
  overflow-y: hidden;
  background: var(--bg);
  border-right: 1px solid var(--border-secondary);
}
.pinned-column {
  min-width: 300px;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.pinned-body {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
.pinned-grid {
  min-width: 300px;
  will-change: transform;
}
.pinned-row {
  display: grid;
}
.pinned-only {
  background: var(--bg);
}
.pinned-only-empty {
  height: calc(100% - 32px);
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-tertiary);
  font-size: 12px;
}
.column-header.pinned {
  background: rgba(88, 166, 255, 0.08);
}
.column-header.placeholder {
  justify-content: center;
}
.columns {
  height: 100%;
  min-width: 0;
  flex: 1 1 auto;
  overflow: auto;
}
.columns-grid {
  min-width: 100%;
}
.columns-header {
  position: sticky;
  top: 0;
  z-index: 1;
  display: grid;
  background: var(--bg-elevated);
  border-bottom: 1px solid var(--border-secondary);
}
.load-edge {
  width: 100%;
  height: 28px;
  border: none;
  border-bottom: 1px solid var(--border-secondary);
  background: transparent;
  color: var(--text-tertiary);
  font-size: 10px;
  cursor: pointer;
}
.load-edge:hover:not(:disabled) {
  background: var(--bg-overlay);
  color: var(--text-secondary);
}
.load-edge:disabled {
  cursor: default;
  opacity: 0.65;
}
.column-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 32px;
  padding: 0 8px;
  border-right: 1px solid var(--border-secondary);
}
.service-name {
  color: var(--text-primary);
  font-size: 12px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pin-btn {
  border: 1px solid var(--border);
  border-radius: 4px;
  background: transparent;
  color: var(--text-secondary);
  font-size: 10px;
  padding: 2px 6px;
  cursor: pointer;
}
.bucket-row {
  display: grid;
  align-items: stretch;
}
.bucket-cell {
  min-height: 28px;
  border-right: 1px solid var(--border-secondary);
  border-bottom: 1px solid rgba(255, 255, 255, 0.04);
  padding: 3px 6px;
}
.bucket-cell.blank {
  background: rgba(255, 255, 255, 0.012);
}
.bucket-time {
  color: var(--text-tertiary);
  font-size: 9px;
  font-variant-numeric: tabular-nums;
  margin-bottom: 2px;
}
.blank-cell {
  min-height: 20px;
}
.entry-stack {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.context-entry {
  display: grid;
  grid-template-columns: 74px 48px minmax(0, 1fr);
  gap: 5px;
  border-radius: 3px;
  padding: 2px 4px;
  color: var(--text-secondary);
  font-size: 10px;
  line-height: 1.45;
}
.context-entry.target {
  background: rgba(88, 166, 255, 0.18);
  outline: 1px solid rgba(88, 166, 255, 0.35);
}
.entry-time,
.entry-level {
  color: var(--text-tertiary);
  font-variant-numeric: tabular-nums;
}
.entry-message {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.columns-empty {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-tertiary);
  font-size: 12px;
}
</style>
