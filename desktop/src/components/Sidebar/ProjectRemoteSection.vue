<!--
ProjectRemoteSection：项目下的远程监听子区块。

职责：
  - 展示绑定了该项目的远程监听任务，按服务聚合
  - 点击分组 emit open 事件，由 SidebarView 打开聚合面板

边界：
  - 不直接打开 tab，只 emit
  - 分组数据由 remote store 聚合计算
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useRemoteStore } from '@/stores/remote'
import type { PanelSource } from '@/stores/panel'

const props = defineProps<{ projectId: string }>()

const emit = defineEmits<{
  open: [payload: { projectId: string; serviceId: string; serviceName: string; logSourceIds: string[]; groupKey: string }]
}>()

const remote = useRemoteStore()
const serviceGroups = computed(() => remote.remoteServiceGroupsOf(props.projectId))

function sourceForGroup(serviceGroup: (typeof serviceGroups.value)[number], groupKey: string): PanelSource {
  return {
    type: 'remote-aggregate',
    projectId: props.projectId,
    serviceId: serviceGroup.serviceId,
    serviceName: serviceGroup.serviceName,
    logSourceIds: serviceGroup.logSourceIds,
    groupKey,
  }
}

function onGroupDragStart(e: DragEvent, serviceGroup: (typeof serviceGroups.value)[number], groupKey: string) {
  const source = sourceForGroup(serviceGroup, groupKey)
  e.dataTransfer?.setData('application/superdev-panel-source', JSON.stringify(source))
  e.dataTransfer?.setData('text/plain', JSON.stringify(source))
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'copy'
}
</script>

<template>
  <div v-if="serviceGroups.length > 0" class="project-remote-section">
    <div class="section-label">远程监听</div>
    <div v-for="sg in serviceGroups" :key="sg.serviceId" class="service-block">
      <div class="service-name">{{ sg.serviceName }}</div>
      <div
        v-for="group in sg.groups"
        :key="group.key"
        class="group-row"
        data-test="project-remote-group"
        draggable="true"
        @dragstart="onGroupDragStart($event, sg, group.key)"
        @click="emit('open', {
          projectId,
          serviceId: sg.serviceId,
          serviceName: sg.serviceName,
          logSourceIds: sg.logSourceIds,
          groupKey: group.key,
        })"
      >
        <span class="chip">{{ group.key }}</span>
        <span class="count">({{ group.hostIds.length }} 节点)</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.project-remote-section {
  border-top: 1px solid var(--border-secondary);
  padding: 4px 0 2px;
}
.section-label {
  padding: 3px 12px;
  color: var(--text-tertiary);
  font-size: 10px;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}
.service-block {
  margin-bottom: 2px;
}
.service-name {
  padding: 2px 12px;
  color: var(--text-secondary);
  font-size: 11px;
  font-weight: 600;
}
.group-row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 3px 20px;
  cursor: pointer;
  font-size: 11px;
}
.group-row:hover {
  background: var(--bg-secondary);
}
.chip {
  padding: 1px 6px;
  background: var(--bg-secondary);
  border-radius: 2px;
  font-size: 10px;
  color: var(--text-primary);
}
.count {
  color: var(--text-tertiary);
}
</style>
