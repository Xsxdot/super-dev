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
import { computed } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'

const props = defineProps<{ tabId: string }>()

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const tab = computed(() => workspace.searchTab(props.tabId))

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
</script>

<template>
  <div class="timeline">
    <button
      v-for="entry in visibleResults"
      :key="entry.id"
      class="timeline-row"
      :class="{ selected: tab?.selectedLogId === entry.id }"
      @click="select(entry.id)"
    >
      <span class="time">{{ timeLabel(entry.timestamp) }}</span>
      <span class="service">{{ serviceName(entry.service_id) }}</span>
      <span class="message">{{ entry.message }}</span>
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
.time { color: var(--text-tertiary); font-variant-numeric: tabular-nums; }
.service { color: #58a6ff; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.message { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
