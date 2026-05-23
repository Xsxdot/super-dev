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

const props = defineProps<{ tabId: string }>()

const agentStore = useAgentStore()
const workspace = useWorkspaceStore()
const localTab = computed(() => workspace.searchTab(props.tabId))
const remoteTab = computed(() => workspace.remoteSearchTab(props.tabId))
const tab = computed(() => localTab.value ?? remoteTab.value)

const rows = computed(() => {
  if (!tab.value) return []
  const serviceCounts = localTab.value
    ? localTab.value.serviceCounts
    : Object.fromEntries(remoteTab.value?.serviceColumns.map(column => [column.service_id, column.result_count]) ?? [])
  return Object.entries(serviceCounts)
    .sort((a, b) => b[1] - a[1])
    .map(([serviceId, count], index) => ({
      serviceId,
      count,
      service: agentStore.serviceById(serviceId),
      serviceName: remoteTab.value?.serviceColumns.find(column => column.service_id === serviceId)?.service_name,
      color: serviceColor(index),
      hidden: tab.value!.hiddenServiceIds.includes(serviceId),
    }))
})

function serviceColor(index: number): string {
  const colors = ['#58a6ff', '#f2cc60', '#56d364', '#ff7b72', '#d2a8ff', '#79c0ff']
  return colors[index % colors.length]
}

function toggle(serviceId: string, hidden: boolean) {
  if (!tab.value) return
  if (hidden) workspace.showService(tab.value.id, serviceId)
  else workspace.hideService(tab.value.id, serviceId)
}
</script>

<template>
  <div class="service-rail">
    <div class="rail-title">命中服务</div>
    <button
      v-for="row in rows"
      :key="row.serviceId"
      class="service-hit"
      :class="{ hidden: row.hidden }"
      @click="toggle(row.serviceId, row.hidden)"
    >
      <span class="dot" :style="{ backgroundColor: row.color }" />
      <span class="name">{{ row.service?.name ?? row.serviceName ?? row.serviceId }}</span>
      <span class="count">{{ row.count }}</span>
    </button>
  </div>
</template>

<style scoped>
.service-rail {
  max-height: 33%;
  min-height: 96px;
  overflow-y: auto;
  padding: 8px;
  border-bottom: 1px solid var(--border-secondary);
}
.rail-title {
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 700;
  margin-bottom: 6px;
}
.service-hit {
  display: grid;
  grid-template-columns: 10px 1fr auto;
  align-items: center;
  gap: 6px;
  width: 100%;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--text-secondary);
  padding: 4px 5px;
  font-size: 11px;
  cursor: pointer;
}
.service-hit:hover { background: var(--bg-overlay); }
.service-hit.hidden { opacity: 0.45; text-decoration: line-through; }
.dot { width: 7px; height: 7px; border-radius: 999px; }
.name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; text-align: left; }
.count { color: var(--text-tertiary); }
</style>
