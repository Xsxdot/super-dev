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
import { useAppI18n } from '@/i18n/useAppI18n'
import { splitSearchHighlight } from '@/lib/searchHighlight'

const props = defineProps<{ tabId: string }>()

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const { t } = useAppI18n()
const tab = computed(() => workspace.searchTab(props.tabId))
const timelineEl = ref<HTMLElement | null>(null)
const EDGE_LOAD_THRESHOLD = 80

const visibleResults = computed(() => {
  if (!tab.value) return []
  const hidden = new Set(tab.value.hiddenServiceIds)
  return tab.value.results.filter(entry => !hidden.has(entry.deployment_id))
})

function timeLabel(timestamp: string): string {
  return new Date(timestamp).toISOString().slice(11, 23)
}

// deploymentId 反查所属 service 名，反查不到时直接显示 id。
function serviceName(deploymentId: string): string {
  return agentStore.serviceForDeployment(deploymentId)?.service.name ?? deploymentId
}

function levelClass(level: string): string {
  if (level === 'ERROR') return 'error'
  if (level === 'WARN') return 'warn'
  if (level === 'DEBUG') return 'debug'
  return 'info'
}

function messageParts(message: string) {
  return splitSearchHighlight(message, tab.value?.query ?? '')
}

function select(entryId: string) {
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
      data-test="search-hit-row"
      :data-entry-id="entry.id"
      @click="select(entry.id)"
    >
      <span class="time">{{ timeLabel(entry.timestamp) }}</span>
      <span class="service" data-test="search-hit-service">{{ serviceName(entry.deployment_id) }}</span>
      <span class="level" :class="levelClass(entry.level)" data-test="search-hit-level">{{ entry.level }}</span>
      <span class="message">
        <template v-for="(part, index) in messageParts(entry.message)" :key="index">
          <mark v-if="part.match" data-test="search-keyword-highlight">{{ part.text }}</mark>
          <span v-else>{{ part.text }}</span>
        </template>
      </span>
    </button>
    <button
      v-if="tab && workspace.canLoadMoreSearchResults(tab.id)"
      class="load-more"
      :disabled="tab.loadingMoreResults"
      @click="workspace.loadMoreSearchResults(tab.id)"
    >
      {{ tab.loadingMoreResults ? t('common.loading') : t('search.loadMore') }}
    </button>
  </div>
</template>

<style scoped>
.timeline {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 7px;
}
.timeline-row {
  display: grid;
  grid-template-columns: 82px minmax(88px, 118px) 48px minmax(0, 1fr);
  gap: 7px;
  align-items: center;
  width: 100%;
  border: 1px solid transparent;
  border-left: 2px solid transparent;
  border-radius: 6px;
  background: transparent;
  color: var(--text-secondary);
  padding: 5px 6px 5px 5px;
  font-size: 11px;
  text-align: left;
  cursor: pointer;
}
.timeline-row:hover {
  border-color: var(--border-secondary);
  background: rgba(255, 255, 255, 0.035);
}
.timeline-row.selected {
  border-color: rgba(88, 166, 255, 0.35);
  border-left-color: #58a6ff;
  background: rgba(88, 166, 255, 0.14);
}
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
.service {
  color: #58a6ff;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 650;
}
.level {
  justify-self: start;
  min-width: 38px;
  border-radius: 999px;
  padding: 1px 6px;
  background: rgba(88, 166, 255, 0.09);
  color: #79c0ff;
  font-size: 10px;
  font-weight: 700;
  text-align: center;
}
.level.warn {
  background: rgba(242, 204, 96, 0.12);
  color: #f2cc60;
}
.level.error {
  background: rgba(248, 81, 73, 0.13);
  color: #ff7b72;
}
.level.debug {
  background: rgba(139, 148, 158, 0.10);
  color: var(--text-tertiary);
}
.message {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-secondary);
}
mark {
  border-radius: 3px;
  background: rgba(242, 204, 96, 0.26);
  color: #ffe08a;
  padding: 0 2px;
}
</style>
