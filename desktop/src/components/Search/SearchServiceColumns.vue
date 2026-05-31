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
import { computed, nextTick, onBeforeUnmount, ref, watch, type ComponentPublicInstance } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import { buildSearchBuckets, type SearchBucketRow } from '@/lib/searchBuckets'
import { splitSearchHighlight } from '@/lib/searchHighlight'
import type { LogContextPageDirection, LogEntry } from '@/api/agent'

const props = defineProps<{ tabId: string }>()

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const localTab = computed(() => workspace.searchTab(props.tabId))
const tab = computed(() => localTab.value ?? null)
const columnsEl = ref<HTMLElement | null>(null)
const selectedFromColumnsScrollId = ref<number | null>(null)
const suppressScrollSelectionId = ref<number | null>(null)
const pinnedScrollTopByService = ref<Record<string, number>>({})
const pinnedBucketsByService = ref<Record<string, SearchBucketRow[]>>({})
const pinnedBucketServiceIdsByService = ref<Record<string, string[]>>({})
const frozenColumnWidthByService = ref<Record<string, number>>({})
const lastScrollTop = ref(0)
const pinnedBodyEls = new Map<string, HTMLElement>()
let suppressScrollSelectionTimer: ReturnType<typeof window.setTimeout> | null = null
const EDGE_LOAD_THRESHOLD = 80
const DEFAULT_COLUMN_WIDTH = 300

type ScrollDirection = 'up' | 'down' | 'none'

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

const visibleBuckets = computed(() => {
  if (!tab.value) return []
  return buildSearchBuckets({
    serviceIds: visibleServiceIds.value,
    itemsByService: tab.value.contextByService,
  })
})

const buckets = computed(() => {
  if (!tab.value) return []
  return visibleBuckets.value
})

function columnTemplateFor(serviceIds: string[]): string {
  const columnCount = serviceIds.length
  const frozenWidths = serviceIds.map(serviceId => frozenColumnWidthByService.value[serviceId])
  if (frozenWidths.some(width => width > 0)) {
    return serviceIds
      .map((_, index) => `${Math.max(DEFAULT_COLUMN_WIDTH, Math.round(frozenWidths[index] || DEFAULT_COLUMN_WIDTH))}px`)
      .join(' ')
  }
  // 每个可见命中服务占一列；服务少时平分可用宽度，服务多时保留最小宽度并横向滚动。
  return columnCount > 0 ? `repeat(${columnCount}, minmax(${DEFAULT_COLUMN_WIDTH}px, 1fr))` : ''
}

function totalColumnWidth(serviceIds: string[]): number {
  return serviceIds.reduce((sum, serviceId) => {
    const width = frozenColumnWidthByService.value[serviceId]
    return sum + Math.max(DEFAULT_COLUMN_WIDTH, Math.round(width || DEFAULT_COLUMN_WIDTH))
  }, 0)
}

const columnTemplate = computed(() => columnTemplateFor(scrollingServiceIds.value))

const pinnedColumnTemplate = computed(() => columnTemplateFor(pinnedServiceIds.value))

const allServicesPinned = computed(() => pinnedServiceIds.value.length > 0 && scrollingServiceIds.value.length === 0)

const pinnedPanelStyle = computed(() => ({
  '--pinned-width': `${totalColumnWidth(pinnedServiceIds.value)}px`,
  gridTemplateColumns: pinnedColumnTemplate.value,
}))

function pinnedBuckets(serviceId: string): SearchBucketRow[] {
  return pinnedBucketsByService.value[serviceId] ?? visibleBuckets.value
}

function pinnedBucketServiceIds(serviceId: string): string[] {
  return pinnedBucketServiceIdsByService.value[serviceId] ?? [
    serviceId,
    ...visibleServiceIds.value.filter(id => id !== serviceId),
  ]
}

function setPinnedBodyRef(serviceId: string, el: Element | ComponentPublicInstance | null) {
  if (el instanceof HTMLElement) {
    pinnedBodyEls.set(serviceId, el)
  } else {
    pinnedBodyEls.delete(serviceId)
  }
}

function restorePinnedScroll(serviceId: string) {
  const el = pinnedBodyEls.get(serviceId)
  if (!el) return
  el.scrollTop = pinnedScrollTopByService.value[serviceId] ?? 0
}

