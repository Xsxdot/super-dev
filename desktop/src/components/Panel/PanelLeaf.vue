<script setup lang="ts">
import { ref, computed } from 'vue'
import { usePanelStore } from '@/stores/panel'
import { useAgentStore } from '@/stores/agent'
import { useDragDrop, type DropEdge } from '@/composables/useDragDrop'
import LogPanel from './LogPanel.vue'

const props = defineProps<{
  panelId: string
  serviceId: string | null
  projectId: string | null
  canClose: boolean
}>()

const panelStore = usePanelStore()
const agentStore = useAgentStore()
const { dropHighlight, getDropEdge, edgeToAxis } = useDragDrop()

const panelEl = ref<HTMLElement | null>(null)
const isFocused = computed(() => panelStore.focusedPanelId === props.panelId)

const service = computed(() =>
  props.serviceId ? agentStore.serviceById(props.serviceId) : null
)

const headerTitle = computed(() => {
  if (service.value) return service.value.name
  if (props.projectId) {
    const proj = agentStore.projectById(props.projectId)
    return proj ? `${proj.name} · 全部` : '未选择'
  }
  return '未选择'
})

function onDragOver(e: DragEvent) {
  e.preventDefault()
  if (!panelEl.value) return
  const rect = panelEl.value.getBoundingClientRect()
  dropHighlight.value = getDropEdge(
    { x: e.clientX - rect.left, y: e.clientY - rect.top },
    { w: rect.width, h: rect.height }
  )
}

function onDragLeave() {
  dropHighlight.value = null
}

function onDrop(e: DragEvent) {
  e.preventDefault()
  const serviceId = e.dataTransfer?.getData('text/plain')
  if (!serviceId || !dropHighlight.value) return

  const edge: DropEdge = dropHighlight.value
  dropHighlight.value = null

  const svc = agentStore.serviceById(serviceId)
  const projectId = svc?.project_id ?? null

  if (edge === 'center') {
    panelStore.replaceScope(props.panelId, serviceId, projectId)
    panelStore.setFocus(props.panelId)
  } else {
    const split = edgeToAxis(edge)
    if (split) {
      panelStore.splitLeaf(props.panelId, split.axis, serviceId, projectId, split.side)
    }
  }
}

function highlightStyle(edge: DropEdge | null) {
  if (!edge) return {}
  const styles: Record<DropEdge, object> = {
    left:   { left: 0, top: 0, width: '20%', height: '100%' },
    right:  { right: 0, top: 0, width: '20%', height: '100%' },
    top:    { left: 0, top: 0, width: '100%', height: '20%' },
    bottom: { left: 0, bottom: 0, width: '100%', height: '20%' },
    center: { left: '20%', top: '20%', width: '60%', height: '60%' },
  }
  return styles[edge]
}
</script>

<template>
  <div
    ref="panelEl"
    class="panel-leaf"
    :class="{ focused: isFocused }"
    @click="panelStore.setFocus(panelId)"
    @dragover="onDragOver"
    @dragleave="onDragLeave"
    @drop="onDrop"
  >
    <!-- Panel header -->
    <div class="panel-header">
      <span class="panel-title">{{ headerTitle }}</span>
      <button v-if="canClose" class="close-btn" @click.stop="panelStore.removeLeaf(panelId)">✕</button>
    </div>

    <!-- Log panel -->
    <LogPanel :panel-id="panelId" :service-id="serviceId" :project-id="projectId" />

    <!-- Drop highlight overlay -->
    <div
      v-if="dropHighlight"
      class="drop-overlay"
      :style="highlightStyle(dropHighlight)"
    />
  </div>
</template>

<style scoped>
.panel-leaf {
  position: relative;
  display: flex;
  flex-direction: column;
  flex: 1;
  overflow: hidden;
  min-width: 200px;
}
.panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 3px 8px;
  background: var(--bg-elevated);
  border-bottom: 1px solid var(--border-secondary);
  flex-shrink: 0;
}
.panel-title {
  font-size: 11px;
  color: var(--text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.close-btn {
  background: transparent;
  border: none;
  color: var(--text-tertiary);
  font-size: 10px;
  cursor: pointer;
  padding: 0 2px;
  line-height: 1;
}
.close-btn:hover { color: var(--text-primary); }

.drop-overlay {
  position: absolute;
  border-radius: 4px;
  background: rgba(31,111,235,0.25);
  border: 2px solid #1f6feb;
  pointer-events: none;
}
</style>
