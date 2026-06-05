<!--
搜索命中服务栏

职责：
  - 显示命中服务和命中数量
  - 控制当前搜索标签内的服务隐藏/显示

边界：
  - 不修改项目级过滤规则
  - 不负责加载上下文
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useAgentStore } from '@/stores/agent'
import { useWorkspaceStore } from '@/stores/workspace'
import { useAppI18n } from '@/i18n/useAppI18n'

const props = defineProps<{ tabId: string }>()

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const { t } = useAppI18n()
const tab = computed(() => workspace.searchTab(props.tabId))

const rows = computed(() => {
  if (!tab.value) return []
  return Object.entries(tab.value.serviceCounts)
    .sort((a, b) => b[1] - a[1])
    .map(([serviceId, count], index) => ({
      serviceId,
      count,
      name: serviceName(serviceId),
      color: serviceColor(index),
      hidden: tab.value!.hiddenServiceIds.includes(serviceId),
      levels: levelCounts(serviceId),
    }))
})

function serviceColor(index: number): string {
  const colors = ['#58a6ff', '#f2cc60', '#56d364', '#ff7b72', '#d2a8ff', '#79c0ff']
  return colors[index % colors.length]
}

function serviceName(serviceId: string): string {
  return agentStore.serviceForDeployment(serviceId)?.service.name
    ?? agentStore.serviceById(serviceId)?.name
    ?? serviceId
}

function levelCounts(serviceId: string): { info: number; warn: number; error: number } {
  const entries = tab.value?.results.filter(entry => entry.deployment_id === serviceId) ?? []
  return entries.reduce(
    (counts, entry) => {
      if (entry.level === 'ERROR') counts.error += 1
      else if (entry.level === 'WARN') counts.warn += 1
      else counts.info += 1
      return counts
    },
    { info: 0, warn: 0, error: 0 },
  )
}

function segmentWidth(row: { count: number; levels: { info: number; warn: number; error: number } }, key: 'info' | 'warn' | 'error'): string {
  const loadedTotal = row.levels.info + row.levels.warn + row.levels.error
  if (loadedTotal === 0) return key === 'info' ? '100%' : '0%'
  return `${Math.max(4, (row.levels[key] / loadedTotal) * 100)}%`
}

function toggle(serviceId: string, hidden: boolean) {
  if (!tab.value) return
  if (hidden) workspace.showService(tab.value.id, serviceId)
  else workspace.hideService(tab.value.id, serviceId)
}
</script>

<template>
  <div class="service-rail">
    <div class="rail-header">
      <div>
        <div class="rail-title">{{ t('search.servicesTitle') }}</div>
        <div class="rail-subtitle">{{ t('search.servicesMatched', { count: rows.length }) }}</div>
      </div>
    </div>
    <button
      v-for="row in rows"
      :key="row.serviceId"
      class="service-hit"
      :class="{ hidden: row.hidden }"
      data-test="matched-service-row"
      @click="toggle(row.serviceId, row.hidden)"
    >
      <span class="dot" :style="{ backgroundColor: row.color }" />
      <span class="name">{{ row.name }}</span>
      <span class="count">{{ row.count }}</span>
      <span class="distribution" data-test="service-distribution-bar" aria-hidden="true">
        <span class="segment info" :style="{ width: segmentWidth(row, 'info') }" />
        <span class="segment warn" :style="{ width: segmentWidth(row, 'warn') }" />
        <span class="segment error" :style="{ width: segmentWidth(row, 'error') }" />
      </span>
    </button>
  </div>
</template>

<style scoped>
.service-rail {
  max-height: 34%;
  min-height: 118px;
  overflow-y: auto;
  padding: 10px;
  border-bottom: 1px solid var(--border-secondary);
  background: linear-gradient(180deg, rgba(88, 166, 255, 0.035), transparent 70%);
}
.rail-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 8px;
}
.rail-title {
  color: var(--text-secondary);
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0;
}
.rail-subtitle {
  margin-top: 2px;
  color: var(--text-tertiary);
  font-size: 10px;
}
.service-hit {
  display: grid;
  grid-template-columns: 10px minmax(0, 1fr) auto;
  align-items: center;
  gap: 7px;
  width: 100%;
  border: 1px solid transparent;
  border-radius: 6px;
  background: rgba(255, 255, 255, 0.018);
  color: var(--text-secondary);
  padding: 7px 7px 6px;
  font-size: 11px;
  cursor: pointer;
  margin-bottom: 6px;
}
.service-hit:hover {
  border-color: var(--border-secondary);
  background: rgba(255, 255, 255, 0.04);
}
.service-hit.hidden { opacity: 0.45; text-decoration: line-through; }
.dot { width: 7px; height: 7px; border-radius: 999px; }
.name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-align: left;
  color: var(--text-primary);
  font-weight: 600;
}
.count { color: var(--text-tertiary); }
.distribution {
  grid-column: 2 / 4;
  display: flex;
  height: 3px;
  overflow: hidden;
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.06);
}
.segment {
  display: block;
  min-width: 0;
}
.segment.info { background: rgba(88, 166, 255, 0.72); }
.segment.warn { background: rgba(242, 204, 96, 0.82); }
.segment.error { background: rgba(248, 81, 73, 0.86); }
</style>