function snapshotColumnWidths() {
  const next = { ...frozenColumnWidthByService.value }
  const preserveExistingWidths = pinnedServiceIds.value.length > 0
  const headers = columnsEl.value?.querySelectorAll<HTMLElement>('.columns-header .column-header[data-service-id]')
  headers?.forEach(header => {
    const serviceId = header.dataset.serviceId
    const width = header.getBoundingClientRect().width
    if (!serviceId || width <= 0) return
    if (preserveExistingWidths && next[serviceId] > 0) return
    next[serviceId] = Math.max(DEFAULT_COLUMN_WIDTH, Math.round(width))
  })
  for (const serviceId of visibleServiceIds.value) {
    next[serviceId] ??= DEFAULT_COLUMN_WIDTH
  }
  frozenColumnWidthByService.value = next
}

const canLoadBefore = computed(() => {
  if (!tab.value) return false
  return scrollingServiceIds.value.some(serviceId => tab.value!.hasMoreBeforeByService[serviceId] !== false)
})

const canLoadAfter = computed(() => {
  if (!tab.value) return false
  return scrollingServiceIds.value.some(serviceId => tab.value!.hasMoreAfterByService[serviceId] !== false)
})

// 列键语义为 deploymentId，反查所属 service 名，反查不到时显示 id。
function serviceName(deploymentId: string): string {
  return agentStore.serviceForDeployment(deploymentId)?.service.name ?? deploymentId
}

function entryKey(entry: LogEntry): string | number {
  return entry.id
}

function timeLabel(entry: LogEntry): string {
  return new Date(entry.timestamp).toISOString().slice(11, 23)
}

const searchQuery = computed(() => localTab.value?.query ?? '')

const messageParts = (message: string) => splitSearchHighlight(message, searchQuery.value)

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

async function togglePin(serviceId: string) {
  if (!tab.value) return
  if (tab.value.selectedLogId !== null) {
    suppressScrollSelection(tab.value.selectedLogId)
  }
  if (tab.value.pinnedServiceIds.includes(serviceId)) {
    workspace.unpinService(tab.value.id, serviceId)
    const { [serviceId]: _removed, ...next } = pinnedScrollTopByService.value
    pinnedScrollTopByService.value = next
    const { [serviceId]: _rows, ...nextBuckets } = pinnedBucketsByService.value
    pinnedBucketsByService.value = nextBuckets
    const { [serviceId]: _serviceIds, ...nextServiceIds } = pinnedBucketServiceIdsByService.value
    pinnedBucketServiceIdsByService.value = nextServiceIds
    pinnedBodyEls.delete(serviceId)
    if (tab.value.pinnedServiceIds.length === 0) {
      frozenColumnWidthByService.value = {}
    }
  } else {
    const scrollTop = columnsEl.value?.scrollTop ?? 0
    snapshotColumnWidths()
    // 固定栏要保留固定瞬间的完整跨服务时间栅格，否则单服务重建会压缩空白行并让内容漂移。
    pinnedBucketsByService.value = {
      ...pinnedBucketsByService.value,
      [serviceId]: visibleBuckets.value,
    }
    pinnedBucketServiceIdsByService.value = {
      ...pinnedBucketServiceIdsByService.value,
      [serviceId]: [
        serviceId,
        ...visibleServiceIds.value.filter(id => id !== serviceId),
      ],
    }
    pinnedScrollTopByService.value = {
      ...pinnedScrollTopByService.value,
      [serviceId]: scrollTop,
    }
    workspace.pinService(tab.value.id, serviceId)
    await nextTick()
    restorePinnedScroll(serviceId)
  }
}

async function loadMore(direction: LogContextPageDirection) {
  if (!tab.value || !localTab.value) return
  const el = columnsEl.value
  const previousHeight = el?.scrollHeight ?? 0
  const previousTop = el?.scrollTop ?? 0
  const changed = await workspace.loadMoreContext(localTab.value.id, direction)
  await nextTick()
  if (direction === 'before' && changed && el) {
    el.scrollTop = previousTop + el.scrollHeight - previousHeight
  }
}

