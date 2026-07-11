<script setup lang="ts">
import { computed, onBeforeUnmount } from 'vue'
import { usePanelStore, type PanelNode, type PanelSplitNode } from '@/stores/panel'
import PanelLeaf from './PanelLeaf.vue'

const props = defineProps<{
  node?: PanelNode
  isRoot?: boolean
}>()

const panelStore = usePanelStore()
const node = computed(() => props.node ?? panelStore.root)

const rootLeafCount = computed(() => panelStore.allLeaves.length)

let activeDrag: { splitId: string; axis: 'h' | 'v'; container: HTMLElement } | null = null

function paneFlex(ratio: number, side: 'first' | 'second') {
  const clamped = Math.min(0.95, Math.max(0.05, ratio))
  const grow = side === 'first' ? clamped : 1 - clamped
  return { flex: `${grow} 1 0%` }
}

function stopDividerDrag() {
  activeDrag = null
  window.removeEventListener('mousemove', onDividerDrag)
  window.removeEventListener('mouseup', stopDividerDrag)
}

function onDividerDrag(event: MouseEvent) {
  if (!activeDrag) return
  const rect = activeDrag.container.getBoundingClientRect()
  const size = activeDrag.axis === 'h' ? rect.width : rect.height
  if (size <= 0) return
  const offset = activeDrag.axis === 'h' ? event.clientX - rect.left : event.clientY - rect.top
  panelStore.updateSplitRatio(activeDrag.splitId, offset / size)
}

function startDividerDrag(split: PanelSplitNode, event: MouseEvent) {
  event.preventDefault()
  stopDividerDrag()
  const container = (event.currentTarget as HTMLElement).parentElement
  if (!container) return
  activeDrag = { splitId: split.id, axis: split.axis, container }
  window.addEventListener('mousemove', onDividerDrag)
  window.addEventListener('mouseup', stopDividerDrag)
}

onBeforeUnmount(stopDividerDrag)
</script>

<template>
  <div
    v-if="node.type === 'leaf'"
    class="panel-leaf-wrapper"
  >
    <!-- leaf id 是布局树中的组件身份；切换 workspace tab 时必须销毁旧叶子的本地缓存与虚拟列表。 -->
    <PanelLeaf
      :key="node.id"
      :panel-id="node.id"
      :service-id="node.serviceId"
      :project-id="node.projectId"
      :source="node.source"
      :can-close="rootLeafCount > 1"
    />
  </div>

  <div
    v-else
    class="panel-split"
    data-test="panel-split"
    :class="node.axis === 'h' ? 'split-h' : 'split-v'"
  >
    <div class="split-first" :style="paneFlex(node.ratio, 'first')">
      <PanelLayout :node="node.first" :is-root="false" />
    </div>
    <div
      class="split-divider"
      data-test="split-divider"
      :class="node.axis === 'h' ? 'divider-v' : 'divider-h'"
      @mousedown="startDividerDrag(node, $event)"
    />
    <div class="split-second" :style="paneFlex(node.ratio, 'second')">
      <PanelLayout :node="node.second" :is-root="false" />
    </div>
  </div>
</template>

<style scoped>
.panel-leaf-wrapper {
  display: flex;
  flex: 1;
  overflow: hidden;
  min-width: 0;
  min-height: 0;
}
.panel-split {
  display: flex;
  flex: 1;
  overflow: hidden;
  min-width: 0;
  min-height: 0;
}
.split-h { flex-direction: row; }
.split-v { flex-direction: column; }
.split-first, .split-second {
  display: flex;
  flex: 1;
  overflow: hidden;
  min-width: 0;
  min-height: 0;
}
.split-divider {
  background: var(--border-secondary);
  flex-shrink: 0;
  position: relative;
  z-index: 2;
}
.divider-v {
  width: 5px;
  margin: 0 -2px;
  cursor: col-resize;
}
.divider-h {
  height: 5px;
  margin: -2px 0;
  cursor: row-resize;
}

.split-divider:hover {
  background: rgba(88, 166, 255, 0.45);
}
</style>
