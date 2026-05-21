<!--
搜索命中时间线

职责：
  - 展示匹配关键词的日志列表
  - 点击日志后请求跨服务上下文

边界：
  - 只展示命中日志，不展示上下文日志
  - 不负责右侧分栏渲染
-->
<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'

const props = defineProps<{ tabId: string }>()

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const tab = computed(() => workspace.searchTab(props.tabId))
const timelineEl = ref<HTMLElement | null>(null)
const EDGE_LOAD_THRESHOLD = 80

const visibleResults = computed(() => {
  if (!tab.value) return []
  const hidden = new Set(tab.value.hiddenServiceIds)
  return tab.value.results.filter(entry => !hidden.has(entry.service_id))
})

function timeLabel(timestamp: string): string {
  return new Date(timestamp).toISOString().slice(11, 23)
}

function serviceName(serviceId: string): string {
  return agentStore.serviceById(serviceId)?.name ?? serviceId
}

function select(entryId: number) {
  if (!tab.value) return
  void workspace.loadContext(tab.value.id, entryId)
}

function handleScroll(event: Event) {
  if (!tab.value) return
  const el = event.currentTarget as HTMLElement
  const distanceToBottom = el.scrollHeight - el.scrollTop - el.clientHeight
  if (distanceToBottom <= EDGE_LOAD_THRESHOLD) {
    void workspace.loadMoreSearchResults(tab.value.id)
  }
}

watch(
  () => tab.value?.selectedLogId,
  async selectedLogId => {
    if (!selectedLogId) return
    await nextTick()
    timelineEl.value
      ?.querySelector(`[data-entry-id="${selectedLogId}"]`)
      ?.scrollIntoView({ block: 'nearest' })
  },
)
</script>

<template>
  <div ref="timelineEl" class="timeline" @scroll="handleScroll">
    <button
      v-for="entry in visibleResults"
      :key="entry.id"
      class="timeline-row"
      :class="{ selected: tab?.selectedLogId === entry.id }"
      :data-entry-id="entry.id"
      @click="select(entry.id)"
    >
      <span class="time">{{ timeLabel(entry.timestamp) }}</span>
      <span class="service">{{ serviceName(entry.service_id) }}</span>
      <span class="message">{{ entry.message }}</span>
    </button>
    <button
      v-if="tab && workspace.canLoadMoreSearchResults(tab.id)"
      class="load-more"
      :disabled="tab.loadingMoreResults"
      @click="workspace.loadMoreSearchResults(tab.id)"
    >
      {{ tab.loadingMoreResults ? '加载中...' : '加载更多命中' }}
    </button>
  </div>
</template>

<style scoped>
.timeline {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 6px;
}
.timeline-row {
  display: grid;
  grid-template-columns: 78px 72px 1fr;
  gap: 6px;
  width: 100%;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--text-secondary);
  padding: 4px 5px;
  font-size: 11px;
  text-align: left;
  cursor: pointer;
}
.timeline-row:hover { background: var(--bg-overlay); }
.timeline-row.selected { background: rgba(88, 166, 255, 0.14); }
.load-more {
  width: 100%;
  height: 28px;
  border: none;
  background: transparent;
  color: var(--text-tertiary);
  font-size: 10px;
  cursor: pointer;
}
.load-more:hover:not(:disabled) {
  background: var(--bg-overlay);
  color: var(--text-secondary);
}
.load-more:disabled {
  cursor: default;
  opacity: 0.65;
}
.time { color: var(--text-tertiary); font-variant-numeric: tabular-nums; }
.service { color: #58a6ff; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.message { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