function syncSelectedResultFromScroll(el: HTMLElement, direction: ScrollDirection) {
  const currentTab = tab.value
  if (!currentTab || !localTab.value) return
  const hidden = new Set(currentTab.hiddenServiceIds)
  const resultIds = new Set(
    currentTab.results
      .filter(entry => !hidden.has(entry.deployment_id))
      .map(entry => entry.id),
  )
  if (resultIds.size === 0) return

  const viewport = el.getBoundingClientRect()
  const viewportCenter = viewport.top + (viewport.bottom - viewport.top) / 2
  const visibleCandidates: Array<{ id: number; center: number; distance: number }> = []

  const entryEls = Array.from(el.querySelectorAll<HTMLElement>('.context-entry'))
  for (const entryEl of entryEls) {
    const entryId = Number(entryEl.dataset.entryId)
    if (!resultIds.has(entryId)) continue
    const rect = entryEl.getBoundingClientRect()
    if (rect.bottom < viewport.top || rect.top > viewport.bottom) continue
    const entryCenter = rect.top + (rect.bottom - rect.top) / 2
    visibleCandidates.push({
      id: entryId,
      center: entryCenter,
      distance: Math.abs(entryCenter - viewportCenter),
    })
  }

  if (visibleCandidates.length === 0) return
  const currentCandidate = visibleCandidates.find(item => item.id === currentTab.selectedLogId)
  let candidate: { id: number; center: number; distance: number } | null = null
  if (direction === 'down' && currentTab.selectedLogId !== null) {
    const below = currentCandidate
      ? visibleCandidates.filter(item => item.center > currentCandidate.center)
      : visibleCandidates
    if (below.length > 0) {
      candidate = below.reduce((last, item) => (item.center > last.center ? item : last), below[0])
    }
    if (!candidate && currentCandidate) return
  } else if (direction === 'up' && currentTab.selectedLogId !== null) {
    const above = currentCandidate
      ? visibleCandidates.filter(item => item.center < currentCandidate.center)
      : visibleCandidates
    if (above.length > 0) {
      candidate = above.reduce((first, item) => (item.center < first.center ? item : first), above[0])
    }
    if (!candidate && currentCandidate) return
  }
  candidate ??= visibleCandidates.reduce(
    (nearest, item) => (item.distance < nearest.distance ? item : nearest),
    visibleCandidates[0],
  )
  if (!candidate) return
  if (workspace.selectSearchResult(currentTab.id, candidate.id)) {
    selectedFromColumnsScrollId.value = candidate.id
  }
}

