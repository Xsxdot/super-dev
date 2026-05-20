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
import { computed } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import { buildSearchBuckets, type SearchBucketRow } from '@/lib/searchBuckets'
import type { LogEntry } from '@/api/agent'

const props = defineProps<{ tabId: string }>()

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const tab = computed(() => workspace.searchTab(props.tabId))

const visibleServiceIds = computed(() => {
  if (!tab.value) return []
  return Object.keys(tab.value.serviceCounts).filter(
    serviceId => !tab.value!.hiddenServiceIds.includes(serviceId),
  )
})

const buckets = computed(() => {
  if (!tab.value) return []
  return buildSearchBuckets({
    serviceIds: visibleServiceIds.value,
    itemsByService: tab.value.contextByService,
  })
})

const columnTemplate = computed(() =>
  visibleServiceIds.value.map(() => 'minmax(300px, 360px)').join(' '),
)

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

function togglePin(serviceId: string) {
  if (!tab.value) return
  if (tab.value.pinnedServiceIds.includes(serviceId)) {
    workspace.unpinService(tab.value.id, serviceId)
  } else {
    workspace.pinService(tab.value.id, serviceId)
  }
}
</script>

<template>
  <div v-if="tab?.contextAnchorTime" class="columns">
    <div class="columns-grid">
      <div class="columns-header" :style="{ gridTemplateColumns: columnTemplate }">
        <div
          v-for="serviceId in visibleServiceIds"
          :key="serviceId"
          class="column-header"
        >
          <span class="service-name">{{ serviceName(serviceId) }}</span>
          <button class="pin-btn" @click="togglePin(serviceId)">
            {{ tab.pinnedServiceIds.includes(serviceId) ? '已固定' : '固定' }}
          </button>
        </div>
      </div>

      <div
        v-for="bucket in buckets"
        :key="bucket.bucketStart"
        class="bucket-row"
        :style="{ gridTemplateColumns: columnTemplate }"
      >
        <div
          v-for="serviceId in visibleServiceIds"
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
  <div v-else class="columns-empty">
    点击左侧命中日志查看跨服务上下文
  </div>
</template>

<style scoped>
.columns {
  height: 100%;
  min-width: 0;
  overflow: auto;
}
.columns-grid {
  min-width: max-content;
}
.columns-header {
  position: sticky;
  top: 0;
  z-index: 1;
  display: grid;
  background: var(--bg-elevated);
  border-bottom: 1px solid var(--border-secondary);
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
