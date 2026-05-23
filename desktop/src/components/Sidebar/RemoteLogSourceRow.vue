<!--
RemoteLogSourceRow：单个远程监听任务及其分组列表。

职责：
  - 展示 LogSource 名称和类型
  - 展开后按 all / tag 分组列出节点数量
  - 将分组打开、编辑、删除事件交给父组件

边界：
  - 不直接发起 HTTP 请求
  - 不打开或渲染日志面板
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRemoteStore } from '@/stores/remote'
import type { PanelSource } from '@/stores/panel'
import { usePanelSourcePointerDrag } from '@/composables/usePanelSourcePointerDrag'
import { tagColor } from '@/lib/tagColor'
import type { LogSource } from '@/api/agent'

const props = defineProps<{ logSource: LogSource }>()

const emit = defineEmits<{
  open: [payload: { logSourceId: string; groupKey: string }]
  edit: [logSource: LogSource]
  delete: [logSource: LogSource]
}>()

const store = useRemoteStore()
const expanded = ref(true)
const groups = computed(() => store.groupsOf(props.logSource.id))
const pointerGroupKey = ref<string>('all')
const sourcePointerDrag = usePanelSourcePointerDrag(() => sourceForGroup(pointerGroupKey.value))

function chipStyle(groupKey: string) {
  if (groupKey === 'all') return undefined
  return { background: tagColor(groupKey) }
}

function sourceForGroup(groupKey: string): PanelSource {
  return { type: 'remote-log-source', logSourceId: props.logSource.id, groupKey }
}

function onGroupPointerDown(e: PointerEvent, groupKey: string) {
  pointerGroupKey.value = groupKey
  sourcePointerDrag.onPointerDown(e)
}

function onGroupClick(groupKey: string) {
  if (sourcePointerDrag.consumeClickSuppression()) return
  emit('open', { logSourceId: props.logSource.id, groupKey })
}

function onGroupDragStart(e: DragEvent, groupKey: string) {
  const source = sourceForGroup(groupKey)
  e.dataTransfer?.setData('application/superdev-panel-source', JSON.stringify(source))
  e.dataTransfer?.setData('text/plain', JSON.stringify(source))
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'copy'
}
</script>

<template>
  <div class="log-source">
    <div class="header" data-test="logsource-header" @click="expanded = !expanded">
      <span class="caret">{{ expanded ? '▾' : '▸' }}</span>
      <span class="name">{{ logSource.name }}</span>
      <span class="type">[{{ logSource.type }}]</span>
      <span class="actions">
        <button class="icon" data-test="logsource-edit" @click.stop="emit('edit', logSource)">✎</button>
        <button class="icon" data-test="logsource-delete" @click.stop="emit('delete', logSource)">✕</button>
      </span>
    </div>
    <div v-if="expanded" class="groups">
      <div
        v-for="group in groups"
        :key="group.key"
        class="group-row"
        :class="{ dragging: sourcePointerDrag.dragging.value && pointerGroupKey === group.key }"
        data-test="logsource-group"
        draggable="true"
        @pointerdown="onGroupPointerDown($event, group.key)"
        @dragstart="onGroupDragStart($event, group.key)"
        @click="onGroupClick(group.key)"
      >
        <span class="chip" :style="chipStyle(group.key)">{{ group.key }}</span>
        <span class="count">({{ group.hostIds.length }} 节点)</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.log-source {
  margin: 2px 0;
}
.header {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  cursor: pointer;
  font-size: 12px;
}
.header:hover {
  background: var(--bg-secondary);
}
.caret {
  width: 10px;
  color: var(--text-tertiary);
}
.name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 600;
}
.type {
  color: var(--text-tertiary);
  font-size: 10px;
}
.actions {
  display: none;
  gap: 2px;
}
.header:hover .actions {
  display: inline-flex;
}
button.icon {
  padding: 0 2px;
  color: var(--text-tertiary);
  background: transparent;
  border: none;
  cursor: pointer;
  font-size: 11px;
}
.groups {
  padding-left: 18px;
}
.group-row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 3px 8px;
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
  color: var(--text-primary);
  background: var(--bg-secondary);
  border-radius: 2px;
  font-size: 10px;
}
.chip[style] {
  color: #fff;
}
.count {
  color: var(--text-tertiary);
}
</style>