function handleScroll(event: Event) {
  const el = event.currentTarget as HTMLElement
  const direction: ScrollDirection =
    el.scrollTop > lastScrollTop.value ? 'down' : el.scrollTop < lastScrollTop.value ? 'up' : 'none'
  lastScrollTop.value = el.scrollTop
  if (suppressScrollSelectionId.value === null) {
    syncSelectedResultFromScroll(el, direction)
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
  <div
    v-if="tab?.contextAnchorTime"
    class="columns-shell"
    :class="{ 'all-pinned': allServicesPinned }"
  >
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
        <div class="column-header pinned" :data-service-id="serviceId">
          <div class="header-main">
            <span class="service-name">{{ serviceName(serviceId) }}</span>
          </div>
          <button class="pin-btn" @click="togglePin(serviceId)">已固定</button>
        </div>
        <div class="pinned-body" :ref="el => setPinnedBodyRef(serviceId, el)">
          <div class="pinned-grid">
            <div
              v-for="bucket in pinnedBuckets(serviceId)"
              :key="bucket.bucketStart"
              class="bucket-row pinned-row"
              :style="{ gridTemplateColumns: 'minmax(300px, 1fr)' }"
            >
              <div
                class="bucket-cell"
                :class="{ blank: isBlank(bucket, serviceId) }"
                :data-service-id="serviceId"
              >
                <div class="pinned-cell-layers">
                  <div
                    v-for="cellServiceId in pinnedBucketServiceIds(serviceId)"
                    :key="cellServiceId"
                    class="pinned-cell-layer"
                    :class="{ 'height-mirror': cellServiceId !== serviceId }"
                    :data-service-id="cellServiceId"
                    :aria-hidden="cellServiceId !== serviceId ? 'true' : undefined"
                  >
                    <div class="bucket-time">{{ bucket.bucketLabel }}</div>
                    <div v-if="isBlank(bucket, cellServiceId)" class="blank-cell" />
                    <div v-else class="entry-stack">
                      <div
                        v-for="entry in cellEntries(bucket, cellServiceId)"
                        :key="entryKey(entry)"
                        class="context-entry"
                        :class="{ target: entry.id === tab.selectedLogId }"
                        :data-entry-id="entry.id"
                        :data-entry-key="entryKey(entry)"
                      >
                        <span class="entry-time">{{ timeLabel(entry) }}</span>
                        <span class="entry-level">{{ entry.level }}</span>
                        <span class="entry-message">
                          <template v-for="(part, index) in messageParts(entry.message)" :key="index">
                            <mark v-if="part.match" data-test="search-keyword-highlight">{{ part.text }}</mark>
                            <span v-else>{{ part.text }}</span>
                          </template>
                        </span>
                      </div>
                    </div>
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
            :data-service-id="serviceId"
          >
            <div class="header-main">
              <span class="service-name">{{ serviceName(serviceId) }}</span>
            </div>
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
            :data-service-id="serviceId"
          >
            <div class="scroll-cell-layers">
              <div
                class="scroll-cell-layer"
                :data-service-id="serviceId"
              >
                <div class="bucket-time">{{ bucket.bucketLabel }}</div>
                <div v-if="isBlank(bucket, serviceId)" class="blank-cell" />
                <div v-else class="entry-stack">
                  <div
                    v-for="entry in cellEntries(bucket, serviceId)"
                    :key="entryKey(entry)"
                    class="context-entry"
                    :class="{ target: entry.id === tab.selectedLogId }"
                    :data-entry-id="entry.id"
                    :data-entry-key="entryKey(entry)"
                  >
                    <span class="entry-time">{{ timeLabel(entry) }}</span>
                    <span class="entry-level">{{ entry.level }}</span>
                    <span class="entry-message">
                      <template v-for="(part, index) in messageParts(entry.message)" :key="index">
                        <mark v-if="part.match" data-test="search-keyword-highlight">{{ part.text }}</mark>
                        <span v-else>{{ part.text }}</span>
                      </template>
                    </span>
                  </div>
                </div>
              </div>
              <div
                v-for="mirrorServiceId in pinnedServiceIds"
                :key="mirrorServiceId"
                class="scroll-cell-layer height-mirror"
                :data-service-id="mirrorServiceId"
                aria-hidden="true"
              >
                <div class="bucket-time">{{ bucket.bucketLabel }}</div>
                <div v-if="isBlank(bucket, mirrorServiceId)" class="blank-cell" />
                <div v-else class="entry-stack">
                  <div
                    v-for="entry in cellEntries(bucket, mirrorServiceId)"
                    :key="entryKey(entry)"
                    class="context-entry"
                    :class="{ target: entry.id === tab.selectedLogId }"
                    :data-entry-id="entry.id"
                    :data-entry-key="entryKey(entry)"
                  >
                    <span class="entry-time">{{ timeLabel(entry) }}</span>
                    <span class="entry-level">{{ entry.level }}</span>
                    <span class="entry-message">
                      <template v-for="(part, index) in messageParts(entry.message)" :key="index">
                        <mark v-if="part.match" data-test="search-keyword-highlight">{{ part.text }}</mark>
                        <span v-else>{{ part.text }}</span>
                      </template>
                    </span>
                  </div>
                </div>
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
.columns-shell.all-pinned .pinned-columns {
  flex: 1 1 100%;
  width: 100%;
  min-width: 0;
  max-width: none;
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
  overflow-y: auto;
  overflow-x: hidden;
  scrollbar-width: none;
}
.pinned-body::-webkit-scrollbar {
  display: none;
}
.pinned-grid {
  min-width: 300px;
}
.pinned-row {
  display: grid;
}
.column-header.pinned {
  background: rgba(88, 166, 255, 0.08);
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
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 4px 8px;
  min-height: 32px;
  padding: 4px 8px;
  border-right: 1px solid var(--border-secondary);
}
.header-main {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
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
.pinned-cell-layers,
.scroll-cell-layers {
  display: grid;
}
.pinned-cell-layer,
.scroll-cell-layer {
  grid-area: 1 / 1;
  min-width: 0;
}
.pinned-cell-layer.height-mirror,
.scroll-cell-layer.height-mirror {
  visibility: hidden;
  pointer-events: none;
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
.entry-message mark {
  border-radius: 2px;
  background: rgba(255, 212, 0, 0.32);
  color: var(--text-primary);
  padding: 0 1px;
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
