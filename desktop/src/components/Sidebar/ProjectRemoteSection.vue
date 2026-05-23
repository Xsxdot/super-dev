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
import { computed, ref } from 'vue'
import { useRemoteStore } from '@/stores/remote'
import type { PanelSource } from '@/stores/panel'
import { usePanelSourcePointerDrag } from '@/composables/usePanelSourcePointerDrag'

const props = defineProps<{ projectId: string }>()

const emit = defineEmits<{
  open: [payload: { projectId: string; serviceId: string; serviceName: string; logSourceIds: string[]; groupKey: string }]
  search: [payload: { projectId: string; groupKey: string }]
}>()

const remote = useRemoteStore()
const serviceGroups = computed(() => remote.remoteServiceGroupsOf(props.projectId))
const pointerServiceGroup = ref<(typeof serviceGroups.value)[number] | null>(null)
const pointerGroupKey = ref<string>('all')
const sourcePointerDrag = usePanelSourcePointerDrag(() => {
  if (!pointerServiceGroup.value) throw new Error('missing project remote drag source')
  return sourceForGroup(pointerServiceGroup.value, pointerGroupKey.value)
})

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

function onGroupPointerDown(e: PointerEvent, serviceGroup: (typeof serviceGroups.value)[number], groupKey: string) {
  pointerServiceGroup.value = serviceGroup
  pointerGroupKey.value = groupKey
  sourcePointerDrag.onPointerDown(e)
}

function onGroupClick(serviceGroup: (typeof serviceGroups.value)[number], groupKey: string) {
  if (sourcePointerDrag.consumeClickSuppression()) return
  emit('open', {
    projectId: props.projectId,
    serviceId: serviceGroup.serviceId,
    serviceName: serviceGroup.serviceName,
    logSourceIds: serviceGroup.logSourceIds,
    groupKey,
  })
}

function onGroupDragStart(e: DragEvent, serviceGroup: (typeof serviceGroups.value)[number], groupKey: string) {
  const source = sourceForGroup(serviceGroup, groupKey)
  e.dataTransfer?.setData('application/superdev-panel-source', JSON.stringify(source))
  e.dataTransfer?.setData('text/plain', JSON.stringify(source))
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'copy'
}

function openProjectRemoteSearch() {
  emit('search', { projectId: props.projectId, groupKey: 'all' })
}
</script>

<template>
  <div v-if="serviceGroups.length > 0" class="project-remote-section">
    <div class="section-header">
      <div class="section-label">远程监听</div>
      <button
        class="section-search"
        data-test="project-remote-search"
        type="button"
        title="搜索远程日志"
        @click="openProjectRemoteSearch"
      >
        搜索
      </button>
    </div>
    <div v-for="sg in serviceGroups" :key="sg.serviceId" class="service-block">
      <div class="service-name">{{ sg.serviceName }}</div>
      <div
        v-for="group in sg.groups"
        :key="group.key"
        class="group-row"
        :class="{ dragging: sourcePointerDrag.dragging.value && pointerServiceGroup?.serviceId === sg.serviceId && pointerGroupKey === group.key }"
        data-test="project-remote-group"
        draggable="true"
        @pointerdown="onGroupPointerDown($event, sg, group.key)"
        @dragstart="onGroupDragStart($event, sg, group.key)"
        @click="onGroupClick(sg, group.key)"
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
.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  padding: 3px 8px 3px 12px;
}
.section-label {
  color: var(--text-tertiary);
  font-size: 10px;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}
.section-search {
  border: 1px solid var(--border);
  border-radius: 4px;
  background: transparent;
  color: var(--text-tertiary);
  font-size: 10px;
  padding: 1px 6px;
  cursor: pointer;
}
.section-search:hover {
  color: var(--text-secondary);
  background: var(--bg-secondary);
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
.group-row.dragging {
  opacity: 0.7;
  cursor: grabbing;
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
